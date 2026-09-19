package genmsg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
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

	// Date, if set (MM/DD/YYYY), is the incident date: every filled date
	// field gets it, and time fields are left blank. Otherwise dates and
	// times are the current ones.
	Date string

	// Training, if set, is saved with the incident when the message is
	// created (see Apply), for IncidentReport. ResolveFlow sets it.
	Training *TrainingRecord

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

	// Handling, if non-empty, is the message's handling order: "R", "P",
	// or "I" (or ROUTINE, PRIORITY, IMMEDIATE). It is set by the tool,
	// never asked of Claude.
	Handling string

	// MsgNo, if non-empty, is the message's number, used instead of the
	// one Apply would pick (see FromPrefix).
	MsgNo string

	// Packet says the message is sent by packet, so the number Apply
	// picks for it gets the "P" suffix.
	Packet bool

	// Time, if non-empty, is the time the message is written, as HH:MM. It
	// fills the message's time fields (see setIncidentDate), which are
	// otherwise left blank when Date is set.
	Time string
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

	// Activity, if non-nil, is called whenever one of the activities
	// Generate runs, several at a time, changes: planning the scenario, and
	// drafting each message. Every message's activity is reported, waiting,
	// before any starts, in the order they're drafted.
	Activity func(Activity)
}

// Activity is the state of one activity of Generate (see Request.Activity).
type Activity struct {
	Key    string `json:"key"`    // stable identifier: "plan", or "message-N" (1-based message index)
	Label  string `json:"label"`  // what it is, e.g. "Message 3 of 12: ICS-213 from Shelter S21"
	Status string `json:"status"` // what it's doing, e.g. "drafting (8s)"
	State  string `json:"state"`  // ActivityWaiting, ActivityWorking, ActivityDone, or ActivityFailed
}

// Values for Activity.State.
const (
	ActivityWaiting = "waiting"
	ActivityWorking = "working"
	ActivityDone    = "done"
	ActivityFailed  = "failed"
)

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

