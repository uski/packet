package genmsg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// Request describes one batch of training messages to generate.
type Request struct {
	MsgType  message.EditableMType // the message type to generate
	Count    int                   // number of messages to generate
	Level    string                // prowords.LevelF3 or prowords.LevelFull
	Scenario string                // optional user-supplied scenario; if empty, a generic one is invented
}

// Result is one generated message: field tag -> human-readable value, plus
// bookkeeping about which proword categories it was asked to exercise and
// which of those (if any) could not be confirmed after generation.
type Result struct {
	Values   map[string]string
	Assigned []prowords.Category
	Missing  []prowords.Category
}

// maxRounds bounds how many times we'll call Claude for a batch: the
// initial attempt plus up to two repair rounds for messages that didn't
// satisfy their assigned categories.
const maxRounds = 3

const systemPrompt = `You are helping a Santa Clara County ARES/RACES credential evaluator generate realistic, short, third-party emergency-communications training messages. A candidate being evaluated will read these messages aloud over amateur radio using proper message-passing procedure, so the written content must naturally require specific prowords when voiced correctly (for example, a message won't require the EMAIL ADDRESS proword unless it actually contains an email address). Respond with ONLY a single JSON array (no markdown code fences, no commentary before or after) containing one object per requested message, each object mapping the given field tags to string values.`

// Generate produces req.Count draft messages of req.MsgType satisfying
// req.Level's proword profile, calling client to draft the content. It does
// not create the messages in any incident; see Apply for that.
func Generate(ctx context.Context, client *ClaudeClient, req Request) ([]Result, error) {
	if req.Count <= 0 {
		return nil, fmt.Errorf("count must be positive")
	}
	if !client.HasAPIKey() {
		return nil, ErrNoAPIKey
	}
	profile, err := prowords.Profile(req.Level)
	if err != nil {
		return nil, err
	}
	plans, _ := Plan(profile, req.Count)

	specs := Generatable(Describe(req.MsgType.NewDraft()))
	if len(specs) == 0 {
		return nil, fmt.Errorf("message type %q has no editable fields to generate", req.MsgType.Tag())
	}

	results := make([]Result, req.Count)
	for i := range results {
		results[i].Assigned = plans[i].Categories
	}

	// pending holds the indices into results still needing generation or
	// repair, and pendingPlans holds the categories still to satisfy for
	// each of them (all of them initially; only the missing ones on
	// repair rounds).
	pending := make([]int, req.Count)
	for i := range pending {
		pending[i] = i
	}
	pendingPlans := plans

	for round := 0; round < maxRounds && len(pending) > 0; round++ {
		prompt := buildPrompt(req, specs, pendingPlans, round > 0)
		text, err := client.Complete(ctx, systemPrompt, prompt)
		if err != nil {
			return nil, err
		}
		parsed, err := parseResponse(extractJSON(text), specs)
		if err != nil {
			return nil, err
		}
		var nextPending []int
		var nextPlans []MessagePlan
		for j, idx := range pending {
			if j >= len(parsed) {
				// The model returned fewer messages than
				// asked; carry this one over to the next
				// round with its full set of requirements.
				nextPending = append(nextPending, idx)
				nextPlans = append(nextPlans, pendingPlans[j])
				continue
			}
			// Repair rounds only overwrite the fields Claude
			// actually returned this time, layering onto what we
			// already had.
			if results[idx].Values == nil {
				results[idx].Values = map[string]string{}
			}
			for tag, val := range parsed[j] {
				results[idx].Values[tag] = val
			}
			results[idx].Missing = validateCategories(results[idx].Assigned, results[idx].Values)
			if len(results[idx].Missing) > 0 {
				nextPending = append(nextPending, idx)
				nextPlans = append(nextPlans, MessagePlan{Categories: results[idx].Missing})
			}
		}
		pending, pendingPlans = nextPending, nextPlans
	}
	return results, nil
}

func buildPrompt(req Request, specs []FieldSpec, plans []MessagePlan, isRepair bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Message type: %s\n\n", req.MsgType.Name())
	b.WriteString("Fields to fill in for each message (JSON key is the field tag in quotes below):\n")
	for _, s := range specs {
		fmt.Fprintf(&b, "- %q: %s", s.Tag, s.Label)
		if s.Help != "" {
			fmt.Fprintf(&b, " -- %s", s.Help)
		}
		if len(s.Choices) > 0 {
			fmt.Fprintf(&b, " (choose exactly one of: %s)", strings.Join(s.Choices, ", "))
		}
		if s.Multiline {
			b.WriteString(" [this is a free-text body field; it may span multiple sentences]")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario to base all of the messages on: %s\n\n", req.Scenario)
	} else {
		b.WriteString("No specific scenario was given. Invent a plausible Santa Clara County emergency-response scenario (e.g. a downed power line, a fallen tree blocking a road, traffic congestion near a shelter, storm damage assessment, a utility outage) and use it consistently across all the messages in this batch.\n\n")
	}
	fmt.Fprintf(&b, "Generate exactly %d message(s). For each one, weave the following requirements naturally into the field values -- they must fit the scenario and read like real, professional emergency radio traffic, not like a checklist:\n\n", len(plans))
	for i, p := range plans {
		fmt.Fprintf(&b, "Message %d requirements:\n", i+1)
		for _, cat := range p.Categories {
			fmt.Fprintf(&b, "  - %s\n", prowords.Prompt(cat))
		}
	}
	if isRepair {
		b.WriteString("\nThe previous attempt did not clearly satisfy all of the requirements above for these messages. Revise them so every requirement is unambiguously satisfied, and return the complete field values again (not just the changed ones).\n")
	}
	b.WriteString("\nRespond with ONLY a JSON array of exactly that many objects, in the same order as listed above, each mapping field tags to their string values. Keep each message concise (a few sentences for any free-text field) and realistic. Do not invent fields beyond the ones listed above.\n")
	return b.String()
}

func parseResponse(text string, specs []FieldSpec) ([]map[string]string, error) {
	var raw []map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("parsing Claude's JSON response: %w", err)
	}
	valid := make(map[string]bool, len(specs))
	for _, s := range specs {
		valid[s.Tag] = true
	}
	out := make([]map[string]string, 0, len(raw))
	for _, obj := range raw {
		values := make(map[string]string, len(obj))
		for k, v := range obj {
			if !valid[k] {
				continue
			}
			if s, ok := v.(string); ok {
				values[k] = s
			} else {
				values[k] = fmt.Sprint(v)
			}
		}
		out = append(out, values)
	}
	return out, nil
}

// validateCategories returns the subset of cats that do not appear to be
// satisfied by the concatenation of all field values.
func validateCategories(cats []prowords.Category, values map[string]string) []prowords.Category {
	var all strings.Builder
	for _, v := range values {
		all.WriteString(v)
		all.WriteString("\n")
	}
	text := all.String()
	var missing []prowords.Category
	for _, c := range cats {
		if !prowords.Validate(c, text) {
			missing = append(missing, c)
		}
	}
	return missing
}
