package genmsg

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rothskeller/packet/v4/message/messageid"
)

// Diagram is a message flow laid out as a sequence diagram: its parties
// (and their principals) are the participants, and its messages and events
// are drawn as a list of items, top to bottom.
type Diagram struct {
	Date         string // incident date, YYYY-MM-DD
	Name         string // net or exercise name, if any
	Participants []DiagramParticipant
	Items        []DiagramItem
}

// Title is the diagram's title: its date and name.
func (d Diagram) Title() string {
	if d.Name == "" {
		return d.Date + " Training scenario"
	}
	return d.Date + " " + d.Name
}

// DiagramParticipant is one column of a diagram.
type DiagramParticipant struct {
	Name      string // unique among the participants
	Label     string // what to show, e.g. with the party's credential
	Principal bool   // a principal rather than a radio operator
}

// DiagramItem is a note, or a set of arrows drawn together.
type DiagramItem struct {
	// Note, if not empty, makes the item a note over participants NoteFrom
	// through NoteTo. Section marks a note that starts a new section of
	// the scenario, set off with extra space.
	Note             string
	NoteFrom, NoteTo int
	Section          bool
	// Block says how Arrows are drawn: "parallel" (at the same height),
	// "linear", or "" (one after another).
	Block  string
	Arrows []DiagramArrow
}

// DiagramArrow is one transmission, or one hand-off between a principal
// and its operator.
type DiagramArrow struct {
	From, To int    // indexes into Diagram.Participants
	Label    string // may have several lines
	Detail   string // longer description, if any
	Handoff  bool
	Reply    bool
}

// seqParty is a party as the diagram layout sees it.
type seqParty struct {
	name, label, principal string
	netControl             bool
}

// seqMessage is one message as the diagram layout sees it.
type seqMessage struct {
	from      string   // sending party's name
	to        []string // receiving parties' names
	external  string   // the recipient, if not a party (and not All Stations)
	label     string   // message number
	handling  string   // R, P, I, or ""
	opToOp    bool
	reply     bool
	detail    string
	principal bool // hand-off from and delivery to principals
}

// seqStep is one flow entry: its events (before it), and its messages,
// one per sender.
type seqStep struct {
	events []FlowMessage
	group  string // hand-off group key, or ""
	msgs   []seqMessage
}

// FlowDiagram lays out fl the way it will be generated (see ResolveFlow).
// Messages are labeled with the numbers the scenario gives them, or else
// those they'd get in an incident with no other messages from their
// senders, or by entry number for a party without a message number prefix.
// The usual "P" suffix is left out.
func FlowDiagram(fl Flow) (Diagram, error) {
	if err := normalizeParties(&fl); err != nil {
		return Diagram{}, err
	}
	senders, err := flowSenders(fl)
	if err != nil {
		return Diagram{}, err
	}
	date, err := flowDate(fl, time.Now())
	if err != nil {
		return Diagram{}, err
	}
	nc, hasNC := netControlParty(fl.Parties)
	parties := make([]seqParty, len(fl.Parties))
	for i, p := range fl.Parties {
		parties[i] = seqParty{name: partyName(p), label: partyName(p), principal: strings.TrimSpace(p.Principal), netControl: hasNC && i == nc}
		if cred := partyCredential(p); cred != "" {
			parties[i].label += " [" + cred + "]"
		}
	}
	msgNos, err := flowMessageNumbers(fl, senders)
	if err != nil {
		return Diagram{}, err
	}
	reserved := map[string]bool{}
	for _, nums := range msgNos {
		for _, id := range nums {
			if p, n, _, err := messageid.Decode(id, true, false); err == nil {
				reserved[numberKey(p, n)] = true
			}
		}
	}
	nextSeq := map[string]int{}
	labels := make([][]string, len(fl.Messages))
	var steps []seqStep
	var events []FlowMessage
	for i, fm := range fl.Messages {
		if isEvent(fm) {
			events = append(events, fm)
			continue
		}
		step := seqStep{events: events}
		events = nil
		if fm.Group > 0 {
			step.group = strconv.Itoa(fm.Group)
		}
		mt, _ := FindMsgType(fm.MsgType)
		_, kind, _ := strings.Cut(mt.Name(), " ") // drop "a" or "an"
		for j, s := range senders[i] {
			entry := strconv.Itoa(i + 1)
			if len(senders[i]) > 26 {
				entry += fmt.Sprintf(".%d", j+1)
			} else if len(senders[i]) > 1 {
				entry += string(rune('a' + j))
			}
			label := "#" + entry
			if id := msgNos[i][j]; id != "" {
				label = strings.TrimSuffix(id, "P")
			} else if pfx := fl.Parties[s].Prefix; pfx != "" {
				if nextSeq[pfx] == 0 {
					nextSeq[pfx] = 101
				}
				for reserved[numberKey(pfx, nextSeq[pfx])] {
					nextSeq[pfx]++
				}
				label = numberKey(pfx, nextSeq[pfx])
				nextSeq[pfx]++
			}
			labels[i] = append(labels[i], label)
			detail := entry + ". " + kind
			if t := flowTime(fm); t != "" {
				detail += " at " + t
			}
			if fm.OpToOp { // plain text and check-in/out messages say so by their type
				detail += " (operator-to-operator)"
			}
			sm := seqMessage{
				from: parties[s].name, label: label, handling: NormalizeHandling(fm.Handling),
				opToOp: isOpToOpMessage(fm), reply: fm.ReplyTo > 0, principal: true,
			}
			for _, r := range flowRecipients(fl, fm, s) {
				sm.to = append(sm.to, parties[r].name)
			}
			switch {
			case fm.To >= 0:
			case isAllStations(fm.ToLabel):
				detail += " to All Stations"
			default:
				sm.external = strings.TrimSpace(fm.ToLabel)
			}
			if fm.ReplyTo > 0 {
				detail += ", reply to " + replyLabel(labels[fm.ReplyTo-1], fm.ReplyTo)
			}
			if purpose := strings.TrimSpace(fm.Purpose); purpose != "" {
				detail += ": " + purpose
			}
			sm.detail = detail
			step.msgs = append(step.msgs, sm)
		}
		steps = append(steps, step)
	}
	return layoutDiagram(date, fl.Name, parties, steps, events), nil
}

