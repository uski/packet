package genmsg

import (
	"strings"
	"testing"
)

func TestFlowDiagram(t *testing.T) {
	fl := Flow{
		Date: "2026-03-14",
		Parties: []FlowParty{
			{Role: "Net Control", Prefix: "EOC"},
			{Role: "Shelter \"A\"", Credential: "F3"},
			{Role: "Shelter B"},
		},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 0, To: -1, ToLabel: "All Stations", Purpose: "request\nstatus"},
			{MsgType: "plain", From: fromEachStation, To: 0, ReplyTo: 1},
			{MsgType: "plain", From: 2, To: -1, ToLabel: "County Health"},
		},
	}
	d, err := FlowDiagram(fl)
	if err != nil {
		t.Fatal(err)
	}
	if d.Date != "03/14/2026" {
		t.Errorf("date = %q", d.Date)
	}
	want := []string{"Net Control EOC", `Shelter "A" [F3]`, "Shelter B", "County Health"}
	if strings.Join(d.Participants, "|") != strings.Join(want, "|") {
		t.Errorf("participants = %q, want %q", d.Participants, want)
	}
	// 1 to both shelters, 2a and 2b back to Net Control, 3 to County Health.
	if len(d.Arrows) != 5 {
		t.Fatalf("got %d arrows, want 5: %+v", len(d.Arrows), d.Arrows)
	}
	if a := d.Arrows[2]; a.From != 1 || a.To != 0 || !a.Reply || !strings.HasPrefix(a.Label, "2a. plain text message, reply to 1") {
		t.Errorf("arrow 3 = %+v", a)
	}
	if a := d.Arrows[4]; a.From != 2 || a.To != 3 || !strings.HasPrefix(a.Label, "3. ") {
		t.Errorf("arrow 5 = %+v", a)
	}

	uml := d.PlantUML("Winter\nstorm")
	for _, s := range []string{
		"@startuml\n", "title Training scenario, 03/14/2026\n", "caption Winter storm\n",
		`participant "Shelter 'A' [F3]" as P1`,
		"P0 -> P1 : 1. plain text message to All Stations: request status\n",
		"P1 --> P0 : 2a. plain text message, reply to 1\n",
		"@enduml\n",
	} {
		if !strings.Contains(uml, s) {
			t.Errorf("PlantUML lacks %q:\n%s", s, uml)
		}
	}

	if _, err := FlowDiagram(Flow{Parties: fl.Parties, Messages: []FlowMessage{{MsgType: "plain", From: 9}}}); err == nil {
		t.Error("an invalid flow should be rejected")
	}
}
