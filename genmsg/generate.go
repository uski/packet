package genmsg

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// MessageSpec describes one message to generate. In the simple case it's
// just a message type; for a multi-party flow (see Flow/ResolveFlow) it
// also carries who the message is from/to and how it relates to the other
// messages in the same Request, so Claude can generate a whole coherent
// exchange in one call while the tool -- not Claude -- keeps the mechanical
// per-message routing (who's sending to whom) perfectly consistent.
type MessageSpec struct {
	MsgType message.EditableMType

	// From, FromLocation, To, and ToLocation, when non-empty, are used
	// directly as the message's From/To ICS Position and Location (set
	// deterministically by Generate/Apply, not asked of Claude, so they
	// stay exactly consistent across a flow -- e.g. message 2's From
	// matches message 1's To when message 2 replies to message 1) and
	// are also given to Claude as context for message types that have no
	// first-class ICS Position/Location fields (e.g. plain text). Leave
	// all empty to let Claude invent a plausible position/location
	// itself, as in the original single-batch (non-flow) mode.
	From         string
	FromLocation string
	To           string
	ToLocation   string

	// Purpose, if non-empty, is a short hint of what this specific
	// message is about (e.g. "request shelter capacity status"). Leave
	// empty to let Claude infer it from the scenario and its place in
	// the flow.
	Purpose string

	// ReplyTo, if non-zero, is the 1-based index into the same Request's
	// Messages of another message this one replies to. Claude is told to
	// make this message's content directly and consistently respond to
	// that message's content, even across repair rounds where the
	// referenced message might not itself need regenerating.
	ReplyTo int

	// Level, if non-empty, overrides Request.Level for this one message:
	// prowords.LevelF3 or prowords.LevelFull. This lets a multi-party
	// flow evaluate different parties at different credential levels
	// (e.g. an F3 candidate's messages drawing only from the reduced
	// proword list, while a Net Control party's messages in the same
	// batch draw from the full list) -- each message is graded only
	// against its own sender's level, never mixed with another party's.
	// Leave empty to use Request.Level, as in the original single-batch
	// (non-flow) mode where every message shares one level.
	Level string
}

// Request describes one batch of training messages to generate. The
// messages need not all be the same type, and (via MessageSpec's From/To/
// Purpose/ReplyTo) need not be independent: a training session might be a
// single ICS-213 asking all stations for a status update, answered by
// several situation-report replies, all generated together for coherence.
type Request struct {
	Incident *incident.Incident // used to apply the incident's own defaults before deciding what the LLM needs to fill in
	Messages []MessageSpec
	Level    string // prowords.LevelF3 or prowords.LevelFull; fallback for any MessageSpec that doesn't set its own Level
	Scenario string // optional user-supplied scenario; if empty, a generic one is invented

	// Progress, if non-nil, is called with human-readable status updates
	// as Generate works. Claude calls in particular can take tens of
	// seconds, so Progress is also called periodically (heartbeatInterval)
	// while one is in flight, not just between calls, so a caller showing
	// these to a user (a CLI status line, a GUI progress panel) always has
	// something recent to display.
	Progress func(string)
}

// heartbeatInterval is how often Progress is called with a "still working"
// update while waiting for a single Claude API call to complete. It's a var
// (not a const) so tests can shrink it.
var heartbeatInterval = 4 * time.Second

// Result is one generated message: field tag -> human-readable value, plus
// bookkeeping about which proword categories it was asked to exercise, how
// many times each proword category was actually detected in the final
// values (see the prowords engine, prowords.CountFields), which assigned
// categories (if any) could not be confirmed after generation, the labels
// of any restricted (dropdown/choice) fields where Claude returned a value
// outside the field's allowed choices (dropped rather than written in, so
// the field keeps whatever default it already had), and the labels of any
// required fields still left empty after all repair rounds.
type Result struct {
	Values        map[string]string
	Assigned      []prowords.Category
	Counts        map[prowords.Category]int
	Missing       []prowords.Category
	InvalidFields []string
	MissingFields []string
}

