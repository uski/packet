package genmsg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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

	// FromPrefix and ToPrefix, when non-empty, are the three-character
	// message number prefixes of the sending and receiving stations (e.g.
	// "S24" for Shelter 24). Apply numbers the message with FromPrefix and
	// addresses it to ToPrefix.
	FromPrefix string
	ToPrefix   string

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
	Words         int // total words across the message's content fields
}

// DrillTrafficPhrase is the fixed marker every generated message must
// contain somewhere in its content, so it's unmistakably training/exercise
// traffic rather than a real report. Apply guarantees its presence
// deterministically (see ensureDrillTraffic), rather than only asking for
// it in the prompt: a plain, fixed literal like this doesn't need an LLM's
// creativity, and guessing wrong here would be a training-safety issue, not
// just a cosmetic one.
const DrillTrafficPhrase = "This is drill traffic"

// maxRounds bounds how many times we'll call Claude for each message: the
// initial attempt plus up to two retries for a response that was unusable,
// too long, or missing required content.
const maxRounds = 3

const briefSystemPrompt = `You are helping a Santa Clara County ARES/RACES credential evaluator plan a short, realistic emergency-communications training exercise whose messages will be written one at a time from your plan. Respond with plain text only.`

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
	plans, err := planByParty(req)
	if err != nil {
		return nil, err
	}

	progress(fmt.Sprintf("Preparing %d message(s)...", count))
	specsPerMsg := make([][]FieldSpec, count)
	// routedPerMsg records, for each message, which field (if any) is the
	// dedicated home for an assigned category (see SelectFields), so
	// buildPrompt can send content there instead of into the body.
	routedPerMsg := make([]map[prowords.Category]string, count)
	assigned := make([][]prowords.Category, count)
	baseWords := make([]int, count)
	for i, m := range req.Messages {
		draft, err := buildDraft(req.Incident, m, nil)
		if err != nil {
			return nil, err
		}
		baseWords[i] = messageWordCount(draft)
		// Categories the pre-filled fields already cover don't need to
		// be asked for again; Assigned keeps the full list so coverage
		// is still reported against it.
		assigned[i] = plans[i].Categories
		plans[i].Categories = missingCategories(plans[i].Categories, prowords.CountFields(AllFieldValues(draft)))
		specs, routed := PromptFields(draft, plans[i].Categories)
		plans[i].CheckOne = countCheckboxes(specs) >= manyCheckboxes
		if len(specs) == 0 {
			return nil, fmt.Errorf("message type %q has no editable fields to generate", m.MsgType.Tag())
		}
		specsPerMsg[i] = specs
		routedPerMsg[i] = routed
	}

	results := make([]Result, count)
	for i := range results {
		results[i].Assigned = assigned[i]
	}

	// Asking for a whole batch in one response can outgrow the output
	// limit, so messages are generated one call at a time; a brief
	// planned up front keeps them consistent with each other.
	var brief string
	if count > 1 {
		const label = "Planning a scenario shared by all the messages"
		progress(label + "...")
		text, err := completeWithHeartbeat(ctx, client, briefSystemPrompt, buildBriefPrompt(req), maxOutputTokens, progress, label)
		if err != nil && !errors.Is(err, ErrOutputCutOff) {
			return nil, err
		}
		brief = strings.TrimSpace(text)
	}

	g := &generation{ctx: ctx, client: client, req: req, brief: brief, specs: specsPerMsg,
		routed: routedPerMsg, baseWords: baseWords, results: results, progress: progress}
	for n, idx := range generationOrder(req.Messages) {
		if err := g.generateMessage(idx, n+1, plans[idx]); err != nil {
			return nil, err
		}
	}
	progress("Done generating messages.")
	return results, nil
}

// generation holds the state shared while a batch's messages are generated
// one at a time.
type generation struct {
	ctx       context.Context
	client    *ClaudeClient
	req       Request
	brief     string
	specs     [][]FieldSpec
	routed    []map[prowords.Category]string
	baseWords []int
	results   []Result
	progress  func(string)
}

