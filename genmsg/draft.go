package genmsg

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/message/field"
)

// MaxWords is the target upper bound on a generated message's total word
// count across all of its content fields. WordTolerance is how far over it
// a message may go ("around" MaxWords) before Generate asks Claude to trim.
const (
	MaxWords      = 50
	WordTolerance = 10
	// minValueWords floors the word budget given to Claude, so a form
	// whose pre-filled fields already approach MaxWords still has room
	// for its required content.
	minValueWords = 15
)

// buildDraft creates a draft of m's message type holding everything the
// tool fills in itself -- the incident's defaults, m's party fields, and
// required dates and times -- with values (keyed as by describeFields) layered on.
func buildDraft(inc *incident.Incident, m MessageSpec, values map[string]string) (*message.DraftMessage, error) {
	draft, ok := m.MsgType.NewDraft().(*message.DraftMessage)
	if !ok {
		return nil, fmt.Errorf("message type %q does not support draft creation", m.MsgType.Tag())
	}
	inc.ApplyDefaults(draft)
	// The incident's default body is a template for hand-written messages.
	// Left in, it would hide the body from Claude as already filled, and the
	// message's details would end up in its subject.
	if f := FindFieldByCommon(draft, "defaultBody"); f != nil {
		f.SetValue(draft, "")
	}
	// A training message is handed to the candidates, whose own radio
	// operators fill in its Radio Operator section.
	clearOperatorFields(draft)
	applyPartyFields(draft, m)
	setFieldValues(draft, values)
	applyHandling(draft, m)
	// After the values, since they can make further dates/times required.
	if m.Date != "" {
		setIncidentDate(draft, m.Date, m.Time)
	} else {
		fillRequiredDateTimes(draft, time.Now())
	}
	return draft, nil
}

const (
	dateHint     = "mm/dd/yyyy"
	timeHint     = "hh:mm"
	dateTimeHint = "mm/dd/yyyy hh:mm"
)

// setIncidentDate sets every date field of msg that has a value or is
// required to date. If tm (HH:MM) is empty, it clears every time field;
// otherwise, it sets the message time field and every other required time
// field to tm, and clears the rest. The Radio Operator section is left
// alone.
func setIncidentDate(msg *message.DraftMessage, date, tm string) {
	for f := range msg.Fields() {
		if !f.Settable() || operatorCommon[f.Common()] || strings.HasPrefix(f.Label(), "Operator") {
			continue
		}
		switch f.EditHint() {
		case dateHint:
			if f.Value(msg) != "" || f.Validate(msg, f, 0) != nil {
				if v := f.FromHuman(msg, date); f.Value(msg) != v {
					f.SetValue(msg, v)
				}
			}
		case timeHint:
			if f.Value(msg) != "" {
				f.SetValue(msg, "")
			}
			if tm != "" && (f.Common() == "messageTime" || f.Validate(msg, f, 0) != nil) {
				f.SetValue(msg, f.FromHuman(msg, tm))
			}
		}
	}
}

// normalizeTime returns t (e.g. "9:05", "0905", or "09:05") as HH:MM, or an
// error if it isn't a time of day. An empty t stays empty.
func normalizeTime(t string) (string, error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", nil
	}
	m := timeOfDayRE.FindStringSubmatch(t)
	if m == nil {
		return "", fmt.Errorf("invalid time %q (use HH:MM)", t)
	}
	h, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	if h > 23 || mm > 59 {
		return "", fmt.Errorf("invalid time %q (use HH:MM)", t)
	}
	return fmt.Sprintf("%02d:%02d", h, mm), nil
}

var timeOfDayRE = regexp.MustCompile(`^(\d{1,2}):?(\d{2})$`)

// fillRequiredDateTimes sets every empty, required date or time field of
// msg to now. The incident's defaults only fill the message date, and time
// fields belonging to a combined date/time field aren't editable, so they'd
// otherwise never be filled; there's nothing for Claude to add by inventing
// them.
func fillRequiredDateTimes(msg *message.DraftMessage, now time.Time) {
	for f := range msg.Fields() {
		if !f.Settable() || f.Value(msg) != "" || f.Validate(msg, f, 0) == nil {
			continue
		}
		var v string
		switch f.EditHint() {
		case dateHint:
			v = now.Format("01/02/2006")
		case timeHint:
			v = now.Format("15:04")
		default:
			continue
		}
		f.SetValue(msg, f.FromHuman(msg, v))
	}
}

// isDateOrTime reports whether f holds a date, a time, or both.
func isDateOrTime(f field.Field) bool {
	switch f.EditHint() {
	case dateHint, timeHint, dateTimeHint:
		return true
	}
	return false
}

// problemSpecs returns a spec, with Problem set to the validation error,
// for each field of msg that Claude could fix and that fails validation: a
// required field (including conditionally required, checkbox, and choice
// fields) left empty, or a malformed value. Administrative fields the
// incident fills in on its own are excluded.
func problemSpecs(msg message.Message) []fieldSpec {
	var specs []fieldSpec
	for f := range msg.Fields() {
		key := fieldKey(f)
		if key == "" || skipCommon[f.Common()] || !f.Settable() || !f.Editable(msg, true) || isDateOrTime(f) {
			continue // dates and times are set by the tool, and times may be left blank on purpose
		}
		if err := f.Validate(msg, f, 0); err != nil && !isFictitiousCallSignError(f.Value(msg), err) {
			s := newFieldSpec(msg, f, key)
			s.Required = true
			s.Problem = err.Error()
			specs = append(specs, s)
		}
	}
	for _, g := range failingCheckboxGroups(msg) {
		for _, key := range g.keys {
			if slices.ContainsFunc(specs, func(s fieldSpec) bool { return s.Tag == key }) {
				continue
			}
			if f := FindField(msg, key); f != nil {
				s := newFieldSpec(msg, f, key)
				s.Required, s.Problem, s.Group = true, g.err.Error(), g.label
				specs = append(specs, s)
			}
		}
	}
	return specs
}

