// SPDX-License-Identifier: AGPL-3.0-only

package webui

import (
	"math"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Login rate limits: attempts per client address and for the whole
// instance, in a sliding one-minute window.
const (
	loginPerIP     = 5
	loginGlobal    = 60
	loginWindow    = time.Minute
	limiterMaxKeys = 10_000
)

// loginLimiter is a sliding-window log limiter.
type loginLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	byIP   map[string][]time.Time
	global []time.Time
}

func newLoginLimiter(now func() time.Time) *loginLimiter {
	return &loginLimiter{now: now, byIP: map[string][]time.Time{}}
}

func prune(ts []time.Time, since time.Time) []time.Time {
	i := 0
	for i < len(ts) && !ts[i].After(since) {
		i++
	}
	return ts[i:]
}

// allow records an attempt from ip; when a limit is reached it returns
// false and how long to wait.
func (l *loginLimiter) allow(ip string) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	since := now.Add(-loginWindow)
	l.global = prune(l.global, since)
	if len(l.byIP) >= limiterMaxKeys {
		for k, v := range l.byIP {
			if v = prune(v, since); len(v) == 0 {
				delete(l.byIP, k)
			}
		}
	}
	mine := prune(l.byIP[ip], since)
	if len(mine) >= loginPerIP {
		l.byIP[ip] = mine
		return mine[0].Add(loginWindow).Sub(now), false
	}
	if len(l.global) >= loginGlobal {
		return l.global[0].Add(loginWindow).Sub(now), false
	}
	l.byIP[ip] = append(mine, now)
	l.global = append(l.global, now)
	return 0, true
}

// retryAfter formats a wait as whole seconds (at least 1) for Retry-After.
func retryAfter(d time.Duration) string {
	return strconv.Itoa(max(1, int(math.Ceil(d.Seconds()))))
}

// clientIP returns the address used for rate limiting and where it came
// from: the peer address, or, when the peer is a trusted proxy, the last
// untrusted hop of X-Forwarded-For (walking from the right and skipping
// trusted proxies). Spoofed entries to the left of a trusted proxy's own
// entry are never used.
func clientIP(r *http.Request, trusted []netip.Prefix) (netip.Addr, string) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return netip.IPv4Unspecified(), "peer"
	}
	peer = peer.Unmap()
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	if !isTrusted(peer) {
		return peer, "peer"
	}
	var hops []string
	for _, v := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		client = a.Unmap()
		if !isTrusted(client) {
			return client, "x-forwarded-for"
		}
	}
	if client == peer {
		return peer, "peer"
	}
	return client, "x-forwarded-for"
}

func (s *Server) logIPSourceOnce(source string) {
	s.ipLogOnce.Do(func() {
		s.o.Logger.Debug("web UI login: client address source for rate limiting", "source", source)
	})
}
