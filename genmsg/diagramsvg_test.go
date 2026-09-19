package genmsg

import (
	"strings"
	"testing"
)

func TestDiagramSVG(t *testing.T) {
	formType(t, "ICS213")
	d, err := FlowDiagram(sampleFlow())
	if err != nil {
		t.Fatal(err)
	}
	svg := d.SVG()
	for _, want := range []string{
		"<svg", "</svg>",
		"NCO XND",                // a participant, by its label
		"Shelter S21 [F3]",       // with its credential
		`rx="14"`,                // principals are drawn rounded
		`stroke-dasharray="6 4"`, // hand-offs are dashed
		">12:33</text>",          // the time column
		"S21-101 ICS213 (R)",     // a label with its type and handling
		"Open Net",               // an event note
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the SVG lacks %q", want)
		}
	}
	// Every text is escaped: the flow has a party named with brackets.
	d.Participants[0].Label = `A <b> & "c"`
	if svg := d.SVG(); strings.Contains(svg, "<b>") || strings.Contains(svg, `& "c"`) {
		t.Error("participant labels must be escaped")
	}
}
