package genmsg

import (
	"strings"
	"testing"
	"time"

	"github.com/rothskeller/packet/v4/message"
)

func TestResolveFlowBasic(t *testing.T) {
	fl := Flow{
		Parties: []FlowParty{
			{Role: "Net Control", Location: "County EOC"},
			{Role: "Shelter Manager", Location: "Roosevelt MS"},
		},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 0, To: -1, ToLabel: "All Stations", Purpose: "request shelter status"},
			{MsgType: "plain", From: 1, To: 0, Purpose: "report shelter status", ReplyTo: 1},
		},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2", len(specs))
	}
	if specs[0].From != "Net Control" || specs[0].FromLocation != "County EOC" {
		t.Errorf("message 1 from: got %q/%q", specs[0].From, specs[0].FromLocation)
	}
	if specs[0].To != "All Stations" || specs[0].ToLocation != "" {
		t.Errorf("message 1 to: got %q/%q", specs[0].To, specs[0].ToLocation)
	}
	if specs[1].From != "Shelter Manager" || specs[1].To != "Net Control" || specs[1].ToLocation != "County EOC" {
		t.Errorf("message 2 from/to: got from=%q to=%q toLoc=%q", specs[1].From, specs[1].To, specs[1].ToLocation)
	}
	if specs[1].ReplyTo != 1 {
		t.Errorf("message 2 replyTo: got %d, want 1", specs[1].ReplyTo)
	}
	if specs[0].MsgType != message.PlainMessage || specs[1].MsgType != message.PlainMessage {
		t.Errorf("expected both messages resolved to PlainMessage")
	}
}

func TestResolveFlowErrors(t *testing.T) {
	base := Flow{Parties: []FlowParty{{Role: "A"}, {Role: "B"}}}

	cases := []struct {
		name string
		fl   Flow
		want string
	}{
		{"no messages", Flow{}, "at least one message"},
		{"bad type", Flow{Parties: base.Parties, Messages: []FlowMessage{{MsgType: "NoSuchType", From: 0, To: 1}}}, "no such message type"},
		{"bad from", Flow{Parties: base.Parties, Messages: []FlowMessage{{MsgType: "plain", From: 5, To: 1}}}, "from"},
		{"bad to", Flow{Parties: base.Parties, Messages: []FlowMessage{{MsgType: "plain", From: 0, To: 5}}}, "to"},
		{"self reply", Flow{Parties: base.Parties, Messages: []FlowMessage{{MsgType: "plain", From: 0, To: 1, ReplyTo: 1}}}, "replyTo"},
		{"reply out of range", Flow{Parties: base.Parties, Messages: []FlowMessage{{MsgType: "plain", From: 0, To: 1, ReplyTo: 9}}}, "replyTo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ResolveFlow(c.fl)
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestResolveFlowPartyLevel(t *testing.T) {
	fl := Flow{
		Parties: []FlowParty{
			{Role: "Net Control", F3: false},
			{Role: "Trainee", F3: true},
		},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 0, To: 1},
			{MsgType: "plain", From: 1, To: 0, ReplyTo: 1},
		},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Level != "full" {
		t.Errorf("message 1 (from non-F3 party) level = %q, want %q", specs[0].Level, "full")
	}
	if specs[1].Level != "f3" {
		t.Errorf("message 2 (from F3 party) level = %q, want %q", specs[1].Level, "f3")
	}
}

func TestResolveFlowEachStationFanOut(t *testing.T) {
	fl := Flow{
		Parties: []FlowParty{
			{Role: "Net Control"},
			{Role: "Shelter A"},
			{Role: "Shelter B"},
			{Role: "Shelter C"},
		},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 0, To: -1, ToLabel: "All Stations", Purpose: "request status"},
			{MsgType: "plain", From: fromEachStation, To: 0, Purpose: "report status", ReplyTo: 1},
		},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	// One broadcast, plus one reply per party excluding the broadcaster
	// (Net Control), for a total of 1 + 3 = 4.
	if len(specs) != 4 {
		t.Fatalf("got %d specs, want 4: %+v", len(specs), specs)
	}
	if specs[0].From != "Net Control" || specs[0].To != "All Stations" {
		t.Errorf("broadcast message: got from=%q to=%q", specs[0].From, specs[0].To)
	}
	wantFrom := map[string]bool{"Shelter A": false, "Shelter B": false, "Shelter C": false}
	for _, s := range specs[1:] {
		if s.To != "Net Control" {
			t.Errorf("fanned-out reply To = %q, want %q", s.To, "Net Control")
		}
		if s.ReplyTo != 1 {
			t.Errorf("fanned-out reply ReplyTo = %d, want 1 (the broadcast)", s.ReplyTo)
		}
		if _, ok := wantFrom[s.From]; !ok {
			t.Errorf("unexpected From %q in fanned-out replies", s.From)
		}
		wantFrom[s.From] = true
	}
	for from, seen := range wantFrom {
		if !seen {
			t.Errorf("expected a fanned-out reply from %q, got none", from)
		}
	}
}

func TestResolveFlowEachStationNoReplyToIncludesAllParties(t *testing.T) {
	fl := Flow{
		Parties: []FlowParty{{Role: "A"}, {Role: "B"}},
		Messages: []FlowMessage{
			{MsgType: "plain", From: fromEachStation, To: -1, ToLabel: "Net Control"},
		},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2 (no replyTo, so no party excluded)", len(specs))
	}
}

// TestResolveFlowNoStationMessagesItself covers each station sending to one
// party without replying to anything: the recipient must not be among the
// senders.
func TestResolveFlowNoStationMessagesItself(t *testing.T) {
	fl := Flow{
		Parties: []FlowParty{{Role: "Net Control"}, {Role: "Shelter A"}, {Role: "Shelter B"}},
		Messages: []FlowMessage{
			{MsgType: "plain", From: fromEachStation, To: 0},
		},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d messages, want 2 (one from each shelter)", len(specs))
	}
	for _, s := range specs {
		if s.From == s.To {
			t.Errorf("%s sends a message to itself", s.From)
		}
	}

	fl.Messages = []FlowMessage{{MsgType: "plain", From: 0, To: 0}}
	if _, err := ResolveFlow(fl); err == nil || !strings.Contains(err.Error(), "to itself") {
		t.Errorf("a message from a party to itself should be rejected, got %v", err)
	}
}

func TestResolveFlowCannotReplyToFannedOutMessage(t *testing.T) {
	fl := Flow{
		Parties: []FlowParty{{Role: "A"}, {Role: "B"}},
		Messages: []FlowMessage{
			{MsgType: "plain", From: fromEachStation, To: -1, ToLabel: "Net Control"},
			{MsgType: "plain", From: 0, To: 1, ReplyTo: 1},
		},
	}
	_, err := ResolveFlow(fl)
	if err == nil || !strings.Contains(err.Error(), "from each station") {
		t.Fatalf("expected an error about replying to a fanned-out message, got %v", err)
	}
}

func TestResolveFlowEachStationAllExcludedIsAnError(t *testing.T) {
	fl := Flow{
		Parties: []FlowParty{{Role: "Net Control"}},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 0, To: -1, ToLabel: "All Stations"},
			{MsgType: "plain", From: fromEachStation, To: 0, ReplyTo: 1},
		},
	}
	_, err := ResolveFlow(fl)
	if err == nil || !strings.Contains(err.Error(), "no parties left") {
		t.Fatalf("expected a \"no parties left\" error, got %v", err)
	}
}

