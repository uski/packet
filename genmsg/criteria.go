package genmsg

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Traffic counts messages of the kinds the Credentialing Program Handbook
// sets minimums for, in one direction (sent or received).
type Traffic struct {
	ThirdParty int `json:"thirdParty"` // 3rd party messages
	Forms      int `json:"forms"`      // of which forms
	OpToOp     int `json:"opToOp"`     // operator-to-operator messages
}

// credentialNeeds gives, for each credential a flow party can be evaluated
// for, the traffic it must send and receive (each direction), per the
// Credentialing Program Handbook's Operator Skills sections. Field I,
// Shadow I, and Packet I state no counts of their own, so they take their
// Type II minimums; the Net Control credentials aren't evaluated on
// message counts. The empty credential is a party not being evaluated.
var credentialNeeds = map[string]Traffic{
	"":   {},
	"F3": {ThirdParty: 2, Forms: 2, OpToOp: 2},
	"F2": {ThirdParty: 3, Forms: 2, OpToOp: 2},
	"F1": {ThirdParty: 3, Forms: 2, OpToOp: 2},
	"S3": {ThirdParty: 2, Forms: 2, OpToOp: 2},
	"S2": {ThirdParty: 3, Forms: 2, OpToOp: 3},
	"S1": {ThirdParty: 3, Forms: 2, OpToOp: 3},
	"P3": {ThirdParty: 2, Forms: 2, OpToOp: 2},
	"P2": {ThirdParty: 2, Forms: 2, OpToOp: 2},
	"P1": {ThirdParty: 2, Forms: 2, OpToOp: 2},
	"N3": {},
	"N2": {},
	"N1": {},
}

// opToOpTypes are the message types (by create tag, lower case) counted as
// operator-to-operator traffic; every other type is a 3rd party form.
var opToOpTypes = map[string]bool{"plain": true, "check-in": true, "check-out": true}

// Form types CompleteFlow picks from, by create tag, the least used in the
// flow first; any not registered are skipped. All have few enough required
// fields to fit a ~50-word message.
var (
	autoNetControlForms = []string{"ICS213", "SitRep", "RoadCl"}
	autoStationForms    = []string{"ICS213", "ResReq", "WSSurvey", "DmgAsmt", "RoadCl", "SitRep"}
)

const (
	// Operator-to-operator traffic is added as ICS-213s marked OpToOp, not
	// plain text; check-in and check-out messages hold only the operator
	// details that generated messages leave empty.
	autoOpToOpType     = "ICS213"
	autoRequestPurpose = "a request or instructions from the served agency"
	autoReportPurpose  = "a report or request from the station's served agency"
)

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
	label := strings.TrimSpace(toLabel)
	return label == "" || strings.EqualFold(label, "All Stations")
}

// PartyCompliance reports how one flow party's traffic compares with what
// its credential requires.
type PartyCompliance struct {
	Party      int      `json:"party"` // index into Flow.Parties
	Role       string   `json:"role"`
	Credential string   `json:"credential"`
	Need       Traffic  `json:"need"`
	Sent       Traffic  `json:"sent"`
	Received   Traffic  `json:"received"`
	Problems   []string `json:"problems,omitempty"`
}

// CheckFlow reports, for each party of fl, the traffic it sends and
// receives and any way that falls short of its credential's minimums. A
// message to "All Stations" counts as received by every other party. The
// prowords each party must transmit are assigned when the messages are
// generated (see planByParty), so only message counts are checked here.
func CheckFlow(fl Flow) ([]PartyCompliance, error) {
	if err := normalizeParties(&fl); err != nil {
		return nil, err
	}
	senders, err := flowSenders(fl)
	if err != nil {
		return nil, err
	}
	report := make([]PartyCompliance, len(fl.Parties))
	for i, p := range fl.Parties {
		report[i] = PartyCompliance{Party: i, Role: p.Role, Credential: p.Credential, Need: credentialNeeds[p.Credential]}
	}
	for i, fm := range fl.Messages {
		opToOp := isOpToOpMessage(fm)
		for _, s := range senders[i] {
			countTraffic(&report[s].Sent, opToOp)
			for _, r := range flowRecipients(fl, fm, s) {
				countTraffic(&report[r].Received, opToOp)
			}
		}
	}
	for i := range report {
		report[i].Problems = trafficProblems(report[i])
	}
	return report, nil
}

