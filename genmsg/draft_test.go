package genmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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

func specsByTag(specs []fieldSpec) map[string]fieldSpec {
	m := make(map[string]fieldSpec, len(specs))
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
	specs, _ := promptFields(draft, nil)
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

func TestShelterRequiredCheckboxGroupIsSurfaced(t *testing.T) {
	mt := formType(t, "Shelter")
	inc := draftTestIncident(t)
	spec := MessageSpec{MsgType: mt}
	draft, err := buildDraft(inc, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	specs, _ := promptFields(draft, nil)
	var group []fieldSpec
	for _, s := range specs {
		if s.Group == "24. Type" {
			group = append(group, s)
		}
	}
	if len(group) != 5 {
		t.Fatalf("expected the 5 checkboxes of the required \"24. Type\" group to be marked, got %+v", group)
	}
	for _, s := range group {
		if s.Optional {
			t.Errorf("checkbox %q of a required group should not be optional", s.Tag)
		}
	}
	if labels := fieldLabels(problemSpecs(draft)); !slices.Contains(labels, "24. Type") {
		t.Errorf("the unchecked required group should be reported once by its label, got %v", labels)
	}
	prompt := buildPrompt(Request{Messages: []MessageSpec{spec}}, "", [][]fieldSpec{specs}, make([]Result, 1), 0, messagePlan{}, make([]map[prowords.Category]string, 1), make([]int, 1), false)
	if !strings.Contains(prompt, `"24. Type": check AT LEAST ONE of these checkboxes`) {
		t.Errorf("prompt should ask for at least one checkbox of the group:\n%s", prompt)
	}

	draft, err = buildDraft(inc, spec, map[string]string{group[0].Tag: "checked"})
	if err != nil {
		t.Fatal(err)
	}
	if labels := fieldLabels(problemSpecs(draft)); slices.Contains(labels, "24. Type") {
		t.Errorf("checking one checkbox should satisfy the group, still reported: %v", labels)
	}
}

func TestLongFormMustCheckOneCheckbox(t *testing.T) {
	mt := formType(t, "CPODSite")
	inc := draftTestIncident(t)
	spec := MessageSpec{MsgType: mt}
	draft, err := buildDraft(inc, spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	specs, _ := promptFields(draft, nil)
	if n := countCheckboxes(specs); n < manyCheckboxes {
		t.Fatalf("CPOD site form has %d checkboxes, want at least %d", n, manyCheckboxes)
	}
	if anyChecked(draft, specs) {
		t.Fatal("a new draft should have no checkbox checked")
	}
	checked := false
	for _, s := range specs {
		if !isCheckbox(s) {
			continue
		}
		d, err := buildDraft(inc, spec, map[string]string{s.Tag: "checked"})
		if err != nil {
			t.Fatal(err)
		}
		if anyChecked(d, specs) {
			checked = true
			break
		}
	}
	if !checked {
		t.Error("checking a checkbox should be detected")
	}
	prompt := buildPrompt(Request{Messages: []MessageSpec{spec}}, "", [][]fieldSpec{specs}, make([]Result, 1), 0, messagePlan{CheckOne: true, CheckOneUnmet: true}, make([]map[prowords.Category]string, 1), make([]int, 1), true)
	if !strings.Contains(prompt, "NOT MET IN YOUR PREVIOUS VERSION: Check at least one checkbox") {
		t.Errorf("revision prompt should flag the unchecked checkbox requirement:\n%s", prompt)
	}
	if !strings.Contains(prompt, `[checkbox: give "checked" to check it]`) {
		t.Errorf("checkboxes should be described as checkboxes, not dropdowns:\n%s", prompt)
	}
}

func TestBuildDraftClearsIncidentDefaultBody(t *testing.T) {
	mt := formType(t, "ICS213")
	var inc *incident.Incident
	if err := incident.Create(t.TempDir(), func(i *incident.Incident) error {
		i.Config.DefaultBody = "Template body for hand-written messages"
		inc = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	draft, err := buildDraft(inc, MessageSpec{MsgType: mt}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := FindField(draft, "12.").Value(draft); got != "" {
		t.Errorf("message body = %q, want the incident's default body cleared", got)
	}
	specs, _ := promptFields(draft, nil)
	if s, ok := specsByTag(specs)["12."]; !ok || s.Optional {
		t.Error("the required message body should be a MUST field for Claude, not hidden as already filled")
	}
}

// TestGenerateShortensLongSubject covers Claude putting the message's
// details in its subject line.
func TestGenerateShortensLongSubject(t *testing.T) {
	const longSubject = "Shelter one is operational with forty five guests and twelve spare cots"
	var calls atomic.Int32
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req claudeRequest
		json.NewDecoder(r.Body).Decode(&req)
		prompts = append(prompts, req.Messages[0].Text())
		if calls.Add(1) == 1 {
			writeClaudeText(w, `[{"subjectHandling":"ROUTINE","subjectSummary":"`+longSubject+`","defaultBody":"`+f3Body+`"}]`)
			return
		}
		writeClaudeText(w, f3PlainJSON)
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
		t.Fatalf("expected 2 calls (draft, then shorten the subject), got %d", calls.Load())
	}
	if !strings.Contains(prompts[0], "a short title of at most 8 words") {
		t.Errorf("the subject field should be described as a short title:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[1], "make it a short title") {
		t.Errorf("revision prompt should ask for a shorter subject:\n%s", prompts[1])
	}
	if got := results[0].Values["subjectSummary"]; got != "Road closure" {
		t.Errorf("subject = %q, want the shortened one", got)
	}
}

func TestBuildDraftClearsOperatorSection(t *testing.T) {
	mt := formType(t, "ICS213")
	var inc *incident.Incident
	if err := incident.Create(t.TempDir(), func(i *incident.Incident) error {
		i.Config.OpCall, i.Config.OpName = "KN6YUY", "Evaluator"
		inc = i
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	draft, err := buildDraft(inc, MessageSpec{MsgType: mt}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for f := range draft.Fields() {
		if (operatorCommon[f.Common()] || strings.HasPrefix(f.Label(), "Operator")) && f.Value(draft) != "" {
			t.Errorf("Radio Operator field %q = %q, want it empty on a generated message", f.Label(), f.Value(draft))
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
	specs := [][]fieldSpec{{
		{Tag: "23n.", Label: "Item 1: Item Name", Required: true},
		{Tag: "24n.", Label: "Item 2: Item Name", Optional: true},
		{Tag: "60.", Label: "Comments", Multiline: true, Optional: true},
	}}
	prompt := buildPrompt(req, "", specs, make([]Result, 1), 0, messagePlan{}, make([]map[prowords.Category]string, 1), make([]int, 1), false)
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
	if !strings.Contains(prompt, "never Title Case ordinary words") {
		t.Errorf("prompt should tell Claude not to capitalize ordinary words:\n%s", prompt)
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
	specs, _ := promptFields(draft, nil)
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
	find := func(specs []fieldSpec, tag string) *fieldSpec {
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
func TestBuildDraftIncidentDate(t *testing.T) {
	mt := sitRepType(t)
	draft, err := buildDraft(draftTestIncident(t), MessageSpec{MsgType: mt, Date: "03/14/2026"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 1a./1b. are the message date and time; 20d./20t. the prepared ones.
	for tag, want := range map[string]string{"1a.": "03/14/2026", "20d.": "03/14/2026", "1b.": "", "20t.": ""} {
		if got := FindField(draft, tag).Value(draft); got != want {
			t.Errorf("field %q = %q, want %q", tag, got, want)
		}
	}
	for _, p := range problemSpecs(draft) {
		if p.DateTime {
			t.Errorf("a blank time field shouldn't be reported for revision, got %+v", p)
		}
	}
	specs, _ := promptFields(draft, nil)
	for _, s := range specs {
		if s.DateTime {
			t.Errorf("date/time field %q shouldn't be offered to Claude", s.Tag)
		}
	}
}

func TestGenerateTrimsOverBudgetMessage(t *testing.T) {
	// Satisfies every f3-profile category on its own, in about 40 words.
	const body = `Please call KJ6ABC at 408-555-1212 or email kj6abc@xanadu-city.org ` +
		`about the Kaczmarek Street closure near model A123, use gate #4 within 2.5 miles; ` +
		`confirm ETA by 1500 & initials J.R. Total 5 units, cost is $250. This is drill traffic.`
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
			repairPrompt = req.Messages[0].Text()
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

func TestHandlingIsTheTools(t *testing.T) {
	inc := draftTestIncident(t)
	for _, tag := range []string{"ICS213", "plain"} {
		mt := formType(t, tag)
		spec := MessageSpec{MsgType: mt, Handling: "I"}
		draft, err := buildDraft(inc, spec, map[string]string{"handling": "ROUTINE", "subjectHandling": "ROUTINE"})
		if err != nil {
			t.Fatal(err)
		}
		var n int
		for f := range draft.Fields() {
			if handlingCommon[f.Common()] {
				n++
				if v := f.Value(draft); v != "IMMEDIATE" {
					t.Errorf("%s %s = %q, want IMMEDIATE", tag, f.Common(), v)
				}
			}
		}
		if n == 0 {
			t.Errorf("%s has no handling field", tag)
		}
	}
	if normalizeHandling(" priority ") != "P" || normalizeHandling("x") != "" {
		t.Error("normalizeHandling")
	}
}

func TestMessageTime(t *testing.T) {
	inc := draftTestIncident(t)
	mt := formType(t, "ICS213")
	timeOf := func(spec MessageSpec) (msgTime, opTime string) {
		t.Helper()
		d, err := buildDraft(inc, spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		return commonValue(d, "messageTime"), commonValue(d, "operatorTime")
	}
	if m, op := timeOf(MessageSpec{MsgType: mt, Date: "09/26/2026", Time: "14:30"}); m != "14:30" || op != "" {
		t.Errorf("with a time, message time = %q and operator time = %q, want 14:30 and blank", m, op)
	}
	if m, _ := timeOf(MessageSpec{MsgType: mt, Date: "09/26/2026"}); m != "" {
		t.Errorf("without a time, message time = %q, want blank", m)
	}

	for in, want := range map[string]string{"": "", "9:05": "09:05", "0905": "09:05", " 23:59 ": "23:59"} {
		if got, err := normalizeTime(in); err != nil || got != want {
			t.Errorf("normalizeTime(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"24:00", "12:60", "noon", "1:2"} {
		if _, err := normalizeTime(in); err == nil {
			t.Errorf("normalizeTime(%q) should fail", in)
		}
	}

	fl := Flow{
		Parties:  []FlowParty{{Role: "Net Control"}, {Role: "Shelter"}},
		Messages: []FlowMessage{{MsgType: "ICS213", From: 0, To: 1, Time: "915"}},
	}
	specs, err := ResolveFlow(fl)
	if err != nil || specs[0].Time != "09:15" {
		t.Errorf("ResolveFlow time = %q, %v; want 09:15", specs[0].Time, err)
	}
	if !strings.Contains(buildBriefPrompt(Request{Messages: specs}), "written at 09:15") {
		t.Error("the brief should give the message's time")
	}
	fl.Messages[0].Time = "25:00"
	if _, err := ResolveFlow(fl); err == nil {
		t.Error("an invalid time should be rejected")
	}
}

// TestICSPositionsComeFromTheForm verifies that a message's To ICS Position
// is one of the positions its form names, matching the party's role when it
// fits, and that a form naming none takes the role as it is.
func TestICSPositionsComeFromTheForm(t *testing.T) {
	inc := draftTestIncident(t)
	formType(t, "ICS213")
	position := func(tag, to string) string {
		t.Helper()
		mt, ok := FindMsgType(tag)
		if !ok {
			t.Skipf("%s not registered", tag)
		}
		draft, err := buildDraft(inc, MessageSpec{MsgType: mt, From: "Shelter Manager", To: to}, nil)
		if err != nil {
			t.Fatal(err)
		}
		return commonValue(draft, "toICSPosition")
	}
	for _, tc := range []struct{ tag, to, want string }{
		// The ICS-213 names no positions, so the party's role stands.
		{"ICS213", "Net Control", "Net Control"},
		{"ICS213", "All Stations", "All Stations"},
		// These forms do, so one of theirs is used: the role when it
		// fits, and the form's own unit when it doesn't.
		{"Shelter", "Net Control", "Care & Shelter Unit"},
		{"Shelter", "Care & Shelter Unit", "Care & Shelter Unit"},
		{"Shelter", "operations", "Operations"},
		{"RoadCl", "Net Control", "Public Works Group"},
		{"ResReq", "Logistics", "Logistics"},
		// A shared structural word is not a match ("Unit" is not a
		// position), so the form's own comes through.
		{"RACES-MAR", "Care & Shelter Unit", "RACES Chief Radio Officer"},
	} {
		if got := position(tc.tag, tc.to); got != tc.want {
			t.Errorf("%s to %q = %q, want %q", tc.tag, tc.to, got, tc.want)
		}
	}
	// The sender's own position is the party's role: no form suggests one.
	mt, _ := FindMsgType("Shelter")
	draft, err := buildDraft(inc, MessageSpec{MsgType: mt, From: "Shelter Manager", To: "Net Control"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := commonValue(draft, "fromICSPosition"); got != "Shelter Manager" {
		t.Errorf("from = %q, want the party's role", got)
	}
}