func TestResolveFlowPrefixes(t *testing.T) {
	fl := Flow{
		Parties: []FlowParty{
			{Role: "Net Control", Prefix: "eoc"},
			{Role: "Shelter A", Prefix: " S24 "},
			{Role: "Shelter B"},
		},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 0, To: -1, ToLabel: "All Stations"},
			{MsgType: "plain", From: fromEachStation, To: 0, ReplyTo: 1},
		},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].FromPrefix != "EOC" || specs[0].ToPrefix != "" {
		t.Errorf("broadcast: FromPrefix=%q ToPrefix=%q, want EOC and none", specs[0].FromPrefix, specs[0].ToPrefix)
	}
	if specs[1].FromPrefix != "S24" || specs[1].ToPrefix != "EOC" {
		t.Errorf("Shelter A's reply: FromPrefix=%q ToPrefix=%q, want S24 and EOC", specs[1].FromPrefix, specs[1].ToPrefix)
	}
	if specs[2].FromPrefix != "" || specs[2].ToPrefix != "EOC" {
		t.Errorf("Shelter B's reply: FromPrefix=%q ToPrefix=%q, want none and EOC", specs[2].FromPrefix, specs[2].ToPrefix)
	}
	if fl.Parties[0].Prefix != "eoc" {
		t.Error("ResolveFlow should not modify the caller's parties")
	}

	fl.Parties[1].Prefix = "SHELTER"
	if _, err := ResolveFlow(fl); err == nil || !strings.Contains(err.Error(), "prefix") {
		t.Errorf("expected an invalid prefix error, got %v", err)
	}
}

func TestFlowDate(t *testing.T) {
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.Local)
	for in, want := range map[string]string{"": "09/16/2026", "2026-03-14": "03/14/2026", "03/14/2026": "03/14/2026", "3/4/2026": "03/04/2026"} {
		if got, err := flowDate(Flow{Date: in}, now); err != nil || got != want {
			t.Errorf("flowDate(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := flowDate(Flow{Date: "next Tuesday"}, now); err == nil {
		t.Error("an unparseable date should be rejected")
	}
	fl := Flow{
		Date:     "2026-03-14",
		Parties:  []FlowParty{{Role: "A"}, {Role: "B"}},
		Messages: []FlowMessage{{MsgType: "plain", From: 0, To: 1}},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if specs[0].Date != "03/14/2026" {
		t.Errorf("message date = %q, want the flow's incident date", specs[0].Date)
	}
	if prompt := buildBriefPrompt(Request{Messages: specs}); !strings.Contains(prompt, "takes place on 03/14/2026") {
		t.Errorf("the brief should give Claude the incident date:\n%s", prompt)
	}
}

func TestFindMsgType(t *testing.T) {
	if _, ok := FindMsgType("plain"); !ok {
		t.Error("expected to find the built-in plain message type")
	}
	if _, ok := FindMsgType("no-such-type"); ok {
		t.Error("expected not to find a bogus type")
	}
}

// TestResolveFlowRecordsF3ForRecipients verifies that a party marked "f3"
// with no credential of its own is recorded as F3 wherever it appears: the
// fallback used to be applied only to the sender, so the same party had a
// credential in the messages it sent and none in those it received.
func TestResolveFlowRecordsF3ForRecipients(t *testing.T) {
	specs, err := ResolveFlow(Flow{
		Parties: []FlowParty{
			{Role: "Net Control", Prefix: "XND"},
			{Role: "Shelter", Prefix: "S21", F3: true},
		},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 1, To: 0},
			{MsgType: "plain", From: 0, To: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := specs[0].Training.FromCredential; got != "F3" {
		t.Errorf("as sender, credential = %q, want F3", got)
	}
	if got := specs[1].Training.To[0].Credential; got != "F3" {
		t.Errorf("as recipient, credential = %q, want F3", got)
	}
}
