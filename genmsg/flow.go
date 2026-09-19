package genmsg

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rothskeller/packet/v4/message/messageid"
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
	// Principal is the served-agency person who hands this party's
	// messages to its operator and receives the messages delivered to it,
	// e.g. "NetMgr" or "FieldMgr". It only affects diagrams.
	Principal string `json:"principal,omitempty"`
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
	// Handling is the message's handling order: "R", "P", "I", or empty to
	// let Claude choose.
	Handling string `json:"handling,omitempty"`
	// Group, if not zero, puts the message in a hand-off group: the
	// messages with the same Group are handed to their operators together,
	// who should send them in handling order (immediate first).
	Group int `json:"group,omitempty"`
	// Event, if not empty, makes this entry a scenario event instead of a
	// message (see FlowEvents): something shown on the diagram but never
	// generated, such as opening the net. The other fields but Text are
	// then ignored.
	Event string `json:"event,omitempty"`
	// MsgNo, if not empty, is the message's number, overriding the one the
	// tool would pick: PPP-NNN, where PPP is the sender's message number
	// prefix and NNN the sequence number, e.g. "XND-101". Only a message
	// with a single sender can have one. Without a suffix letter, it gets
	// the flow's own (see Flow.Packet).
	MsgNo string `json:"msgNo,omitempty"`
	// Time is the time the message is written (HH:MM), for its time
	// fields; empty leaves them blank.
	Time string `json:"time,omitempty"`
	// Text is an event's text, replacing its default one. A "note" event
	// needs one.
	Text string `json:"text,omitempty"`
}

// FlowEvents lists the kinds of flow events, with their default text.
var FlowEvents = []struct{ Kind, Label, Text string }{
	{"note", "Note", ""},
	{"open-net", "Open net", "Open Net"},
	{"check-ins", "Check-ins (voice)", "Check In"},
	{"hw-check", "Health and welfare check", "Health and Welfare Check"},
	{"shift-change", "Net Control shift change", "Net Control Shift Change"},
	{"closing", "Announce net closing", "Announce:\nNet is closing"},
	{"check-outs", "Check-outs (voice)", "Check Out"},
	{"net-closed", "Net closed", "Net is closed."},
}

// eventText returns the text of event fm, or "" if fm isn't a known event.
func eventText(fm FlowMessage) (string, bool) {
	for _, e := range FlowEvents {
		if e.Kind == fm.Event {
			if t := strings.TrimSpace(fm.Text); t != "" {
				return t, true
			}
			return e.Text, true
		}
	}
	return "", false
}

// msgNoRE matches a message number as a scenario gives it: PPP-NNN, the
// sender's three-character prefix and the sequence number, with an optional
// suffix letter.
var msgNoRE = regexp.MustCompile(`^([A-Z0-9]{3})-(\d{3,4})([A-Z]?)$`)

// flowMessageNumbers returns, for each message of fl and each of its
// senders (see flowSenders), the message number the scenario gives it, or
// "" to let the tool pick one. A number must be PPP-NNN, where PPP is the
// sender's message number prefix, so only a message with a single sender
// can have one. It fails if a number is malformed, doesn't match its
// sender, or is given twice.
func flowMessageNumbers(fl Flow, senders [][]int) ([][]string, error) {
	nums := make([][]string, len(fl.Messages))
	used := map[string]int{} // number without suffix -> 1-based message
	for i, fm := range fl.Messages {
		given := strings.ToUpper(strings.TrimSpace(fm.MsgNo))
		nums[i] = make([]string, len(senders[i]))
		if given == "" || isEvent(fm) {
			continue
		}
		m := msgNoRE.FindStringSubmatch(given)
		if m == nil {
			return nil, fmt.Errorf("message %d: invalid message number %q (use PPP-NNN: the sender's prefix and a number, e.g. XND-101)", i+1, fm.MsgNo)
		}
		prefix, suffix := m[1], m[3]
		if suffix == "" {
			suffix = numberSuffix(fl.Packet)
		}
		seq, _ := strconv.Atoi(m[2])
		if len(senders[i]) != 1 {
			return nil, fmt.Errorf("message %d: a message from each station can't have one message number; give each station its own message to number it", i+1)
		}
		sender := fl.Parties[senders[i][0]]
		if sender.Prefix == "" {
			return nil, fmt.Errorf("message %d: %s has no message number prefix; give it one to number its messages", i+1, sender.Role)
		}
		if prefix != sender.Prefix {
			return nil, fmt.Errorf("message %d: message number %s must start with its sender's prefix, %s", i+1, given, sender.Prefix)
		}
		id, err := messageid.Encode(prefix, seq, suffix)
		if err != nil {
			return nil, fmt.Errorf("message %d: invalid message number %q: %v", i+1, fm.MsgNo, err)
		}
		key := numberKey(prefix, seq)
		if j, dup := used[key]; dup {
			return nil, fmt.Errorf("message %d: message number %s is also given to message %d", i+1, key, j)
		}
		used[key] = i + 1
		nums[i][0] = id
	}
	return nums, nil
}

