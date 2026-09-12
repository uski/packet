package genmsg

import (
	"fmt"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
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
// hand-created message. It returns one Applied per result, in the same
// order, for the caller to report to the evaluator (e.g. a table of
// message ID, type, and proword counts, and a warning for any message
// whose assigned categories were not fully satisfied).
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
		le, addErr := inc.AddDraftMessage(newmsg)
		if addErr != nil {
			return applied, fmt.Errorf("adding generated draft message: %w", addErr)
		}
		applied = append(applied, Applied{ID: le.LocalMsgID, MsgType: msgtype, Result: res})
	}
	return applied, nil
}
