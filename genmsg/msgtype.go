package genmsg

import (
	"strings"

	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// This file answers "what kind of message is this?": how a message type is
// named and found, whether its traffic is operator-to-operator rather than
// a served agency's, and whether it has any content to generate at all.

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

// opToOpTypes are the message types (by create tag, lower case) counted as
// operator-to-operator traffic; every other type is a 3rd party form.
var opToOpTypes = map[string]bool{"plain": true, "check-in": true, "check-out": true}

// isOpToOp reports whether messages of the given type are always counted as
// operator-to-operator traffic.
func isOpToOp(msgType string) bool {
	mt, ok := FindMsgType(msgType)
	return ok && opToOpTypes[strings.ToLower(mt.CreateTag())]
}

// isOpToOpMessage reports whether fm is counted as operator-to-operator
// traffic: marked so, or of a type that always is.
func isOpToOpMessage(fm FlowMessage) bool {
	return fm.OpToOp || isOpToOp(fm.MsgType)
}

// isAllStations reports whether a message whose To is -1 goes to every
// other party.
func isAllStations(toLabel string) bool {
	return strings.EqualFold(strings.TrimSpace(toLabel), "All Stations")
}

// contentlessTypes are the message types (by create tag, lower case) whose
// sending is the whole point: a check-in or check-out has nothing to write,
// so it is created as is, without asking Claude for anything.
var contentlessTypes = map[string]bool{"check-in": true, "check-out": true}

// isContentless says whether m is a message with no content to generate.
func isContentless(m MessageSpec) bool {
	return IsContentlessType(m.MsgType)
}

// IsContentlessType says whether messages of type t have no content: a
// check-in or check-out, whose sending is all that counts. Such a message
// is never generated, and no proword is ever counted in it.
func IsContentlessType(t message.MType) bool {
	emt, ok := t.(message.EditableMType)
	return ok && contentlessTypes[strings.ToLower(emt.CreateTag())]
}

// messageCounts counts the prowords in msg's content (see allFieldValues);
// a contentless message (see IsContentlessType) has none.
func messageCounts(msg message.Message) map[prowords.Category]int {
	if IsContentlessType(msg.Type()) {
		return map[prowords.Category]int{}
	}
	return prowords.CountFields(allFieldValues(msg))
}

// typeAbbrev returns the short name of message type mt: its form tag, as
// in a subject line (e.g. "ICS213", "ResReq"), or "Plain".
func typeAbbrev(mt message.EditableMType) string {
	if mt == nil {
		return ""
	}
	if tag := mt.CreateTag(); tag != "" && !strings.EqualFold(tag, "plain") {
		return tag
	}
	return "Plain"
}