// DrillTrafficPhrase is the fixed marker every generated message must
// contain somewhere in its content, so it's unmistakably training/exercise
// traffic rather than a real report. Apply guarantees its presence
// deterministically (see ensureDrillTraffic), rather than only asking for
// it in the prompt: a plain, fixed literal like this doesn't need an LLM's
// creativity, and guessing wrong here would be a training-safety issue, not
// just a cosmetic one.
const DrillTrafficPhrase = "This is drill traffic"

// maxRounds bounds how many times we'll call Claude for a batch: the
// initial attempt plus up to two repair rounds for messages that didn't
// satisfy their assigned categories.
const maxRounds = 3

const systemPrompt = `You are helping a Santa Clara County ARES/RACES credential evaluator generate realistic, short, third-party emergency-communications training messages. A candidate being evaluated will read these messages aloud over amateur radio using proper message-passing procedure, so the written content must naturally require specific prowords when voiced correctly (for example, a message won't require the EMAIL ADDRESS proword unless it actually contains an email address). Some batches are a single coherent multi-party exchange (e.g. one message requesting a status update, answered by several reply messages) rather than independent messages -- when a message says it replies to another, its content must directly and consistently respond to that message, as a real reply would. Respond with ONLY a single JSON array (no markdown code fences, no commentary before or after) containing one object per requested message, each object mapping the given field tags to string values.`

// Generate produces one draft message for each entry of req.Messages,
// together satisfying req.Level's proword profile (spread across the
// batch), calling client to draft the content. It does not create the
// messages in any incident; see Apply for that.
func Generate(ctx context.Context, client *ClaudeClient, req Request) ([]Result, error) {
	progress := req.Progress
	if progress == nil {
		progress = func(string) {}
	}
	count := len(req.Messages)
	if count == 0 {
		return nil, fmt.Errorf("at least one message must be given")
	}
	if req.Incident == nil {
		return nil, fmt.Errorf("an incident is required")
	}
	if !client.HasAPIKey() {
		return nil, ErrNoAPIKey
	}
	for i, m := range req.Messages {
		if m.ReplyTo < 0 || m.ReplyTo > count || m.ReplyTo == i+1 {
			return nil, fmt.Errorf("message %d: invalid replyTo %d", i+1, m.ReplyTo)
		}
	}
	plans, err := planByLevel(req)
	if err != nil {
		return nil, err
	}

	progress(fmt.Sprintf("Preparing %d message(s)...", count))
	specsPerMsg := make([][]FieldSpec, count)
	for i, m := range req.Messages {
		draft, ok := m.MsgType.NewDraft().(*message.DraftMessage)
		if !ok {
			return nil, fmt.Errorf("message type %q does not support draft creation", m.MsgType.Tag())
		}
		req.Incident.ApplyDefaults(draft)
		applyPartyFields(draft, m)
		specs := Generatable(Describe(draft))
		if len(specs) == 0 {
			return nil, fmt.Errorf("message type %q has no editable fields to generate", m.MsgType.Tag())
		}
		specsPerMsg[i] = specs
	}

	results := make([]Result, count)
	for i := range results {
		results[i].Assigned = plans[i].Categories
	}

	// pending holds the indices into results/req.Messages/specsPerMsg
	// still needing generation or repair, and pendingPlans holds the
	// categories still to satisfy for each of them (all of them
	// initially; only the missing ones on repair rounds).
	pending := make([]int, count)
	for i := range pending {
		pending[i] = i
	}
	pendingPlans := plans

	for round := 0; round < maxRounds && len(pending) > 0; round++ {
		var label string
		if round == 0 {
			label = fmt.Sprintf("Asking Claude to draft %d message(s)", len(pending))
		} else {
			label = fmt.Sprintf("Asking Claude to revise %d message(s) still missing required content (attempt %d of %d)", len(pending), round+1, maxRounds)
		}
		progress(label + "...")
		prompt := buildPrompt(req, specsPerMsg, results, pending, pendingPlans, round > 0)
		text, err := completeWithHeartbeat(ctx, client, systemPrompt, prompt, progress, label)
		if err != nil {
			return nil, err
		}
		progress("Received a response; checking proword coverage...")
		parsed, invalid, err := parseResponse(extractJSON(text), specsPerMsg, pending)
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
			results[idx].InvalidFields = append(results[idx].InvalidFields, invalid[j]...)
			results[idx].Counts = prowords.CountFields(results[idx].Values)
			results[idx].Missing = missingCategories(results[idx].Assigned, results[idx].Counts)
			missingFields := missingRequiredFields(specsPerMsg[idx], results[idx].Values)
			results[idx].MissingFields = fieldLabels(missingFields)
			if len(results[idx].Missing) > 0 || len(missingFields) > 0 {
				nextPending = append(nextPending, idx)
				nextPlans = append(nextPlans, MessagePlan{Categories: results[idx].Missing, MissingFields: missingFields})
			}
		}
		pending, pendingPlans = nextPending, nextPlans
	}
	progress("Done generating messages.")
	return results, nil
}

