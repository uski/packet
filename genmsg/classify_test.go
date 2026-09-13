package genmsg

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/form/formdefs"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// commonTag returns the addressable key (as computed by Describe) of the
// field in specs with the given Common name, for tests that need a real
// form's actual field tag rather than a hand-built FieldSpec.
func commonTag(specs []FieldSpec, common string) (string, bool) {
	for _, s := range specs {
		if s.Common == common {
			return s.Tag, true
		}
	}
	return "", false
}

func TestClassifyField(t *testing.T) {
	cases := []struct {
		name string
		spec FieldSpec
		want []prowords.Category
	}{
		{"from name", FieldSpec{Common: "fromName"}, []prowords.Category{prowords.ISpell}},
		{"to name", FieldSpec{Common: "toName"}, []prowords.Category{prowords.ISpell}},
		{"contact info (phone and email)", FieldSpec{Label: "9. To Contact Info", Help: "This is contact information (phone number, email, etc.) for the recipient."},
			[]prowords.Category{prowords.EmailAddress, prowords.TelephoneFigures}},
		{"gps field", FieldSpec{Label: "GPS Coordinates"}, []prowords.Category{prowords.GPSCoordinates}},
		{"callsign field", FieldSpec{Label: "Unit Call Sign"}, []prowords.Category{prowords.AmateurCall}},
		{"packet address field", FieldSpec{Help: "The station's packet address."}, []prowords.Category{prowords.PacketAddress}},
		{"website field", FieldSpec{Label: "Website"}, []prowords.Category{prowords.InternetAddress}},
		{"generic body field", FieldSpec{Label: "Message", Help: "The message content."}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ClassifyField(c.spec)
			if len(got) != len(c.want) {
				t.Fatalf("ClassifyField(%+v) = %v, want %v", c.spec, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("ClassifyField(%+v) = %v, want %v", c.spec, got, c.want)
				}
			}
		})
	}
}

func TestSelectFieldsRoutesToDedicatedField(t *testing.T) {
	all := []FieldSpec{
		{Tag: "10.", Common: "messageSummary", Multiline: true, Label: "Message"},
		{Tag: "13.", Common: "fromContact", Label: "From Contact Info", Help: "This is contact information (phone number, email, etc.) for the message author. It is optional and rarely provided."},
		{Tag: "FmName", Common: "fromName", Label: "From Name", Help: "This is the name of the message author. It is optional and rarely provided."},
		{Tag: "irrelevant", Common: "handling", Label: "Handling"},
	}
	selected, routed := SelectFields(all, []prowords.Category{prowords.EmailAddress, prowords.ISpell, prowords.Figures})

	if routed[prowords.EmailAddress] != "13." {
		t.Errorf("EmailAddress routed to %q, want %q", routed[prowords.EmailAddress], "13.")
	}
	if routed[prowords.ISpell] != "FmName" {
		t.Errorf("ISpell routed to %q, want %q", routed[prowords.ISpell], "FmName")
	}
	if _, ok := routed[prowords.Figures]; ok {
		t.Errorf("Figures should not have been routed to any field, got %q", routed[prowords.Figures])
	}

	tags := make(map[string]bool, len(selected))
	for _, s := range selected {
		tags[s.Tag] = true
	}
	if !tags["10."] {
		t.Error("expected the always-included message body to still be selected")
	}
	if !tags["13."] || !tags["FmName"] {
		t.Error("expected the routed contact-info and name fields to be added to the selection")
	}
	if tags["irrelevant"] {
		t.Error("a field matching no assigned category should not be added")
	}
}

func TestSelectFieldsNoDuplicateWhenAlreadySelected(t *testing.T) {
	// A field that's already Required (or otherwise in Generatable's base
	// selection) and also happens to classify for an assigned category
	// should be routed to (so the prompt tells Claude to put that
	// category's content there), but not duplicated in the field list.
	all := []FieldSpec{
		{Tag: "FmName", Common: "fromName", Label: "From Name", Required: true},
	}
	selected, routed := SelectFields(all, []prowords.Category{prowords.ISpell})
	if len(selected) != 1 {
		t.Fatalf("got %d selected fields, want 1 (no duplicate), got %+v", len(selected), selected)
	}
	if routed[prowords.ISpell] != "FmName" {
		t.Errorf("expected ISpell routed to FmName even though it was already required, got %q", routed[prowords.ISpell])
	}
}

