package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/form/formdefs"
	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
	"github.com/rothskeller/pdf/v2"
)

func TestAnnGroups(t *testing.T) {
	f := genmsg.FieldProwords{Label: "Message", Value: "Need 1 generator [220V] for Diego Marchetti now"}
	f.Matches = prowordMatches(f.Value)
	groups := annGroups(f)
	var got []string
	for _, g := range groups {
		if len(g.prowords) > 0 {
			got = append(got, g.text+"="+strings.Join(g.prowords, "+"))
		}
	}
	// "Diego Marchetti" is one match over two groups: named under the first.
	want := "1=FIGURE(S)|[220V]=MIXED GROUP SYMBOL(S)|Diego=I SPELL"
	if strings.Join(got, "|") != want {
		t.Errorf("annotations = %q, want %q", strings.Join(got, "|"), want)
	}
	if len(groups) != 8 || groups[7].text != "now" {
		t.Errorf("groups = %+v, want one per word", groups)
	}
}

func prowordMatches(value string) []prowords.Match {
	for _, f := range genmsg.ProwordFields(plainWith(value)) {
		if f.Value == value {
			return f.Matches
		}
	}
	return nil
}

// plainWith returns a plain text message whose body is value.
func plainWith(value string) message.Message {
	draft := message.PlainMessage.NewDraft().(*message.DraftMessage)
	genmsg.FindField(draft, "defaultBody").SetValue(draft, value)
	return draft
}

func TestAnnotatedPDF(t *testing.T) {
	formdefs.UseInternalForms = true
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	mt := message.FindCreateTag("ICS213")
	if mt == nil {
		t.Skip("ICS213 not registered (build without -tags sccopifo?)")
	}
	draft := mt.(message.EditableMType).NewDraft().(*message.DraftMessage)
	for f := range draft.Fields() {
		switch f.Tag() {
		case "5.":
			f.SetValue(draft, "PRIORITY")
		case "10.":
			f.SetValue(draft, "Generator request")
		case "12.":
			f.SetValue(draft, strings.Repeat("Need 1 generator [220V] at 214 Kaczmarek Street, call 408-555-1212. ", 40))
		}
	}
	fname := filepath.Join(t.TempDir(), "notes.pdf")
	if err := annotatedPDF(draft, "Prowords in S21-101P", fname); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fname)
	if err != nil {
		t.Fatal(err)
	}
	p, err := pdf.Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the annotations are not a valid PDF: %v", err)
	}
	// A long message runs onto a second page.
	if n, err := p.NumPages(); err != nil || n < 2 {
		t.Errorf("pages = %d, %v; want a long message to run over", n, err)
	}
}

func TestAnnGroupsOddSpacing(t *testing.T) {
	// A non-breaking space and a tab: the groups and the positions used to
	// be found by two different rules, which put the names under the wrong
	// words when they disagreed.
	f := genmsg.FieldProwords{Label: "Message", Value: "Need cots\tand 1 generator [220V]"}
	f.Matches = prowordMatches(f.Value)
	var got []string
	for _, g := range annGroups(f) {
		got = append(got, g.text+"="+strings.Join(g.prowords, "+"))
	}
	want := "Need cots=MIXED GROUP|and=|1=FIGURE(S)|generator=|[220V]=MIXED GROUP SYMBOL(S)"
	if strings.Join(got, "|") != want {
		t.Errorf("groups = %q,\nwant %q", strings.Join(got, "|"), want)
	}
}
