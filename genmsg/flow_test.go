package genmsg

import (
	"strings"
	"testing"

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

func TestFindMsgType(t *testing.T) {
	if _, ok := FindMsgType("plain"); !ok {
		t.Error("expected to find the built-in plain message type")
	}
	if _, ok := FindMsgType("no-such-type"); ok {
		t.Error("expected not to find a bogus type")
	}
}