func countTraffic(t *Traffic, opToOp bool) {
	if opToOp {
		t.OpToOp++
	} else {
		t.ThirdParty++
		t.Forms++
	}
}

// flowRecipients returns the parties that receive message fm as sent by
// party sender: its To party, or for "All Stations" every other party.
func flowRecipients(fl Flow, fm FlowMessage, sender int) []int {
	if fm.To >= 0 {
		return []int{fm.To}
	}
	if !isAllStations(fm.ToLabel) {
		return nil
	}
	var all []int
	for p := range fl.Parties {
		if p != sender {
			all = append(all, p)
		}
	}
	return all
}

func trafficProblems(c PartyCompliance) []string {
	var problems []string
	check := func(verb string, have, need int, what string) {
		if have < need {
			problems = append(problems, fmt.Sprintf("%s %d of %d %s", verb, have, need, what))
		}
	}
	check("sends", c.Sent.ThirdParty, c.Need.ThirdParty, "3rd party messages")
	if c.Sent.ThirdParty >= c.Need.ThirdParty {
		check("sends", c.Sent.Forms, c.Need.Forms, "forms")
	}
	check("sends", c.Sent.OpToOp, c.Need.OpToOp, "operator-to-operator messages")
	check("receives", c.Received.ThirdParty, c.Need.ThirdParty, "3rd party messages")
	if c.Received.ThirdParty >= c.Need.ThirdParty {
		check("receives", c.Received.Forms, c.Need.Forms, "forms")
	}
	check("receives", c.Received.OpToOp, c.Need.OpToOp, "operator-to-operator messages")
	return problems
}

// shortfall returns how many more messages of the given kind (a form, or
// operator-to-operator) c must send (or, if received, receive).
func shortfall(c PartyCompliance, opToOp, received bool) int {
	t := c.Sent
	if received {
		t = c.Received
	}
	if opToOp {
		return max(0, c.Need.OpToOp-t.OpToOp)
	}
	return max(0, c.Need.ThirdParty-t.ThirdParty, c.Need.Forms-t.Forms)
}

// CompleteFlow returns fl with messages added after its existing ones until
// every party meets its credential's minimums (see CheckFlow), and how many
// were added. Traffic goes through the Net Control party (see
// netControlParty), as on a real net: Net Control sends to a station, or to
// All Stations when several stations still need that kind of message, and
// a station sends to Net Control, alternating between new messages and
// replies to unanswered Net Control messages of the same kind. Net Control's messages are added first, so the
// stations' replies can follow them. 3rd party messages rotate through
// several form types; operator-to-operator messages are ICS-213s marked
// OpToOp. No plain text messages are added.
func CompleteFlow(fl Flow) (Flow, int, error) {
	if len(fl.Parties) < 2 {
		return fl, 0, errors.New("at least two parties are needed to exchange messages")
	}
	nc, ok := netControlParty(fl.Parties)
	if !ok {
		return fl, 0, errors.New(`auto-adding traffic needs a Net Control party: give one party a Net Control credential, or a role containing "Net Control"`)
	}
	fl.Messages = slices.Clone(fl.Messages)
	for added := 0; added <= 1000; added++ {
		report, err := CheckFlow(fl)
		if err != nil {
			return fl, added, err
		}
		p, opToOp, received, found := firstShortfall(report)
		if !found {
			return fl, added, nil
		}
		var msg FlowMessage
		switch {
		case p == nc && received:
			msg = FlowMessage{From: neediestPartner(report, nc, opToOp, false), To: nc}
		case p == nc:
			msg = FlowMessage{From: nc, To: neediestPartner(report, nc, opToOp, true)}
		case received:
			msg = FlowMessage{From: nc, To: p}
			if stationsShort(report, nc, opToOp) >= 2 {
				msg.To, msg.ToLabel = -1, "All Stations"
			}
		default:
			msg = FlowMessage{From: p, To: nc}
		}
		if msg.From != nc && prefersReply(fl, msg.From) {
			if msg.ReplyTo, err = unanswered(fl, nc, msg.From, opToOp); err != nil {
				return fl, added, err
			}
		}
		msg.MsgType, msg.Purpose = autoType(fl, msg.From == nc, opToOp, msg.ReplyTo > 0)
		msg.OpToOp = opToOp
		if msg.MsgType == "" {
			return fl, added, errors.New("none of the form types used for auto-added traffic are available")
		}
		fl.Messages = append(fl.Messages, msg)
	}
	return fl, 0, errors.New("could not add enough messages to meet the credential criteria")
}

