package genmsg

import (
	"fmt"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
)

// Apply creates a new draft message in inc for each Result, using the
// corresponding entry of msgTypes (which must be the same slice, or an
// equivalent one, passed as Request.MsgTypes to Generate) and setting its
// fields from Values exactly as "packet new" plus "packet set" would for a
// hand-created message. It returns the local message IDs of the created
// drafts, in the same order as results, along with any results whose
// assigned categories were not fully satisfied (for the caller to warn the
// evaluator about, e.g. "message 3 is missing GPS COORDINATES content").
func Apply(inc *incident.Incident, msgTypes []message.EditableMType, results []Result) (ids []string, incomplete []Result, err error) {
	if len(msgTypes) != len(results) {
		return nil, nil, fmt.Errorf("genmsg.Apply: %d message types but %d results", len(msgTypes), len(results))
	}
	ids = make([]string, 0, len(results))
	for i, res := range results {
		msgtype := msgTypes[i]
		newmsg, ok := msgtype.NewDraft().(*message.DraftMessage)
		if !ok {
			return ids, incomplete, fmt.Errorf("message type %q does not support draft creation", msgtype.Tag())
		}
		inc.ApplyDefaults(newmsg)
		for f := range newmsg.Fields() {
			tag := f.Tag()
			if tag == "" {
				continue
			}
			if v, ok := res.Values[tag]; ok && v != "" {
				f.SetValue(newmsg, f.FromHuman(newmsg, v))
			}
		}
		le, addErr := inc.AddDraftMessage(newmsg)
		if addErr != nil {
			return ids, incomplete, fmt.Errorf("adding generated draft message: %w", addErr)
		}
		ids = append(ids, le.LocalMsgID)
		if len(res.Missing) > 0 {
			incomplete = append(incomplete, res)
		}
	}
	return ids, incomplete, nil
}
