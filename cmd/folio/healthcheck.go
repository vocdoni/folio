// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// healthURL derives the local /healthz URL from FOLIO_ADDR or PORT with the
// same precedence as `folio serve` (FOLIO_ADDR, then :$PORT, then :8080). A
// wildcard or empty host is probed on the loopback address.
func healthURL(getenv func(string) string) (string, error) {
	addr := getenv("FOLIO_ADDR")
	if addr == "" {
		addr = ":8080"
		if p := getenv("PORT"); p != "" {
			addr = ":" + p
		}
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("listen address %q: %w", addr, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
}

// healthcheckCmd probes the local /healthz and exits 0 only on HTTP 200, so
// the image needs no curl. It is what the Dockerfile HEALTHCHECK runs.
func healthcheckCmd(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: folio healthcheck")
		return 2
	}
	u, err := healthURL(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "folio healthcheck:", err)
		return 1
	}
	if err := probe(u, 5*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "folio healthcheck:", err)
		return 1
	}
	return 0
}

func probe(u string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", u, resp.Status)
	}
	return nil
}