// numberSuffix returns the suffix a message number gets: "P" for a packet
// message, none otherwise.
func numberSuffix(packet bool) string {
	if packet {
		return "P"
	}
	return ""
}

// flowTime returns fm's time as HH:MM, or "" if it has none (or an invalid
// one, which flowSenders rejects).
func flowTime(fm FlowMessage) string {
	t, _ := NormalizeTime(fm.Time)
	return t
}

// isEvent says whether fm is an event rather than a message.
func isEvent(fm FlowMessage) bool { return fm.Event != "" }

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
	// Packet says the messages are sent by packet, so their numbers get
	// the "P" suffix (e.g. "ABC-123P"); without it, they have no suffix
	// (e.g. "ABC-123").
	Packet bool `json:"packet,omitempty"`
	// Name names the net or exercise, e.g. "Evaluation Net", for diagram
	// titles.
	Name string `json:"name,omitempty"`
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
	var needNetControl bool
	for i, fm := range fl.Messages {
		if isEvent(fm) {
			text, ok := eventText(fm)
			if !ok {
				return nil, fmt.Errorf("entry %d: unknown event %q", i+1, fm.Event)
			}
			if text == "" {
				return nil, fmt.Errorf("entry %d: a note needs text", i+1)
			}
			needNetControl = needNetControl || fm.Event != "note" && fm.Event != "hw-check" && fm.Event != "shift-change"
			continue
		}
		if _, ok := FindMsgType(fm.MsgType); !ok {
			return nil, fmt.Errorf("message %d: no such message type %q", i+1, fm.MsgType)
		}
		if fm.ReplyTo < 0 || fm.ReplyTo > len(fl.Messages) || fm.ReplyTo == i+1 {
			return nil, fmt.Errorf("message %d: invalid \"replyTo\" %d", i+1, fm.ReplyTo)
		}
		if fm.ReplyTo > 0 && isEvent(fl.Messages[fm.ReplyTo-1]) {
			return nil, fmt.Errorf("message %d: entry %d it replies to is an event, not a message", i+1, fm.ReplyTo)
		}
		if fm.Handling != "" && NormalizeHandling(fm.Handling) == "" {
			return nil, fmt.Errorf("message %d: invalid handling order %q (use R, P, or I)", i+1, fm.Handling)
		}
		if _, err := NormalizeTime(fm.Time); err != nil {
			return nil, fmt.Errorf("message %d: %s", i+1, err)
		}
		if fm.Group < 0 {
			return nil, fmt.Errorf("message %d: invalid hand-off group %d", i+1, fm.Group)
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
	if _, err := flowMessageNumbers(fl, senders); err != nil {
		return nil, err
	}
	if needNetControl {
		if _, ok := netControlParty(fl.Parties); !ok {
			return nil, errors.New(`net events (opening, check-ins, closing) need a Net Control party: give one party a Net Control credential, or a role containing "Net Control"`)
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
	if !slices.ContainsFunc(fl.Messages, func(fm FlowMessage) bool { return !isEvent(fm) }) {
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
	msgNos, err := flowMessageNumbers(fl, partyIndices)
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
	batch := time.Now().UTC().Format("20060102T150405.000000000")
	var events []FlowMessage // events waiting for the next message's record
	for i, fm := range fl.Messages {
		if isEvent(fm) {
			events = append(events, fm)
			continue
		}
		to, err := resolveTo(fm, fl.Parties)
		if err != nil {
			return nil, fmt.Errorf("message %d: %s", i+1, err)
		}
		for k, p := range partyIndices[i] {
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
				Handling:     NormalizeHandling(fm.Handling),
				Time:         flowTime(fm),
				Packet:       fl.Packet,
				MsgNo:        msgNos[i][k],
			}
			spec.MsgType, _ = FindMsgType(fm.MsgType) // already validated above
			rec := &TrainingRecord{
				From: partyName(fl.Parties[p]), FromCredential: fl.Parties[p].Credential,
				FromPrincipal: fl.Parties[p].Principal, OpToOp: isOpToOpMessage(fm),
				Batch: batch, Step: i + 1, Group: fm.Group, Events: events,
			}
			events = nil
			if rec.FromCredential == "" && fl.Parties[p].F3 {
				rec.FromCredential = "F3"
			}
			if r := flowRecipients(fl, fm, p); len(r) > 0 {
				for _, q := range r {
					rec.To = append(rec.To, TrainingParty{Name: partyName(fl.Parties[q]), Credential: fl.Parties[q].Credential, Principal: fl.Parties[q].Principal})
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

	if len(events) > 0 { // events after the last message
		last := specs[len(specs)-1].Training
		last.EventsAfter = events
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
