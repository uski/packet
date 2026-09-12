package genmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
		MsgTypes: []message.EditableMType{message.PlainMessage},
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
