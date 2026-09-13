package genmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rothskeller/packet/v4/form/formdefs"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

func draftTestIncident(t *testing.T) *incident.Incident {
	t.Helper()
	var inc *incident.Incident
	if err := incident.Create(t.TempDir(), func(i *incident.Incident) error { inc = i; return nil }); err != nil {
		t.Fatal(err)
	}
	return inc
}

func formType(t *testing.T, tag string) message.EditableMType {
	t.Helper()
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	mt, ok := FindMsgType(tag)
	if !ok {
		t.Skipf("%s not registered (build without -tags sccopifo?)", tag)
	}
	return mt
}

func sitRepType(t *testing.T) message.EditableMType { return formType(t, "SitRep") }

func specsByTag(specs []FieldSpec) map[string]FieldSpec {
	m := make(map[string]FieldSpec, len(specs))
	for _, s := range specs {
		m[s.Tag] = s
	}
	return m
}

func TestPromptFieldsResourceRequestOffersEveryItemRow(t *testing.T) {
	mt := formType(t, "ResReq")
	spec := MessageSpec{MsgType: mt, From: "Shelter Manager", FromLocation: "Roosevelt MS", To: "Logistics", ToLocation: "County EOC"}
	draft, err := buildDraft(draftTestIncident(t), spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	specs, _ := PromptFields(draft, nil)
	byTag := specsByTag(specs)
	if s, ok := byTag["23n."]; !ok || s.Optional {
		t.Error("Item 1's name (23n.) is required and should be a MUST field")
	}
	// Item 2 onward is blocked until the row before it is filled, but a
	// message requesting several items needs them.
	for _, tag := range []string{"24n.", "24q.", "28n.", "60."} {
		if s, ok := byTag[tag]; !ok || !s.Optional {
			t.Errorf("field %q should be offered as optional", tag)
		}
	}
	// Filled by the tool itself: the party position and the prepared time.
	for _, tag := range []string{"7a.", "22t.", "OpRelayRcvd", "OpRelaySent"} {
		if _, ok := byTag[tag]; ok {
			t.Errorf("tool-filled field %q should not be offered to Claude", tag)
		}
	}
}

func TestSetFieldValuesKeepsLaterItemRows(t *testing.T) {
	mt := formType(t, "ResReq")
	inc := draftTestIncident(t)
	values := map[string]string{"23n.": "Blankets", "23q.": "50", "24n.": "Generator", "24q.": "1", "25n.": "Cots", "25q.": "20"}
	// Map iteration order is random, so repeat to catch order dependence.
	for range 20 {
		draft, err := buildDraft(inc, MessageSpec{MsgType: mt}, values)
		if err != nil {
			t.Fatal(err)
		}
		for tag, want := range values {
			if got := FindField(draft, tag).Value(draft); got != want {
				t.Fatalf("field %q = %q, want %q", tag, got, want)
			}
		}
	}
}

func TestBuildPromptSeparatesOptionalFields(t *testing.T) {
	req := Request{Messages: []MessageSpec{{MsgType: message.PlainMessage}}}
	specs := [][]FieldSpec{{
		{Tag: "23n.", Label: "Item 1: Item Name", Required: true},
		{Tag: "24n.", Label: "Item 2: Item Name", Optional: true},
		{Tag: "60.", Label: "Comments", Multiline: true, Optional: true},
	}}
	prompt := buildPrompt(req, "", specs, make([]Result, 1), []int{0}, []MessagePlan{{}}, make([]map[prowords.Category]string, 1), make([]int, 1), false)
	must := strings.Index(prompt, "Fields you MUST fill in")
	other := strings.Index(prompt, "Other fields on this form")
	if must < 0 || other < must {
		t.Fatalf("expected a MUST section followed by an optional section:\n%s", prompt)
	}
	if i := strings.Index(prompt, `"23n."`); i < must || i > other {
		t.Errorf("required field should be listed in the MUST section:\n%s", prompt)
	}
	for _, tag := range []string{`"24n."`, `"60."`} {
		if i := strings.Index(prompt, tag); i < other {
			t.Errorf("optional field %s should be listed in the optional section:\n%s", tag, prompt)
		}
	}
	if !strings.Contains(prompt, "each requested item in its own item row") {
		t.Errorf("prompt should tell Claude to fill form fields by their meaning:\n%s", prompt)
	}
}

func TestBuildDraftFillsRequiredDateTimes(t *testing.T) {
	mt := sitRepType(t)
	draft, err := buildDraft(draftTestIncident(t), MessageSpec{MsgType: mt}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 1b. is the message time; 20t. the prepared time. Neither is editable
	// on its own nor set by the incident's defaults.
	for _, tag := range []string{"1b.", "20t."} {
		f := FindField(draft, tag)
		if f == nil {
			t.Fatalf("SitRep has no field %q", tag)
		}
		if v := f.Value(draft); v == "" || f.Validate(draft, f, 0) != nil {
			t.Errorf("required time field %q = %q, want a valid time", tag, v)
		}
	}
}

func TestPromptFieldsSitRepCommentsAreOptional(t *testing.T) {
	mt := sitRepType(t)
	draft, err := buildDraft(draftTestIncident(t), MessageSpec{MsgType: mt}, nil)
	if err != nil {
		t.Fatal(err)
	}
	specs, _ := PromptFields(draft, nil)
	byTag := specsByTag(specs)
	if s, ok := byTag["22."]; !ok || s.Optional {
		t.Error("required Incident Name (22.) should be a MUST field")
	}
	for _, tag := range []string{"25.", "26.", "70b."} {
		if s, ok := byTag[tag]; !ok || !s.Optional {
			t.Errorf("optional free-text field %q should be offered as optional, not forced", tag)
		}
	}
}

func TestProblemSpecsReportsEmptyRequiredFields(t *testing.T) {
	mt := sitRepType(t)
	inc := draftTestIncident(t)
	spec := MessageSpec{MsgType: mt}
	find := func(specs []FieldSpec, tag string) *FieldSpec {
		for i := range specs {
			if specs[i].Tag == tag {
				return &specs[i]
			}
		}
		return nil
	}

	draft, err := buildDraft(inc, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	problems := problemSpecs(draft)
	if p := find(problems, "22."); p == nil || p.Problem == "" {
		t.Errorf("expected empty required Incident Name (22.) to be reported with its problem, got %+v", problems)
	}
	if find(problems, "20t.") != nil {
		t.Error("the automatically filled prepared time should not be reported")
	}

	draft, err = buildDraft(inc, spec, map[string]string{"22.": "Winter Storm"})
	if err != nil {
		t.Fatal(err)
	}
	if find(problemSpecs(draft), "22.") != nil {
		t.Error("Incident Name should no longer be reported once filled")
	}
}

func TestMessageWordCount(t *testing.T) {
	inc := draftTestIncident(t)
	spec := MessageSpec{MsgType: message.PlainMessage}
	empty, err := buildDraft(inc, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	filled, err := buildDraft(inc, spec, map[string]string{"defaultBody": "Main St closed near 5th."})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := messageWordCount(filled)-messageWordCount(empty), 5; got != want {
		t.Errorf("adding a 5-word body added %d words, want %d", got, want)
	}
}

// TestGenerateTrimsOverBudgetMessage verifies that a message well over the
// word budget is sent back to Claude to be shortened.
func TestGenerateTrimsOverBudgetMessage(t *testing.T) {
	// Satisfies every f3-profile category on its own, in about 40 words.
	const body = `Please call KJ6ABC at 408-555-1212 or email kj6abc@xanadu-city.org ` +
		`about the Kaczmarek Street closure near model A123, use channel #4 at 146.595 MHz; ` +
		`confirm ETA by 1500, initials J.R. Total 5 units, cost is $250. This is drill traffic.`
	padding := strings.Repeat(" more", 40)

	var calls atomic.Int32
	var repairPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req claudeRequest
		json.NewDecoder(r.Body).Decode(&req)
		text := body
		if calls.Add(1) == 1 {
			text += padding
		} else {
			repairPrompt = req.Messages[0].Content
		}
		resp := claudeResponse{}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"subjectHandling":"ROUTINE","subjectSummary":"Road closure","defaultBody":"` + text + `"}]`}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	results, err := Generate(context.Background(), client, Request{
		Incident: draftTestIncident(t),
		Messages: []MessageSpec{{MsgType: message.PlainMessage}},
		Level:    "f3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 Claude calls (draft, then trim), got %d", calls.Load())
	}
	if !strings.Contains(repairPrompt, "cut it to at most") {
		t.Errorf("repair prompt did not ask for the message to be shortened:\n%s", repairPrompt)
	}
	if w := results[0].Words; w == 0 || w > MaxWords+WordTolerance {
		t.Errorf("final message is %d words, want between 1 and %d", w, MaxWords+WordTolerance)
	}
}
