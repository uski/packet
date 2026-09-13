package genmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
)

// TestGenerateRepairsOmittedRequiredField verifies the actual bug this fixes:
// if Claude's response leaves a required field out entirely -- even though
// it already satisfies every assigned proword category, so nothing else
// would trigger a repair round -- Generate must still notice the field is
// required and empty, ask again specifically for it, and end up with it
// filled in the final Result.
func TestGenerateRepairsOmittedRequiredField(t *testing.T) {
	// This body satisfies every f3-profile proword category on its own
	// (verified separately against prowords.CountFields), so the only
	// reason a second round should be needed is the omitted required
	// "subjectSummary" field.
	const body = `Please call KJ6ABC at 408-555-1212 or email kj6abc@xanadu-city.org ` +
		`about the Kaczmarek Street closure near model A123, use channel #4 at 146.595 MHz; ` +
		`confirm ETA by 1500 & initials J.R. Total 5 units, cost is $250. This is drill traffic.`

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		var reqBody struct {
			Messages []claudeMessage `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&reqBody)
		prompt := reqBody.Messages[0].Content

		var text string
		if n == 1 {
			// Deliberately omit the required subjectSummary field.
			text = `[{"subjectHandling":"ROUTINE","defaultBody":"` + body + `"}]`
		} else {
			// A real repair response should have been told exactly
			// which required field it must fill in this time.
			if !strings.Contains(prompt, "REQUIRED") || !strings.Contains(prompt, "subjectSummary") {
				t.Errorf("round %d prompt did not call out the missing required field:\n%s", n, prompt)
			}
			text = `[{"subjectHandling":"ROUTINE","subjectSummary":"Road closure","defaultBody":"` + body + `"}]`
		}
		resp := claudeResponse{}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: text}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error { inc = i; return nil }); err != nil {
		t.Fatal(err)
	}

	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	results, err := Generate(context.Background(), client, Request{
		Incident: inc,
		Messages: []MessageSpec{{MsgType: message.PlainMessage}},
		Level:    "f3",
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected at least 2 Claude calls (initial + repair for the omitted required field), got %d", calls.Load())
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := results[0].Values["subjectSummary"]; got == "" {
		t.Error("subjectSummary should have been filled in by the repair round, but is still empty")
	}
	if len(results[0].MissingFields) != 0 {
		t.Errorf("expected no missing required fields in the final result, got %v", results[0].MissingFields)
	}
}