// planByLevel groups req.Messages by their effective proword level (a
// message's own Level if set, else req.Level) and runs Plan independently
// within each group, so that messages from parties evaluated at different
// credential levels each only draw proword requirements from their own
// level's profile -- an F3 party's messages never get saddled with a
// full-list-only category, and a full-list party's messages aren't limited
// to the reduced list just because they share a batch with an F3 party. The
// returned slice is in req.Messages order.
func planByLevel(req Request) ([]MessagePlan, error) {
	count := len(req.Messages)
	groups := map[string][]int{}
	for i, m := range req.Messages {
		lvl := m.Level
		if lvl == "" {
			lvl = req.Level
		}
		groups[lvl] = append(groups[lvl], i)
	}
	plans := make([]MessagePlan, count)
	for lvl, idxs := range groups {
		profile, err := prowords.Profile(lvl)
		if err != nil {
			return nil, err
		}
		groupPlans, _ := Plan(profile, len(idxs))
		for j, idx := range idxs {
			plans[idx] = groupPlans[j]
		}
	}
	return plans, nil
}

// applyPartyFields sets draft's From/To ICS Position and Location fields
// directly from m, for whichever of those the message type has and m
// provides -- used both on the probe draft in Generate (so Describe sees
// them as already-filled and excludes them from Claude's fill list) and on
// the real draft in Apply (so the final message actually has them). Fields
// the message type doesn't have (e.g. a plain text message has no
// first-class ICS Position field) are silently skipped; the same
// information is still given to Claude as prompt context in buildPrompt so
// it can work it into whatever fields do exist.
func applyPartyFields(draft *message.DraftMessage, m MessageSpec) {
	set := func(common, value string) {
		if value == "" {
			return
		}
		if f := FindFieldByCommon(draft, common); f != nil {
			f.SetValue(draft, f.FromHuman(draft, value))
		}
	}
	set("fromICSPosition", m.From)
	set("fromLocation", m.FromLocation)
	set("toICSPosition", m.To)
	set("toLocation", m.ToLocation)
}

// completeWithHeartbeat calls client.Complete, calling progress with a
// "still working" update derived from label every heartbeatInterval while
// the call is in flight, so a caller displaying progress to a user always
// has something recent to show during a slow API call.
func completeWithHeartbeat(ctx context.Context, client *ClaudeClient, system, prompt string, progress func(string), label string) (string, error) {
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		elapsed := heartbeatInterval
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				progress(fmt.Sprintf("%s... (%s elapsed)", label, elapsed))
				elapsed += heartbeatInterval
			}
		}
	}()
	return client.Complete(ctx, system, prompt)
}

