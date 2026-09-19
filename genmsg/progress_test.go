package genmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// TestGenerateReportsProgress verifies that Generate calls its Progress
// callback both between steps and, via the heartbeat, while a slow Claude
// call is still in flight -- the actual bug report this addresses ("no
// progress" during generation).
func TestGenerateReportsProgress(t *testing.T) {
	orig := heartbeatInterval
	heartbeatInterval = 5 * time.Millisecond
	defer func() { heartbeatInterval = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond) // long enough for several heartbeats
		resp := claudeResponse{}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"subjectSummary":"Test","subjectHandling":"ROUTINE","defaultBody":"Body with 5 units and a phone 408-555-1212."}]`}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error { inc = i; return nil }); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var messages []string
	progress := func(s string) {
		mu.Lock()
		messages = append(messages, s)
		mu.Unlock()
	}

	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	_, err := Generate(context.Background(), client, Request{
		Incident: inc,
		Messages: []MessageSpec{{MsgType: message.PlainMessage}},
		Level:    "f3",
		Progress: progress,
	})
	if err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(messages) < 3 {
		t.Fatalf("expected several progress messages (prep, at least one heartbeat, done), got %d: %v", len(messages), messages)
	}
	if messages[0] == "" {
		t.Error("expected a non-empty first progress message")
	}
	if messages[len(messages)-1] != "Done generating messages." {
		t.Errorf("expected final message to be the completion message, got %q", messages[len(messages)-1])
	}
}

// TestGenerateReportsActivities verifies that each parallel activity is
// reported under its own key: every message waiting first, in generation
// order, then working, then done.
func TestGenerateReportsActivities(t *testing.T) {
	orig := heartbeatInterval
	heartbeatInterval = 5 * time.Millisecond
	defer func() { heartbeatInterval = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		resp := claudeResponse{}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"subjectSummary":"Test","subjectHandling":"ROUTINE","defaultBody":"Body with 5 units and a phone 408-555-1212."}]`}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error { inc = i; return nil }); err != nil {
		t.Fatal(err)
	}
	var (
		mu     sync.Mutex
		events []Activity
	)
	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	_, err := Generate(context.Background(), client, Request{
		Incident: inc,
		Messages: []MessageSpec{
			{MsgType: message.PlainMessage, From: "Net Control", FromPrefix: "XND"},
			{MsgType: message.PlainMessage, From: "Shelter", FromPrefix: "S21", ReplyTo: 1},
		},
		Level: "f3",
		Activity: func(a Activity) {
			mu.Lock()
			events = append(events, a)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()

	states := map[string][]string{}
	var keys []string
	for _, a := range events {
		if states[a.Key] == nil {
			keys = append(keys, a.Key)
		}
		if s := states[a.Key]; len(s) == 0 || s[len(s)-1] != a.State {
			states[a.Key] = append(s, a.State)
		}
	}
	if want := []string{"plan", "message-1", "message-2"}; !slices.Equal(keys, want) {
		t.Errorf("activity keys in order = %v, want %v", keys, want)
	}
	if got := states["plan"]; !slices.Equal(got, []string{ActivityWorking, ActivityDone}) {
		t.Errorf("plan states = %v", got)
	}
	for _, key := range []string{"message-1", "message-2"} {
		if got := states[key]; !slices.Equal(got, []string{ActivityWaiting, ActivityWorking, ActivityDone}) {
			t.Errorf("%s states = %v", key, got)
		}
	}
	var sawReplyWait, sawLabel, sawElapsed bool
	for _, a := range events {
		sawReplyWait = sawReplyWait || a.Key == "message-2" && strings.Contains(a.Status, "waiting for message 1")
		// Message 2 is the reply, drafted second in the request but
		// first in generation order; the activity names it as the
		// request does.
		sawLabel = sawLabel || a.Label == "Message 2 of 2: plain text message from Shelter S21"
		sawElapsed = sawElapsed || a.State == ActivityWorking && strings.Contains(a.Status, "drafting (")
	}
	if !sawReplyWait || !sawLabel || !sawElapsed {
		t.Errorf("reply wait %v, label %v, elapsed %v: %+v", sawReplyWait, sawLabel, sawElapsed, events)
	}
}

// TestCheckInsAreSentAsIs verifies that check-in and check-out messages are
// never written by Claude, carry no proword requirements, and count no
// prowords.
func TestCheckInsAreSentAsIs(t *testing.T) {
	checkIn, checkOut := formType(t, "Check-In"), formType(t, "Check-Out")
	var (
		mu      sync.Mutex
		prompts []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []claudeMessage `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		for _, m := range req.Messages {
			prompts = append(prompts, m.Text())
		}
		mu.Unlock()
		resp := claudeResponse{}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"subjectSummary":"Test","subjectHandling":"ROUTINE","defaultBody":"Body with 5 units and a phone 408-555-1212."}]`}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	inc := draftTestIncident(t)
	shelter := func(mt message.EditableMType) MessageSpec {
		return MessageSpec{MsgType: mt, From: "Shelter", FromPrefix: "S21", Level: "full"}
	}
	specs := []MessageSpec{shelter(checkIn), shelter(message.PlainMessage), shelter(checkOut)}
	var activities []Activity
	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	results, err := Generate(context.Background(), client, Request{
		Incident: inc, Messages: specs, Level: "full",
		Activity: func(a Activity) { activities = append(activities, a) },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only the plain text message is written (drafted, then revised, as
	// the canned response misses requirements): no plan is needed for one.
	if len(prompts) == 0 {
		t.Error("the plain text message should be written by Claude")
	}
	for _, p := range prompts {
		if !strings.Contains(p, "Message 2 -- type: a plain text message") || strings.Contains(p, "check-in") || strings.Contains(p, "check-out") {
			t.Errorf("Claude should only be asked for the plain text message, got:\n%s", p)
		}
	}
	for _, i := range []int{0, 2} {
		r := results[i]
		if len(r.Values) != 0 || len(r.Assigned) != 0 || len(r.Counts) != 0 || len(r.Missing) != 0 {
			t.Errorf("message %d should have no content, requirements, or prowords: %+v", i+1, r)
		}
	}
	full, _ := prowords.Profile(prowords.LevelFull)
	if len(results[1].Assigned) != len(full) {
		t.Errorf("the plain text message should carry every requirement, got %v", results[1].Assigned)
	}
	if brief := buildBriefPrompt(Request{Messages: specs}); !strings.Contains(brief, "Message 1: a check-in message (sent as is, with no content to write)") {
		t.Errorf("the brief should say the check-in has no content:\n%s", brief)
	}
	var sawDone bool
	for _, a := range activities {
		sawDone = sawDone || a.Key == "message-1" && a.State == ActivityDone && strings.Contains(a.Status, "nothing to write")
	}
	if !sawDone {
		t.Errorf("the check-in's activity should be done with nothing to write: %+v", activities)
	}

	// Even with a call sign and name filled in, a check-in counts nothing.
	draft, err := buildDraft(inc, specs[0], map[string]string{"tacname": "Shelter Kaczmarek", "taccall": "XSHEL4"})
	if err != nil {
		t.Fatal(err)
	}
	for f := range draft.Fields() {
		if f.Common() == "operatorCall" {
			f.SetValue(draft, "W6XRL4")
		}
	}
	if n := len(messageCounts(draft)); n != 0 {
		t.Errorf("a check-in counted %d prowords", n)
	}
	for _, f := range ProwordFields(draft) {
		if len(f.Matches) != 0 {
			t.Errorf("check-in field %s shows prowords: %v", f.Label, f.Matches)
		}
	}
}

// TestGenerateReportsCutOffPlan verifies that a scenario plan cut off at
// the output limit is reported rather than silently dropped: the messages
// are still written, but they don't share a scenario.
func TestGenerateReportsCutOffPlan(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := claudeResponse{}
		if calls.Add(1) == 1 { // the plan
			resp.StopReason = "max_tokens"
			json.NewEncoder(w).Encode(resp)
			return
		}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"subjectSummary":"Test","subjectHandling":"ROUTINE","defaultBody":"Body."}]`}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	inc := draftTestIncident(t)
	var activities []Activity
	results, err := Generate(context.Background(), &ClaudeClient{APIKey: "k", URL: srv.URL}, Request{
		Incident: inc,
		Messages: []MessageSpec{{MsgType: message.PlainMessage}, {MsgType: message.PlainMessage}},
		Level:    "f3",
		Activity: func(a Activity) { activities = append(activities, a) },
	})
	if err != nil {
		t.Fatalf("a cut-off plan should not fail the generation: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("got %d results, want 2", len(results))
	}
	var reported bool
	for _, a := range activities {
		if a.Key == "plan" && a.State == ActivityFailed && strings.Contains(a.Status, "cut off") {
			reported = true
		}
		if a.Key == "plan" && a.State == ActivityDone {
			t.Error("a cut-off plan should not be reported as done")
		}
	}
	if !reported {
		t.Errorf("the cut-off plan was not reported: %+v", activities)
	}
}

// TestGenerateWithReplyCycle exercises two messages that reply to each
// other, which generationOrder deliberately tolerates: one of them is
// written before the message it answers, so its prompt reads a result that
// another goroutine may still be writing (hence the lock in
// generateMessage). This checks that such a flow completes rather than
// deadlocking; the -race detector will not always see the read itself,
// since a two-message cycle makes one wait for the other.
func TestGenerateWithReplyCycle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Millisecond)
		resp := claudeResponse{}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"subjectSummary":"Test","subjectHandling":"ROUTINE","defaultBody":"Body with 5 units and a phone 408-555-1212."}]`}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	inc := draftTestIncident(t)
	results, err := Generate(context.Background(), &ClaudeClient{APIKey: "k", URL: srv.URL}, Request{
		Incident: inc,
		Messages: []MessageSpec{
			{MsgType: message.PlainMessage, From: "Net Control", ReplyTo: 2},
			{MsgType: message.PlainMessage, From: "Shelter", ReplyTo: 1},
		},
		Level: "f3",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range results {
		if len(r.Values) == 0 {
			t.Errorf("message %d has no values", i+1)
		}
	}
}

// TestCheckInHasNoWarnings verifies that a check-in, whose fields the tool
// deliberately leaves for the candidate, is not reported as missing them.
func TestCheckInHasNoWarnings(t *testing.T) {
	checkIn := formType(t, "Check-In")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a check-in should not be sent to Claude")
	}))
	defer srv.Close()
	results, err := Generate(context.Background(), &ClaudeClient{APIKey: "k", URL: srv.URL}, Request{
		Incident: draftTestIncident(t),
		Messages: []MessageSpec{{MsgType: checkIn, From: "Shelter", FromPrefix: "S21"}},
		Level:    "full",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results[0].MissingFields) != 0 || len(results[0].Missing) != 0 {
		t.Errorf("a check-in reported %v missing fields and %v missing prowords",
			results[0].MissingFields, results[0].Missing)
	}
}
