package genmsg

import (
	"slices"
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

func TestParseResponse(t *testing.T) {
	specs := []fieldSpec{{Tag: "10."}, {Tag: "12."}}
	// Claude answers with an array; any object past the one message asked
	// for is ignored, as is a field the message doesn't have.
	text := `[{"10.":"Road closure","12.":"Main St is closed near 5th.","bogus":"dropped"},{"body":"Body two."}]`
	out, invalid, err := parseResponse(text, specs)
	if err != nil {
		t.Fatal(err)
	}
	if out["12."] != "Main St is closed near 5th." || out["10."] != "Road closure" {
		t.Errorf("values = %v", out)
	}
	if _, ok := out["bogus"]; ok {
		t.Errorf("a field the message doesn't have should be dropped, got %v", out)
	}
	if len(invalid) != 0 {
		t.Errorf("expected no invalid fields, got %v", invalid)
	}
	// A bare object is taken as the message.
	if out, _, err = parseResponse(`{"10.":"Road closure"}`, specs); err != nil || out["10."] != "Road closure" {
		t.Errorf("bare object: %v, %v", out, err)
	}
	// Nothing at all is not an error here; the caller reports it.
	if out, _, err = parseResponse(`[]`, specs); err != nil || out != nil {
		t.Errorf("empty array: %v, %v", out, err)
	}
}

func TestParseResponseInvalidJSON(t *testing.T) {
	if _, _, err := parseResponse("not json", nil); err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestParseResponseRestrictedChoice(t *testing.T) {
	specs := []fieldSpec{{Tag: "5.", Label: "Handling", Choices: []string{"ROUTINE", "PRIORITY", "IMMEDIATE"}}}

	// Exact match passes through unchanged.
	out, invalid, err := parseResponse(`[{"5.":"PRIORITY"}]`, specs)
	if err != nil {
		t.Fatal(err)
	}
	if out["5."] != "PRIORITY" || len(invalid) != 0 {
		t.Errorf("exact match: got value=%q invalid=%v", out["5."], invalid)
	}

	// Case/whitespace-insensitive match is normalized to the canonical form.
	out, invalid, err = parseResponse(`[{"5.": " priority "}]`, specs)
	if err != nil {
		t.Fatal(err)
	}
	if out["5."] != "PRIORITY" || len(invalid) != 0 {
		t.Errorf("fuzzy match: got value=%q invalid=%v", out["5."], invalid)
	}

	// A value outside the choices is dropped, not written verbatim, and
	// reported as invalid.
	out, invalid, err = parseResponse(`[{"5.":"Urgent"}]`, specs)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["5."]; ok {
		t.Errorf("expected field to be dropped for an out-of-choice value, got %q", out["5."])
	}
	if len(invalid) != 1 || invalid[0] != "Handling" {
		t.Errorf("expected invalid=[Handling], got %v", invalid)
	}
}

func TestParseResponseCheckbox(t *testing.T) {
	specs := []fieldSpec{
		{Tag: "a", Label: "A", Choices: []string{"checked"}},
		{Tag: "b", Label: "B", Choices: []string{"checked"}},
		{Tag: "c", Label: "C", Choices: []string{"checked"}},
	}
	out, invalid, err := parseResponse(`[{"a": true, "b": "Yes", "c": false}]`, specs)
	if err != nil {
		t.Fatal(err)
	}
	if out["a"] != "checked" || out["b"] != "checked" {
		t.Errorf("true and \"Yes\" should check a checkbox, got %v", out)
	}
	if _, ok := out["c"]; ok {
		t.Errorf("false should leave a checkbox unchecked, got %v", out)
	}
	if len(invalid) != 0 {
		t.Errorf("checkbox answers should not be reported invalid, got %v", invalid)
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
	plans, err := planByParty(req)
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

// TestPlanByPartyCoversEachSendersOwnList checks what the Credentialing
// Program Handbook requires: a candidate is evaluated on what that candidate
// transmits, so each party's own messages must exercise its whole list.
func TestPlanByPartyCoversEachSendersOwnList(t *testing.T) {
	req := Request{Messages: []MessageSpec{
		{MsgType: message.PlainMessage, From: "Net Control", Level: prowords.LevelFull},
		{MsgType: message.PlainMessage, From: "Shelter A", Level: prowords.LevelF3},
		{MsgType: message.PlainMessage, From: "Net Control", Level: prowords.LevelFull},
		{MsgType: message.PlainMessage, From: "Shelter A", Level: prowords.LevelF3},
	}}
	plans, err := planByParty(req)
	if err != nil {
		t.Fatal(err)
	}
	netControl := append(slices.Clone(plans[0].Categories), plans[2].Categories...)
	shelter := append(slices.Clone(plans[1].Categories), plans[3].Categories...)
	full, _ := prowords.Profile(prowords.LevelFull)
	f3, _ := prowords.Profile(prowords.LevelF3)
	for _, cat := range full {
		if !slices.Contains(netControl, cat) {
			t.Errorf("Net Control's own messages never exercise %s", cat)
		}
	}
	for _, cat := range f3 {
		if !slices.Contains(shelter, cat) {
			t.Errorf("Shelter A's own messages never exercise %s", cat)
		}
	}
	for _, cat := range shelter {
		if !slices.Contains(f3, cat) {
			t.Errorf("the F3 party was assigned %s, outside its reduced list", cat)
		}
	}
}

func TestPlanByLevelFallsBackToRequestLevel(t *testing.T) {
	req := Request{
		Level:    prowords.LevelF3,
		Messages: []MessageSpec{{MsgType: message.PlainMessage}},
	}
	plans, err := planByParty(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || len(plans[0].Categories) == 0 {
		t.Fatalf("expected message with no per-message Level to fall back to Request.Level, got %+v", plans)
	}
}

func TestPlanByLevelInvalidLevel(t *testing.T) {
	req := Request{Messages: []MessageSpec{{MsgType: message.PlainMessage, Level: "bogus"}}}
	if _, err := planByParty(req); err == nil {
		t.Error("expected an error for an unknown level")
	}
}

func TestFieldLabels(t *testing.T) {
	if got := fieldLabels(nil); got != nil {
		t.Errorf("fieldLabels(nil) = %v, want nil", got)
	}
	specs := []fieldSpec{{Tag: "5.", Label: "Handling"}, {Tag: "10.", Label: "Subject"}}
	got := fieldLabels(specs)
	want := []string{"Handling", "Subject"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("fieldLabels(%+v) = %v, want %v", specs, got, want)
	}
}

func TestPromptsAskForRealism(t *testing.T) {
	mt := formType(t, "ICS213")
	req := Request{Messages: []MessageSpec{{MsgType: mt}, {MsgType: mt}}}
	if !strings.Contains(sharedPrompt(req, ""), "model number") {
		t.Error("the message prompt should ask for realistic details such as model numbers")
	}
	if !strings.Contains(buildBriefPrompt(req), "model number") {
		t.Error("the brief should settle realistic details such as model numbers")
	}
	for name, prompt := range map[string]string{
		"message": sharedPrompt(req, ""), "brief": buildBriefPrompt(req),
		"message system": systemPrompt, "brief system": briefSystemPrompt,
	} {
		if !strings.Contains(prompt, "Xanadu City") || !strings.Contains(prompt, "Xanadu County") {
			t.Errorf("the %s prompt should set the exercise in Xanadu City, Xanadu County", name)
		}
		if strings.Contains(prompt, "Santa Clara County emergency") || strings.Contains(prompt, "plausible for Santa Clara") {
			t.Errorf("the %s prompt should not set the exercise in a real county", name)
		}
	}
}

func TestPromptsSetContentRules(t *testing.T) {
	mt := formType(t, "ICS213")
	req := Request{Messages: []MessageSpec{{MsgType: mt}, {MsgType: mt}}}
	for name, prompt := range map[string]string{"message": sharedPrompt(req, ""), "brief": buildBriefPrompt(req)} {
		if !strings.Contains(prompt, "Never mention an amateur radio frequency") || !strings.Contains(prompt, `MUST start with "https://"`) {
			t.Errorf("the %s prompt lacks the frequency and https rules", name)
		}
		if strings.Contains(prompt, "146.") || strings.Contains(prompt, `"xanadu-city.org/`) {
			t.Errorf("the %s prompt still has an example with a frequency or a URL without https://", name)
		}
	}
}

func TestIncidentDate(t *testing.T) {
	mt := formType(t, "ICS213")
	spec := func(date string) MessageSpec { return MessageSpec{MsgType: mt, Date: date} }
	for _, tc := range []struct {
		name  string
		specs []MessageSpec
		want  string
	}{
		{"none", []MessageSpec{spec(""), spec("")}, ""},
		{"agreed", []MessageSpec{spec("09/26/2026"), spec("09/26/2026")}, "09/26/2026"},
		{"one set", []MessageSpec{spec(""), spec("09/26/2026")}, "09/26/2026"},
		// Rather than tell Claude a date half the messages don't have.
		{"disagreeing", []MessageSpec{spec("09/26/2026"), spec("09/27/2026")}, ""},
	} {
		if got := incidentDate(Request{Messages: tc.specs}); got != tc.want {
			t.Errorf("%s: incidentDate = %q, want %q", tc.name, got, tc.want)
		}
	}
}
