package genmsg

import (
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// applyForReport creates an incident, applies specs and results to it, and
// returns the report on it.
func applyForReport(t *testing.T, specs []MessageSpec, results []Result) []PartyReport {
	t.Helper()
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "YUY-100P"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := incident.Write(dir, func(i *incident.Incident) error {
		_, err := Apply(i, specs, results)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var reports []PartyReport
	if err := incident.Read(dir, func(i *incident.Incident) (err error) {
		reports, err = IncidentReport(i)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return reports
}

func reportByName(t *testing.T, reports []PartyReport, name string) PartyReport {
	t.Helper()
	for _, p := range reports {
		if p.Name == name {
			return p
		}
	}
	names := make([]string, len(reports))
	for i, p := range reports {
		names[i] = p.Name
	}
	t.Fatalf("no party %q in the report; parties: %v", name, names)
	return PartyReport{}
}

func plainValues(body string) map[string]string {
	return map[string]string{"subjectHandling": "ROUTINE", "subjectSummary": "Status", "defaultBody": body}
}

func TestIncidentReportFromFlowRecords(t *testing.T) {
	formType(t, "ICS213")
	fl := Flow{
		Parties: []FlowParty{
			{Role: "Net Control", Prefix: "EOC"},
			{Role: "Shelter A", Prefix: "S24", Credential: "F3"},
			{Role: "Shelter B", Prefix: "S25", Credential: "S2"},
		},
		Messages: []FlowMessage{
			{MsgType: "ICS213", From: 0, To: -1, ToLabel: "All Stations"},
			{MsgType: "ICS213", From: fromEachStation, To: 0, ReplyTo: 1, OpToOp: true},
		},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	ics := map[string]string{"5.": "ROUTINE", "10.": "Status", "12.": f3Body}
	results := make([]Result, len(specs))
	for i := range results {
		results[i].Values = ics
	}
	reports := applyForReport(t, specs, results)

	nc := reportByName(t, reports, "Net Control EOC")
	a := reportByName(t, reports, "Shelter A S24")
	b := reportByName(t, reports, "Shelter B S25")
	if a.Credential != "F3" || b.Credential != "S2" || nc.Credential != "" {
		t.Errorf("credentials = %q, %q, %q; want the flow's", nc.Credential, a.Credential, b.Credential)
	}
	if nc.Sent != (Traffic{ThirdParty: 1}) || nc.Received != (Traffic{OpToOp: 2}) {
		t.Errorf("Net Control sent %+v, received %+v", nc.Sent, nc.Received)
	}
	if a.Sent != (Traffic{OpToOp: 1}) || a.Received != (Traffic{ThirdParty: 1}) {
		t.Errorf("Shelter A sent %+v, received %+v; the reply is marked operator-to-operator", a.Sent, a.Received)
	}
	if a.Counts[prowords.TelephoneFigures] == 0 {
		t.Errorf("Shelter A's prowords should be counted from its message, got %v", a.Counts)
	}
	f3 := a.Reach[0]
	if f3.Credential != "F3" || f3.Met {
		t.Fatalf("Shelter A shouldn't reach F3 with one message each way, got %+v", f3)
	}
	missing := strings.Join(f3.Missing, "; ")
	if !strings.Contains(missing, "sends 0 of 2 3rd party form messages") || strings.Contains(missing, "never sends") {
		t.Errorf("F3 shortfall = %q; want the traffic shortfall and no missing F3 proword", missing)
	}
}

func TestIncidentReportGuessesWithoutRecords(t *testing.T) {
	mt := formType(t, "ICS213")
	specs := []MessageSpec{
		{MsgType: mt, From: "Shelter C", FromPrefix: "S30", To: "Net Control", ToPrefix: "EOC"},
		{MsgType: mt, From: "Net Control", To: "All Stations"},
		{MsgType: message.PlainMessage, FromPrefix: "S30", ToPrefix: "EOC"},
	}
	ics := map[string]string{"5.": "ROUTINE", "10.": "Status", "12.": "Call 408-555-1212."}
	results := []Result{{Values: ics}, {Values: ics}, {Values: plainValues("Power is out.")}}
	reports := applyForReport(t, specs, results)

	c := reportByName(t, reports, "Shelter C S30")
	if c.Messages != 2 || c.Sent != (Traffic{ThirdParty: 1, OpToOp: 1}) {
		t.Errorf("Shelter C: %d messages, sent %+v; want its form and its plain message together", c.Messages, c.Sent)
	}
	if c.Received != (Traffic{ThirdParty: 1}) {
		t.Errorf("Shelter C should receive Net Control's All Stations message, got %+v", c.Received)
	}
	nc := reportByName(t, reports, "Net Control")
	if nc.Received.ThirdParty != 1 {
		t.Errorf("Net Control should receive Shelter C's form, got %+v", nc.Received)
	}
}

// TestReportCountsOnlySentProwords verifies that a party's proword counts
// come only from the messages it sends: the ones it receives are the
// sender's, not its own.
func TestReportCountsOnlySentProwords(t *testing.T) {
	formType(t, "ICS213")
	fl := Flow{
		Parties: []FlowParty{
			{Role: "Net Control", Prefix: "EOC"},
			{Role: "Shelter A", Prefix: "S24"},
		},
		Messages: []FlowMessage{
			{MsgType: "plain", From: 0, To: 1},
			{MsgType: "plain", From: 1, To: 0},
		},
	}
	specs, err := ResolveFlow(fl)
	if err != nil {
		t.Fatal(err)
	}
	// Only Net Control's message has any proword content.
	results := []Result{
		{Values: plainValues("Call 408-555-1212 about 25 cots.")},
		{Values: plainValues("Received.")},
	}
	reports := applyForReport(t, specs, results)
	nc := reportByName(t, reports, "Net Control EOC")
	shelter := reportByName(t, reports, "Shelter A S24")
	if nc.Counts[prowords.TelephoneFigures] != 1 || nc.Counts[prowords.Figures] != 1 {
		t.Errorf("the sender should count its own prowords: %v", nc.Counts)
	}
	if shelter.Counts[prowords.TelephoneFigures] != 0 || shelter.Counts[prowords.Figures] != 0 {
		t.Errorf("the recipient counted prowords it only received: %v", shelter.Counts)
	}
	if shelter.Received.OpToOp != 1 || shelter.Sent.OpToOp != 1 { // plain messages are operator-to-operator
		t.Errorf("the recipient's traffic should still count both ways: %+v %+v", shelter.Sent, shelter.Received)
	}
}

// TestRecordNamesSenderFromSpec verifies that the saved record names the
// sender the same way the message itself does: the name used to be written
// twice, once by ResolveFlow and once by the spec's From and FromPrefix.
func TestRecordNamesSenderFromSpec(t *testing.T) {
	formType(t, "ICS213")
	specs, err := ResolveFlow(Flow{
		Parties:  []FlowParty{{Role: "Net Control", Prefix: "XND"}, {Role: "Shelter", Prefix: "S21"}},
		Messages: []FlowMessage{{MsgType: "plain", From: 1, To: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reports := applyForReport(t, specs, []Result{{Values: plainValues("Status.")}})
	p := reportByName(t, reports, "Shelter S21")
	if p.Messages != 1 {
		t.Errorf("%s sent %d messages", p.Name, p.Messages)
	}
}

func TestPartyReportReached(t *testing.T) {
	p := PartyReport{Reach: []CredentialCheck{
		{Credential: "F3", Met: true},
		{Credential: "F2", Missing: []string{"sends 1 of 3"}},
	}}
	if c, ok := p.Reached("F3"); !ok || !c.Met {
		t.Errorf("F3 = %+v, %v", c, ok)
	}
	if c, ok := p.Reached("F2"); !ok || c.Met || len(c.Missing) != 1 {
		t.Errorf("F2 = %+v, %v", c, ok)
	}
	if _, ok := p.Reached("N1"); ok {
		t.Error("a credential the report doesn't cover should say so")
	}
}
