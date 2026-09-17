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
	if d.Title() != "2026-03-14 Training scenario" {
		t.Errorf("title = %q", d.Title())
	}
	var labels []string
	for _, p := range d.Participants {
		labels = append(labels, p.Label)
	}
	want := []string{"Net Control EOC", `Shelter "A" [F3]`, "Shelter B", "County Health"}
	if strings.Join(labels, "|") != strings.Join(want, "|") {
		t.Errorf("participants = %q, want %q", labels, want)
	}
	// 1 to both shelters (linear), 2a and 2b back to Net Control, 3 to County Health.
	if len(d.Items) != 4 {
		t.Fatalf("got %d items, want 4: %+v", len(d.Items), d.Items)
	}
	if it := d.Items[0]; it.Block != "linear" || len(it.Arrows) != 2 || it.Arrows[0].Label != "EOC-101" {
		t.Errorf("item 1 = %+v", it)
	}
	if a := d.Items[1].Arrows[0]; a.From != 1 || a.To != 0 || !a.Reply || a.Label != "#2a" || !strings.HasPrefix(a.Detail, "2a. plain text message, reply to EOC-101") {
		t.Errorf("item 2 = %+v", a)
	}
	if a := d.Items[3].Arrows[0]; a.From != 2 || a.To != 3 || !strings.HasPrefix(a.Detail, "3. ") {
		t.Errorf("item 4 = %+v", a)
	}

	uml := d.PlantUML("Winter\nstorm")
	for _, s := range []string{
		"@startuml\n", "title 2026-03-14 Training scenario\n", `caption Winter\nstorm` + "\n",
		`participant "Shelter 'A' [F3]" as P1`,
		`P0 -> P1 : EOC-101\n1. plain text message to All Stations: request\nstatus` + "\n",
		`P1 --> P0 : #2a\n2a. plain text message, reply to EOC-101` + "\n",
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

// sampleFlow is a scenario in the style of an SCCo evaluation net: an
// all-stations request, and a prioritization drill.
func sampleFlow() Flow {
	return Flow{
		Date: "2026-09-26",
		Name: "Evaluation Net",
		Parties: []FlowParty{
			{Role: "NCO", Prefix: "XND", Credential: "N3", Principal: "NetMgr"},
			{Role: "Shelter", Prefix: "S21", Credential: "F3", Principal: "FieldMgr"},
			{Role: "Shelter", Prefix: "S22", Credential: "F2", Principal: "FieldMgr"},
		},
		Messages: []FlowMessage{
			{Event: "open-net"},
			{Event: "check-ins"},
			{Event: "note", Text: "XND-101 is an all-stations 213\nrequesting a shelter form"},
			{MsgType: "ICS213", From: 0, To: -1, ToLabel: "All Stations"},
			{Event: "note", Text: "Prioritization drill"},
			{MsgType: "ICS213", From: fromEachStation, To: 0, Handling: "R", Group: 1},
			{MsgType: "ICS213", From: fromEachStation, To: 0, Handling: "P", Group: 1, ReplyTo: 4},
			{MsgType: "ICS213", From: 1, To: 2, OpToOp: true},
			{Event: "closing"},
			{Event: "check-outs"},
			{Event: "net-closed"},
		},
	}
}

func TestSequenceDiagram(t *testing.T) {
	formType(t, "ICS213")
	d, err := FlowDiagram(sampleFlow())
	if err != nil {
		t.Fatal(err)
	}
	got := d.SequenceDiagram()
	want := `title 2026-09-26 Evaluation Net

rparticipant NetMgr
participant NCO XND
participant Shelter S21
participant Shelter S22
rparticipant FieldMgr

note over NCO XND: Open Net
linear on
Shelter S21->NCO XND: Check In
Shelter S22->NCO XND: Check In
linear off

entryspacing 2
note over NetMgr,FieldMgr: XND-101 is an all-stations 213\nrequesting a shelter form
entryspacing 0.5
NetMgr-->>NCO XND: XND-101
linear on
NCO XND->Shelter S21: XND-101
NCO XND->Shelter S22: XND-101
Shelter S21-->>FieldMgr: XND-101
Shelter S22-->>FieldMgr: XND-101
linear off

entryspacing 2
note over NetMgr,FieldMgr: Prioritization drill
entryspacing 0.5
FieldMgr-->>Shelter S21: S21-101 (R)\nS21-102 (P)
FieldMgr-->>Shelter S22: S22-101 (R)\nS22-102 (P)
parallel on
Shelter S21->NCO XND: S21-102 (P)
NCO XND-->>NetMgr:
parallel off
parallel on
Shelter S22->NCO XND: S22-102 (P)
NCO XND-->>NetMgr:
parallel off
parallel on
Shelter S21->NCO XND: S21-101 (R)
NCO XND-->>NetMgr:
parallel off
parallel on
Shelter S22->NCO XND: S22-101 (R)
NCO XND-->>NetMgr:
parallel off
Shelter S21->Shelter S22: S21-103

entryspacing 2
note over NCO XND: Announce:\nNet is closing
entryspacing 0.5
linear on
Shelter S21->NCO XND: Check Out
Shelter S22->NCO XND: Check Out
linear off
note over NCO XND: Net is closed.
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFlowEvents(t *testing.T) {
	formType(t, "ICS213")
	fl := sampleFlow()
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 6 {
		t.Fatalf("got %d specs, want 6", len(specs))
	}
	if specs[0].Handling != "" || specs[1].Handling != "R" || specs[3].Handling != "P" {
		t.Errorf("handling = %q %q %q", specs[0].Handling, specs[1].Handling, specs[3].Handling)
	}
	// The reply's target is the fourth entry, the first message.
	if specs[3].ReplyTo != 1 {
		t.Errorf("reply to = %d, want 1", specs[3].ReplyTo)
	}
	first, last := specs[0].Training, specs[5].Training
	if len(first.Events) != 3 || first.FromPrincipal != "NetMgr" || first.To[0].Principal != "FieldMgr" || first.Step != 4 {
		t.Errorf("first record = %+v", first)
	}
	if len(last.EventsAfter) != 3 || specs[1].Training.Group != 1 || len(specs[1].Training.Events) != 1 {
		t.Errorf("last record = %+v, second = %+v", last, specs[1].Training)
	}

	report, err := CheckFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if n := report[0].Received.ThirdParty; n != 4 {
		t.Errorf("Net Control received %d messages, want 4 (events aren't traffic)", n)
	}
	completed, _, err := CompleteFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	if !isEvent(completed.Messages[len(fl.Messages)-1]) {
		t.Error("CompleteFlow should keep the events")
	}

	for _, bad := range []FlowMessage{
		{Event: "party"},
		{Event: "note"},
		{MsgType: "ICS213", From: 1, To: 0, ReplyTo: 1},
		{MsgType: "ICS213", From: 1, To: 0, Handling: "X"},
	} {
		fl := sampleFlow()
		fl.Messages = append(fl.Messages, bad)
		if _, err := ResolveFlow(fl); err == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
	fl = sampleFlow()
	fl.Parties[0].Role, fl.Parties[0].Credential = "EOC", ""
	if _, err := ResolveFlow(fl); err == nil {
		t.Error("net events without a Net Control party should be rejected")
	}
	if _, err := ResolveFlow(Flow{Parties: fl.Parties, Messages: []FlowMessage{{Event: "note", Text: "x"}}}); err == nil {
		t.Error("a flow with no messages should be rejected")
	}
}
