package genmsg

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message/messageid"
)

// This file owns station message numbers: the ones a scenario gives its
// messages, and the ones the tool picks for the rest. Apply uses it to
// number the messages it creates, and the diagrams to label the messages a
// scenario would create.

// msgNoRE matches a message number as a scenario gives it: PPP-NNN, the
// sender's three-character prefix and the sequence number, with an optional
// suffix letter.
var msgNoRE = regexp.MustCompile(`^([A-Z0-9]{3})-(\d{3,4})([A-Z]?)$`)

// numberSuffix returns the suffix a message number gets: "P" for a packet
// message, none otherwise.
func numberSuffix(packet bool) string {
	if packet {
		return "P"
	}
	return ""
}

// numberKey identifies a message number regardless of its suffix.
func numberKey(prefix string, seq int) string {
	return fmt.Sprintf("%s-%03d", strings.ToUpper(prefix), seq)
}

// displayNumber returns id as a diagram shows it: without the "P" that
// marks a packet message, which is the same on every number in the net.
func displayNumber(id string) string {
	return strings.TrimSuffix(id, "P")
}

// numberer hands out station message numbers, keeping clear of the ones a
// scenario reserved for particular messages.
type numberer struct {
	reserved map[string]bool // numberKey -> reserved by a scenario
	next     map[string]int  // prefix -> the last number handed out
	inc      *incident.Incident
}

// newNumberer returns a numberer that avoids the numbers given to specs.
// With an incident, the numbers already used in it are avoided too, and the
// first number for a station follows the highest it has used there; without
// one, each station starts at 101, as it would in an empty incident.
func newNumberer(inc *incident.Incident, specs []MessageSpec) (*numberer, error) {
	n := &numberer{reserved: map[string]bool{}, next: map[string]int{}, inc: inc}
	for _, spec := range specs {
		if spec.MsgNo == "" {
			continue
		}
		p, seq, _, err := messageid.Decode(spec.MsgNo, true, false)
		if err != nil {
			return nil, fmt.Errorf("invalid message number %q: %w", spec.MsgNo, err)
		}
		n.reserved[numberKey(p, seq)] = true
	}
	if inc == nil || len(n.reserved) == 0 {
		return n, nil
	}
	if err := n.checkIncident(); err != nil {
		return nil, err
	}
	n.reserveFromOwnPrefix()
	return n, nil
}

// checkIncident fails if a number a scenario reserved is already used by a
// message in the incident.
func (n *numberer) checkIncident() error {
	for _, le := range n.inc.Log {
		if le.Status == incident.StatusDeleted {
			continue // its number is free again
		}
		for _, id := range logNumbers(le) {
			if p, seq, _, err := messageid.Decode(id, true, false); err == nil && n.reserved[numberKey(p, seq)] {
				return fmt.Errorf("message number %s is already used in this incident; delete that message first (e.g. with Message > Delete All Messages) or change the scenario's number", id)
			}
		}
	}
	return nil
}

// reserveFromOwnPrefix moves the incident's next message number past any
// reserved number with its own prefix, so that neither the messages it
// numbers itself nor later ones take one.
func (n *numberer) reserveFromOwnPrefix() {
	own, next, suffix, err := messageid.Decode(n.inc.Config.TxMessageID, true, false)
	if err != nil {
		return
	}
	for key := range n.reserved {
		if p, seq, _, err := messageid.Decode(key, true, false); err == nil && p == own && seq >= next {
			next = seq + 1
		}
	}
	if id, err := messageid.Encode(own, next, suffix); err == nil && id != n.inc.Config.TxMessageID {
		if n.inc.Config.TxMessageID == n.inc.Config.RxMessageID {
			n.inc.Config.RxMessageID = id
		}
		n.inc.Config.TxMessageID = id
	}
}

// number returns the next free message number for the station with the
// given prefix, e.g. "S24-101P".
func (n *numberer) number(prefix, suffix string) (string, error) {
	if _, ok := n.next[prefix]; !ok {
		n.next[prefix] = n.highestUsed(prefix)
	}
	n.next[prefix]++
	for n.reserved[numberKey(prefix, n.next[prefix])] {
		n.next[prefix]++
	}
	return messageid.Encode(prefix, n.next[prefix], suffix)
}

// highestUsed returns the highest number the station with prefix has used
// in the incident, or 100 if it has used none (so the first is 101).
func (n *numberer) highestUsed(prefix string) int {
	seq := 100
	if n.inc == nil {
		return seq
	}
	for _, le := range n.inc.Log {
		if le.Status == incident.StatusDeleted {
			continue
		}
		for _, id := range logNumbers(le) {
			if p, s, _, err := messageid.Decode(id, true, false); err == nil && p == prefix && s > seq {
				seq = s
			}
		}
	}
	return seq
}

// logNumbers returns the message numbers a log entry carries.
func logNumbers(le *incident.LogEntry) []string {
	return []string{le.LocalMsgID, le.FromMsgID, le.ToMsgID}
}

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
		seq, _ := strconv.Atoi(m[2])
		if suffix == "" {
			suffix = numberSuffix(fl.Packet)
		}
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
