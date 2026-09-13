package genmsg

import (
	"errors"
	"testing"

	"github.com/rothskeller/packet/v4/prowords"
)

func TestFictionalizeCallSigns(t *testing.T) {
	in := map[string]string{
		"body":  "Contact KJ6ABC or W6XRL4 at kj6abc@xanadu-city.org via w6xrl4@w4xsc.#nca.ca.usa; N2O and F150 unchanged.",
		"other": "KJ6ABC",
	}
	out := fictionalizeCallSigns(in)
	want := "Contact KJ6ABC4 or W6XRL4 at kj6abc4@xanadu-city.org via w6xrl4@w4xsc4.#nca.ca.usa; N2O and F150 unchanged."
	if out["body"] != want {
		t.Errorf("got  %q\nwant %q", out["body"], want)
	}
	if out["other"] != "KJ6ABC4" {
		t.Errorf("the same call sign should become the same fictitious one, got %q", out["other"])
	}
	if in["other"] != "KJ6ABC" {
		t.Error("the input map should not be modified")
	}
	if prowords.Count(out["other"])[prowords.AmateurCall] != 1 {
		t.Error("a fictitious call sign should still count as AMATEUR CALL")
	}
}

func TestIsFictitiousCallSignError(t *testing.T) {
	formErr := errors.New(`Field "Call Sign" does not contain a FCC call sign.`)
	if !isFictitiousCallSignError("W6XRL4", formErr) {
		t.Error("a fictitious call sign's format error should be ignored")
	}
	if isFictitiousCallSignError("hello", formErr) {
		t.Error("a value that isn't a call sign at all is still an error")
	}
	if isFictitiousCallSignError("W6XRL4", errors.New(`Field "Call Sign" is required.`)) {
		t.Error("other errors are still errors")
	}
}