// generateMessage generates message idx, the n-th in generation order, in
// its own Claude call. It asks again, up to maxRounds times in all, while
// the response is unusable (cut off or unparseable), too long, or missing
// required fields or assigned proword categories. It fails only if no
// usable response arrives at all.
func (g *generation) generateMessage(idx, n int, plan MessagePlan) error {
	count := len(g.req.Messages)
	res := &g.results[idx]
	wanted, checkOne := plan.Categories, plan.CheckOne
	var best Result
	bestScore := -1
	var retryNote, reason string
	var lastErr error
	for round := range maxRounds {
		label := fmt.Sprintf("Drafting message %d of %d", n, count)
		if round > 0 {
			label = fmt.Sprintf("Revising message %d of %d, attempt %d of %d: %s", n, count, round+1, maxRounds, reason)
		}
		g.progress(label + "...")
		prompt := buildPrompt(g.req, g.brief, g.specs, g.results, []int{idx}, []MessagePlan{plan}, g.routed, g.baseWords, res.Values != nil) + retryNote
		text, err := completeWithHeartbeat(g.ctx, g.client, systemPrompt, prompt, maxOutputTokens, g.progress, label)
		if errors.Is(err, ErrOutputCutOff) {
			lastErr, reason = err, "the previous response was cut off"
			retryNote = "\nYour previous response was far too long and was cut off. Respond with ONLY the JSON array, keeping every value short.\n"
			continue
		} else if err != nil {
			return err
		}
		parsed, invalid, err := parseResponse(extractJSON(text), g.specs, []int{idx})
		if err == nil && len(parsed) == 0 {
			err = errors.New("the response held no message object")
		}
		if err == nil {
			parsed[0] = fictionalizeCallSigns(parsed[0])
		}
		if err != nil {
			lastErr, reason = err, "the previous response was unusable"
			retryNote = fmt.Sprintf("\nYour previous response could not be used (%s). Respond with ONLY a JSON array holding one object that maps field tags to string values.\n", err)
			continue
		}
		retryNote = ""
		// Each response holds the message's complete values, replacing
		// the previous round's, so content Claude drops to get under the
		// word budget is really gone.
		draft, err := buildDraft(g.req.Incident, g.req.Messages[idx], parsed[0])
		if err != nil {
			return err
		}
		res.Values = parsed[0]
		res.InvalidFields = invalid[0]
		res.Counts = prowords.CountFields(AllFieldValues(draft))
		res.Missing = missingCategories(res.Assigned, res.Counts)
		res.Words = messageWordCount(draft)
		problems := problemSpecs(draft)
		res.MissingFields = fieldLabels(problems)
		for _, p := range problems {
			if i := slices.IndexFunc(g.specs[idx], func(s FieldSpec) bool { return s.Tag == p.Tag }); i >= 0 {
				// e.g. Item 2's quantity, required once Item 2 is named
				g.specs[idx][i].Optional, g.specs[idx][i].Group = false, p.Group
			} else {
				g.specs[idx] = append(g.specs[idx], p)
			}
		}
		over := res.Words > MaxWords+WordTolerance
		score := 100*len(problems) + len(res.Missing)
		if over {
			score += 10
		}
		needCheck := checkOne && !anyChecked(draft, g.specs[idx])
		if needCheck {
			score++
		}
		long := longSummaries(draft)
		score += len(long)
		if bestScore >= 0 && score >= bestScore {
			break // the revision didn't improve on the best version so far
		}
		best, bestScore = *res, score
		if score == 0 {
			return nil
		}
		// The revision sees every requirement, with the unmet ones
		// flagged, so fixing one doesn't lose another.
		plan = MessagePlan{Categories: wanted, Unmet: res.Missing, MissingFields: problems, CheckOne: checkOne, CheckOneUnmet: needCheck, LongFields: long}
		if over {
			plan.Words = res.Words
		}
		reason = revisionReason(res, problems, over, needCheck, len(long) > 0)
		slog.Info("revising generated training message", "message", idx+1, "reason", reason, "values", res.Values)
	}
	if bestScore < 0 {
		return fmt.Errorf("message %d: no usable response from Claude after %d attempts: %w", idx+1, maxRounds, lastErr)
	}
	*res = best
	return nil
}

