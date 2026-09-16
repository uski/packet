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

const (
	autoFormType      = "ICS213"
	autoOpToOpType    = "plain"
	autoOpToOpPurpose = "operator-to-operator traffic, such as a check-in, health and welfare, or status report"
)

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
		mt, _ := FindMsgType(fm.MsgType)
		opToOp := opToOpTypes[strings.ToLower(mt.CreateTag())]
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
	if label := strings.TrimSpace(fm.ToLabel); label != "" && !strings.EqualFold(label, "All Stations") {
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
// were added. Each added message is an ICS-213 (3rd party) or plain text
// (operator-to-operator) message between a party that falls short and the
// other party that most needs the other side of that message.
func CompleteFlow(fl Flow) (Flow, int, error) {
	if len(fl.Parties) < 2 {
		return fl, 0, errors.New("at least two parties are needed to exchange messages")
	}
	if _, ok := FindMsgType(autoFormType); !ok {
		return fl, 0, fmt.Errorf("the %s form isn't available", autoFormType)
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
		// The partner is on the other side of the message.
		q := neediestPartner(report, p, opToOp, !received)
		msg := FlowMessage{MsgType: autoFormType, From: p, To: q}
		if received {
			msg.From, msg.To = q, p
		}
		if opToOp {
			msg.MsgType, msg.Purpose = autoOpToOpType, autoOpToOpPurpose
		}
		fl.Messages = append(fl.Messages, msg)
	}
	return fl, 0, errors.New("could not add enough messages to meet the credential criteria")
}

func firstShortfall(report []PartyCompliance) (party int, opToOp, received, found bool) {
	for _, c := range report {
		for _, kind := range []bool{false, true} {
			for _, dir := range []bool{false, true} {
				if shortfall(c, kind, dir) > 0 {
					return c.Party, kind, dir, true
				}
			}
		}
	}
	return 0, false, false, false
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