// operatorCommon lists the common fields of a form's Radio Operator section.
var operatorCommon = map[string]bool{
	"operatorName":        true,
	"operatorCall":        true,
	"operatorDate":        true,
	"operatorTime":        true,
	"operatorMethod":      true,
	"operatorMethodOther": true,
	"receiverSender":      true,
}

// clearOperatorFields empties msg's Radio Operator section (operator name,
// call sign, date and time, sent or received, how, and relay stations),
// which the incident's defaults fill with the local operator.
func clearOperatorFields(msg *message.DraftMessage) {
	for f := range msg.Fields() {
		if f.Settable() && f.Value(msg) != "" && (operatorCommon[f.Common()] || strings.HasPrefix(f.Label(), "Operator")) {
			f.SetValue(msg, "")
		}
	}
}

// maxSummaryWords caps a subject, title, or summary field, which Claude
// otherwise tends to fill with the message's details.
const maxSummaryWords = 8

// longSummaries returns the subject-like fields of msg (see alwaysInclude)
// longer than maxSummaryWords, with Problem giving their word count.
func longSummaries(msg message.Message) []fieldSpec {
	var long []fieldSpec
	for f := range msg.Fields() {
		if !alwaysInclude[f.Common()] {
			continue
		}
		if n := len(strings.Fields(f.Value(msg))); n > maxSummaryWords {
			s := newFieldSpec(msg, f, fieldKey(f))
			s.Problem = fmt.Sprintf("%d words", n)
			long = append(long, s)
		}
	}
	return long
}

// manyCheckboxes is how many checkboxes make a form long enough that a
// generated message must check at least one, so the sender and receiver
// have to handle a checked box.
const manyCheckboxes = 4

type checkboxGroup struct {
	label string
	err   error
	keys  []string
}

// failingCheckboxGroups returns the checkbox groups of msg that fail
// validation, such as a required group with nothing checked. A group has no
// key of its own, so otherwise Claude would see only its checkboxes, each
// looking optional, and the group would never be reported.
func failingCheckboxGroups(msg message.Message) []checkboxGroup {
	var groups []checkboxGroup
	for f := range msg.Fields() {
		kids := f.Children()
		if len(kids) == 0 {
			continue
		}
		var keys []string
		for _, c := range kids {
			cs := c.Choices(msg)
			if len(cs) != 1 || cs[0].Human != "checked" || fieldKey(c) == "" {
				keys = nil // not a checkbox group (e.g. a date/time pair)
				break
			}
			keys = append(keys, fieldKey(c))
		}
		if len(keys) == 0 {
			continue
		}
		if err := f.Validate(msg, f, 0); err != nil {
			groups = append(groups, checkboxGroup{label: f.Label(), err: err, keys: keys})
		}
	}
	return groups
}

func countCheckboxes(specs []fieldSpec) int {
	var n int
	for _, s := range specs {
		if isCheckbox(s) {
			n++
		}
	}
	return n
}

// anyChecked reports whether any checkbox among specs is checked in msg.
func anyChecked(msg message.Message, specs []fieldSpec) bool {
	for _, s := range specs {
		if !isCheckbox(s) {
			continue
		}
		if f := FindField(msg, s.Tag); f != nil && f.Value(msg) != "" {
			return true
		}
	}
	return false
}

// messageWordCount returns the total number of words across msg's content
// fields: every field with a value except the administrative ones the
// incident fills in and date/time stamps.
func messageWordCount(msg message.Message) int {
	var n int
	for f := range contentFields(msg) {
		// Dates and times are the tool's to fill, and a field nobody
		// can set can't be shortened.
		if !f.Settable() || isDateOrTime(f) {
			continue
		}
		n += len(strings.Fields(f.Value(msg)))
	}
	return n
}

// applyPartyFields sets draft's From/To ICS Position and Location fields
// directly from m, for whichever of those the message type has and m
// provides -- used both on the probe draft in Generate (so describeFields sees
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

// handlingCommon are the common names of the fields holding a message's
// handling order.
var handlingCommon = map[string]bool{"handling": true, "subjectHandling": true}

// handlingNames maps handling order codes to their names.
var handlingNames = map[string]string{"R": "ROUTINE", "P": "PRIORITY", "I": "IMMEDIATE"}

// normalizeHandling returns h as a handling order code ("R", "P", "I"), or
// "" if it is empty or isn't one.
func normalizeHandling(h string) string {
	h = strings.ToUpper(strings.TrimSpace(h))
	for code, name := range handlingNames {
		if h == code || h == name {
			return code
		}
	}
	return ""
}

// applyHandling sets draft's handling order to m.Handling, if given.
func applyHandling(draft *message.DraftMessage, m MessageSpec) {
	code := normalizeHandling(m.Handling)
	if code == "" {
		return
	}
	for f := range draft.Fields() {
		if handlingCommon[f.Common()] && f.Settable() {
			f.SetValue(draft, f.FromHuman(draft, handlingNames[code]))
		}
	}
}

// draftMu serializes building and checking drafts: the message package's
// field definitions, shared by every draft of a type, keep state from each
// validation, so two drafts can't be checked at once. Only the Claude calls
// run in parallel.
var draftMu sync.Mutex
