package genmsg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultClaudeAPIURL = "https://api.anthropic.com/v1/messages"
	defaultClaudeModel  = "claude-sonnet-5"
	anthropicVersion    = "2023-06-01"
)

// ErrNoAPIKey is returned when no Anthropic API key is available.
var ErrNoAPIKey = errors.New("no Anthropic API key found; set the ANTHROPIC_API_KEY environment variable")

// ErrOutputCutOff is returned, wrapped, when Claude's response reached the
// output token limit before it finished.
var ErrOutputCutOff = errors.New("Claude's response was cut off at the output token limit")

// maxOutputTokens is the output budget for one call: ample for a scenario
// brief or one message's JSON, since messages are generated one at a time.
const maxOutputTokens = 8192

// ClaudeClient calls the Anthropic Messages API to generate training
// message content. The zero value is usable: it reads its API key from the
// ANTHROPIC_API_KEY environment variable and uses the default model and
// endpoint, both of which can be overridden (the model may also be
// overridden with the PACKET_CLAUDE_MODEL environment variable).
type ClaudeClient struct {
	APIKey string // if empty, read from ANTHROPIC_API_KEY
	Model  string // if empty, defaultClaudeModel (or PACKET_CLAUDE_MODEL)
	URL    string // if empty, defaultClaudeAPIURL
	HTTP   *http.Client
}

func (c *ClaudeClient) apiKey() string {
	if c.APIKey != "" {
		return c.APIKey
	}
	return os.Getenv("ANTHROPIC_API_KEY")
}

func (c *ClaudeClient) model() string {
	if c.Model != "" {
		return c.Model
	}
	if m := os.Getenv("PACKET_CLAUDE_MODEL"); m != "" {
		return m
	}
	return defaultClaudeModel
}

func (c *ClaudeClient) url() string {
	if c.URL != "" {
		return c.URL
	}
	return defaultClaudeAPIURL
}

func (c *ClaudeClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	// Without streaming, response headers only arrive once the whole
	// completion is done, and a large multi-party batch (up to
	// maxTokensFor's cap) can take several minutes to generate.
	return &http.Client{Timeout: 10 * time.Minute}
}

// HasAPIKey reports whether a Claude API key is configured, so callers can
// give a clear error before doing any other work.
func (c *ClaudeClient) HasAPIKey() bool { return c.apiKey() != "" }

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Complete sends system and prompt to Claude as a single-turn conversation,
// allowing up to maxTokens tokens of output, and returns its text response.
// If Claude's response is cut off before finishing (stop_reason
// "max_tokens"), ErrOutputCutOff is returned rather than truncated text.
func (c *ClaudeClient) Complete(ctx context.Context, system, prompt string, maxTokens int) (string, error) {
	key := c.apiKey()
	if key == "" {
		return "", ErrNoAPIKey
	}
	reqBody := claudeRequest{
		Model:     c.model(),
		MaxTokens: maxTokens,
		System:    system,
		Messages:  []claudeMessage{{Role: "user", Content: prompt}},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("encoding Claude request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("building Claude request: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", key)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Claude API: %w", err)
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading Claude response: %w", err)
	}
	var cr claudeResponse
	if jsonErr := json.Unmarshal(respBytes, &cr); jsonErr != nil {
		return "", fmt.Errorf("parsing Claude response (status %s): %w", resp.Status, jsonErr)
	}
	if cr.Error != nil {
		return "", fmt.Errorf("Claude API error (%s): %s", cr.Error.Type, cr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Claude API returned status %s", resp.Status)
	}
	var text strings.Builder
	for _, part := range cr.Content {
		if part.Type == "text" {
			text.WriteString(part.Text)
		}
	}
	if cr.StopReason == "max_tokens" {
		return "", fmt.Errorf("%w (%d tokens)", ErrOutputCutOff, maxTokens)
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("Claude API returned no text content")
	}
	return text.String(), nil
}

// extractJSON strips leading/trailing markdown code fences (```json ... ```
// or ``` ... ```) that models sometimes add despite instructions not to.
func extractJSON(s string) string {
	// Take the outermost JSON array or object, dropping any fences or
	// commentary around it.
	start := strings.IndexAny(s, "[{")
	end := strings.LastIndexAny(s, "]}")
	if start < 0 || end < start {
		return strings.TrimSpace(s)
	}
	return s[start : end+1]
}
