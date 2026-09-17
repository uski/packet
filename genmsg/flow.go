package genmsg

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// FlowParty is one participant in a multi-party message flow: a short
// role/position name and an optional location, e.g. {"Net Control", "County
// EOC"} or {"Shelter Manager", "Roosevelt Middle School"}. Messages in the
// flow reference parties by index rather than repeating this information,
// so the same party's identity stays exactly consistent everywhere it's
// used as a sender or recipient. F3 marks this party as being evaluated on
// the reduced (Field Communicator Type III) proword list rather than the
// full list -- see MessageSpec.Level -- so a single flow can mix parties at
// different credential levels.
type FlowParty struct {
	Role     string `json:"role"`
	Location string `json:"location,omitempty"`
	F3       bool   `json:"f3,omitempty"`
	// Prefix is the party's three-character message number prefix, e.g.
	// "S24" for Shelter 24 (see MessageSpec.FromPrefix).
	Prefix string `json:"prefix,omitempty"`
	// Credential is the credential the party is being evaluated for (see
	// CheckFlow), or empty if it isn't being evaluated. "F3" also selects
	// the reduced proword list, as F3 does.
	Credential string `json:"credential,omitempty"`
}

// stationPrefixRE matches a station's message number prefix.
var stationPrefixRE = regexp.MustCompile(`^(?:[A-Z][A-Z0-9]{2}|[0-9][A-Z]{2})$`)

// fromEachStation is the sentinel value of FlowMessage.From that fans a
// single message entry out into one message per party (see ResolveFlow),
// for the common case of a broadcast request answered by every station.
const fromEachStation = -1

// FlowMessage is one message within a multi-party flow, referencing
// parties and other messages by index. This is the wire format (from a
// GUI dialog or a CLI --flow file); ResolveFlow turns a Flow into
// []MessageSpec for Generate/Apply.
type FlowMessage struct {
	MsgType string `json:"msgType"` // create tag or key, e.g. "ICS213" or "plain"
	From    int    `json:"from"`    // index into Flow.Parties, or -1 for "one message from each party" (fan-out)
	To      int    `json:"to"`      // index into Flow.Parties, or -1 to use ToLabel instead
	ToLabel string `json:"toLabel"` // used when To is -1, e.g. "All Stations"; ignored otherwise
	Purpose string `json:"purpose"` // optional hint of what this specific message is about
	ReplyTo int    `json:"replyTo"` // 1-based index into Flow.Messages this replies to, or 0 for none
	// OpToOp marks the message as operator-to-operator traffic, such as a
	// status report between radio operators, rather than a served agency's
	// message. Plain text, check-in, and check-out messages always are.
	OpToOp bool `json:"opToOp,omitempty"`
}

// opToOpPurpose describes operator-to-operator traffic to Claude.
const opToOpPurpose = "operator-to-operator traffic between the radio operators themselves, such as a status report or health and welfare message, not a served agency's message"

// flowPurpose returns the purpose to give Claude for fm.
func flowPurpose(fm FlowMessage) string {
	purpose := strings.TrimSpace(fm.Purpose)
	if !fm.OpToOp {
		return purpose
	}
	if purpose == "" {
		return opToOpPurpose
	}
	return opToOpPurpose + ": " + purpose
}

// Flow is a multi-party message-flow specification: the parties involved
// and the messages exchanged between them. ResolveFlow validates it and
// resolves it into a []MessageSpec.
type Flow struct {
	// Date is the incident date (MM/DD/YYYY or YYYY-MM-DD), used for every
	// message's date fields; empty means today. Time fields are left blank.
	Date     string        `json:"date,omitempty"`
	Parties  []FlowParty   `json:"parties"`
	Messages []FlowMessage `json:"messages"`
}