// revisionReason describes, for the progress display, why a message is
// being sent back to Claude.
func revisionReason(res *Result, problems []FieldSpec, over, needCheck, longSummary bool) string {
	var parts []string
	if longSummary {
		parts = append(parts, "shortening the subject")
	}
	if needCheck {
		parts = append(parts, "checking a checkbox")
	}
	if len(problems) > 0 {
		parts = append(parts, "fixing "+strings.Join(fieldLabels(problems), ", "))
	}
	if over {
		parts = append(parts, fmt.Sprintf("shortening from %d words", res.Words))
	}
	if len(res.Missing) > 0 {
		names := make([]string, len(res.Missing))
		for i, cat := range res.Missing {
			names[i] = prowords.ProwordName(cat)
		}
		parts = append(parts, "adding "+strings.Join(names, ", "))
	}
	return strings.Join(parts, "; ")
}

// generationOrder returns the message indices in the order to generate
// them: a message comes after the message it replies to, so its prompt can
// include that message's actual content; otherwise original order is kept.
func generationOrder(msgs []MessageSpec) []int {
	order := make([]int, 0, len(msgs))
	visited := make([]bool, len(msgs))
	var visit func(i int)
	visit = func(i int) {
		if visited[i] {
			return
		}
		visited[i] = true // set before recursing, so a reply cycle terminates
		if r := msgs[i].ReplyTo; r > 0 {
			visit(r - 1)
		}
		order = append(order, i)
	}
	for i := range msgs {
		visit(i)
	}
	return order
}

// buildBriefPrompt asks Claude to plan the exercise every message in req
// will be written from: the incident, the facts they must agree on, and
// what each message says.
func buildBriefPrompt(req Request) string {
	var b strings.Builder
	if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario: %s\n\n", req.Scenario)
	} else {
		b.WriteString("No scenario was given: invent a plausible Santa Clara County emergency-response scenario (e.g. a downed power line, a fallen tree blocking a road, traffic congestion near a shelter, storm damage, a utility outage).\n\n")
	}
	fmt.Fprintf(&b, "The exercise has %d messages. Each will be written separately later, by someone who sees only your brief and that one message's details:\n", len(req.Messages))
	for i, m := range req.Messages {
		fmt.Fprintf(&b, "Message %d: %s", i+1, m.MsgType.Name())
		if m.From != "" {
			fmt.Fprintf(&b, ", from %s", partyLabel(m.From, m.FromLocation))
		}
		if m.To != "" {
			fmt.Fprintf(&b, ", to %s", partyLabel(m.To, m.ToLocation))
		}
		if m.Purpose != "" {
			fmt.Fprintf(&b, ", purpose: %s", m.Purpose)
		}
		if m.ReplyTo > 0 {
			fmt.Fprintf(&b, ", replying to message %d", m.ReplyTo)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nWrite a concise exercise brief of at most 250 words, in plain text:\n" +
		"1. The incident: what happened, where, and when.\n" +
		"2. Shared facts every message must agree on: names of people, places and addresses, quantities, times, amateur call signs. Use only the xanadu-city.org domain for any email or web address, and only fictitious call signs ending with a digit, like W6XRL4, never a real call sign.\n" +
		"3. One line per message saying specifically what it reports, requests, or answers, so each reply answers what was actually asked.\n" +
		"Do not write the messages themselves. Each message will be at most about 50 words.\n")
	return b.String()
}

