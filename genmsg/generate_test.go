package genmsg

import (
	"testing"

	"github.com/rothskeller/packet/v4/message"
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
	out, invalid, err := parseResponse(text, specsPerMsg, pending)
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
	if len(invalid[0]) != 0 || len(invalid[1]) != 0 {
		t.Errorf("expected no invalid fields, got %v", invalid)
	}
}

func TestParseResponseInvalidJSON(t *testing.T) {
	if _, _, err := parseResponse("not json", nil, nil); err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseResponseRestrictedChoice(t *testing.T) {
	specsPerMsg := [][]FieldSpec{
		{{Tag: "5.", Label: "Handling", Choices: []string{"ROUTINE", "PRIORITY", "IMMEDIATE"}}},
	}
	pending := []int{0}

	// Exact match passes through unchanged.
	out, invalid, err := parseResponse(`[{"5.":"PRIORITY"}]`, specsPerMsg, pending)
	if err != nil {
		t.Fatal(err)
	}
	if out[0]["5."] != "PRIORITY" || len(invalid[0]) != 0 {
		t.Errorf("exact match: got value=%q invalid=%v", out[0]["5."], invalid[0])
	}

	// Case/whitespace-insensitive match is normalized to the canonical form.
	out, invalid, err = parseResponse(`[{"5.": " priority "}]`, specsPerMsg, pending)
	if err != nil {
		t.Fatal(err)
	}
	if out[0]["5."] != "PRIORITY" || len(invalid[0]) != 0 {
		t.Errorf("fuzzy match: got value=%q invalid=%v", out[0]["5."], invalid[0])
	}

	// A value outside the choices is dropped, not written verbatim, and
	// reported as invalid.
	out, invalid, err = parseResponse(`[{"5.":"Urgent"}]`, specsPerMsg, pending)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[0]["5."]; ok {
		t.Errorf("expected field to be dropped for an out-of-choice value, got %q", out[0]["5."])
	}
	if len(invalid[0]) != 1 || invalid[0][0] != "Handling" {
		t.Errorf("expected invalid=[Handling], got %v", invalid[0])
	}
}

func TestExtractJSON(t *testing.T) {
	cases := map[string]string{
		`[1,2,3]`:                     `[1,2,3]`,
		"```json\n[1,2,3]\n```":       `[1,2,3]`,
		"```\n[1,2,3]\n```":           `[1,2,3]`,
		"  [1,2,3]  ":                 `[1,2,3]`,
		"Here it is:\n[1,2,3]\nDone.": `[1,2,3]`,
	}
	for in, want := range cases {
		if got := extractJSON(in); got != want {
			t.Errorf("extractJSON(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMissingCategories(t *testing.T) {
	values := map[string]string{
		"12.": "Please call 408-555-1212 about 5 units at the shelter.",
	}
	counts := prowords.CountFields(values)
	missing := missingCategories([]prowords.Category{prowords.TelephoneFigures, prowords.EmailAddress}, counts)
	if len(missing) != 1 || missing[0] != prowords.EmailAddress {
		t.Errorf("got missing=%v, want just EmailAddress", missing)
	}
}

func TestPlanByLevelGroupsMessagesByTheirOwnLevel(t *testing.T) {
	// A batch mixing an F3 party's message with two full-level messages
	// must plan each group independently: the F3 message may only ever
	// draw from the reduced list, and the full-level messages should
	// still get full-list-only categories (e.g. GPSCoordinates) spread
	// across just the two of them, not diluted by the F3 message.
	req := Request{
		Messages: []MessageSpec{
			{MsgType: message.PlainMessage, Level: prowords.LevelF3},
			{MsgType: message.PlainMessage, Level: prowords.LevelFull},
			{MsgType: message.PlainMessage, Level: prowords.LevelFull},
		},
	}
	plans, err := planByLevel(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 3 {
		t.Fatalf("got %d plans, want 3", len(plans))
	}
	fullOnly := map[prowords.Category]bool{
		prowords.GPSCoordinates: true, prowords.PacketAddress: true, prowords.InternetAddress: true,
		prowords.CaseSensitive: true, prowords.SubscriptSuperscript: true, prowords.Newline: true,
	}
	for _, cat := range plans[0].Categories {
		if fullOnly[cat] {
			t.Errorf("F3 message was assigned a full-list-only category: %v", cat)
		}
	}
	var sawFullOnly bool
	for _, p := range plans[1:] {
		for _, cat := range p.Categories {
			if fullOnly[cat] {
				sawFullOnly = true
			}
		}
	}
	if !sawFullOnly {
		t.Error("expected the full-level messages to collectively cover at least one full-list-only category")
	}
}

func TestPlanByLevelFallsBackToRequestLevel(t *testing.T) {
	req := Request{
		Level:    prowords.LevelF3,
		Messages: []MessageSpec{{MsgType: message.PlainMessage}},
	}
	plans, err := planByLevel(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || len(plans[0].Categories) == 0 {
		t.Fatalf("expected message with no per-message Level to fall back to Request.Level, got %+v", plans)
	}
}

func TestPlanByLevelInvalidLevel(t *testing.T) {
	req := Request{Messages: []MessageSpec{{MsgType: message.PlainMessage, Level: "bogus"}}}
	if _, err := planByLevel(req); err == nil {
		t.Error("expected an error for an unknown level")
	}
}

func TestFieldLabels(t *testing.T) {
	if got := fieldLabels(nil); got != nil {
		t.Errorf("fieldLabels(nil) = %v, want nil", got)
	}
	specs := []FieldSpec{{Tag: "5.", Label: "Handling"}, {Tag: "10.", Label: "Subject"}}
	got := fieldLabels(specs)
	want := []string{"Handling", "Subject"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("fieldLabels(%+v) = %v, want %v", specs, got, want)
	}
}