// replyLabel names the message a reply answers.
func replyLabel(labels []string, entry int) string {
	if len(labels) == 1 {
		return labels[0]
	}
	return "#" + strconv.Itoa(entry)
}

// partyCredential returns the credential p is evaluated for, if any.
func partyCredential(p FlowParty) string {
	if p.Credential == "" && p.F3 {
		return "F3"
	}
	return p.Credential
}

// handlingRank orders handling orders by urgency.
var handlingRank = map[string]int{"I": 0, "P": 1, "R": 2, "": 2}

// layoutDiagram turns parties, steps, and the events after the last step
// into a diagram. date is MM/DD/YYYY.
//
// Participants are, left to right: the Net Control party's principal, the
// parties, the other principals, and recipients that aren't parties. A
// 3rd-party message is handed by its sender's principal to the sender (the
// messages of a hand-off group together), transmitted, and delivered to
// the recipient's principal; operator-to-operator traffic is only
// transmitted. The messages of a hand-off group are transmitted in
// handling order, most urgent first.
func layoutDiagram(date, name string, parties []seqParty, steps []seqStep, trailing []FlowMessage) Diagram {
	d := Diagram{Date: date, Name: strings.TrimSpace(name)}
	if t, err := time.Parse("01/02/2006", date); err == nil {
		d.Date = t.Format("2006-01-02")
	}
	index := map[string]int{}
	principals := map[string]int{}
	add := func(name, label string, principal bool) int {
		key := name
		if principal {
			key = "\x00" + name
		}
		if i, ok := index[key]; ok {
			return i
		}
		unique := name
		for n := 2; slices.ContainsFunc(d.Participants, func(p DiagramParticipant) bool { return p.Name == unique }); n++ {
			unique = fmt.Sprintf("%s %d", name, n)
		}
		index[key] = len(d.Participants)
		d.Participants = append(d.Participants, DiagramParticipant{Name: unique, Label: label, Principal: principal})
		return index[key]
	}
	addPrincipal := func(name string) {
		if name != "" {
			principals[name] = add(name, name, true)
		}
	}
	nc := -1
	for _, p := range parties {
		if p.netControl {
			addPrincipal(p.principal)
		}
	}
	for _, p := range parties {
		i := add(p.name, p.label, false)
		if p.netControl {
			nc = i
		}
	}
	principalOf := map[string]string{}
	for _, p := range parties {
		addPrincipal(p.principal)
		principalOf[p.name] = p.principal
	}
	for _, st := range steps {
		for _, m := range st.msgs {
			if m.external != "" {
				add(m.external, m.external, false)
			}
		}
	}
	party := func(name string) int { return index[name] }
	principal := func(partyName string) (int, bool) {
		pn := principalOf[partyName]
		if pn == "" {
			return 0, false
		}
		return principals[pn], true
	}

	addEvents := func(events []FlowMessage) {
		for _, ev := range events {
			d.Items = append(d.Items, eventItems(ev, len(d.Participants), nc, parties, party)...)
		}
	}
	done := make([]bool, len(steps))
	for k := range steps {
		if done[k] {
			continue
		}
		addEvents(steps[k].events)
		unit := []seqMessage(nil)
		for j := k; j < len(steps); j++ {
			if j == k || steps[k].group != "" && steps[j].group == steps[k].group {
				if j != k {
					addEvents(steps[j].events)
				}
				done[j] = true
				unit = append(unit, steps[j].msgs...)
			}
		}
		grouped := len(unit) > 1 && steps[k].group != ""

		// Hand-offs, one per sender with a principal.
		var senders []string
		handed := map[string][]string{}
		for _, m := range unit {
			if _, ok := principal(m.from); !ok || m.opToOp || !m.principal {
				continue
			}
			if handed[m.from] == nil {
				senders = append(senders, m.from)
			}
			label := m.label
			if grouped && m.handling != "" {
				label += " (" + m.handling + ")"
			}
			handed[m.from] = append(handed[m.from], label)
		}
		for _, s := range senders {
			p, _ := principal(s)
			d.Items = append(d.Items, DiagramItem{Arrows: []DiagramArrow{{From: p, To: party(s), Label: strings.Join(handed[s], "\n"), Handoff: true}}})
		}

		// Transmissions, each with its deliveries.
		if grouped {
			slices.SortStableFunc(unit, func(a, b seqMessage) int { return cmp.Compare(handlingRank[a.handling], handlingRank[b.handling]) })
		}
		for _, m := range unit {
			label := m.label
			if m.handling != "" {
				label += " (" + m.handling + ")"
			}
			var sends, deliveries []DiagramArrow
			to := m.to
			if m.external != "" {
				to = []string{m.external}
			}
			for _, r := range to {
				sends = append(sends, DiagramArrow{From: party(m.from), To: party(r), Label: label, Detail: m.detail, Reply: m.reply})
				if p, ok := principal(r); ok && !m.opToOp && m.principal && m.external == "" {
					deliveries = append(deliveries, DiagramArrow{From: party(r), To: p, Handoff: true})
				}
			}
			switch {
			case len(sends) > 1:
				for i := range deliveries {
					deliveries[i].Label = m.label
				}
				d.Items = append(d.Items, DiagramItem{Block: "linear", Arrows: append(sends, deliveries...)})
			case len(deliveries) > 0:
				d.Items = append(d.Items, DiagramItem{Block: "parallel", Arrows: append(sends, deliveries...)})
			case len(sends) > 0:
				d.Items = append(d.Items, DiagramItem{Arrows: sends})
			}
		}
	}
	addEvents(trailing)
	return d
}