// planByParty groups req.Messages by sender and effective proword level (a
// message's own Level if set, else req.Level) and runs Plan independently
// within each group, so that messages from parties evaluated at different
// credential levels each only draw proword requirements from their own
// level's profile -- an F3 party's messages never get saddled with a
// full-list-only category, and a full-list party's messages aren't limited
// to the reduced list just because they share a batch with an F3 party.
//
// Grouping by sender matters because each candidate is evaluated on what
// that candidate transmits: the Credentialing Program Handbook requires
// each credential's whole proword list, so a party's own messages have to
// cover it rather than the batch covering it between them. The returned
// slice is in req.Messages order.
func planByParty(req Request) ([]MessagePlan, error) {
	type party struct{ level, from, prefix string }
	count := len(req.Messages)
	groups := map[party][]int{}
	for i, m := range req.Messages {
		lvl := m.Level
		if lvl == "" {
			lvl = req.Level
		}
		p := party{lvl, m.From, m.FromPrefix}
		groups[p] = append(groups[p], i)
	}
	plans := make([]MessagePlan, count)
	for p, idxs := range groups {
		profile, err := prowords.Profile(p.level)
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
func completeWithHeartbeat(ctx context.Context, client *ClaudeClient, system, prompt string, maxTokens int, progress func(string), label string) (string, error) {
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
	return client.Complete(ctx, system, prompt, maxTokens)
}

// buildPrompt describes each pending message (its type, its own fields, its
// From/To/purpose flow context, and its assigned proword requirements) so
// the model can generate content appropriate to a mixed-type, possibly
// multi-party batch. results holds whatever has already been generated in
// prior rounds, used to give a reply message the content of the message
// it's replying to even when that message isn't itself pending this round.
// routedPerMsg (indexed the same way as specsPerMsg, by absolute message
// index) names, for a category with a dedicated field to hold it (see
// SelectFields/ClassifyField), that field's tag -- so the prompt tells
// Claude to put that content only there, rather than leaving it to be woven
// into the free-text body, keeping the message shorter.
func buildPrompt(req Request, brief string, specsPerMsg [][]FieldSpec, results []Result, pending []int, plans []MessagePlan, routedPerMsg []map[prowords.Category]string, baseWords []int, isRepair bool) string {
	var b strings.Builder
	if brief != "" {
		fmt.Fprintf(&b, "This message belongs to a training exercise of %d messages, each written separately from the exercise brief below. Keep every name, place, number, and fact consistent with the brief, and write what the brief says this message is about.\n\nEXERCISE BRIEF:\n%s\n\n", len(req.Messages), brief)
	} else if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario to base all of the messages on: %s\n\n", req.Scenario)
	} else {
		b.WriteString("No specific scenario was given. Invent a plausible Santa Clara County emergency-response scenario (e.g. a downed power line, a fallen tree blocking a road, traffic congestion near a shelter, storm damage assessment, a utility outage) and use it consistently across all the messages in this batch.\n\n")
	}
	fmt.Fprintf(&b, "Generate exactly %d message(s), described below in order. They may be different form types with different fields (a training session can mix, for example, an ICS-213, a plain text message, and a Road Closure form). Weave each message's listed requirements naturally into that message's own field values -- they must fit the scenario and read like real, professional emergency radio traffic, not like a checklist. Proword content in ANY field counts, so each requirement only needs to be met ONCE, in the single field that suits it best (a person's name in a name field, an email address or phone number in a contact field) -- never repeat it in another field, and never add a sentence to the free-text body just to carry it (e.g. not \"Contact Jane Doe at jane@xanadu-city.org for logistics.\" when there are name and contact fields to hold them). Requirements already satisfied by pre-filled fields have been left out. Fill each form the way a trained operator fills out the real form: put every piece of information in the field made for it -- for example each requested item in its own item row (Item 1's name and quantity, then Item 2's), a person in a name field, a phone number in a phone field -- and use a free-text field such as Comments or Special Instructions only for information no other field holds, never to restate other fields (e.g. not \"Need 50 blankets, generator\" in Comments when the form has item fields). A subject, title, or summary field is only a short headline of a few words: the message's details go in its message body or the form's other fields, never in the subject.\n\n", len(pending))
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
		routed := routedPerMsg[idx]
		b.WriteString("Fields you MUST fill in (the JSON key is the field tag in quotes):\n")
		shownGroups := map[string]bool{}
		for _, s := range specsPerMsg[idx] {
			if s.Optional {
				continue
			}
			if s.Group != "" {
				if !shownGroups[s.Group] {
					shownGroups[s.Group] = true
					fmt.Fprintf(&b, "  - %q: check AT LEAST ONE of these checkboxes by giving it the value \"checked\", leaving the others out:", s.Group)
					for _, c := range specsPerMsg[idx] {
						if c.Group == s.Group {
							fmt.Fprintf(&b, " %q (%s);", c.Tag, c.Label)
						}
					}
					b.WriteString("\n")
				}
				continue
			}
			fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
			if s.Help != "" {
				fmt.Fprintf(&b, " -- %s", s.Help)
			}
			if isCheckbox(s) {
				b.WriteString(` [checkbox: give "checked" to check it]`)
			} else if len(s.Choices) > 0 {
				fmt.Fprintf(&b, " (this is a dropdown: the value MUST be EXACTLY one of these, verbatim, character for character -- never free-form text: %s)", strings.Join(s.Choices, ", "))
			}
			if s.Multiline {
				b.WriteString(" [this is a free-text body field; it may span multiple sentences]")
			}
			if shortNameField[s.Common] {
				b.WriteString(" [keep this SHORT: at most 3 words, ideally 2, e.g. \"EOC Net Control\" or \"Command Post\", not a full sentence]")
			}
			if alwaysInclude[s.Common] {
				fmt.Fprintf(&b, " [a short title of at most %d words, in sentence case; the message's details go in its message fields, never here]", maxSummaryWords)
			}
			if names := routedForTag(routed, s.Tag); len(names) > 0 {
				fmt.Fprintf(&b, " [put the %s content here -- do NOT also add it to the free-text body]", strings.Join(names, "/"))
			}
			b.WriteString("\n")
		}
		var optional []FieldSpec
		for _, s := range specsPerMsg[idx] {
			if s.Optional {
				optional = append(optional, s)
			}
		}
		if len(optional) > 0 {
			b.WriteString("Other fields on this form, in form order. Put the message's details in the fields they belong in, and leave the fields that don't apply empty:\n")
			for _, s := range optional {
				fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
				if isCheckbox(s) {
					b.WriteString(` [checkbox: give "checked" to check it]`)
				} else if len(s.Choices) > 0 {
					fmt.Fprintf(&b, " (dropdown: exactly one of: %s)", strings.Join(s.Choices, ", "))
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("Requirements for this message:\n")
		for _, cat := range plans[pos].Categories {
			text := prowords.Prompt(cat)
			if slices.Contains(plans[pos].Unmet, cat) {
				text = "NOT MET IN YOUR PREVIOUS VERSION: " + text
			}
			if tag, ok := routed[cat]; ok {
				fmt.Fprintf(&b, "  - %s (put this ONLY in the %q field above, not in the free-text body)\n", text, tag)
			} else {
				fmt.Fprintf(&b, "  - %s\n", text)
			}
		}
		if plans[pos].CheckOne {
			text := `Check at least one checkbox on this form that fits the message, by giving it the value "checked", so the sender and receiver must handle a checked box.`
			if plans[pos].CheckOneUnmet {
				text = "NOT MET IN YOUR PREVIOUS VERSION: " + text
			}
			fmt.Fprintf(&b, "  - %s\n", text)
		}
		budget := max(MaxWords-baseWords[idx], minValueWords)
		fmt.Fprintf(&b, "Word budget: this whole message must total about %d words or fewer across ALL of its fields. Its pre-filled fields already use %d, so the values you give for it must total at most %d words (a phone number, email address, call sign, or other group without spaces counts as one word). Optional fields may stay empty; leave them out rather than go over.\n", MaxWords, baseWords[idx], budget)
		if plans[pos].Words > 0 {
			fmt.Fprintf(&b, "Your previous response made this message %d words in total: cut it to at most %d by shortening text and leaving optional fields empty (never leave a required field empty).\n", plans[pos].Words, MaxWords)
		}
		for _, s := range plans[pos].LongFields {
			fmt.Fprintf(&b, "Your previous version's %q (%s) was %s: make it a short title of at most %d words and move the details into the message fields.\n", s.Tag, s.Label, s.Problem, maxSummaryWords)
		}
		if prev := results[idx].Values; isRepair && prev != nil {
			if data, err := json.Marshal(prev); err == nil {
				fmt.Fprintf(&b, "Your previous version of this message (field tag -> value). Revise it rather than starting over, keeping everything that already works:\n%s\n", data)
			}
		}
		if len(plans[pos].MissingFields) > 0 {
			b.WriteString("The following REQUIRED fields are empty or invalid. You MUST give every one of them a valid, non-empty value this time:\n")
			reported := map[string]bool{}
			for _, s := range plans[pos].MissingFields {
				if s.Group != "" {
					if !reported[s.Group] {
						reported[s.Group] = true
						fmt.Fprintf(&b, "  - %q: check at least one of its checkboxes -- problem: %s\n", s.Group, s.Problem)
					}
					continue
				}
				fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
				if s.Problem != "" {
					fmt.Fprintf(&b, " -- problem: %s", s.Problem)
				}
				if len(s.Choices) > 0 {
					fmt.Fprintf(&b, " (must be exactly one of: %s)", strings.Join(s.Choices, ", "))
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	if isRepair {
		b.WriteString("Revise your previous version rather than starting over: fix what is flagged above, keep everything that already works, and return the complete field values again (not just the changed ones). Every field under \"Fields you MUST fill in\" needs a non-empty value in your response -- do not omit any of them.\n\n")
	}
	fmt.Fprintf(&b, "Every message must also include the exact phrase %q somewhere in its content, to clearly mark it as training/exercise traffic rather than a real report.\n\n", DrillTrafficPhrase)
	b.WriteString("Respond with ONLY a JSON array of exactly that many objects, in the same order as listed above, each mapping THAT message's own field tags to their string values. Keep every message SHORT: real emergency radio traffic is deliberately terse, and each message's word budget above covers ALL of its fields together, so a free-text field should be one or two short sentences at most -- include only what's needed to satisfy the listed requirements. Only use the field tags listed for each message: give every MUST field a non-empty value, fill an optional field only when the message's information belongs there, and never add other keys. For any field marked as a dropdown above, its value must be one of the listed choices, verbatim -- do not invent your own wording for it. Everywhere else, use normal sentence capitalization: capitalize only the first word of a sentence or phrase, proper names, and acronyms, and never Title Case ordinary words (write \"Generator runtime is 8 hours\", not \"Generator Runtime is 8 hours\"), because capitalized ordinary words read as names that call for I SPELL. Every amateur radio call sign, anywhere in a message (including inside email and packet addresses), must be fictitious so it can't belong to a real station: write it in the format of a real call sign followed by one extra digit, like \"W6XRL4\" or \"K6ABC2\", never a real-format call sign like \"KJ6ABC\". Before answering, check your values against every requirement and the word budget.\n")
	return b.String()
}

// routedForTag returns the (sorted, for deterministic prompt text) proword
// names of the categories routed to the field with the given tag, if any.
func routedForTag(routed map[prowords.Category]string, tag string) []string {
	if len(routed) == 0 {
		return nil
	}
	var names []string
	for cat, t := range routed {
		if t == tag {
			names = append(names, prowords.ProwordName(cat))
		}
	}
	slices.Sort(names)
	return names
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
	if strings.HasPrefix(text, "{") {
		text = "[" + text + "]" // a single message sent as a bare object
	}
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
			if isCheckbox(spec) {
				switch strings.ToLower(strings.TrimSpace(sval)) {
				case "true", "yes", "x", "1", "on":
					sval = "checked"
				case "", "false", "no", "unchecked", "0", "off":
					continue
				}
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

// isCheckbox reports whether s is a checkbox, whose only value is "checked".
func isCheckbox(s FieldSpec) bool {
	return len(s.Choices) == 1 && s.Choices[0] == "checked"
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

// fieldLabels returns the human-readable labels of specs, for reporting
// which required fields a message is still missing; a checkbox group is
// reported once, by its own label.
func fieldLabels(specs []FieldSpec) []string {
	var labels []string
	seen := map[string]bool{}
	for _, s := range specs {
		label := s.Label
		if s.Group != "" {
			label = s.Group
		}
		if !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
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
