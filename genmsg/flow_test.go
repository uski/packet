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

func TestFindMsgType(t *testing.T) {
	if _, ok := FindMsgType("plain"); !ok {
		t.Error("expected to find the built-in plain message type")
	}
	if _, ok := FindMsgType("no-such-type"); ok {
		t.Error("expected not to find a bogus type")
	}
}
