package genmsg

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Diagram is a flow laid out as a UML sequence diagram: its parties are
// the participants, and each message a party sends to another is an arrow.
type Diagram struct {
	Date         string // incident date, MM/DD/YYYY
	Participants []string
	Arrows       []DiagramArrow
}

// DiagramArrow is one message from one participant to another.
type DiagramArrow struct {
	From, To int // indexes into Diagram.Participants
	Label    string
	Reply    bool
}

// FlowDiagram lays out fl the way it will be generated (see ResolveFlow):
// an "each station" message is an arrow from each sending party, and an
// "All Stations" message an arrow to each receiving party. Arrows are
// numbered by message, with a letter for each sender of an "each station"
// message. A recipient that isn't a party is added as a participant.
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
	d := Diagram{Date: date}
	for _, p := range fl.Parties {
		name := partyName(p)
		if cred := partyCredential(p); cred != "" {
			name += " [" + cred + "]"
		}
		d.Participants = append(d.Participants, name)
	}
	others := map[string]int{} // recipients that aren't parties
	for i, fm := range fl.Messages {
		mt, _ := FindMsgType(fm.MsgType)
		_, kind, _ := strings.Cut(mt.Name(), " ") // drop "a" or "an"
		for j, s := range senders[i] {
			label := strconv.Itoa(i + 1)
			if len(senders[i]) > 26 {
				label += fmt.Sprintf(".%d", j+1)
			} else if len(senders[i]) > 1 {
				label += string(rune('a' + j))
			}
			label += ". " + kind
			if fm.OpToOp { // plain text and check-in/out messages say so by their type
				label += " (operator-to-operator)"
			}
			recipients := flowRecipients(fl, fm, s)
			switch {
			case fm.To >= 0:
			case isAllStations(fm.ToLabel):
				label += " to All Stations"
			default:
				name := strings.TrimSpace(fm.ToLabel)
				idx, ok := others[name]
				if !ok {
					idx = len(d.Participants)
					d.Participants = append(d.Participants, name)
					others[name] = idx
				}
				recipients = []int{idx}
			}
			if fm.ReplyTo > 0 {
				label += fmt.Sprintf(", reply to %d", fm.ReplyTo)
			}
			if purpose := strings.TrimSpace(fm.Purpose); purpose != "" {
				label += ": " + purpose
			}
			for _, r := range recipients {
				d.Arrows = append(d.Arrows, DiagramArrow{From: s, To: r, Label: label, Reply: fm.ReplyTo > 0})
			}
		}
	}
	return d, nil
}

// partyCredential returns the credential p is evaluated for, if any.
func partyCredential(p FlowParty) string {
	if p.Credential == "" && p.F3 {
		return "F3"
	}
	return p.Credential
}

// PlantUML returns d as a PlantUML sequence diagram, with scenario (if any)
// as its caption. Replies are dashed arrows.
func (d Diagram) PlantUML(scenario string) string {
	var b strings.Builder
	b.WriteString("@startuml\n")
	fmt.Fprintf(&b, "title Training scenario, %s\n", d.Date)
	if s := pumlText(scenario); s != "" {
		fmt.Fprintf(&b, "caption %s\n", s)
	}
	for i, p := range d.Participants {
		fmt.Fprintf(&b, "participant \"%s\" as P%d\n", strings.ReplaceAll(pumlText(p), `"`, "'"), i)
	}
	for _, a := range d.Arrows {
		arrow := "->"
		if a.Reply {
			arrow = "-->"
		}
		fmt.Fprintf(&b, "P%d %s P%d : %s\n", a.From, arrow, a.To, pumlText(a.Label))
	}
	b.WriteString("@enduml\n")
	return b.String()
}

// pumlText makes s safe for a single PlantUML line.
func pumlText(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
