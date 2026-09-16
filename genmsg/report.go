package genmsg

import (
	"log/slog"
	"strings"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/message/messageid"
	"github.com/rothskeller/packet/v4/prowords"
)

// PartyReport is what IncidentReport found for one party.
type PartyReport struct {
	Name       string
	Credential string // the credential the party was generated for, if known
	Messages   int    // messages sent
	Counts     map[prowords.Category]int
	Sent       Traffic
	Received   Traffic
	Reach      []CredentialCheck
}

// CredentialCheck says whether a party's traffic meets one credential's
// criteria, and if not, what's missing.
type CredentialCheck struct {
	Credential string // e.g. "F3"
	Label      string // e.g. "Field III (F3)"
	Met        bool
	Missing    []string
}

// ReportCredentials lists the credentials a report checks, in order. The
// Net Control credentials have no message criteria, so they're left out.
var ReportCredentials = []struct{ Code, Label string }{
	{"F3", "Field III (F3)"}, {"F2", "Field II"}, {"F1", "Field I"},
	{"S3", "Shadow III"}, {"S2", "Shadow II"}, {"S1", "Shadow I"},
	{"P3", "Packet III"}, {"P2", "Packet II"}, {"P1", "Packet I"},
}

// reportMessage is one message as IncidentReport sees it.
type reportMessage struct {
	record        *TrainingRecord
	fromRole      string
	fromPrefix    string
	toRole        string
	toAddr        string
	opToOp        bool
	counts        map[prowords.Category]int
	senderName    string
	recipientName []string
}

