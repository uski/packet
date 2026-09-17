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
	// The same as the scenario's diagram, with the actual numbers.
	for _, want := range []string{
		"title 2026-09-26 Evaluation Net\n",
		"rparticipant NetMgr\nparticipant NCO XND\nparticipant Shelter S21\nparticipant Shelter S22\nrparticipant FieldMgr\n",
		"note over NCO XND: Open Net\nlinear on\nShelter S21->NCO XND: Check In\n",
		"NetMgr-->>NCO XND: XND-101P\nlinear on\nNCO XND->Shelter S21: XND-101P\n",
		"FieldMgr-->>Shelter S21: S21-101P (R)\\nS21-102P (P)\nFieldMgr-->>Shelter S22: S22-101P (R)\\nS22-102P (P)\n",
		"parallel on\nShelter S21->NCO XND: S21-102P (P)\nNCO XND-->>NetMgr:\nparallel off\n",
		"Shelter S21->Shelter S22: S21-103P\n",
		"note over NCO XND: Net is closed.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("diagram lacks %q:\n%s", want, got)
		}
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