// flowDate returns fl's incident date as MM/DD/YYYY, or today's if unset.
func flowDate(fl Flow, now time.Time) (string, error) {
	s := strings.TrimSpace(fl.Date)
	if s == "" {
		return now.Format("01/02/2006"), nil
	}
	for _, layout := range []string{"2006-01-02", "01/02/2006", "1/2/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("01/02/2006"), nil
		}
	}
	return "", fmt.Errorf("invalid incident date %q (use MM/DD/YYYY)", fl.Date)
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

// partyLevel returns the proword level a party's messages should be
// evaluated at: the reduced F3 list if the party is marked F3, else the
// full list.
func partyLevel(p FlowParty) string {
	if p.F3 || p.Credential == "F3" {
		return prowords.LevelF3
	}
	return prowords.LevelFull
}

// normalizeParties replaces fl's parties with a validated copy, with
// message number prefixes in upper case.
func normalizeParties(fl *Flow) error {
	fl.Parties = slices.Clone(fl.Parties)
	for i := range fl.Parties {
		p := strings.ToUpper(strings.TrimSpace(fl.Parties[i].Prefix))
		if p != "" && !stationPrefixRE.MatchString(p) {
			return fmt.Errorf("party %d: invalid message number prefix %q (use three characters, e.g. S24)", i+1, fl.Parties[i].Prefix)
		}
		fl.Parties[i].Prefix = p
		if _, ok := credentialNeeds[fl.Parties[i].Credential]; !ok {
			return fmt.Errorf("party %d: unknown credential %q", i+1, fl.Parties[i].Credential)
		}
	}
	return nil
}

// flowSenders validates fl's messages against its parties and returns, for
// each message, the indices of the parties that send it: its From party,
// or for an "each station" message every party except its recipient and
// the sender of the message it replies to.
func flowSenders(fl Flow) ([][]int, error) {
	senders := make([][]int, len(fl.Messages))
	for i, fm := range fl.Messages {
		if _, ok := FindMsgType(fm.MsgType); !ok {
			return nil, fmt.Errorf("message %d: no such message type %q", i+1, fm.MsgType)
		}
		if fm.ReplyTo < 0 || fm.ReplyTo > len(fl.Messages) || fm.ReplyTo == i+1 {
			return nil, fmt.Errorf("message %d: invalid \"replyTo\" %d", i+1, fm.ReplyTo)
		}
		if fm.To >= len(fl.Parties) {
			return nil, fmt.Errorf("message %d: invalid \"to\" party index %d", i+1, fm.To)
		}
		if fm.From == fromEachStation {
			excluded := map[int]bool{fm.To: true}
			if fm.ReplyTo > 0 {
				excluded[fl.Messages[fm.ReplyTo-1].From] = true
			}
			var idxs []int
			for p := range fl.Parties {
				if !excluded[p] {
					idxs = append(idxs, p)
				}
			}
			if len(idxs) == 0 {
				return nil, fmt.Errorf("message %d: \"from each station\" has no parties left once its recipient and the sender of the message it replies to are excluded", i+1)
			}
			senders[i] = idxs
		} else if fm.From < 0 || fm.From >= len(fl.Parties) {
			return nil, fmt.Errorf("message %d: invalid \"from\" party index %d", i+1, fm.From)
		} else if fm.From == fm.To {
			return nil, fmt.Errorf("message %d: %s can't send a message to itself", i+1, fl.Parties[fm.From].Role)
		} else {
			senders[i] = []int{fm.From}
		}
	}
	return senders, nil
}

// resolveTo resolves a FlowMessage's To/ToLabel against parties into the
// receiving party; a ToLabel recipient has only a Role.
func resolveTo(fm FlowMessage, parties []FlowParty) (FlowParty, error) {
	switch {
	case fm.To < 0:
		return FlowParty{Role: strings.TrimSpace(fm.ToLabel)}, nil
	case fm.To < len(parties):
		return parties[fm.To], nil
	default:
		return FlowParty{}, fmt.Errorf("invalid \"to\" party index %d", fm.To)
	}
}