// buildPrompt describes each pending message (its type, its own fields, its
// From/To/purpose flow context, and its assigned proword requirements) so
// the model can generate content appropriate to a mixed-type, possibly
// multi-party batch. results holds whatever has already been generated in
// prior rounds, used to give a reply message the content of the message
// it's replying to even when that message isn't itself pending this round.
func buildPrompt(req Request, specsPerMsg [][]FieldSpec, results []Result, pending []int, plans []MessagePlan, isRepair bool) string {
	var b strings.Builder
	if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario to base all of the messages on: %s\n\n", req.Scenario)
	} else {
		b.WriteString("No specific scenario was given. Invent a plausible Santa Clara County emergency-response scenario (e.g. a downed power line, a fallen tree blocking a road, traffic congestion near a shelter, storm damage assessment, a utility outage) and use it consistently across all the messages in this batch.\n\n")
	}
	fmt.Fprintf(&b, "Generate exactly %d message(s), described below in order. They may be different form types with different fields (a training session can mix, for example, an ICS-213, a plain text message, and a Road Closure form). Weave each message's listed requirements naturally into that message's own field values -- they must fit the scenario and read like real, professional emergency radio traffic, not like a checklist.\n\n", len(pending))
	pendingSet := make(map[int]bool, len(pending))
	for _, idx := range pending {
		pendingSet[idx] = true
	}
	for pos, idx := range pending {
		m := req.Messages[idx]
		label := idx + 1 // stable original 1-based number, independent of pending/round
		fmt.Fprintf(&b, "Message %d -- type: %s\n", label, m.MsgType.Name())
		if m.From != "" || m.To != "" {
			b.WriteString("This message is from ")
			b.WriteString(partyLabel(m.From, m.FromLocation))
			if m.To != "" {
				b.WriteString(" to ")
				b.WriteString(partyLabel(m.To, m.ToLocation))
			}
			b.WriteString(".\n")
		}
		if m.Purpose != "" {
			fmt.Fprintf(&b, "Purpose of this message: %s\n", m.Purpose)
		}
		if m.ReplyTo > 0 {
			fmt.Fprintf(&b, "This is a direct reply to Message %d: its content must specifically and consistently respond to that message (answer what it asked, reference the specifics it mentioned), not just share the same general scenario.\n", m.ReplyTo)
			refIdx := m.ReplyTo - 1
			if !pendingSet[refIdx] && results[refIdx].Values != nil {
				b.WriteString("For reference, Message ")
				fmt.Fprintf(&b, "%d's already-generated content was:\n", m.ReplyTo)
				for _, v := range results[refIdx].Values {
					b.WriteString("  ")
					b.WriteString(v)
					b.WriteString("\n")
				}
			}
		}
		b.WriteString("Fields to fill in (JSON key is the field tag in quotes below):\n")
		for _, s := range specsPerMsg[idx] {
			fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
			if s.Help != "" {
				fmt.Fprintf(&b, " -- %s", s.Help)
			}
			if len(s.Choices) > 0 {
				fmt.Fprintf(&b, " (this is a dropdown: the value MUST be EXACTLY one of these, verbatim, character for character -- never free-form text: %s)", strings.Join(s.Choices, ", "))
			}
			if s.Multiline {
				b.WriteString(" [this is a free-text body field; it may span multiple sentences]")
			}
			if shortNameField[s.Common] {
				b.WriteString(" [keep this SHORT: at most 3 words, ideally 2, e.g. \"EOC Net Control\" or \"Command Post\", not a full sentence]")
			}
			b.WriteString("\n")
		}
		b.WriteString("Requirements for this message:\n")
		for _, cat := range plans[pos].Categories {
			fmt.Fprintf(&b, "  - %s\n", prowords.Prompt(cat))
		}
		if len(plans[pos].MissingFields) > 0 {
			b.WriteString("Your previous response left the following REQUIRED fields empty or missing entirely. You MUST provide a non-empty value for every one of them this time:\n")
			for _, s := range plans[pos].MissingFields {
				fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
				if s.Help != "" {
					fmt.Fprintf(&b, " -- %s", s.Help)
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	if isRepair {
		b.WriteString("The previous attempt did not clearly satisfy all of the requirements above for these messages. Revise them so every requirement is unambiguously satisfied, and return the complete field values again (not just the changed ones). Every field listed under \"Fields to fill in\" for a message is required to have a non-empty value in your response -- do not omit any of them.\n\n")
	}
	fmt.Fprintf(&b, "Every message must also include the exact phrase %q somewhere in its content, to clearly mark it as training/exercise traffic rather than a real report.\n\n", DrillTrafficPhrase)
	b.WriteString("Respond with ONLY a JSON array of exactly that many objects, in the same order as listed above, each mapping THAT message's own field tags to their string values. Keep every message SHORT: real emergency radio traffic is deliberately terse to save airtime, so free-text fields should be one to three short sentences, not a full paragraph -- include only what's needed to satisfy the listed requirements, don't pad it out. Only set the fields listed for each message; every field listed is required and MUST be given a non-empty value, but never add fields beyond that list. For any field marked as a dropdown above, its value must be one of the listed choices, verbatim -- do not invent your own wording for it.\n")
	return b.String()
}

// partyLabel formats a From/To party for the prompt, e.g. "Net Control
// (County EOC)", or just the name if no location was given.
func partyLabel(name, location string) string {
	if location == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, location)
}

// shortNameField lists common field names for ICS position and location
// fields, which should be kept short (a role or place name, not a
// sentence) to read naturally over voice and fit real-world form fields.
var shortNameField = map[string]bool{
	"toICSPosition":   true,
	"fromICSPosition": true,
	"toLocation":      true,
	"fromLocation":    true,
}

// parseResponse parses Claude's JSON array response, validating each
// object's keys against the field tags of the corresponding pending
// message (by position). For a restricted (dropdown) field, the value must
// match one of the field's Choices (case/whitespace-insensitively); if it
// doesn't, the value is dropped (the field is left unset, so whatever
// default it already had stands) rather than writing free-form text into a
// field meant to hold one of a fixed set of choices, and the field's label
// is reported in the corresponding entry of invalid.
func parseResponse(text string, specsPerMsg [][]FieldSpec, pending []int) (values []map[string]string, invalid [][]string, err error) {
	var raw []map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, nil, fmt.Errorf("parsing Claude's JSON response: %w", err)
	}
	values = make([]map[string]string, 0, len(raw))
	invalid = make([][]string, 0, len(raw))
	for pos, obj := range raw {
		if pos >= len(pending) {
			break // extra objects beyond what we asked for are ignored
		}
		specByTag := make(map[string]FieldSpec, len(specsPerMsg[pending[pos]]))
		for _, s := range specsPerMsg[pending[pos]] {
			specByTag[s.Tag] = s
		}
		vals := make(map[string]string, len(obj))
		var bad []string
		for k, v := range obj {
			spec, ok := specByTag[k]
			if !ok {
				continue
			}
			var sval string
			if s, ok := v.(string); ok {
				sval = s
			} else {
				sval = fmt.Sprint(v)
			}
			if len(spec.Choices) > 0 {
				canon, ok := matchChoice(sval, spec.Choices)
				if !ok {
					bad = append(bad, spec.Label)
					continue
				}
				sval = canon
			}
			vals[k] = sval
		}
		values = append(values, vals)
		invalid = append(invalid, bad)
	}
	return values, invalid, nil
}

// matchChoice returns the canonical form (as declared in choices) matching
// value case- and whitespace-insensitively, if any.
func matchChoice(value string, choices []string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	for _, c := range choices {
		if strings.EqualFold(strings.TrimSpace(c), trimmed) {
			return c, true
		}
	}
	return "", false
}

// missingRequiredFields returns the subset of specs marked Required whose
// value in values is empty or absent -- i.e. required fields Claude's
// response did not actually fill in, regardless of whether they affect
// proword coverage.
func missingRequiredFields(specs []FieldSpec, values map[string]string) []FieldSpec {
	var missing []FieldSpec
	for _, s := range specs {
		if s.Required && strings.TrimSpace(values[s.Tag]) == "" {
			missing = append(missing, s)
		}
	}
	return missing
}

// fieldLabels returns the human-readable labels of specs, for reporting
// which required fields a message is still missing.
func fieldLabels(specs []FieldSpec) []string {
	if len(specs) == 0 {
		return nil
	}
	labels := make([]string, len(specs))
	for i, s := range specs {
		labels[i] = s.Label
	}
	return labels
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
