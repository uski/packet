package genmsg

import (
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/form/formdefs"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

func TestApplyPartyFieldsSetsAndExcludesFromLLMFill(t *testing.T) {
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	mt, ok := FindMsgType("ICS213")
	if !ok {
		t.Skip("ICS213 not registered (build without -tags sccopifo?)")
	}
	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error { inc = i; return nil }); err != nil {
		t.Fatal(err)
	}

	draft := mt.NewDraft().(*message.DraftMessage)
	inc.ApplyDefaults(draft)
	spec := MessageSpec{MsgType: mt, From: "Net Control", FromLocation: "County EOC", To: "All Stations"}
	applyPartyFields(draft, spec)

	if got := FindFieldByCommon(draft, "fromICSPosition").Value(draft); got != "Net Control" {
		t.Errorf("fromICSPosition = %q, want %q", got, "Net Control")
	}
	if got := FindFieldByCommon(draft, "fromLocation").Value(draft); got != "County EOC" {
		t.Errorf("fromLocation = %q, want %q", got, "County EOC")
	}
	if got := FindFieldByCommon(draft, "toICSPosition").Value(draft); got != "All Stations" {
		t.Errorf("toICSPosition = %q, want %q", got, "All Stations")
	}

	// Since these fields now already have values, Generatable must
	// exclude them from what Claude is asked to fill -- otherwise the
	// LLM's own guess could clobber the deterministic party fields we
	// (or Apply) will set.
	specs := Generatable(Describe(draft))
	for _, s := range specs {
		if s.Common == "fromICSPosition" || s.Common == "fromLocation" || s.Common == "toICSPosition" {
			t.Errorf("field %q should have been excluded once pre-filled by applyPartyFields", s.Common)
		}
	}
}

func TestBuildPromptIncludesFlowContext(t *testing.T) {
	req := Request{
		Messages: []MessageSpec{
			{MsgType: message.PlainMessage, From: "Net Control", FromLocation: "County EOC", To: "All Stations", Purpose: "request shelter status"},
			{MsgType: message.PlainMessage, From: "Shelter Manager", To: "Net Control", Purpose: "report shelter status", ReplyTo: 1},
		},
	}
	specs := []FieldSpec{{Tag: "defaultBody", Multiline: true}}
	specsPerMsg := [][]FieldSpec{specs, specs}
	results := make([]Result, 2)
	routedPerMsg := make([]map[prowords.Category]string, 2)

	// Each message is asked for on its own, and carries its own flow
	// context; the reply also names the message it answers.
	first := buildPrompt(req, "", specsPerMsg, results, 0, MessagePlan{}, routedPerMsg, make([]int, 2), false)
	second := buildPrompt(req, "", specsPerMsg, results, 1, MessagePlan{}, routedPerMsg, make([]int, 2), false)
	for prompt, wants := range map[string][]string{
		first:  {"Message 1", "Net Control (County EOC)", "All Stations", "request shelter status"},
		second: {"Message 2", "Shelter Manager", "report shelter status", "direct reply to Message 1"},
	} {
		for _, want := range wants {
			if !strings.Contains(prompt, want) {
				t.Errorf("prompt missing %q\n--- prompt ---\n%s", want, prompt)
			}
		}
	}
}

func TestBuildPromptInjectsContextForNonPendingReplyTarget(t *testing.T) {
	req := Request{
		Messages: []MessageSpec{
			{MsgType: message.PlainMessage, From: "Net Control", To: "All Stations", Purpose: "request shelter status"},
			{MsgType: message.PlainMessage, From: "Shelter Manager", To: "Net Control", ReplyTo: 1},
		},
	}
	specs := []FieldSpec{{Tag: "defaultBody", Multiline: true}}
	specsPerMsg := [][]FieldSpec{specs, specs}
	// Message 1 is already written; its content must still be surfaced as
	// context for the message that replies to it.
	results := []Result{
		{Values: map[string]string{"defaultBody": "How many beds are available at your shelter?"}},
		{},
	}
	routedPerMsg := make([]map[prowords.Category]string, 2)

	prompt := buildPrompt(req, "", specsPerMsg, results, 1, MessagePlan{}, routedPerMsg, make([]int, 2), true)

	if !strings.Contains(prompt, "How many beds are available") {
		t.Errorf("expected the non-pending replied-to message's content to be injected as context\n--- prompt ---\n%s", prompt)
	}
}

func TestPartyIdentity(t *testing.T) {
	for _, tc := range []struct{ role, prefix, want string }{
		{"Shelter", "S21", "Shelter S21"},
		{"Shelter", "", "Shelter"},
		{"", "S21", "S21"},
		{" Shelter ", " S21 ", "Shelter S21"},
	} {
		if got := PartyName(tc.role, tc.prefix); got != tc.want {
			t.Errorf("PartyName(%q, %q) = %q, want %q", tc.role, tc.prefix, got, tc.want)
		}
	}
	// "f3" without a credential of its own is evaluated for F3, and only
	// F3 uses the reduced proword list.
	f3 := FlowParty{Role: "Shelter", F3: true}
	if partyCredential(f3) != "F3" || partyLevel(f3) != prowords.LevelF3 {
		t.Errorf("an f3 party is %q at level %q", partyCredential(f3), partyLevel(f3))
	}
	f2 := FlowParty{Role: "Shelter", Credential: "F2"}
	if partyCredential(f2) != "F2" || partyLevel(f2) != prowords.LevelFull {
		t.Errorf("an F2 party is %q at level %q", partyCredential(f2), partyLevel(f2))
	}
	for _, tc := range []struct {
		credential, role string
		want             bool
	}{
		{"N3", "Anything", true},
		{"", "Net Control", true},
		{"", "net control operator", true},
		{"F3", "Shelter", false},
		{"", "Shelter", false},
	} {
		if got := isNetControl(tc.credential, tc.role); got != tc.want {
			t.Errorf("isNetControl(%q, %q) = %v", tc.credential, tc.role, got)
		}
	}
	// A Net Control credential wins over a role that merely says so.
	parties := []FlowParty{{Role: "Net Control"}, {Role: "Shelter", Credential: "N2"}}
	if i, ok := netControlParty(parties); !ok || i != 1 {
		t.Errorf("netControlParty = %d, %v; want the party with the credential", i, ok)
	}
}