// eventItems draws event ev. Notes about the scenario span every
// participant; notes about the net are over Net Control.
func eventItems(ev FlowMessage, participants, nc int, parties []seqParty, party func(string) int) []DiagramItem {
	text, ok := eventText(ev)
	if !ok || text == "" {
		return nil
	}
	all := DiagramItem{Note: text, NoteFrom: 0, NoteTo: participants - 1, Section: true}
	if nc < 0 {
		return []DiagramItem{all}
	}
	switch ev.Event {
	case "note", "hw-check", "shift-change":
		return []DiagramItem{all}
	case "check-ins", "check-outs":
		item := DiagramItem{Block: "linear"}
		for _, p := range parties {
			if !p.netControl {
				item.Arrows = append(item.Arrows, DiagramArrow{From: party(p.name), To: nc, Label: text})
			}
		}
		if len(item.Arrows) == 0 {
			return nil
		}
		return []DiagramItem{item}
	default:
		return []DiagramItem{{Note: text, NoteFrom: nc, NoteTo: nc, Section: ev.Event == "closing"}}
	}
}

// SequenceDiagram returns d in the syntax of sequencediagram.org.
// Principals are rounded participants, and hand-offs dashed arrows.
func (d Diagram) SequenceDiagram() string {
	var b strings.Builder
	fmt.Fprintf(&b, "title %s\n\n", sdText(d.Title()))
	for _, p := range d.Participants {
		kind := "participant"
		if p.Principal {
			kind = "rparticipant"
		}
		fmt.Fprintf(&b, "%s %s\n", kind, sdName(p.Name))
	}
	b.WriteString("\n")
	spacing := ""
	space := func(s string) {
		if s != spacing {
			fmt.Fprintf(&b, "entryspacing %s\n", s)
			spacing = s
		}
	}
	prevNote := false
	for _, it := range d.Items {
		if it.Note != "" {
			switch {
			case it.Section && prevNote:
				space("1")
			case it.Section:
				b.WriteString("\n")
				space("2")
			case spacing != "":
				space("0.5")
			}
			over := sdName(d.Participants[it.NoteFrom].Name)
			if it.NoteTo != it.NoteFrom {
				over += "," + sdName(d.Participants[it.NoteTo].Name)
			}
			fmt.Fprintf(&b, "note over %s: %s\n", over, sdText(it.Note))
			prevNote = true
			continue
		}
		if spacing != "" {
			space("0.5")
		}
		prevNote = false
		if it.Block != "" {
			fmt.Fprintf(&b, "%s on\n", it.Block)
		}
		for _, a := range it.Arrows {
			arrow := "->"
			if a.Handoff {
				arrow = "-->>"
			}
			fmt.Fprintf(&b, "%s%s%s:", sdName(d.Participants[a.From].Name), arrow, sdName(d.Participants[a.To].Name))
			if label := sdText(a.Label); label != "" {
				b.WriteString(" " + label)
			}
			b.WriteString("\n")
		}
		if it.Block != "" {
			fmt.Fprintf(&b, "%s off\n", it.Block)
		}
	}
	return b.String()
}

