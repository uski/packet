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

	"github.com/rothskeller/packet/v4/message"
)

// f3Body satisfies every f3-profile proword category on its own, in about
// 40 words, so a plain message using it needs no repair round.
const f3Body = `Please call KJ6ABC at 408-555-1212 or email kj6abc@xanadu-city.org ` +
	`about the Kaczmarek Street closure near model A123, use channel #4 at 146.595 MHz; ` +
	`confirm ETA by 1500 & initials J.R. Total 5 units, cost is $250. This is drill traffic.`

const f3PlainJSON = `[{"subjectHandling":"ROUTINE","subjectSummary":"Road closure","defaultBody":"` + f3Body + `"}]`

func writeClaudeText(w http.ResponseWriter, text string) {
	resp := claudeResponse{}
	resp.Content = []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}{{Type: "text", Text: text}}
	json.NewEncoder(w).Encode(resp)
}

func TestGenerationOrderPutsRepliesAfterTheirTargets(t *testing.T) {
	if got, want := generationOrder([]MessageSpec{{ReplyTo: 2}, {}, {ReplyTo: 1}}), []int{1, 0, 2}; !slices.Equal(got, want) {
		t.Errorf("generationOrder = %v, want %v", got, want)
	}
	got := generationOrder([]MessageSpec{{ReplyTo: 2}, {ReplyTo: 1}})
	if len(got) != 2 || got[0] == got[1] {
		t.Errorf("a reply cycle should still yield each message once, got %v", got)
	}
}

// TestGenerateBatchPlansScenarioThenEachMessage is the regression test for
// a batch failing because all of its messages were requested in a single
// response that outgrew the output limit.
func TestGenerateBatchPlansScenarioThenEachMessage(t *testing.T) {
	const brief = "BRIEF: Kaczmarek Street closure."
	var mu sync.Mutex
	var systems, prompts []string
	var maxTokens []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req claudeRequest
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		systems = append(systems, req.System)
		prompts = append(prompts, req.Messages[0].Content)
		maxTokens = append(maxTokens, req.MaxTokens)
		mu.Unlock()
		if req.System == briefSystemPrompt {
			writeClaudeText(w, brief)
		} else {
			writeClaudeText(w, f3PlainJSON)
		}
	}))
	defer srv.Close()

	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	_, err := Generate(context.Background(), client, Request{
		Incident: draftTestIncident(t),
		Messages: []MessageSpec{
			{MsgType: message.PlainMessage, From: "Net Control", To: "All Stations", Purpose: "request status"},
			{MsgType: message.PlainMessage, From: "Shelter A", To: "Net Control", ReplyTo: 1},
			{MsgType: message.PlainMessage, From: "Shelter B", To: "Net Control", ReplyTo: 1},
		},
		Level: "f3",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(prompts) != 4 {
		t.Fatalf("expected 4 Claude calls (brief, then one per message), got %d", len(prompts))
	}
	if systems[0] != briefSystemPrompt || !strings.Contains(prompts[0], "replying to message 1") {
		t.Errorf("first call should plan the brief from the whole flow, got system=%q prompt:\n%s", systems[0], prompts[0])
	}
	for i := 1; i < 4; i++ {
		if systems[i] != systemPrompt || !strings.Contains(prompts[i], brief) || !strings.Contains(prompts[i], "Generate exactly 1 message(s)") {
			t.Errorf("call %d should generate a single message from the brief, got:\n%s", i+1, prompts[i])
		}
		if strings.Contains(prompts[i], "direct reply to Message 1") && !strings.Contains(prompts[i], "already-generated content") {
			t.Errorf("a reply's prompt should include the content of the message it replies to:\n%s", prompts[i])
		}
	}
	for i, n := range maxTokens {
		if n > maxOutputTokens {
			t.Errorf("call %d asked for %d output tokens, want at most %d", i+1, n, maxOutputTokens)
		}
	}
}

func TestGenerateRetriesCutOffResponse(t *testing.T) {
	var calls atomic.Int32
	var retryPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req claudeRequest
		json.NewDecoder(r.Body).Decode(&req)
		if calls.Add(1) == 1 {
			resp := claudeResponse{StopReason: "max_tokens"}
			resp.Content = []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}{{Type: "text", Text: `[{"defaultBody":"`}}
			json.NewEncoder(w).Encode(resp)
			return
		}
		retryPrompt = req.Messages[0].Content
		writeClaudeText(w, "Here is the message:\n"+f3PlainJSON)
	}))
	defer srv.Close()

	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	results, err := Generate(context.Background(), client, Request{
		Incident: draftTestIncident(t),
		Messages: []MessageSpec{{MsgType: message.PlainMessage}},
		Level:    "f3",
	})
	if err != nil {
		t.Fatalf("a cut-off response should be retried, not fail the batch: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", calls.Load())
	}
	if !strings.Contains(retryPrompt, "cut off") {
		t.Errorf("retry prompt should say the previous response was cut off:\n%s", retryPrompt)
	}
	if results[0].Values["defaultBody"] == "" {
		t.Error("expected the retried response's values to be used")
	}
}

