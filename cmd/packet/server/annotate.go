package server

import (
	"os"
	"strings"

	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
	"github.com/rothskeller/pdf/v2"
)

// Layout of an annotation page, in points.
const (
	annPageWidth  = 612.0 // US Letter
	annPageHeight = 792.0
	annMargin     = 40.0
	annFont       = "Helvetica"
	annTitleSize  = 13.0
	annLabelSize  = 8.0
	annTextSize   = 11.0
	annNameSize   = 5.5 // the proword names under the text
	annLineHeight = 26.0
	annNameDrop   = 8.0 // how far the names sit below the baseline
	annGroupBar   = 5   // a vertical bar after every this many groups
)

var (
	annGray = []byte{0x66, 0x66, 0x66}
	annRed  = []byte{0xcc, 0x00, 0x00}
)

// annotatedPDF writes to fname a PDF of msg's fields (see
// genmsg.ProwordFields) with the proword each piece of text calls for
// written in small type under it, and a vertical bar after every fifth
// group, as an evaluator marks up a message by hand.
func annotatedPDF(msg message.Message, title, fname string) (err error) {
	fh, err := os.Create(fname)
	if err != nil {
		return err
	}
	defer fh.Close()
	out := pdf.New(fh)
	out.Info["Title"] = title
	out.Info["Producer"] = "https://github.com/rothskeller/packet"
	a := &annotator{pdf: out}
	if err = a.newPage(); err != nil {
		return err
	}
	if err = a.draw(title, annMargin, a.y, annTitleSize, annFont, nil); err != nil {
		return err
	}
	a.y -= annTitleSize + 10
	if err = a.draw("Prowords are named under the text that calls for them; a bar follows every "+
		itoa(annGroupBar)+" groups.", annMargin, a.y, annLabelSize, annFont, annGray); err != nil {
		return err
	}
	a.y -= annLineHeight
	for _, f := range genmsg.ProwordFields(msg) {
		if err = a.field(f); err != nil {
			return err
		}
	}
	if err = out.Write(); err != nil {
		return err
	}
	return fh.Close()
}

func itoa(n int) string { return string(rune('0' + n)) }

// annotator lays out an annotation page, tracking the current page and the
// baseline of the line being written.
type annotator struct {
	pdf  *pdf.PDF
	page int
	y    float64
}

// newPage starts a new page, with the first line's baseline at the top.
func (a *annotator) newPage() error {
	if err := a.pdf.AddPage(pdf.RectangleWH(0, 0, annPageWidth, annPageHeight)); err != nil {
		return err
	}
	a.page++
	a.y = annPageHeight - annMargin - annTextSize
	return nil
}

// room makes sure there is space for another line, starting a new page if
// not.
func (a *annotator) room() error {
	if a.y-annNameDrop-annNameSize >= annMargin {
		return nil
	}
	return a.newPage()
}

// draw writes s with its left edge at x and its baseline at y.
func (a *annotator) draw(s string, x, y, size float64, font string, color []byte) error {
	if s = strings.TrimRight(s, " "); s == "" {
		return nil
	}
	w, _, _ := pdf.MeasureText(s, font, size)
	return pdf.Text{
		String: s, Page: a.page, Baseline: y, Font: font, FontSize: size, Color: color,
		Rectangle: pdf.RectangleWH(x, y-size, w+2, size*2), Align: "l",
	}.Draw(a.pdf)
}

// field writes one field: its label, then its value laid out group by
// group with the prowords named underneath.
func (a *annotator) field(f genmsg.FieldProwords) error {
	if err := a.room(); err != nil {
		return err
	}
	if err := a.draw(f.Label, annMargin, a.y, annLabelSize, annFont, annGray); err != nil {
		return err
	}
	a.y -= annLabelSize + 6
	x := annMargin
	var groups int
	for _, g := range annGroups(f) {
		width, _, _ := pdf.MeasureText(g.text, annFont, annTextSize)
		space, _, _ := pdf.MeasureText(" ", annFont, annTextSize)
		name := strings.Join(g.prowords, " ")
		nameWidth, _, _ := pdf.MeasureText(name, annFont, annNameSize)
		// The group takes at least the width of the name under it, so
		// two names can never run together.
		advance := max(width, nameWidth) + space
		if x > annMargin && x+advance > annPageWidth-annMargin {
			a.y -= annLineHeight
			if err := a.room(); err != nil {
				return err
			}
			x = annMargin
		}
		if err := a.draw(g.text, x, a.y, annTextSize, annFont, nil); err != nil {
			return err
		}
		if name != "" {
			if err := a.draw(name, x, a.y-annNameDrop, annNameSize, annFont, annRed); err != nil {
				return err
			}
		}
		x += advance
		if groups++; groups%annGroupBar == 0 {
			bar := pdf.Line{
				P1: pdf.Point{X: x - space/2, Y: a.y - 3}, P2: pdf.Point{X: x - space/2, Y: a.y + annTextSize - 2},
				Page: a.page, Width: 0.75, Stroke: annRed,
			}
			if err := bar.Draw(a.pdf); err != nil {
				return err
			}
			x += space
		}
	}
	a.y -= annLineHeight
	return nil
}

// annGroup is one whitespace-delimited group of a field's value, with the
// prowords its text calls for.
type annGroup struct {
	text     string
	prowords []string
}

// annGroups splits f's value into groups, each with the prowords of the
// matches that start in it. A match covering several groups (a two-word
// name, say) is named under the first of them.
func annGroups(f genmsg.FieldProwords) []annGroup {
	var groups []annGroup
	for _, field := range strings.Fields(f.Value) {
		groups = append(groups, annGroup{text: field})
	}
	// Walk the value again to find where each group starts, so the
	// matches can be assigned to them by position.
	var starts []int
	inGroup := false
	for i, r := range f.Value {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			inGroup = false
		case !inGroup:
			starts = append(starts, i)
			inGroup = true
		}
	}
	for _, m := range f.Matches {
		name := prowords.ProwordName(m.Category)
		for i := len(starts) - 1; i >= 0; i-- {
			if starts[i] <= m.Start && i < len(groups) {
				groups[i].prowords = append(groups[i].prowords, name)
				break
			}
		}
	}
	return groups
}