// sdUnsafe matches what can't be in a sequencediagram.org participant name.
var sdUnsafe = regexp.MustCompile(`[^\p{L}\p{N} ._()#/&'-]+|-+>|<-+`)

// sdName makes s usable as a sequencediagram.org participant name.
func sdName(s string) string {
	return strings.Join(strings.Fields(sdUnsafe.ReplaceAllString(s, " ")), " ")
}

// sdText makes s a single sequencediagram.org line, keeping its line
// breaks as "\n".
func sdText(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.Join(lines, `\n`)
}

// PlantUML returns d as a PlantUML sequence diagram, with scenario (if any)
// as its caption. Principals are actors, and hand-offs dashed arrows.
func (d Diagram) PlantUML(scenario string) string {
	var b strings.Builder
	b.WriteString("@startuml\n")
	fmt.Fprintf(&b, "title %s\n", pumlText(d.Title()))
	if s := pumlText(scenario); s != "" {
		fmt.Fprintf(&b, "caption %s\n", s)
	}
	for i, p := range d.Participants {
		kind := "participant"
		if p.Principal {
			kind = "actor"
		}
		fmt.Fprintf(&b, "%s \"%s\" as P%d\n", kind, strings.ReplaceAll(pumlText(p.Label), `"`, "'"), i)
	}
	for _, it := range d.Items {
		if it.Note != "" {
			if it.Section {
				b.WriteString("|||\n")
			}
			fmt.Fprintf(&b, "note over P%d", it.NoteFrom)
			if it.NoteTo != it.NoteFrom {
				fmt.Fprintf(&b, ", P%d", it.NoteTo)
			}
			fmt.Fprintf(&b, " : %s\n", pumlText(it.Note))
			continue
		}
		for _, a := range it.Arrows {
			arrow := "->"
			if a.Handoff {
				arrow = "-->>"
			} else if a.Reply {
				arrow = "-->"
			}
			label := pumlText(a.Label)
			if a.Detail != "" {
				label += `\n` + pumlText(a.Detail)
			}
			fmt.Fprintf(&b, "P%d %s P%d : %s\n", a.From, arrow, a.To, label)
		}
	}
	b.WriteString("@enduml\n")
	return b.String()
}

// pumlText makes s safe for a single PlantUML line, keeping its line
// breaks as "\n".
func pumlText(s string) string {
	return sdText(s)
}
