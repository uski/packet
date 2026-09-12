package genmsg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// Request describes one batch of training messages to generate. The
// messages need not all be the same type: a training session might mix an
// ICS-213, a plain text message, and a Road Closure form, for example.
// MsgTypes has one entry per message to generate, in order; its length is
// the message count.
type Request struct {
	Incident *incident.Incident // used to apply the incident's own defaults before deciding what the LLM needs to fill in
	MsgTypes []message.EditableMType
	Level    string // prowords.LevelF3 or prowords.LevelFull
	Scenario string // optional user-supplied scenario; if empty, a generic one is invented
}

// Result is one generated message: field tag -> human-readable value, plus
// bookkeeping about which proword categories it was asked to exercise, how
// many times each proword category was actually detected in the final
// values (see the prowords engine, prowords.CountFields), and which
// assigned categories (if any) could not be confirmed after generation.
type Result struct {
	Values   map[string]string
	Assigned []prowords.Category
	Counts   map[prowords.Category]int
	Missing  []prowords.Category
}

// maxRounds bounds how many times we'll call Claude for a batch: the
// initial attempt plus up to two repair rounds for messages that didn't
// satisfy their assigned categories.
const maxRounds = 3

const systemPrompt = `You are helping a Santa Clara County ARES/RACES credential evaluator generate realistic, short, third-party emergency-communications training messages. A candidate being evaluated will read these messages aloud over amateur radio using proper message-passing procedure, so the written content must naturally require specific prowords when voiced correctly (for example, a message won't require the EMAIL ADDRESS proword unless it actually contains an email address). Respond with ONLY a single JSON array (no markdown code fences, no commentary before or after) containing one object per requested message, each object mapping the given field tags to string values.`

// Generate produces one draft message for each entry of req.MsgTypes,
// together satisfying req.Level's proword profile (spread across the
// batch), calling client to draft the content. It does not create the
// messages in any incident; see Apply for that.
func Generate(ctx context.Context, client *ClaudeClient, req Request) ([]Result, error) {
	count := len(req.MsgTypes)
	if count == 0 {
		return nil, fmt.Errorf("at least one message type must be given")
	}
	if req.Incident == nil {
		return nil, fmt.Errorf("an incident is required")
	}
	if !client.HasAPIKey() {
		return nil, ErrNoAPIKey
	}
	profile, err := prowords.Profile(req.Level)
	if err != nil {
		return nil, err
	}
	plans, _ := Plan(profile, count)

	specsPerMsg := make([][]FieldSpec, count)
	for i, mt := range req.MsgTypes {
		draft, ok := mt.NewDraft().(*message.DraftMessage)
		if !ok {
			return nil, fmt.Errorf("message type %q does not support draft creation", mt.Tag())
		}
		req.Incident.ApplyDefaults(draft)
		specs := Generatable(Describe(draft))
		if len(specs) == 0 {
			return nil, fmt.Errorf("message type %q has no editable fields to generate", mt.Tag())
		}
		specsPerMsg[i] = specs
	}

	results := make([]Result, count)
	for i := range results {
		results[i].Assigned = plans[i].Categories
	}

	// pending holds the indices into results/req.MsgTypes/specsPerMsg
	// still needing generation or repair, and pendingPlans holds the
	// categories still to satisfy for each of them (all of them
	// initially; only the missing ones on repair rounds).
	pending := make([]int, count)
	for i := range pending {
		pending[i] = i
	}
	pendingPlans := plans

	for round := 0; round < maxRounds && len(pending) > 0; round++ {
		prompt := buildPrompt(req, specsPerMsg, pending, pendingPlans, round > 0)
		text, err := client.Complete(ctx, systemPrompt, prompt)
		if err != nil {
			return nil, err
		}
		parsed, err := parseResponse(extractJSON(text), specsPerMsg, pending)
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
			results[idx].Counts = prowords.CountFields(results[idx].Values)
			results[idx].Missing = missingCategories(results[idx].Assigned, results[idx].Counts)
			if len(results[idx].Missing) > 0 {
				nextPending = append(nextPending, idx)
				nextPlans = append(nextPlans, MessagePlan{Categories: results[idx].Missing})
			}
		}
		pending, pendingPlans = nextPending, nextPlans
	}
	return results, nil
}

// buildPrompt describes each pending message (its type, its own fields, and
// its assigned proword requirements) so the model can generate content
// appropriate to a mixed-type batch.
func buildPrompt(req Request, specsPerMsg [][]FieldSpec, pending []int, plans []MessagePlan, isRepair bool) string {
	var b strings.Builder
	if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario to base all of the messages on: %s\n\n", req.Scenario)
	} else {
		b.WriteString("No specific scenario was given. Invent a plausible Santa Clara County emergency-response scenario (e.g. a downed power line, a fallen tree blocking a road, traffic congestion near a shelter, storm damage assessment, a utility outage) and use it consistently across all the messages in this batch.\n\n")
	}
	fmt.Fprintf(&b, "Generate exactly %d message(s), described below in order. They may be different form types with different fields (a training session can mix, for example, an ICS-213, a plain text message, and a Road Closure form). Weave each message's listed requirements naturally into that message's own field values -- they must fit the scenario and read like real, professional emergency radio traffic, not like a checklist.\n\n", len(pending))
	for pos, idx := range pending {
		mt := req.MsgTypes[idx]
		fmt.Fprintf(&b, "Message %d -- type: %s\n", pos+1, mt.Name())
		b.WriteString("Fields to fill in (JSON key is the field tag in quotes below):\n")
		for _, s := range specsPerMsg[idx] {
			fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
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
		b.WriteString("Requirements for this message:\n")
		for _, cat := range plans[pos].Categories {
			fmt.Fprintf(&b, "  - %s\n", prowords.Prompt(cat))
		}
		b.WriteString("\n")
	}
	if isRepair {
		b.WriteString("The previous attempt did not clearly satisfy all of the requirements above for these messages. Revise them so every requirement is unambiguously satisfied, and return the complete field values again (not just the changed ones).\n\n")
	}
	b.WriteString("Respond with ONLY a JSON array of exactly that many objects, in the same order as listed above, each mapping THAT message's own field tags to their string values. Keep every message SHORT: real emergency radio traffic is deliberately terse to save airtime, so free-text fields should be one to three short sentences, not a full paragraph -- include only what's needed to satisfy the listed requirements, don't pad it out. Only set the fields listed for each message; every field listed is one you should fill in, but never add fields beyond that list.\n")
	return b.String()
}

// parseResponse parses Claude's JSON array response, validating each
// object's keys against the field tags of the corresponding pending
// message (by position).
func parseResponse(text string, specsPerMsg [][]FieldSpec, pending []int) ([]map[string]string, error) {
	var raw []map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, fmt.Errorf("parsing Claude's JSON response: %w", err)
	}
	out := make([]map[string]string, 0, len(raw))
	for pos, obj := range raw {
		if pos >= len(pending) {
			break // extra objects beyond what we asked for are ignored
		}
		valid := make(map[string]bool, len(specsPerMsg[pending[pos]]))
		for _, s := range specsPerMsg[pending[pos]] {
			valid[s.Tag] = true
		}
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

// missingCategories returns the subset of cats that counts shows zero
// occurrences of.
func missingCategories(cats []prowords.Category, counts map[prowords.Category]int) []prowords.Category {
	var missing []prowords.Category
	for _, c := range cats {
		if counts[c] == 0 {
			missing = append(missing, c)
		}
	}
	return missing
}
