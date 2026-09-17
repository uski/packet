package genmsg

import (
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/incident"
)

func msgNoFlow() Flow {
	return Flow{
		Parties: []FlowParty{
			{Role: "Net Control", Prefix: "XND"},
			{Role: "Shelter", Prefix: "S21"},
			{Role: "Shelter", Prefix: "S22"},
		},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 1, To: 0}, // auto: S21-101
			{MsgType: "plain", From: 0, To: -1, ToLabel: "All Stations", MsgNo: "xnd-108"},
			{MsgType: "plain", From: 1, To: 0, MsgNo: "s21-102"},
			{MsgType: "plain", From: 2, To: 0, MsgNo: "S22-102"},
			{MsgType: "plain", From: 2, To: 0, MsgNo: "S22-101R"}, // suffix kept
			{MsgType: "plain", From: 1, To: 0},                    // auto: skips 102
		},
	}
}

func TestFlowMessageNumbers(t *testing.T) {
	specs, err := ResolveFlow(msgNoFlow())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range specs {
		got = append(got, s.MsgNo)
	}
	if want := "|XND-108P|S21-102P|S22-102P|S22-101R|"; strings.Join(got, "|") != want {
		t.Errorf("numbers = %q, want %q", strings.Join(got, "|"), want)
	}

	for name, change := range map[string]func(*Flow){
		"malformed":      func(fl *Flow) { fl.Messages[1].MsgNo = "XND101" },
		"sequence alone": func(fl *Flow) { fl.Messages[1].MsgNo = "108" },
		"short sequence": func(fl *Flow) { fl.Messages[1].MsgNo = "XND-18" },
		"other prefix":   func(fl *Flow) { fl.Messages[1].MsgNo = "S21-108" },
		"each station":   func(fl *Flow) { fl.Messages[1].From = fromEachStation },
		"no prefix":      func(fl *Flow) { fl.Parties[1].Prefix = "" },
		"duplicate":      func(fl *Flow) { fl.Messages[4].MsgNo = "S22-102" },
	} {
		fl := msgNoFlow()
		change(&fl)
		if _, err := ResolveFlow(fl); err == nil {
			t.Errorf("%s: should be rejected", name)
		}
	}

	d, err := FlowDiagram(msgNoFlow())
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, it := range d.Items {
		if len(it.Arrows) > 0 {
			labels = append(labels, it.Arrows[0].Label)
		}
	}
	if want := "S21-101|XND-108|S21-102|S22-102|S22-101R|S21-103"; strings.Join(labels, "|") != want {
		t.Errorf("diagram labels = %q, want %q", strings.Join(labels, "|"), want)
	}
}

func TestApplyMessageNumbers(t *testing.T) {
	specs, err := ResolveFlow(msgNoFlow())
	if err != nil {
		t.Fatal(err)
	}
	results := make([]Result, len(specs))
	for i := range results {
		results[i].Values = plainValues("Status.")
	}
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "XND-100P"
		i.Config.RxMessageID = "XND-100P"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A deleted message's number is free again.
	if err := incident.Write(dir, func(i *incident.Incident) error {
		old, err := Apply(i, specs[:1], results[:1])
		if err != nil {
			return err
		}
		return i.DeleteMessage(i.GetLogEntryByIdent(old[0].Ident))
	}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := incident.Write(dir, func(i *incident.Incident) error {
		applied, err := Apply(i, specs, results)
		for _, a := range applied {
			ids = append(ids, a.ID)
		}
		if i.Config.TxMessageID != "XND-109P" || i.Config.RxMessageID != "XND-109P" {
			t.Errorf("the incident's next number is %s/%s, want XND-109P past the scenario's XND-108", i.Config.TxMessageID, i.Config.RxMessageID)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if want := "S21-101P|XND-108P|S21-102P|S22-102P|S22-101R|S21-103P"; strings.Join(ids, "|") != want {
		t.Errorf("numbers = %q, want %q", strings.Join(ids, "|"), want)
	}
	// Numbers already in the incident can't be given again.
	if err := incident.Write(dir, func(i *incident.Incident) error {
		_, err := Apply(i, specs[1:2], results[1:2])
		return err
	}); err == nil || !strings.Contains(err.Error(), "XND-108P is already used") {
		t.Errorf("reusing a number: err = %v", err)
	}
}