// IncidentReport recounts, from the current content of every message in
// inc, the prowords each party has sent, the traffic it has sent and
// received, and which credentials' criteria (see CheckFlow) that meets.
// Sender, recipients, and operator-to-operator status come from the record
// kept when a multi-party flow generated the message; for other messages,
// the sender is guessed from the message number prefix and From ICS
// Position, and the recipient from the To address and To ICS Position.
// Parties appear in the order of their first message.
func IncidentReport(inc *incident.Incident) ([]PartyReport, error) {
	records, err := loadTrainingRecords(inc.Dir)
	if err != nil {
		return nil, err
	}
	draftMu.Lock()
	defer draftMu.Unlock()
	ownPrefix, _, _, _ := messageid.Decode(inc.Config.TxMessageID, true, false)

	var msgs []*reportMessage
	for _, le := range inc.Log {
		if le.Status == incident.StatusDeleted || le.Status == incident.StatusHandEntered || le.Flags&incident.FIsReceipt != 0 {
			continue
		}
		msg, err := inc.GetMessageFromLogEntry(le)
		if msg == nil {
			slog.Warn("proword report: can't read message", "id", le.LocalMsgID, "err", err)
			continue
		}
		rm := &reportMessage{counts: map[prowords.Category]int{}}
		for _, f := range ProwordFields(msg) {
			for _, m := range f.Matches {
				rm.counts[m.Category]++
			}
		}
		if emt, ok := msg.Type().(message.EditableMType); ok {
			rm.opToOp = opToOpTypes[strings.ToLower(emt.CreateTag())]
		}
		if rec, ok := records[le.Ident]; ok {
			rm.record = &rec
			rm.opToOp = rm.opToOp || rec.OpToOp
		} else {
			id := le.LocalMsgID
			if le.Status == incident.StatusReceived {
				id = le.FromMsgID
			}
			if pfx, _, _, err := messageid.Decode(id, true, false); err == nil && pfx != ownPrefix {
				rm.fromPrefix = pfx
			}
			rm.fromRole = commonValue(msg, "fromICSPosition")
			rm.toRole = commonValue(msg, "toICSPosition")
			rm.toAddr = commonValue(msg, "headerTo")
		}
		msgs = append(msgs, rm)
	}

	// Name the senders. A station's plain text messages have no ICS
	// position, so its prefix is named after the role its forms give.
	prefixRole := map[string]string{}
	for _, rm := range msgs {
		if rm.record == nil && rm.fromPrefix != "" && rm.fromRole != "" && prefixRole[rm.fromPrefix] == "" {
			prefixRole[rm.fromPrefix] = rm.fromRole
		}
	}
	var reports []*PartyReport
	byName := map[string]*PartyReport{}
	party := func(name string) *PartyReport {
		if p, ok := byName[name]; ok {
			return p
		}
		p := &PartyReport{Name: name, Counts: map[prowords.Category]int{}}
		byName[name] = p
		reports = append(reports, p)
		return p
	}
	roleName := map[string]string{}
	for _, rm := range msgs {
		if rec := rm.record; rec != nil {
			rm.senderName = rec.From
			if p := party(rec.From); rec.FromCredential != "" {
				p.Credential = rec.FromCredential
			}
			continue
		}
		switch role := rm.fromRole; {
		case rm.fromPrefix != "" && prefixRole[rm.fromPrefix] != "":
			rm.senderName = prefixRole[rm.fromPrefix] + " " + rm.fromPrefix
		case rm.fromPrefix != "":
			rm.senderName = rm.fromPrefix
		case role != "":
			rm.senderName = role
		default:
			rm.senderName = "(unknown sender)"
		}
		party(rm.senderName)
		if rm.fromRole != "" {
			roleName[strings.ToLower(rm.fromRole)] = rm.senderName
		}
	}

	// Name the recipients, now that every sender is known.
	for _, rm := range msgs {
		if rec := rm.record; rec != nil {
			for _, t := range rec.To {
				if p := party(t.Name); t.Credential != "" {
					p.Credential = t.Credential
				}
				rm.recipientName = append(rm.recipientName, t.Name)
			}
			continue
		}
		addrPrefix, _, _ := strings.Cut(rm.toAddr, "@")
		addrPrefix = strings.ToUpper(strings.TrimSpace(addrPrefix))
		switch {
		case isAllStations(rm.toRole) && rm.toRole != "":
			for _, p := range reports {
				if p.Name != rm.senderName {
					rm.recipientName = append(rm.recipientName, p.Name)
				}
			}
		case addrPrefix != "" && prefixRole[addrPrefix] != "":
			rm.recipientName = []string{prefixRole[addrPrefix] + " " + addrPrefix}
		case rm.toRole != "" && roleName[strings.ToLower(rm.toRole)] != "":
			rm.recipientName = []string{roleName[strings.ToLower(rm.toRole)]}
		case rm.toRole != "":
			rm.recipientName = []string{rm.toRole}
		}
	}

	for _, rm := range msgs {
		p := byName[rm.senderName]
		p.Messages++
		for cat, n := range rm.counts {
			p.Counts[cat] += n
		}
		countTraffic(&p.Sent, rm.opToOp)
		for _, r := range rm.recipientName {
			countTraffic(&party(r).Received, rm.opToOp)
		}
	}
	out := make([]PartyReport, len(reports))
	for i, p := range reports {
		p.Reach = credentialReach(p)
		out[i] = *p
	}
	return out, nil
}

func commonValue(msg message.Message, common string) string {
	if f := FindFieldByCommon(msg, common); f != nil {
		return strings.TrimSpace(f.Value(msg))
	}
	return ""
}

// credentialReach checks p against each of ReportCredentials: its message
// minimums, and every proword on its list having been sent at least once.
func credentialReach(p *PartyReport) []CredentialCheck {
	checks := make([]CredentialCheck, 0, len(ReportCredentials))
	for _, c := range ReportCredentials {
		check := CredentialCheck{Credential: c.Code, Label: c.Label}
		check.Missing = trafficProblems(PartyCompliance{Need: credentialNeeds[c.Code], Sent: p.Sent, Received: p.Received})
		level := prowords.LevelFull
		if c.Code == "F3" {
			level = prowords.LevelF3
		}
		profile, _ := prowords.Profile(level)
		var absent []string
		for _, cat := range profile {
			if p.Counts[cat] == 0 {
				absent = append(absent, prowords.ProwordName(cat))
			}
		}
		if len(absent) > 0 {
			check.Missing = append(check.Missing, "never sends "+strings.Join(absent, ", "))
		}
		check.Met = len(check.Missing) == 0
		checks = append(checks, check)
	}
	return checks
}
