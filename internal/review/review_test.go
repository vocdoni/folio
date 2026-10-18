// SPDX-License-Identifier: AGPL-3.0-only

package review

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	in := []Finding{
		{Page: 1, Severity: "PROBLEM", Category: "overflow", Message: " table runs off the page "},
		{Page: 2, Severity: "critical", Category: "colour", Message: "odd"},
		{Page: 9, Severity: "info", Category: "blank", Message: "not reviewed"},
		{Page: 1, Severity: "info", Category: "blank", Message: "   "},
		{Page: 2, Severity: "info", Category: "other", Message: strings.Repeat("é", MaxMessageRunes+10)},
	}
	got := Normalize(in, []int{1, 2})
	if len(got) != 3 {
		t.Fatalf("got %d findings: %+v", len(got), got)
	}
	if got[0] != (Finding{Page: 1, Severity: "problem", Category: "overflow", Message: "table runs off the page"}) {
		t.Errorf("first: %+v", got[0])
	}
	if got[1].Severity != "info" || got[1].Category != "other" {
		t.Errorf("unknown values not mapped: %+v", got[1])
	}
	if n := len([]rune(got[2].Message)); n != MaxMessageRunes+1 {
		t.Errorf("message not trimmed: %d runes", n)
	}
	if Normalize(nil, nil) == nil {
		t.Error("Normalize must not return nil")
	}
}
