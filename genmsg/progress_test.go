package genmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
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
		sawLabel = sawLabel || a.Label == "Message 2 of 2: plain text message from Shelter S21"
		sawElapsed = sawElapsed || a.State == ActivityWorking && strings.Contains(a.Status, "drafting (")
	}
	if !sawReplyWait || !sawLabel || !sawElapsed {
		t.Errorf("reply wait %v, label %v, elapsed %v: %+v", sawReplyWait, sawLabel, sawElapsed, events)
	}
}
