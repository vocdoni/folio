// SPDX-License-Identifier: AGPL-3.0-only

package model

import (
	"errors"
	"testing"
)

func TestCanonicalSortsKeysAndKeepsNumbers(t *testing.T) {
	got, err := CanonicalBytes([]byte(`{ "b": 1.50, "a": {"z": "<&>", "y": [3, 1e2]} }`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":{"y":[3,1e2],"z":"<&>"},"b":1.50}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if _, err := CanonicalBytes([]byte(`{} {}`)); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestDecodeJSONUsesNumbers(t *testing.T) {
	v, err := DecodeJSON([]byte(`{"n": 0.1}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(map[string]any)["n"].(interface{ String() string }); !ok {
		t.Fatalf("number decoded as %T", v.(map[string]any)["n"])
	}
	if v, _ := DecodeJSON(nil); v == nil {
		t.Fatal("empty input must decode to an empty object")
	}
}

func TestErrorUnwrapAndRetryable(t *testing.T) {
	cause := errors.New("boom")
	e := &Error{Code: ErrRenderFailed, Message: "x", Err: cause}
	if !errors.Is(e, cause) {
		t.Fatal("Unwrap broken")
	}
	if !Errorf(ErrBusy, "q").Retryable || Errorf(ErrTimeout, "t").Retryable {
		t.Fatal("only busy is retryable")
	}
}
