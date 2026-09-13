package genmsg

import (
	"fmt"
	"strings"
	"time"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
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
// required dates and times -- with values (keyed as by Describe) layered on.
func buildDraft(inc *incident.Incident, m MessageSpec, values map[string]string) (*message.DraftMessage, error) {
	draft, ok := m.MsgType.NewDraft().(*message.DraftMessage)
	if !ok {
		return nil, fmt.Errorf("message type %q does not support draft creation", m.MsgType.Tag())
	}
	inc.ApplyDefaults(draft)
	applyPartyFields(draft, m)
	setFieldValues(draft, values)
	// After the values, since they can make further dates/times required.
	fillRequiredDateTimes(draft, time.Now())
	return draft, nil
}

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
		case "mm/dd/yyyy":
			v = now.Format("01/02/2006")
		case "hh:mm":
			v = now.Format("15:04")
		default:
			continue
		}
		f.SetValue(msg, f.FromHuman(msg, v))
	}
}

// problemSpecs returns a spec, with Problem set to the validation error,
// for each field of msg that Claude could fix and that fails validation: a
// required field (including conditionally required, checkbox, and choice
// fields) left empty, or a malformed value. Administrative fields the
// incident fills in on its own are excluded.
func problemSpecs(msg message.Message) []FieldSpec {
	var specs []FieldSpec
	for f := range msg.Fields() {
		key := fieldKey(f)
		if key == "" || skipCommon[f.Common()] || !f.Settable() || !f.Editable(msg, true) {
			continue
		}
		if err := f.Validate(msg, f, 0); err != nil {
			s := newFieldSpec(msg, f, key)
			s.Required = true
			s.Problem = err.Error()
			specs = append(specs, s)
		}
	}
	return specs
}

// messageWordCount returns the total number of words across msg's content
// fields: every field with a value except the administrative ones the
// incident fills in and date/time stamps.
func messageWordCount(msg message.Message) int {
	var n int
	for f := range msg.Fields() {
		if fieldKey(f) == "" || skipCommon[f.Common()] || !f.Settable() {
			continue
		}
		switch f.EditHint() {
		case "mm/dd/yyyy", "hh:mm", "mm/dd/yyyy hh:mm":
			continue
		}
		n += len(strings.Fields(f.Value(msg)))
	}
	return n
}