// f3PlainJSONNoEmail is f3PlainJSON without its email address, so it
// misses exactly one f3 category.
var f3PlainJSONNoEmail = strings.Replace(f3PlainJSON, " or email kj6abc@xanadu-city.org", "", 1)

func TestGenerateRevisionShowsPreviousVersionAndUnmetRequirement(t *testing.T) {
	var calls atomic.Int32
	var revisionPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req claudeRequest
		json.NewDecoder(r.Body).Decode(&req)
		if calls.Add(1) == 1 {
			writeClaudeText(w, f3PlainJSONNoEmail)
			return
		}
		revisionPrompt = req.Messages[0].Content
		writeClaudeText(w, f3PlainJSON)
	}))
	defer srv.Close()

	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	if _, err := Generate(context.Background(), client, Request{
		Incident: draftTestIncident(t),
		Messages: []MessageSpec{{MsgType: message.PlainMessage}},
		Level:    "f3",
	}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected 2 calls, got %d", calls.Load())
	}
	if !strings.Contains(revisionPrompt, "Your previous version") || !strings.Contains(revisionPrompt, "Kaczmarek Street") {
		t.Errorf("revision prompt should include the previous version to edit:\n%s", revisionPrompt)
	}
	if !strings.Contains(revisionPrompt, "NOT MET IN YOUR PREVIOUS VERSION: Include a plausible email address") {
		t.Errorf("revision prompt should flag the unmet EMAIL ADDRESS requirement:\n%s", revisionPrompt)
	}
	if !strings.Contains(revisionPrompt, "TELEPHONE FIGURES") {
		t.Errorf("revision prompt should still list the requirements already met, so they aren't lost:\n%s", revisionPrompt)
	}
}

func TestGenerateKeepsBestVersionWhenRevisionIsWorse(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			writeClaudeText(w, f3PlainJSONNoEmail)
			return
		}
		writeClaudeText(w, `[{"subjectHandling":"ROUTINE","subjectSummary":"Road closure","defaultBody":"Road closed."}]`)
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
		t.Errorf("expected revising to stop after a revision that made things worse, got %d calls", calls.Load())
	}
	if !strings.Contains(results[0].Values["defaultBody"], "Kaczmarek Street") {
		t.Errorf("expected the better first version to be kept, got %q", results[0].Values["defaultBody"])
	}
	if len(results[0].Missing) != 1 {
		t.Errorf("expected the kept version's single missing category to be reported, got %v", results[0].Missing)
	}
}

func TestGenerateFailsAfterRepeatedUnusableResponses(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeClaudeText(w, "I cannot help with that.")
	}))
	defer srv.Close()

	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	_, err := Generate(context.Background(), client, Request{
		Incident: draftTestIncident(t),
		Messages: []MessageSpec{{MsgType: message.PlainMessage}},
		Level:    "f3",
	})
	if err == nil || !strings.Contains(err.Error(), "no usable response") {
		t.Errorf("expected a \"no usable response\" error, got %v", err)
	}
	if calls.Load() != maxRounds {
		t.Errorf("expected %d attempts, got %d", maxRounds, calls.Load())
	}
}
