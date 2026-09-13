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
