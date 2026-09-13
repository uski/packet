package genmsg

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClaudeClientComplete(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Errorf("x-api-key header = %q, want %q", got, "test-key")
		}
		var req claudeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model == "" {
			t.Error("request had no model set")
		}
		resp := claudeResponse{}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"a":"b"}]`}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	text, err := c.Complete(context.Background(), "system", "prompt", 8192)
	if err != nil {
		t.Fatal(err)
	}
	if text != `[{"a":"b"}]` {
		t.Errorf("got %q", text)
	}
}

func TestClaudeClientNoAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	c := &ClaudeClient{}
	if c.HasAPIKey() {
		t.Fatal("expected no API key")
	}
	if _, err := c.Complete(context.Background(), "s", "p", 8192); err != ErrNoAPIKey {
		t.Errorf("got err %v, want ErrNoAPIKey", err)
	}
}

func TestClaudeClientMaxTokensCutOff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := claudeResponse{StopReason: "max_tokens"}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"a":"truncated mid-str`}} // deliberately unparseable, as a real cut-off response would be
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	_, err := c.Complete(context.Background(), "system", "prompt", 123)
	if err == nil {
		t.Fatal("expected an error for a max_tokens stop reason, got nil")
	}
	if !errors.Is(err, ErrOutputCutOff) || !strings.Contains(err.Error(), "123") {
		t.Errorf("expected a clear cut-off error mentioning the token limit, got %q", err.Error())
	}
}

func TestClaudeClientAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"type": "invalid_request_error", "message": "bad model"},
		})
	}))
	defer srv.Close()

	c := &ClaudeClient{APIKey: "k", URL: srv.URL}
	if _, err := c.Complete(context.Background(), "s", "p", 8192); err == nil {
		t.Error("expected error from API error response, got nil")
	}
}
