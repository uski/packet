package genmsg

import (
	"fmt"
	"strings"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/message/address"
	"github.com/rothskeller/packet/v4/message/field"
	"github.com/rothskeller/packet/v4/message/messageid"
)

// Applied is one message that was successfully created in an incident by
// Apply: its assigned local message ID, its message type, and the
// generation Result (including the proword counts an evaluator can show in
// a summary table).
type Applied struct {
	Ident   int // the incident log entry's identifier
	ID      string
	MsgType message.EditableMType
	Result  Result
}

// Apply creates a new draft message in inc for each Result, using the
// corresponding entry of specs (which must be the same slice, or an
// equivalent one, passed as Request.Messages to Generate): its message type
// determines the draft created, and its From/To/FromLocation/ToLocation (if
// any) are set directly via applyPartyFields, the same way Generate applied
// them to decide what to ask Claude for. Values is then layered on top
// exactly as "packet new" plus "packet set" would for a hand-created
// message, and DrillTrafficPhrase's presence is guaranteed (see
// ensureDrillTraffic) before creating it. It returns one Applied per
// result, in the same order, for the caller to report to the evaluator
// (e.g. a table of message ID, type, and proword counts, and a warning for
// any message whose assigned categories were not fully satisfied).
func Apply(inc *incident.Incident, specs []MessageSpec, results []Result) ([]Applied, error) {
	if len(specs) != len(results) {
		return nil, fmt.Errorf("genmsg.Apply: %d message specs but %d results", len(specs), len(results))
	}
	applied := make([]Applied, len(results))
	ids := make([]string, len(results))
	reserved, err := reserveMessageNumbers(inc, specs)
	if err != nil {
		return nil, err
	}
	nextSeq := map[string]int{}
	records := map[int]TrainingRecord{}
	// A reply is created after the message it answers, so it can refer to
	// that message's number.
	for _, i := range generationOrder(specs) {
		res, spec := results[i], specs[i]
		newmsg, err := buildDraft(inc, spec, res.Values)
		if err != nil {
			return nil, err
		}
		if spec.MsgNo != "" || spec.FromPrefix != "" {
			id := spec.MsgNo
			if id == "" {
				if id, err = nextStationMessageID(inc, spec.FromPrefix, nextSeq, reserved); err != nil {
					return nil, err
				}
			}
			// Forms carry the number in a field; plain messages in the subject line.
			setCommonField(newmsg, "originMessageID", id)
			setCommonField(newmsg, "subjectMessageID", id)
		}
		if spec.ToPrefix != "" {
			if addrs, err := address.ParseList(spec.ToPrefix); err == nil && len(addrs) > 0 {
				setCommonField(newmsg, "headerTo", spec.ToPrefix)
			}
		}
		if r := spec.ReplyTo; r > 0 && ids[r-1] != "" {
			setCommonField(newmsg, "reference", ids[r-1])
		}
		ensureDrillTraffic(newmsg)
		// Measured on the final message, after the drill-traffic phrase.
		res.Counts = messageCounts(newmsg)
		res.Missing = missingCategories(res.Assigned, res.Counts)
		res.MissingFields = fieldLabels(problemSpecs(newmsg))
		res.Words = messageWordCount(newmsg)
		le, addErr := inc.AddDraftMessage(newmsg)
		if addErr != nil {
			return nil, fmt.Errorf("adding generated draft message: %w", addErr)
		}
		ids[i] = le.LocalMsgID
		applied[i] = Applied{Ident: le.Ident, ID: le.LocalMsgID, MsgType: spec.MsgType, Result: res}
		if spec.Training != nil {
			records[le.Ident] = *spec.Training
		}
	}
	if err := saveTrainingRecords(inc.Dir, records); err != nil {
		return nil, fmt.Errorf("saving the training message records: %w", err)
	}
	return applied, nil
}

func setCommonField(msg *message.DraftMessage, common, value string) {
	if f := FindFieldByCommon(msg, common); f != nil {
		f.SetValue(msg, f.FromHuman(msg, value))
	}
}

// nextStationMessageID returns the next message number for the station with
// the given prefix, e.g. "S24-101P": one past the highest number with that
// prefix already in inc, or in this batch (tracked in next).
func nextStationMessageID(inc *incident.Incident, prefix string, next map[string]int, reserved map[string]bool) (string, error) {
	if _, ok := next[prefix]; !ok {
		seq := 100
		for _, le := range inc.Log {
			if le.Status == incident.StatusDeleted {
				continue // its number is free again
			}
			for _, id := range []string{le.LocalMsgID, le.FromMsgID, le.ToMsgID} {
				if p, n, _, err := messageid.Decode(id, true, false); err == nil && p == prefix && n > seq {
					seq = n
				}
			}
		}
		next[prefix] = seq
	}
	next[prefix]++
	for reserved[numberKey(prefix, next[prefix])] {
		next[prefix]++
	}
	return messageid.Encode(prefix, next[prefix], "P")
}

// numberKey identifies a message number regardless of its suffix.
func numberKey(prefix string, seq int) string {
	return fmt.Sprintf("%s-%03d", strings.ToUpper(prefix), seq)
}

// reserveMessageNumbers returns the numbers specs give their messages (see
// MessageSpec.MsgNo), by numberKey, so no other message gets them. It fails
// if one is already used by a message in inc. Numbers with inc's own prefix
// move inc's next message number past them, so neither the messages Apply
// numbers that way nor later ones take them.
func reserveMessageNumbers(inc *incident.Incident, specs []MessageSpec) (map[string]bool, error) {
	reserved := map[string]bool{}
	for _, spec := range specs {
		if spec.MsgNo == "" {
			continue
		}
		p, n, _, err := messageid.Decode(spec.MsgNo, true, false)
		if err != nil {
			return nil, fmt.Errorf("invalid message number %q: %w", spec.MsgNo, err)
		}
		reserved[numberKey(p, n)] = true
	}
	if len(reserved) == 0 {
		return reserved, nil
	}
	for _, le := range inc.Log {
		if le.Status == incident.StatusDeleted {
			continue
		}
		for _, id := range []string{le.LocalMsgID, le.FromMsgID, le.ToMsgID} {
			if p, n, _, err := messageid.Decode(id, true, false); err == nil && reserved[numberKey(p, n)] {
				return nil, fmt.Errorf("message number %s is already used in this incident; delete that message first (e.g. with Message > Delete All Messages) or change the scenario's number", id)
			}
		}
	}
	own, next, suffix, err := messageid.Decode(inc.Config.TxMessageID, true, false)
	if err != nil {
		return reserved, nil
	}
	for key := range reserved {
		if p, n, _, err := messageid.Decode(key, true, false); err == nil && p == own && n >= next {
			next = n + 1
		}
	}
	if id, err := messageid.Encode(own, next, suffix); err == nil && id != inc.Config.TxMessageID {
		if inc.Config.TxMessageID == inc.Config.RxMessageID {
			inc.Config.RxMessageID = id
		}
		inc.Config.TxMessageID = id
	}
	return reserved, nil
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
		if f.Multiline() && (multiline == nil || f.Common() == "defaultBody") {
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
