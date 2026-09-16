package genmsg

import (
	"strings"
	"testing"
)

func criteriaTestParties() []FlowParty {
	return []FlowParty{
		{Role: "Net Control"},
		{Role: "Shelter A", Credential: "F3"},
		{Role: "Shelter B", Credential: "S2"},
	}
}

func TestCheckFlowCounts(t *testing.T) {
	formType(t, "ICS213")
	fl := Flow{
		Parties: criteriaTestParties(),
		Messages: []FlowMessage{
			{MsgType: "ICS213", From: 0, To: -1, ToLabel: "All Stations"},
			{MsgType: "plain", From: fromEachStation, To: 0},
			{MsgType: "ICS213", From: 1, To: 2},
		},
	}
	report, err := CheckFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	a, b := report[1], report[2]
	if a.Sent != (Traffic{ThirdParty: 1, Forms: 1, OpToOp: 1}) || a.Received != (Traffic{ThirdParty: 1, Forms: 1}) {
		t.Errorf("Shelter A sent %+v, received %+v", a.Sent, a.Received)
	}
	if b.Received != (Traffic{ThirdParty: 2, Forms: 2}) {
		t.Errorf("Shelter B received %+v, want the broadcast and Shelter A's form", b.Received)
	}
	if len(a.Problems) == 0 || !strings.Contains(strings.Join(a.Problems, "; "), "sends 1 of 2 3rd party messages") {
		t.Errorf("Shelter A's problems = %v", a.Problems)
	}
	if len(report[0].Problems) != 0 {
		t.Errorf("Net Control isn't evaluated, but has problems %v", report[0].Problems)
	}
}

func TestCompleteFlowMeetsCriteria(t *testing.T) {
	formType(t, "ICS213")
	existing := FlowMessage{MsgType: "ICS213", From: 0, To: -1, ToLabel: "All Stations", Purpose: "request status"}
	fl := Flow{Parties: criteriaTestParties(), Messages: []FlowMessage{existing}}
	out, added, err := CompleteFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if added == 0 || len(out.Messages) != 1+added {
		t.Fatalf("added %d messages, flow has %d", added, len(out.Messages))
	}
	if out.Messages[0] != existing || len(fl.Messages) != 1 {
		t.Error("the existing messages should be kept first, and the input left unchanged")
	}
	report, err := CheckFlow(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range report {
		if len(c.Problems) != 0 {
			t.Errorf("%s still falls short after completing: %v", c.Role, c.Problems)
		}
	}
	types := map[string]bool{}
	replies, fresh := 0, 0
	for i, m := range out.Messages[1:] {
		n := i + 2
		switch {
		case m.From == 0 && (m.To > 0 || m.To == -1 && m.ToLabel == "All Stations"):
			if m.ReplyTo != 0 {
				t.Errorf("added message %d from Net Control shouldn't be a reply", n)
			}
		case m.From > 0 && m.To == 0:
			if m.ReplyTo > 0 {
				replies++
				target := out.Messages[m.ReplyTo-1]
				if m.ReplyTo >= n || target.From != 0 || !(target.To == m.From || target.To == -1) {
					t.Errorf("added message %d replies to message %d, which isn't an earlier Net Control message to that station", n, m.ReplyTo)
				}
				if isOpToOp(target.MsgType) != isOpToOp(m.MsgType) {
					t.Errorf("added message %d replies to a different kind of message", n)
				}
			} else {
				fresh++
			}
		default:
			t.Errorf("added message %d goes from party %d to %d, not between Net Control and a station", n, m.From, m.To)
		}
		if !isOpToOp(m.MsgType) {
			types[m.MsgType] = true
		}
	}
	if replies == 0 || fresh == 0 {
		t.Errorf("expected station messages to mix replies (%d) and new messages (%d)", replies, fresh)
	}
	if len(types) < 3 {
		t.Errorf("expected varied form types, got %v", types)
	}
	// Each station replies to a given message at most once.
	seen := map[[2]int]bool{}
	for _, m := range out.Messages {
		if m.ReplyTo > 0 {
			key := [2]int{m.From, m.ReplyTo}
			if seen[key] {
				t.Errorf("party %d replies to message %d twice", m.From, m.ReplyTo)
			}
			seen[key] = true
		}
	}
	if _, err := ResolveFlow(out); err != nil {
		t.Errorf("completed flow doesn't resolve: %v", err)
	}
	if again, n, _ := CompleteFlow(out); n != 0 || len(again.Messages) != len(out.Messages) {
		t.Errorf("completing an already compliant flow added %d messages", n)
	}
}

func TestCompleteFlowErrors(t *testing.T) {
	formType(t, "ICS213")
	if _, _, err := CompleteFlow(Flow{Parties: []FlowParty{{Role: "A", Credential: "F3"}}}); err == nil {
		t.Error("a single party can't exchange messages")
	}
	noNetControl := Flow{Parties: []FlowParty{{Role: "Shelter A", Credential: "F3"}, {Role: "Shelter B", Credential: "F3"}}}
	if _, _, err := CompleteFlow(noNetControl); err == nil || !strings.Contains(err.Error(), "Net Control") {
		t.Errorf("auto-adding traffic without a Net Control party should fail, got %v", err)
	}
	if _, err := CheckFlow(Flow{Parties: []FlowParty{{Role: "A", Credential: "X9"}}}); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Errorf("an unknown credential should be rejected, got %v", err)
	}
}
