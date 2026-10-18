// SPDX-License-Identifier: AGPL-3.0-only

package main

import "testing"

func TestServeRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv("FOLIO_PUBLIC_URL", "https://folio.example.com")
	t.Setenv("FOLIO_TOKEN", "too-short")
	t.Setenv("FOLIO_TOKENS", "")
	if code := serveCmd(nil); code != 2 {
		t.Fatalf("exit code %d, want 2", code)
	}
	if code := serveCmd([]string{"extra"}); code != 2 {
		t.Fatalf("extra arguments: exit code %d, want 2", code)
	}
}
