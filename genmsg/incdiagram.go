package genmsg

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rothskeller/packet/v4/incident"
)

// IncidentDiagram lays out the messages of inc (see IncidentReport) as a
// sequence diagram, with their actual numbers and handling orders, named
// name. The principals, hand-off groups, and events of the flow that
// generated a message come from its record; the messages of one flow are
// drawn in flow order, where the first of them is in the log. The date is
// the first message date found, or today's.
func IncidentDiagram(inc *incident.Incident, name string) (Diagram, error) {
	msgs, names, err := readIncidentMessages(inc)
	if err != nil {
		return Diagram{}, err
	}
	msgs = flowOrder(msgs)

	principals := map[string]string{}
	credentials := map[string]string{}
	for _, rm := range msgs {
		if rec := rm.record; rec != nil {
			setIfEmpty(principals, rec.From, rec.FromPrincipal)
			setIfEmpty(credentials, rec.From, rec.FromCredential)
			for _, t := range rec.To {
				setIfEmpty(principals, t.Name, t.Principal)
				setIfEmpty(credentials, t.Name, t.Credential)
			}
		}
	}
	parties := make([]seqParty, len(names))
	for i, n := range names {
		parties[i] = seqParty{name: n, label: n, principal: principals[n]}
		if c := credentials[n]; c != "" {
			parties[i].label += " [" + c + "]"
		}
	}
	ncName := ""
	for _, n := range names {
		if strings.HasPrefix(credentials[n], "N") {
			ncName = n
			break
		}
	}
	if ncName == "" {
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), "net control") {
				ncName = n
				break
			}
		}
	}
	for i := range parties {
		parties[i].netControl = parties[i].name == ncName
	}

	date := time.Now().Format("01/02/2006")
	for _, rm := range msgs {
		if t, err := time.Parse("01/02/2006", rm.date); err == nil {
			date = t.Format("01/02/2006")
			break
		}
	}

	var steps []seqStep
	var trailing []FlowMessage
	for k, rm := range msgs {
		sm := seqMessage{
			from: rm.senderName, to: rm.recipientName, label: strings.TrimSuffix(rm.id, "P"),
			kind: rm.kind, time: rm.time,
			handling: rm.handling, opToOp: rm.opToOp, principal: true,
		}
		st := seqStep{msgs: []seqMessage{sm}}
		rec := rm.record
		if rec != nil {
			st.events = rec.Events
			trailing = append(trailing, rec.EventsAfter...)
			if rec.Group > 0 {
				st.group = rec.Batch + "/" + strconv.Itoa(rec.Group)
			}
			// The messages of one "each station" entry are one step.
			if prev := k - 1; prev >= 0 && rec.Step > 0 && len(steps) > 0 {
				if pr := msgs[prev].record; pr != nil && pr.Batch == rec.Batch && pr.Step == rec.Step {
					last := &steps[len(steps)-1]
					last.msgs = append(last.msgs, sm)
					continue
				}
			}
		} else if len(rm.recipientName) == 0 && rm.toRole != "" {
			sm.external = rm.toRole
			st.msgs[0] = sm
		}
		steps = append(steps, st)
	}
	return layoutDiagram(date, name, parties, steps, trailing), nil
}

func setIfEmpty(m map[string]string, key, value string) {
	if m[key] == "" && value != "" {
		m[key] = value
	}
}

// flowOrder puts the messages of each generated flow in flow order, at the
// place of the flow's first message; the log has them in the order they
// were created, with replies after the messages they answer.
func flowOrder(msgs []*reportMessage) []*reportMessage {
	batches := map[string][]*reportMessage{}
	for _, rm := range msgs {
		if rec := rm.record; rec != nil && rec.Batch != "" {
			batches[rec.Batch] = append(batches[rec.Batch], rm)
		}
	}
	out := make([]*reportMessage, 0, len(msgs))
	for _, rm := range msgs {
		rec := rm.record
		if rec == nil || rec.Batch == "" {
			out = append(out, rm)
			continue
		}
		batch, ok := batches[rec.Batch]
		if !ok {
			continue // already placed
		}
		slices.SortStableFunc(batch, func(a, b *reportMessage) int { return a.record.Step - b.record.Step })
		out = append(out, batch...)
		delete(batches, rec.Batch)
	}
	return out
}
