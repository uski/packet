package genmsg

import (
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
)

func TestApplyEnsuresDrillTraffic(t *testing.T) {
	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "LOC-100P"
		inc = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// End-to-end: Apply must succeed and produce a usable message ID even
	// though Claude's values (deliberately) don't mention drill traffic.
	results := []Result{{Values: map[string]string{
		"subjectHandling": "ROUTINE",
		"subjectSummary":  "Road closure",
		"defaultBody":     "Main St is closed near 5th.",
	}}}
	applied, err := Apply(inc, []MessageSpec{{MsgType: message.PlainMessage}}, results)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0].ID == "" {
		t.Fatalf("expected one applied message with an ID, got %+v", applied)
	}
}

func TestApplyNumbersAddressesAndReferencesByPrefix(t *testing.T) {
	mt := formType(t, "ICS213")
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "YUY-100P"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Drafts are saved to disk when the incident write completes.
	apply := func(specs []MessageSpec, results []Result) (applied []Applied, log []*incident.LogEntry) {
		t.Helper()
		if err := incident.Write(dir, func(i *incident.Incident) (err error) {
			applied, err = Apply(i, specs, results)
			log = i.Log
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return applied, log
	}
	ics := map[string]string{"5.": "ROUTINE", "10.": "Shelter status", "12.": "Report shelter status."}
	specs := []MessageSpec{
		{MsgType: mt, From: "Shelter Manager", FromPrefix: "S24", To: "Net Control", ToPrefix: "EOC", ReplyTo: 2},
		{MsgType: mt, From: "Net Control", FromPrefix: "EOC", To: "All Stations"},
		{MsgType: message.PlainMessage, FromPrefix: "S24", ToPrefix: "EOC"},
	}
	results := []Result{
		{Values: ics},
		{Values: ics},
		{Values: map[string]string{"subjectHandling": "ROUTINE", "subjectSummary": "Status", "defaultBody": "Status."}},
	}
	applied, log := apply(specs, results)
	// Message 1 replies to message 2, so message 2 is created first, but
	// the results stay in request order.
	for i, want := range []string{"S24-101P", "EOC-101P", "S24-102P"} {
		if applied[i].ID != want {
			t.Errorf("message %d number = %q, want %q", i+1, applied[i].ID, want)
		}
	}
	for _, le := range log {
		if le.LocalMsgID == "S24-101P" && le.ToCall != "EOC" {
			t.Errorf("S24-101P is addressed to %q, want EOC", le.ToCall)
		}
	}
	if err := incident.Write(dir, func(i *incident.Incident) error {
		msg, err := i.GetMessageByLMI("S24-101P")
		if err != nil {
			return err
		}
		if ref := FindFieldByCommon(msg, "reference"); ref == nil || ref.Value(msg) != "EOC-101P" {
			t.Error("reply S24-101P should reference EOC-101P, the message it answers")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	more, _ := apply(specs[2:], results[2:])
	if more[0].ID != "S24-103P" {
		t.Errorf("a later batch should continue the station's numbering, got %q, want S24-103P", more[0].ID)
	}
}

func TestEnsureDrillTrafficAppendsWhenMissing(t *testing.T) {
	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error {
		inc = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	draft := message.PlainMessage.NewDraft().(*message.DraftMessage)
	inc.ApplyDefaults(draft)
	FindField(draft, "defaultBody").SetValue(draft, "Main St is closed near 5th.")

	ensureDrillTraffic(draft)

	body := FindField(draft, "defaultBody").Value(draft)
	if !strings.Contains(strings.ToLower(body), "drill traffic") {
		t.Errorf("expected drill traffic phrase to be appended, got %q", body)
	}
	if !strings.Contains(body, "5th") {
		t.Errorf("expected original content to be preserved, got %q", body)
	}
}

func TestEnsureDrillTrafficNotDuplicated(t *testing.T) {
	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error {
		inc = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	draft := message.PlainMessage.NewDraft().(*message.DraftMessage)
	inc.ApplyDefaults(draft)
	FindField(draft, "defaultBody").SetValue(draft, "Main St is closed. This is drill traffic.")

	ensureDrillTraffic(draft)

	body := FindField(draft, "defaultBody").Value(draft)
	if n := strings.Count(strings.ToLower(body), "drill traffic"); n != 1 {
		t.Errorf("expected exactly one occurrence of the phrase, got %d in %q", n, body)
	}
}

func TestEnsureDrillTrafficFoundInOtherField(t *testing.T) {
	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error {
		inc = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	draft := message.PlainMessage.NewDraft().(*message.DraftMessage)
	inc.ApplyDefaults(draft)
	FindField(draft, "defaultBody").SetValue(draft, "Main St is closed near 5th.")
	FindField(draft, "subjectSummary").SetValue(draft, "This is drill traffic - road closure")

	ensureDrillTraffic(draft)

	body := FindField(draft, "defaultBody").Value(draft)
	if strings.Contains(strings.ToLower(body), "drill traffic") {
		t.Errorf("phrase already present in another field; body should be untouched, got %q", body)
	}
}