func TestSelectFieldsOnRealICS213RoutesContactAndName(t *testing.T) {
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	mt, ok := FindMsgType("ICS213")
	if !ok {
		t.Skip("ICS213 not registered (build without -tags sccopifo?)")
	}
	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error { inc = i; return nil }); err != nil {
		t.Fatal(err)
	}
	draft := mt.NewDraft().(*message.DraftMessage)
	inc.ApplyDefaults(draft)
	applyPartyFields(draft, MessageSpec{MsgType: mt, From: "Logistics", To: "Net Control"})

	all := Describe(draft)
	selected, routed := SelectFields(all, []prowords.Category{prowords.EmailAddress, prowords.ISpell})

	fromContactTag, ok := commonTag(all, "fromContact")
	if !ok {
		t.Fatal("ICS-213 should have a fromContact field")
	}
	fromNameTag, ok := commonTag(all, "fromName")
	if !ok {
		t.Fatal("ICS-213 should have a fromName field")
	}
	if routed[prowords.EmailAddress] != fromContactTag {
		t.Errorf("EmailAddress routed to %q, want the From Contact Info field %q", routed[prowords.EmailAddress], fromContactTag)
	}
	if routed[prowords.ISpell] != fromNameTag {
		t.Errorf("ISpell routed to %q, want the From Name field %q", routed[prowords.ISpell], fromNameTag)
	}
	var gotContact, gotName bool
	for _, s := range selected {
		if s.Tag == fromContactTag {
			gotContact = true
		}
		if s.Tag == fromNameTag {
			gotName = true
		}
	}
	if !gotContact || !gotName {
		t.Errorf("expected the From Contact Info and From Name fields (normally excluded, being optional) to be selected for the LLM to fill, got %+v", selected)
	}
}

// TestGenerateRoutesEmailAndNameToDedicatedFields is an end-to-end
// regression test for the reported bug: instead of cramming an email
// address and a person's name into the free-text body (e.g. "Contact Diego
// Marchetti at diego.marchetti@xanadu-city.org for logistics
// coordination."), Generate should ask Claude to put them in the ICS-213's
// own From Name / From Contact Info fields, which exist for exactly this
// and are otherwise never used since they're marked optional.
func TestGenerateRoutesEmailAndNameToDedicatedFields(t *testing.T) {
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	mt, ok := FindMsgType("ICS213")
	if !ok {
		t.Skip("ICS213 not registered (build without -tags sccopifo?)")
	}

	// The full profile assigns both TELEPHONE FIGURES and EMAIL ADDRESS,
	// which SelectFields spreads across the From and To contact fields,
	// so the response fills both; parseResponse keeps only the fields
	// that were actually asked for.
	var capturedPrompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req claudeRequest
		json.NewDecoder(r.Body).Decode(&req)
		capturedPrompt = req.Messages[0].Content
		resp := claudeResponse{}
		resp.Content = []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{{Type: "text", Text: `[{"5.":"ROUTINE","9a.":"County EOC","9b.":"Warehouse","10.":"Logistics coordination update",` +
			`"12.":"Please review the attached status report and confirm receipt.",` +
			`"FmTel":"diego.marchetti@xanadu-city.org","FmName":"Diego Marchetti",` +
			`"ToTel":"diego.marchetti@xanadu-city.org","ToName":"Diego Marchetti"}]`}}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	dir := t.TempDir()
	var inc *incident.Incident
	if err := incident.Create(dir, func(i *incident.Incident) error { inc = i; return nil }); err != nil {
		t.Fatal(err)
	}

	client := &ClaudeClient{APIKey: "test-key", URL: srv.URL}
	results, err := Generate(context.Background(), client, Request{
		Incident: inc,
		Messages: []MessageSpec{{MsgType: mt, From: "Logistics", To: "Net Control"}},
		Level:    "full",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(capturedPrompt, `"FmTel"`) || !strings.Contains(capturedPrompt, "do NOT also add it to the free-text body") {
		t.Errorf("expected the prompt to route EMAIL ADDRESS to the FmTel field with an explicit do-not-duplicate note\n--- prompt ---\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, `"FmName"`) {
		t.Errorf("expected the prompt to route I SPELL (a name) to the FmName field\n--- prompt ---\n%s", capturedPrompt)
	}

	res := results[0]
	if res.Counts[prowords.EmailAddress] == 0 {
		t.Error("expected EmailAddress to be counted from the FmTel field, not just the body")
	}
	if res.Counts[prowords.ISpell] == 0 {
		t.Error("expected ISpell to be counted from the FmName field")
	}
	body := res.Values["12."]
	if strings.Contains(body, "@") || strings.Contains(body, "Diego") {
		t.Errorf("the free-text body should not need to repeat the routed email/name, got %q", body)
	}
}

func TestSelectFieldsSkipsSkipCommonFields(t *testing.T) {
	// operatorCall always holds a real callsign (see incident.ApplyDefaults),
	// so it happens to classify for nothing here, but skipCommon fields in
	// general must never be selected even if they would otherwise match.
	all := []FieldSpec{
		{Tag: "opcall", Common: "operatorCall", Label: "Operator Call Sign"},
	}
	selected, routed := SelectFields(all, []prowords.Category{prowords.AmateurCall})
	if len(selected) != 0 {
		t.Errorf("expected no fields selected (operatorCall is skipCommon), got %+v", selected)
	}
	if _, ok := routed[prowords.AmateurCall]; ok {
		t.Errorf("expected AmateurCall not routed to a skipCommon field, got %q", routed[prowords.AmateurCall])
	}
}