// Generate produces one draft message for each entry of req.Messages,
// together satisfying req.Level's proword profile (spread across the
// batch), calling client to draft the content. It does not create the
// messages in any incident; see Apply for that.
func Generate(ctx context.Context, client *ClaudeClient, req Request) ([]Result, error) {
	// Messages are drafted several at a time; the callbacks may not expect
	// concurrent calls.
	var callbackMu sync.Mutex
	progress := func(s string) {
		if req.Progress != nil {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			req.Progress(s)
		}
	}
	activity := func(a Activity) {
		if req.Activity != nil {
			callbackMu.Lock()
			defer callbackMu.Unlock()
			req.Activity(a)
		}
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
		if isContentless(m) {
			continue
		}
		// Categories the pre-filled fields already cover don't need to
		// be asked for again; Assigned keeps the full list so coverage
		// is still reported against it.
		assigned[i] = plans[i].Categories
		plans[i].Categories = missingCategories(plans[i].Categories, messageCounts(draft))
		specs, routed := PromptFields(draft, plans[i].Categories)
		if m.Handling != "" {
			specs = slices.DeleteFunc(specs, func(s FieldSpec) bool { return handlingCommon[s.Common] })
		}
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
	var toWrite int // messages Claude writes
	for _, m := range req.Messages {
		if !isContentless(m) {
			toWrite++
		}
	}
	var brief string
	if toWrite > 1 {
		const label = "Planning a scenario shared by all the messages"
		plan := Activity{Key: "plan", Label: label, Status: "planning", State: ActivityWorking}
		progress(label + "...")
		activity(plan)
		text, err := completeWithHeartbeat(ctx, client, briefSystemPrompt, "", buildBriefPrompt(req), maxOutputTokens, func(elapsed time.Duration) {
			progress(fmt.Sprintf("%s... (%s elapsed)", label, elapsed))
			plan.Status = fmt.Sprintf("planning (%s)", elapsed)
			activity(plan)
		})
		if err != nil && !errors.Is(err, ErrOutputCutOff) {
			plan.Status, plan.State = "failed: "+err.Error(), ActivityFailed
			activity(plan)
			return nil, err
		}
		brief = strings.TrimSpace(text)
		plan.Status, plan.State = "done", ActivityDone
		activity(plan)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	g := &generation{ctx: ctx, client: client, req: req, shared: sharedPrompt(req, brief), specs: specsPerMsg,
		routed: routedPerMsg, baseWords: baseWords, results: results, progress: progress, activity: activity}
	if toWrite > 1 {
		// Messages start in parallel, before any could read a cache entry
		// written by another, so write it once up front.
		if err := client.Prewarm(ctx, systemPrompt, g.shared); err != nil {
			slog.Warn("prewarming the prompt cache failed", "err", err)
		}
	}
	if err := g.generateAll(plans, cancel); err != nil {
		return nil, err
	}
	progress("Done generating messages.")
	return results, nil
}

// maxParallel is how many messages are drafted at once.
const maxParallel = 4

// generateAll drafts every message, up to maxParallel at a time. A reply
// waits until the message it answers is done, so its prompt can include
// that message's content. The first error cancels the rest.
func (g *generation) generateAll(plans []MessagePlan, cancel context.CancelFunc) error {
	order := generationOrder(g.req.Messages)
	pos := make([]int, len(order))
	for n, idx := range order {
		pos[idx] = n
	}
	done := make([]chan struct{}, len(order))
	for i := range done {
		done[i] = make(chan struct{})
	}
	for n, idx := range order {
		a := g.messageActivity(idx, n+1)
		a.Status, a.State = "waiting to start", ActivityWaiting
		if isContentless(g.req.Messages[idx]) {
			a.Status, a.State = "nothing to write: sending it is what counts", ActivityDone
		} else if r := g.req.Messages[idx].ReplyTo; r > 0 && pos[r-1] < n {
			a.Status = fmt.Sprintf("waiting for message %d, which it replies to", pos[r-1]+1)
		}
		g.activity(a)
	}
	sem := make(chan struct{}, maxParallel)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		finished int
	)
	for n, idx := range order {
		if isContentless(g.req.Messages[idx]) {
			if err := g.createAsIs(idx); err != nil {
				cancel()
				return err
			}
			close(done[idx])
			mu.Lock()
			finished++
			mu.Unlock()
			continue
		}
		wg.Go(func() {
			defer close(done[idx])
			// Only wait for a target generated earlier, so a reply cycle
			// can't deadlock.
			if r := g.req.Messages[idx].ReplyTo; r > 0 && pos[r-1] < n {
				select {
				case <-done[r-1]:
				case <-g.ctx.Done():
					g.stopped(idx, n+1)
					return
				}
			}
			a := g.messageActivity(idx, n+1)
			select {
			case sem <- struct{}{}:
			default:
				a.Status = fmt.Sprintf("waiting for a free slot (%d messages are drafted at a time)", maxParallel)
				g.activity(a)
				select {
				case sem <- struct{}{}:
				case <-g.ctx.Done():
					g.stopped(idx, n+1)
					return
				}
			}
			defer func() { <-sem }()
			err := g.generateMessage(idx, n+1, plans[idx])
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if errors.Is(err, context.Canceled) {
					a.Status, a.State = "stopped", ActivityFailed
				} else {
					a.Status, a.State = "failed: "+err.Error(), ActivityFailed
				}
				g.activity(a)
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			finished++
			res := g.results[idx]
			a.Status, a.State = fmt.Sprintf("done (%d words)", res.Words), ActivityDone
			if len(res.Missing) > 0 || len(res.MissingFields) > 0 || res.Words > MaxWords+WordTolerance {
				a.Status = fmt.Sprintf("done, with warnings to review (%d words)", res.Words)
			}
			g.activity(a)
			g.progress(fmt.Sprintf("Finished message %d of %d (%d of %d done)", n+1, len(order), finished, len(order)))
		})
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return g.ctx.Err()
}

// generation holds the state shared while a batch's messages are generated.
type generation struct {
	ctx       context.Context
	client    *ClaudeClient
	req       Request
	shared    string // sharedPrompt, the cached prefix of every message's prompt
	specs     [][]FieldSpec
	routed    []map[prowords.Category]string
	baseWords []int
	results   []Result
	progress  func(string)
	activity  func(Activity)
}

// createAsIs records message idx, which has no content to generate (see
// isContentless), as it is.
func (g *generation) createAsIs(idx int) error {
	_, err := g.evaluate(idx, map[string]string{}, nil, false)
	return err
}

// stopped reports that message idx, the n-th in generation order, won't be
// drafted, as another failed.
func (g *generation) stopped(idx, n int) {
	a := g.messageActivity(idx, n)
	a.Status, a.State = "stopped", ActivityFailed
	g.activity(a)
}

// messageActivity returns the activity of message idx, the n-th in
// generation order, with only its Key and Label set.
func (g *generation) messageActivity(idx, n int) Activity {
	m := g.req.Messages[idx]
	label := fmt.Sprintf("Message %d of %d: %s", n, len(g.req.Messages), strings.TrimPrefix(strings.TrimPrefix(m.MsgType.Name(), "a "), "an "))
	if from := strings.TrimSpace(m.From + " " + m.FromPrefix); from != "" {
		label += " from " + from
	}
	return Activity{Key: fmt.Sprintf("message-%d", idx+1), Label: label}
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
		act := g.messageActivity(idx, n)
		act.State, act.Status = ActivityWorking, "drafting"
		if round > 0 {
			act.Status = fmt.Sprintf("revising, attempt %d of %d: %s", round+1, maxRounds, reason)
		}
		status := act.Status
		g.progress(label + "...")
		g.activity(act)
		prompt := messagePrompt(g.req, g.specs, g.results, []int{idx}, []MessagePlan{plan}, g.routed, g.baseWords, res.Values != nil) + retryNote
		text, err := completeWithHeartbeat(g.ctx, g.client, systemPrompt, g.shared, prompt, maxOutputTokens, func(elapsed time.Duration) {
			g.progress(fmt.Sprintf("%s... (%s elapsed)", label, elapsed))
			act.Status = fmt.Sprintf("%s (%s)", status, elapsed)
			g.activity(act)
		})
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
		act.Status = "checking the draft"
		g.activity(act)
		ev, err := g.evaluate(idx, parsed[0], invalid[0], checkOne)
		if err != nil {
			return err
		}
		if bestScore >= 0 && ev.score >= bestScore {
			break // the revision didn't improve on the best version so far
		}
		best, bestScore = *res, ev.score
		if ev.score == 0 {
			return nil
		}
		// The revision sees every requirement, with the unmet ones
		// flagged, so fixing one doesn't lose another.
		plan = MessagePlan{Categories: wanted, Unmet: res.Missing, MissingFields: ev.problems, CheckOne: checkOne, CheckOneUnmet: ev.needCheck, LongFields: ev.long}
		if ev.over {
			plan.Words = res.Words
		}
		reason = revisionReason(res, ev.problems, ev.over, ev.needCheck, len(ev.long) > 0)
		slog.Info("revising generated training message", "message", idx+1, "reason", reason, "values", res.Values)
	}
	if bestScore < 0 {
		return fmt.Errorf("message %d: no usable response from Claude after %d attempts: %w", idx+1, maxRounds, lastErr)
	}
	*res = best
	return nil
}

// evaluation is how one version of a message measures up (see evaluate).
type evaluation struct {
	problems  []FieldSpec // fields failing validation
	over      bool        // over the word budget
	needCheck bool        // a long form with no checkbox checked
	long      []FieldSpec // subject-like fields that are too long
	score     int         // 0 when nothing needs revising; lower is better
}

// evaluate records values, one version of message idx, in its Result and
// scores it: each response holds the message's complete values, replacing
// the previous round's, so content Claude drops to get under the word
// budget is really gone.
func (g *generation) evaluate(idx int, values map[string]string, invalid []string, checkOne bool) (evaluation, error) {
	draftMu.Lock()
	defer draftMu.Unlock()
	draft, err := buildDraft(g.req.Incident, g.req.Messages[idx], values)
	if err != nil {
		return evaluation{}, err
	}
	res := &g.results[idx]
	res.Values = values
	res.InvalidFields = invalid
	res.Counts = messageCounts(draft)
	res.Missing = missingCategories(res.Assigned, res.Counts)
	res.Words = messageWordCount(draft)
	var ev evaluation
	ev.problems = problemSpecs(draft)
	res.MissingFields = fieldLabels(ev.problems)
	for _, p := range ev.problems {
		if i := slices.IndexFunc(g.specs[idx], func(s FieldSpec) bool { return s.Tag == p.Tag }); i >= 0 {
			// e.g. Item 2's quantity, required once Item 2 is named
			g.specs[idx][i].Optional, g.specs[idx][i].Group = false, p.Group
		} else {
			g.specs[idx] = append(g.specs[idx], p)
		}
	}
	ev.over = res.Words > MaxWords+WordTolerance
	ev.needCheck = checkOne && !anyChecked(draft, g.specs[idx])
	ev.long = longSummaries(draft)
	ev.score = 100*len(ev.problems) + len(res.Missing) + len(ev.long)
	if ev.over {
		ev.score += 10
	}
	if ev.needCheck {
		ev.score++
	}
	return ev, nil
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

// completeWithHeartbeat calls client.Complete, calling tick with the time
// elapsed every heartbeatInterval while the call is in flight, so a caller
// displaying progress to a user always has something recent to show during
// a slow API call.
func completeWithHeartbeat(ctx context.Context, client *ClaudeClient, system, shared, prompt string, maxTokens int, tick func(elapsed time.Duration)) (string, error) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	defer wg.Wait() // tick is never called once this returns
	defer close(done)
	interval := heartbeatInterval
	wg.Go(func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		elapsed := interval
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				tick(elapsed)
				elapsed += interval
			}
		}
	})
	return client.Complete(ctx, system, shared, prompt, maxTokens)
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
