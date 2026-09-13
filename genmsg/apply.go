package genmsg

import (
	"fmt"
	"strings"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/message/field"
)

// Applied is one message that was successfully created in an incident by
// Apply: its assigned local message ID, its message type, and the
// generation Result (including the proword counts an evaluator can show in
// a summary table).
type Applied struct {
	ID      string
	MsgType message.EditableMType
	Result  Result
}

// Apply creates a new draft message in inc for each Result, using the
// corresponding entry of msgTypes (which must be the same slice, or an
// equivalent one, passed as Request.MsgTypes to Generate) and setting its
// fields from Values exactly as "packet new" plus "packet set" would for a
// hand-created message. It then guarantees DrillTrafficPhrase appears
// somewhere in the message (see ensureDrillTraffic) before creating it. It
// returns one Applied per result, in the same order, for the caller to
// report to the evaluator (e.g. a table of message ID, type, and proword
// counts, and a warning for any message whose assigned categories were not
// fully satisfied).
func Apply(inc *incident.Incident, msgTypes []message.EditableMType, results []Result) ([]Applied, error) {
	if len(msgTypes) != len(results) {
		return nil, fmt.Errorf("genmsg.Apply: %d message types but %d results", len(msgTypes), len(results))
	}
	applied := make([]Applied, 0, len(results))
	for i, res := range results {
		msgtype := msgTypes[i]
		newmsg, ok := msgtype.NewDraft().(*message.DraftMessage)
		if !ok {
			return applied, fmt.Errorf("message type %q does not support draft creation", msgtype.Tag())
		}
		inc.ApplyDefaults(newmsg)
		for key, v := range res.Values {
			if v == "" {
				continue
			}
			if f := FindField(newmsg, key); f != nil {
				f.SetValue(newmsg, f.FromHuman(newmsg, v))
			}
		}
		ensureDrillTraffic(newmsg)
		le, addErr := inc.AddDraftMessage(newmsg)
		if addErr != nil {
			return applied, fmt.Errorf("adding generated draft message: %w", addErr)
		}
		applied = append(applied, Applied{ID: le.LocalMsgID, MsgType: msgtype, Result: res})
	}
	return applied, nil
}

// ensureDrillTraffic guarantees DrillTrafficPhrase appears somewhere in
// msg's editable field values. The prompt already asks Claude for this, but
// it's a fixed, non-creative requirement where "always" needs to actually
// mean always, so this enforces it deterministically: if no field already
// contains the phrase, it's appended to the message's main free-text
// (multiline) field, or failing that its subject-like field, whichever is
// found first.
func ensureDrillTraffic(msg *message.DraftMessage) {
	needle := strings.ToLower(DrillTrafficPhrase)
	var multiline, fallback field.Field
	for f := range msg.Fields() {
		if !f.Editable(msg, false) || !f.Settable() {
			continue
		}
		if strings.Contains(strings.ToLower(f.Value(msg)), needle) {
			return // already present somewhere; nothing to do
		}
		if multiline == nil && f.Multiline() {
			multiline = f
		}
		if fallback == nil && (f.Common() == "messageSummary" || f.Common() == "subjectSummary") {
			fallback = f
		}
	}
	target := multiline
	if target == nil {
		target = fallback
	}
	if target == nil {
		return // no suitable field on this message type; best effort only
	}
	cur := target.Value(msg)
	var next string
	if cur == "" {
		next = DrillTrafficPhrase + "."
	} else {
		next = strings.TrimRight(cur, " ") + "  " + DrillTrafficPhrase + "."
	}
	target.SetValue(msg, target.FromHuman(msg, next))
}
