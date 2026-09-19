package genmsg

import (
	"strconv"
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/incident"
)

func TestIncidentDiagram(t *testing.T) {
	formType(t, "ICS213")
	fl := sampleFlow()
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	ics := map[string]string{"10.": "Status", "12.": f3Body}
	results := make([]Result, len(specs))
	for i := range results {
		results[i].Values = ics
	}
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "YUY-100P"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := incident.Write(dir, func(i *incident.Incident) error {
		if _, err := Apply(i, specs, results); err != nil {
			return err
		}
		d, err := IncidentDiagram(i, "Evaluation Net")
		got = d.SequenceDiagram()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// The same as the scenario's diagram: the messages have the numbers,
	// types, and times it shows.
	scenario, err := FlowDiagram(fl)
	if err != nil {
		t.Fatal(err)
	}
	if want := scenario.SequenceDiagram(); got != want {
		t.Errorf("incident diagram:\n%s\nscenario diagram:\n%s", got, want)
	}
}

func TestFlowOrder(t *testing.T) {
	rec := func(batch string, step int) *reportMessage {
		return &reportMessage{id: batch + strconv.Itoa(step), record: &TrainingRecord{Batch: batch, Step: step}}
	}
	other := &reportMessage{id: "x"}
	msgs := []*reportMessage{rec("a", 3), other, rec("b", 2), rec("a", 1), rec("b", 1), rec("a", 2)}
	var ids []string
	for _, rm := range flowOrder(msgs) {
		ids = append(ids, rm.id)
	}
	if got := strings.Join(ids, " "); got != "a1 a2 a3 x b1 b2" {
		t.Errorf("order = %s", got)
	}
}

// TestIncidentDiagramTwoFlows verifies that each flow's closing events are
// drawn after that flow's own messages: they all used to be collected and
// drawn at the very end, so the first flow's "net closed" landed below the
// second flow's traffic.
func TestIncidentDiagramTwoFlows(t *testing.T) {
	formType(t, "ICS213")
	flow := func(name string) Flow {
		return Flow{
			Parties: []FlowParty{{Role: "NCO", Prefix: "XND", Credential: "N3"}, {Role: "Shelter", Prefix: "S21"}},
			Messages: []FlowMessage{
				{Event: "open-net", Text: name + " open"},
				{MsgType: "ICS213", From: 0, To: 1, Purpose: name},
				{Event: "net-closed", Text: name + " closed"},
			},
		}
	}
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error { i.Config.TxMessageID = "YUY-100P"; return nil }); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := incident.Write(dir, func(i *incident.Incident) error {
		for _, name := range []string{"First net", "Second net"} {
			specs, err := ResolveFlow(flow(name))
			if err != nil {
				return err
			}
			if _, err = Apply(i, specs, make([]Result, len(specs))); err != nil {
				return err
			}
		}
		d, err := IncidentDiagram(i, "Two nets")
		got = d.SequenceDiagram()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	first, second := strings.Index(got, "First net closed"), strings.Index(got, "Second net open")
	if first < 0 || second < 0 || first > second {
		t.Errorf("the first net should close before the second opens:\n%s", got)
	}
}
