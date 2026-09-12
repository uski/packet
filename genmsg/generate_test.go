package genmsg

import (
	"testing"

	"github.com/rothskeller/packet/v4/prowords"
)

func TestParseResponse(t *testing.T) {
	// Two pending messages of different types with different field sets,
	// as would happen in a mixed-type batch (e.g. ICS-213 then plain
	// text).
	specsPerMsg := [][]FieldSpec{
		{{Tag: "10."}, {Tag: "12."}},
		{{Tag: "body"}},
	}
	pending := []int{0, 1}
	text := `[{"10.":"Road closure","12.":"Main St is closed near 5th."},{"body":"Body two.","bogus":"dropped"}]`
	out, err := parseResponse(text, specsPerMsg, pending)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d messages, want 2", len(out))
	}
	if out[1]["bogus"] != "" {
		t.Errorf("field not in message 2's specs should have been dropped, got %q", out[1]["bogus"])
	}
	if out[0]["12."] != "Main St is closed near 5th." {
		t.Errorf("unexpected value: %q", out[0]["12."])
	}
	if out[1]["body"] != "Body two." {
		t.Errorf("unexpected value: %q", out[1]["body"])
	}
}

func TestParseResponseInvalidJSON(t *testing.T) {
	if _, err := parseResponse("not json", nil, nil); err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		`[1,2,3]`:               `[1,2,3]`,
		"```json\n[1,2,3]\n```": `[1,2,3]`,
		"```\n[1,2,3]\n```":     `[1,2,3]`,
		"  [1,2,3]  ":           `[1,2,3]`,
	}
	for in, want := range cases {
		if got := extractJSON(in); got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateCategories(t *testing.T) {
	values := map[string]string{
		"12.": "Please call 408-555-1212 about the outage on 5th Street.",
	}
	missing := validateCategories([]prowords.Category{prowords.TelephoneFigures, prowords.EmailAddress}, values)
	if len(missing) != 1 || missing[0] != prowords.EmailAddress {
		t.Errorf("got missing=%v, want just EmailAddress", missing)
	}
}
