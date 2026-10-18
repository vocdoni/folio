// SPDX-License-Identifier: AGPL-3.0-only

package assets

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

var (
	// ErrForbiddenAddress means the dialed IP is not a public unicast address.
	ErrForbiddenAddress = errors.New("refusing to fetch from a non-public address")
	// ErrBadURL means the URL is not an acceptable https URL.
	ErrBadURL = errors.New("asset URL must be an absolute https URL without credentials")
)

const (
	fetchTimeout = 20 * time.Second
	maxRedirects = 3
)

// blockedPrefixes are refused in addition to what netip classifies as
// loopback, private, link-local, multicast or unspecified.
var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"), // CGNAT (also Alibaba metadata 100.100.100.200)
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),   // NAT64 can reach internal IPv4
	netip.MustParsePrefix("64:ff9b:1::/48"), // local-use NAT64
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/32"), // Teredo
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), // 6to4 embeds IPv4
}

// PublicAddr reports whether ip is a public unicast address that Folio may
// connect to. 169.254.169.254 and fd00:ec2::254 (cloud metadata) are covered
// by the link-local and private checks.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// Fetcher downloads asset URLs with SSRF protection: https only, the IP is
// checked in the dialer's Control hook (after DNS resolution, so DNS
// rebinding cannot bypass it), environment proxies are ignored, at most 3
// redirects each re-checked, 20 s total timeout, size capped.
type Fetcher struct {
	client   *http.Client
	maxBytes int64
}

// FetchOption customises a Fetcher (tests only).
type FetchOption func(*fetchConfig)

type fetchConfig struct {
	allow   func(netip.Addr) bool
	rootCAs *x509.CertPool
}

// WithAddrPolicy replaces PublicAddr (tests use it to reach httptest servers).
func WithAddrPolicy(allow func(netip.Addr) bool) FetchOption {
	return func(c *fetchConfig) { c.allow = allow }
}

// WithRootCAs trusts the given pool instead of the system roots (tests only).
func WithRootCAs(p *x509.CertPool) FetchOption { return func(c *fetchConfig) { c.rootCAs = p } }

// NewFetcher returns a Fetcher that stops reading after maxBytes.
func NewFetcher(maxBytes int64, opts ...FetchOption) *Fetcher {
	cfg := fetchConfig{allow: PublicAddr}
	for _, o := range opts {
		o(&cfg)
	}
	dialer := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(network, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil {
				return fmt.Errorf("%w: %s", ErrForbiddenAddress, address)
			}
			if !cfg.allow(ap.Addr()) {
				return fmt.Errorf("%w: %s", ErrForbiddenAddress, ap.Addr())
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                  nil, // never honour HTTP(S)_PROXY for asset fetches
		DialContext:            dialer.DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: cfg.rootCAs},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		DisableKeepAlives:      true,
		ForceAttemptHTTP2:      true,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   fetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return fmt.Errorf("too many redirects (max %d)", maxRedirects)
			}
			return checkURL(req.URL)
		},
	}
	return &Fetcher{client: client, maxBytes: maxBytes}
}

func checkURL(u *url.URL) error {
	if u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ErrBadURL
	}
	return nil
}

// Fetch downloads rawURL. Errors wrap ErrBadURL, ErrForbiddenAddress or ErrTooLarge
// where applicable.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, ErrBadURL
	}
	if err := checkURL(u); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, ErrBadURL
	}
	req.Header.Set("User-Agent", "folio-asset-fetch")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch asset: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch asset: read: %w", err)
	}
	if int64(len(data)) > f.maxBytes {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, f.maxBytes)
	}
	return data, nil
}
