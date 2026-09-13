package genmsg

import (
	"testing"

	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

func TestProwordFields(t *testing.T) {
	mt := formType(t, "ICS213")
	draft, err := buildDraft(draftTestIncident(t), MessageSpec{MsgType: mt, From: "Shelter Manager", To: "Net Control"},
		map[string]string{"10.": "Shelter status", "12.": "Call 408-555-1212 about 5kW generators."})
	if err != nil {
		t.Fatal(err)
	}
	fields := ProwordFields(draft)
	var body, position *FieldProwords
	for i := range fields {
		switch fields[i].Value {
		case "Call 408-555-1212 about 5kW generators.":
			body = &fields[i]
		case "Shelter Manager":
			position = &fields[i]
		}
	}
	if body == nil || position == nil {
		t.Fatalf("expected the body and From position among the fields, got %+v", fields)
	}
	cats := map[prowords.Category]string{}
	for _, m := range body.Matches {
		cats[m.Category] = body.Value[m.Start:m.End]
	}
	if cats[prowords.TelephoneFigures] != "408-555-1212" || cats[prowords.MixedGroupFigures] != "5kW" {
		t.Errorf("body matches = %+v, want the phone number and 5kW", body.Matches)
	}
	if position.Matches != nil {
		t.Errorf("an ICS position name should not be matched, got %+v", position.Matches)
	}

	plain := message.PlainMessage.NewDraft().(*message.DraftMessage)
	FindField(plain, "defaultBody").SetValue(plain, "Call 408-555-1212.")
	if fields := ProwordFields(plain); len(fields) != 1 || len(fields[0].Matches) == 0 {
		t.Errorf("plain message body should be listed with its matches, got %+v", fields)
	}
}