// ResolveFlow validates fl and resolves it into a []MessageSpec suitable
// for Request.Messages and Apply, looking up each message's type by tag or
// key among the registered editable message types (see FindMsgType) and
// each From/To party reference among fl.Parties.
//
// A FlowMessage with From == -1 ("one message from each party") fans out
// into one MessageSpec per party, each with that party as its sender --
// the common case of a broadcast request that every station answers. The
// fan-out leaves out the recipient party, and the sender of the message it
// replies to (ReplyTo > 0), since no station sends a message to itself or
// replies to its own broadcast. An ordinary message's From and To must be
// different parties. Another message
// may not reply to a fanned-out one (ReplyTo pointing at a From == -1
// entry), since there would be no single message to reply to.
func ResolveFlow(fl Flow) ([]MessageSpec, error) {
	if len(fl.Messages) == 0 {
		return nil, fmt.Errorf("at least one message is required")
	}
	if err := normalizeParties(&fl); err != nil {
		return nil, err
	}
	partyIndices, err := flowSenders(fl)
	if err != nil {
		return nil, err
	}
	date, err := flowDate(fl, time.Now())
	if err != nil {
		return nil, err
	}

	// Second pass: build the expanded spec list, and record where each
	// original message's spec(s) landed so ReplyTo can be remapped.
	var specs []MessageSpec
	origToNew := make([][]int, len(fl.Messages)) // 0-based original index -> 0-based new indices
	type pending struct {
		specIdx int
		replyTo int // original 1-based ReplyTo, to be remapped once all messages are placed
	}
	var pendingReplies []pending
	for i, fm := range fl.Messages {
		to, err := resolveTo(fm, fl.Parties)
		if err != nil {
			return nil, fmt.Errorf("message %d: %s", i+1, err)
		}
		for _, p := range partyIndices[i] {
			spec := MessageSpec{
				From:         fl.Parties[p].Role,
				FromLocation: fl.Parties[p].Location,
				FromPrefix:   fl.Parties[p].Prefix,
				To:           to.Role,
				ToLocation:   to.Location,
				ToPrefix:     to.Prefix,
				Purpose:      flowPurpose(fm),
				Level:        partyLevel(fl.Parties[p]),
				Date:         date,
			}
			spec.MsgType, _ = FindMsgType(fm.MsgType) // already validated above
			rec := &TrainingRecord{From: partyName(fl.Parties[p]), FromCredential: fl.Parties[p].Credential, OpToOp: isOpToOpMessage(fm)}
			if rec.FromCredential == "" && fl.Parties[p].F3 {
				rec.FromCredential = "F3"
			}
			if r := flowRecipients(fl, fm, p); len(r) > 0 {
				for _, q := range r {
					rec.To = append(rec.To, TrainingParty{Name: partyName(fl.Parties[q]), Credential: fl.Parties[q].Credential})
				}
			} else if to.Role != "" {
				rec.To = []TrainingParty{{Name: to.Role}}
			}
			spec.Training = rec
			origToNew[i] = append(origToNew[i], len(specs))
			pendingReplies = append(pendingReplies, pending{specIdx: len(specs), replyTo: fm.ReplyTo})
			specs = append(specs, spec)
		}
	}

	// Third pass: remap each spec's ReplyTo from the original 1-based
	// message numbering to the expanded 1-based spec numbering.
	for _, pr := range pendingReplies {
		if pr.replyTo == 0 {
			continue
		}
		targets := origToNew[pr.replyTo-1]
		if len(targets) != 1 {
			return nil, fmt.Errorf("message %d: cannot reply to message %d, which is \"from each station\" and so has no single message to reply to", replyingOrigMessage(origToNew, pr.specIdx)+1, pr.replyTo)
		}
		specs[pr.specIdx].ReplyTo = targets[0] + 1
	}
	return specs, nil
}

// replyingOrigMessage returns the 0-based original message index that
// produced the spec at newIdx, for error messages after expansion.
func replyingOrigMessage(origToNew [][]int, newIdx int) int {
	for orig, news := range origToNew {
		for _, n := range news {
			if n == newIdx {
				return orig
			}
		}
	}
	return -1
}
