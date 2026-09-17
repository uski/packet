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
