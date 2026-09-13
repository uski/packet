package genmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// TestGenerateScalesMaxTokensWithBatchSize is a regression test for
// generating a larger multi-party batch failing with "parsing Claude's JSON
// response: unexpected end of JSON input" -- a fixed 8192-token output
// budget was too small for a batch of several messages' worth of JSON, so
// Claude's response was silently cut off mid-JSON. Generate must ask for a
// larger budget as the batch grows.
func TestGenerateScalesMaxTokensWithBatchSize(t *testing.T) {
	var gotMaxTokens int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req claudeRequest
		json.NewDecoder(r.Body).Decode(&req)
		gotMaxTokens = req.MaxTokens
		resp := claudeResponse{}
		var msgs []string
		for range 5 {
			msgs = append(msgs, `{"subjectSummary":"Test","subjectHandling":"ROUTINE","defaultBody":"Body with 5 units and a phone 408-555-1212."}`)
		}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: "[" + strings.Join(msgs, ",") + "]"}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error { inc = i; return nil }); err != nil {
		t.Fatal(err)
	}

	specs := make([]MessageSpec, 5)
	for i := range specs {
		specs[i] = MessageSpec{MsgType: message.PlainMessage}
	}
	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	_, err := Generate(context.Background(), client, Request{
		Incident: inc,
		Messages: specs,
		Level:    "f3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotMaxTokens <= 8192 {
		t.Errorf("max_tokens sent for a 5-message batch was %d, want more than the single-message default of 8192", gotMaxTokens)
	}
}