// netControlParty returns the index of the party with a Net Control
// credential, or else the first whose role mentions Net Control.
func netControlParty(parties []FlowParty) (int, bool) {
	for i, p := range parties {
		if strings.HasPrefix(p.Credential, "N") {
			return i, true
		}
	}
	for i, p := range parties {
		if strings.Contains(strings.ToLower(p.Role), "net control") {
			return i, true
		}
	}
	return 0, false
}

// firstShortfall finds a party short of some traffic: first any shortfall
// in received messages, which Net Control's messages fill, then in sent
// ones, which the stations' messages (often replies) fill.
func firstShortfall(report []PartyCompliance) (party int, opToOp, received, found bool) {
	for _, dir := range []bool{true, false} {
		for _, c := range report {
			for _, kind := range []bool{false, true} {
				if shortfall(c, kind, dir) > 0 {
					return c.Party, kind, dir, true
				}
			}
		}
	}
	return 0, false, false, false
}

// stationsShort counts the parties other than Net Control that still need
// to receive messages of the given kind.
func stationsShort(report []PartyCompliance, nc int, opToOp bool) int {
	var n int
	for _, c := range report {
		if c.Party != nc && shortfall(c, opToOp, true) > 0 {
			n++
		}
	}
	return n
}

// prefersReply reports whether station s's next message should be a reply,
// so that its messages alternate between replies and new messages.
func prefersReply(fl Flow, s int) bool {
	var replies, others int
	for _, m := range fl.Messages {
		if m.From != s {
			continue
		}
		if m.ReplyTo > 0 {
			replies++
		} else {
			others++
		}
	}
	return replies <= others
}

// unanswered returns the 1-based number of a message of the given kind from
// Net Control to station s (directly or to All Stations) that s hasn't
// replied to, or 0 if there's none.
func unanswered(fl Flow, nc, s int, opToOp bool) (int, error) {
	senders, err := flowSenders(fl)
	if err != nil {
		return 0, err
	}
	answered := map[int]bool{}
	for i, m := range fl.Messages {
		if m.ReplyTo > 0 && slices.Contains(senders[i], s) {
			answered[m.ReplyTo] = true
		}
	}
	for i, m := range fl.Messages {
		if m.From == nc && !answered[i+1] && isOpToOpMessage(m) == opToOp &&
			(m.To == s || m.To < 0 && isAllStations(m.ToLabel)) {
			return i + 1, nil
		}
	}
	return 0, nil
}

// autoType picks the type and purpose of an added message: an ICS-213 for
// operator-to-operator traffic (whose purpose comes from its OpToOp mark),
// otherwise the form in the sender's pool used least so far in fl. A reply
// gets no purpose, since it answers its target.
func autoType(fl Flow, fromNetControl, opToOp, reply bool) (msgType, purpose string) {
	if opToOp {
		if mt, ok := FindMsgType(autoOpToOpType); ok {
			return mt.CreateTag(), ""
		}
		return "", ""
	}
	pool, purpose := autoStationForms, autoReportPurpose
	if fromNetControl {
		pool, purpose = autoNetControlForms, autoRequestPurpose
	}
	if reply {
		purpose = ""
	}
	used := map[string]int{}
	for _, m := range fl.Messages {
		if mt, ok := FindMsgType(m.MsgType); ok {
			used[mt.CreateTag()]++
		}
	}
	for _, tag := range pool {
		mt, ok := FindMsgType(tag)
		if !ok {
			continue
		}
		if msgType == "" || used[mt.CreateTag()] < used[msgType] {
			msgType = mt.CreateTag()
		}
	}
	return msgType, purpose
}

// neediestPartner returns the party other than p with the largest
// shortfall of the given kind and direction, the lowest-numbered on a tie.
func neediestPartner(report []PartyCompliance, p int, opToOp, received bool) int {
	best, bestNeed := -1, -1
	for _, c := range report {
		if c.Party == p {
			continue
		}
		if need := shortfall(c, opToOp, received); need > bestNeed {
			best, bestNeed = c.Party, need
		}
	}
	return best
}
