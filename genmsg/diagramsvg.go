package genmsg

import (
	"fmt"
	"html"
	"slices"
	"strings"
)

// This file draws a Diagram as an SVG, for a page that shows or prints it;
// diagram.go has the same diagram as sequencediagram.org and PlantUML text.

// Sequence diagram layout, in pixels.
const (
	sdMargin     = 20
	sdMinColumn  = 130
	sdCharWidth  = 7
	sdHeadHeight = 32
	sdLine       = 15 // height of a line of text
	sdGap        = 12 // space between rows
	sdSection    = 24 // extra space before a section note
	sdTimeColumn = 70 // width of the message time column, when there is one
)

// SVG draws d as an SVG sequence diagram: a box and a lifeline for
// each participant (rounded for principals), notes, and labeled arrows,
// dashed for hand-offs and replies. Arrows drawn in parallel share a row.
// Message times are in a column at the left, on their messages' first row.
// A label line too long for its arrow is shortened, with the whole label
// shown on hover.
func (d Diagram) SVG() string {
	col := sdMinColumn
	for _, p := range d.Participants {
		col = max(col, len([]rune(p.Label))*sdCharWidth+24)
	}
	left := sdMargin
	if slices.ContainsFunc(d.Items, func(it DiagramItem) bool { return it.Time != "" }) {
		left += sdTimeColumn
	}
	width := left + sdMargin + col*len(d.Participants)
	x := func(i int) int { return left + col*i + col/2 }

	var body strings.Builder
	y := sdMargin + sdHeadHeight + sdGap
	for _, it := range d.Items {
		if it.Note != "" {
			if it.Section {
				y += sdSection
			}
			lines := strings.Split(it.Note, "\n")
			h := len(lines)*sdLine + 10
			x1, x2 := x(it.NoteFrom)-col/2+12, x(it.NoteTo)+col/2-12
			fmt.Fprintf(&body, `<rect x="%d" y="%d" width="%d" height="%d" fill="#fff8c4" stroke="#b8a940"/>`, x1, y, x2-x1, h)
			writeLines(&body, (x1+x2)/2, y+sdLine, lines, "")
			y += h + sdGap
			continue
		}
		rows := [][]DiagramArrow{}
		for _, a := range it.Arrows {
			if it.Block == "parallel" && len(rows) > 0 && !overlaps(rows[len(rows)-1], a) {
				rows[len(rows)-1] = append(rows[len(rows)-1], a)
			} else {
				rows = append(rows, []DiagramArrow{a})
			}
		}
		for r, row := range rows {
			lines := 1
			for _, a := range row {
				lines = max(lines, strings.Count(a.Label, "\n")+1)
			}
			y += lines * sdLine
			if r == 0 && it.Time != "" {
				fmt.Fprintf(&body, `<text x="%d" y="%d" font-weight="bold" fill="#555">%s</text>`, sdMargin, y+4, html.EscapeString(it.Time))
			}
			for _, a := range row {
				drawArrow(&body, a, x(a.From), x(a.To), y, col)
			}
			y += sdGap
		}
	}
	height := y + sdMargin

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="sans-serif" font-size="12">`, width, height, width, height)
	b.WriteString(`<defs><marker id="arrowhead" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="#333"/></marker>`)
	b.WriteString(`<marker id="openhead" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10" fill="none" stroke="#333"/></marker></defs>`)
	for i, p := range d.Participants {
		cx := x(i)
		fill, rx := "#dff6df", 4
		if p.Principal {
			fill, rx = "#e3ecff", 14
		}
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#999" stroke-dasharray="4 3"/>`, cx, sdMargin+sdHeadHeight, cx, height-sdMargin)
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="%d" fill="%s" stroke="#555"/>`, cx-col/2+8, sdMargin, col-16, sdHeadHeight, rx, fill)
		fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" dominant-baseline="middle" font-weight="bold">%s</text>`, cx, sdMargin+sdHeadHeight/2, html.EscapeString(p.Label))
	}
	b.WriteString(body.String())
	b.WriteString(`</svg>`)
	return b.String()
}

// overlaps says whether arrow a would cross any arrow of row.
func overlaps(row []DiagramArrow, a DiagramArrow) bool {
	lo, hi := min(a.From, a.To), max(a.From, a.To)
	for _, r := range row {
		if min(r.From, r.To) < hi && lo < max(r.From, r.To) {
			return true
		}
	}
	return false
}

// drawArrow draws a from x1 to x2 at height y, with its label above it.
func drawArrow(b *strings.Builder, a DiagramArrow, x1, x2, y, col int) {
	style := ` marker-end="url(#arrowhead)"`
	switch {
	case a.Handoff:
		style = ` stroke-dasharray="6 4" marker-end="url(#openhead)"`
	case a.Reply:
		style = ` stroke-dasharray="6 4" marker-end="url(#arrowhead)"`
	}
	fmt.Fprintf(b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#333"%s/>`, x1, y, x2, y, style)
	if a.Label == "" {
		return
	}
	title := a.Label
	if a.Detail != "" {
		title += "\n" + a.Detail
	}
	lines := strings.Split(a.Label, "\n")
	room := max((max(x1, x2)-min(x1, x2))/6, col/6)
	for i, l := range lines {
		if r := []rune(l); len(r) > room && room > 1 {
			lines[i] = string(append(r[:room-1], '…'))
		}
	}
	writeLines(b, (x1+x2)/2, y-4-(len(lines)-1)*sdLine, lines, title)
}

// writeLines writes centered text lines, the first at baseline y.
func writeLines(b *strings.Builder, x, y int, lines []string, title string) {
	fmt.Fprintf(b, `<text x="%d" y="%d" text-anchor="middle">`, x, y)
	if title != "" {
		fmt.Fprintf(b, `<title>%s</title>`, html.EscapeString(title))
	}
	for i, l := range lines {
		dy := "0"
		if i > 0 {
			dy = fmt.Sprint(sdLine)
		}
		fmt.Fprintf(b, `<tspan x="%d" dy="%s">%s</tspan>`, x, dy, html.EscapeString(l))
	}
	b.WriteString(`</text>`)
}
