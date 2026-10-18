// SPDX-License-Identifier: AGPL-3.0-only

package ai

import (
	"slices"
	"strconv"
	"testing"
)

func within(t *testing.T, in, got, want string, tol int) {
	t.Helper()
	if len(got) != 7 || len(want) != 7 {
		t.Fatalf("%s: got %q want %q", in, got, want)
	}
	for i := 1; i < 7; i += 2 {
		g, _ := strconv.ParseInt(got[i:i+2], 16, 32)
		w, _ := strconv.ParseInt(want[i:i+2], 16, 32)
		if d := int(g - w); d > tol || d < -tol {
			t.Fatalf("%s: got %s want %s (±%d)", in, got, want, tol)
		}
	}
}

func TestParseColor(t *testing.T) {
	cases := []struct {
		in, want string
		tol      int
	}{
		{"#c8246b", "#C8246B", 0},
		{"#FA0", "#FFAA00", 0},
		{"#c8246b80", "#C8246B", 0},
		{"rgb(28, 36, 33)", "#1C2421", 0},
		{"rgb(28 36 33 / 50%)", "#1C2421", 0},
		{"rgba(255,0,0,.5)", "#FF0000", 0},
		{"rgb(100% 0% 0%)", "#FF0000", 0},
		{"hsl(0 100% 50%)", "#FF0000", 0},
		{"hsl(120deg, 100%, 25%)", "#008000", 1},
		{"hsla(240, 100%, 50%, .3)", "#0000FF", 0},
		{"hwb(0 0% 0%)", "#FF0000", 0},
		{"hwb(0 50% 50%)", "#808080", 1},
		{"RebeccaPurple", "#663399", 0},
		{"white", "#FFFFFF", 0},
		{"oklch(100% 0 0)", "#FFFFFF", 1},
		{"oklab(0% 0 0)", "#000000", 0},
		{"lab(100 0 0)", "#FFFFFF", 1},
		{"lch(0 0 0)", "#000000", 0},
		// References computed independently (oklch.py and a Lindbloom Lab implementation).
		{"oklch(47% .085 158)", "#296948", 1},
		{"oklch(0.47 0.085 158)", "#296948", 1},
		{"oklch(47% 0.085 158deg / .5)", "#296948", 1},
		{"oklch(62% 0.09 158)", "#539771", 1},
		{"oklch(.988 .011 97)", "#FDFBF3", 1},
		{"oklch(.24 .013 106)", "#202019", 1},
		{"oklab(0.7 0.1 0.1)", "#E57F4E", 1},
		{"lab(50 40 30)", "#BB5846", 3},
		{"lch(50 50 40)", "#B95943", 3},
	}
	for _, c := range cases {
		got, ok := ParseColor(c.in)
		if !ok {
			t.Errorf("%s: not parsed", c.in)
			continue
		}
		within(t, c.in, got, c.want, c.tol)
	}
	for _, bad := range []string{"", "var(--x)", "currentcolor", "transparent", "oklch(from red l c h)", "rgb(a b c)", "calc(1px)", "#12", "#12345", "notacolor", "rgb(1 2)"} {
		if got, ok := ParseColor(bad); ok {
			t.Errorf("%q parsed as %s", bad, got)
		}
	}
}

func TestFindColors(t *testing.T) {
	got := FindColors("1px solid #C8246B, 0 0 4px oklch(47% .085 158 / .4) red", true)
	if len(got) != 3 || got[0] != "#C8246B" || got[2] != "#FF0000" {
		t.Fatalf("%v", got)
	}
	if got := FindColors("var(--primary) url(#grad) auto", true); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	if got := FindColors("red", false); len(got) != 0 {
		t.Fatalf("names only in colour properties: %v", got)
	}
	if !slices.Equal(FindColors("rgb(1 2 3)", false), []string{"#010203"}) {
		t.Fatal("functions always count")
	}
	if got := FindColors("var(--red-500) 1px", true); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
