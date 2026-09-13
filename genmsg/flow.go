package genmsg

import (
	"fmt"
	"strings"

	"github.com/rothskeller/packet/v4/message"
)

// FlowParty is one participant in a multi-party message flow: a short
// role/position name and an optional location, e.g. {"Net Control", "County
// EOC"} or {"Shelter Manager", "Roosevelt Middle School"}. Messages in the
// flow reference parties by index rather than repeating this information,
// so the same party's identity stays exactly consistent everywhere it's
// used as a sender or recipient.
type FlowParty struct {
	Role     string `json:"role"`
	Location string `json:"location,omitempty"`
}

// FlowMessage is one message within a multi-party flow, referencing
// parties and other messages by index. This is the wire format (from a
// GUI dialog or a CLI --flow file); ResolveFlow turns a Flow into
// []MessageSpec for Generate/Apply.
type FlowMessage struct {
	MsgType string `json:"msgType"` // create tag or key, e.g. "ICS213" or "plain"
	From    int    `json:"from"`    // index into Flow.Parties
	To      int    `json:"to"`      // index into Flow.Parties, or -1 to use ToLabel instead
	ToLabel string `json:"toLabel"` // used when To is -1, e.g. "All Stations"; ignored otherwise
	Purpose string `json:"purpose"` // optional hint of what this specific message is about
	ReplyTo int    `json:"replyTo"` // 1-based index into Flow.Messages this replies to, or 0 for none
}

// Flow is a multi-party message-flow specification: the parties involved
// and the messages exchanged between them. ResolveFlow validates it and
// resolves it into a []MessageSpec.
type Flow struct {
	Parties  []FlowParty   `json:"parties"`
	Messages []FlowMessage `json:"messages"`
}

// FindMsgType looks up a registered, editable message type by its create
// tag or key (case-insensitively), e.g. "ICS213" or "plain".
func FindMsgType(tag string) (message.EditableMType, bool) {
	for mt := range message.AllTypes() {
		if emt, ok := mt.(message.EditableMType); ok {
			if strings.EqualFold(tag, emt.CreateTag()) || strings.EqualFold(tag, emt.CreateKey()) {
				return emt, true
			}
		}
	}
	return nil, false
}

// ResolveFlow validates fl and resolves it into a []MessageSpec suitable
// for Request.Messages and Apply, looking up each message's type by tag or
// key among the registered editable message types (see FindMsgType) and
// each From/To party reference among fl.Parties.
func ResolveFlow(fl Flow) ([]MessageSpec, error) {
	if len(fl.Messages) == 0 {
		return nil, fmt.Errorf("at least one message is required")
	}
	specs := make([]MessageSpec, len(fl.Messages))
	for i, fm := range fl.Messages {
		mt, ok := FindMsgType(fm.MsgType)
		if !ok {
			return nil, fmt.Errorf("message %d: no such message type %q", i+1, fm.MsgType)
		}
		if fm.From < 0 || fm.From >= len(fl.Parties) {
			return nil, fmt.Errorf("message %d: invalid \"from\" party index %d", i+1, fm.From)
		}
		spec := MessageSpec{
			MsgType:      mt,
			From:         fl.Parties[fm.From].Role,
			FromLocation: fl.Parties[fm.From].Location,
			Purpose:      strings.TrimSpace(fm.Purpose),
			ReplyTo:      fm.ReplyTo,
		}
		switch {
		case fm.To < 0:
			spec.To = strings.TrimSpace(fm.ToLabel)
		case fm.To < len(fl.Parties):
			spec.To = fl.Parties[fm.To].Role
			spec.ToLocation = fl.Parties[fm.To].Location
		default:
			return nil, fmt.Errorf("message %d: invalid \"to\" party index %d", i+1, fm.To)
		}
		if fm.ReplyTo < 0 || fm.ReplyTo > len(fl.Messages) || fm.ReplyTo == i+1 {
			return nil, fmt.Errorf("message %d: invalid \"replyTo\" %d", i+1, fm.ReplyTo)
		}
		specs[i] = spec
	}
	return specs, nil
}
