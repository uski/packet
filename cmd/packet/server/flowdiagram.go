package server

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
)

// scenarioFlow is a flow with its scenario text, as the multi-party dialog
// sends it.
type scenarioFlow struct {
	genmsg.Flow
	Scenario string `json:"scenario"`
}

// servePostGenTrainingFlowUML handles POST /gentrain-flow-uml requests, whose
// JSON body is a scenarioFlow. It responds with the flow as a sequence
// diagram, to be saved as a file: in the syntax of sequencediagram.org, or
// with format=plantuml, of PlantUML.
func (s *Server) servePostGenTrainingFlowUML(w http.ResponseWriter, r *http.Request) {
	var sf scenarioFlow
	if err := json.NewDecoder(r.Body).Decode(&sf); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	d, err := genmsg.FlowDiagram(sf.Flow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.URL.Query().Get("format") == "plantuml" {
		w.Header().Set("Content-Disposition", `attachment; filename="scenario.puml"`)
		io.WriteString(w, d.PlantUML(sf.Scenario))
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="scenario.uml"`)
	io.WriteString(w, d.SequenceDiagram())
}

var flowDiagramTemplate = template.Must(template.New("flow-diagram").Parse(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>{{.Title}}</title>
  <style>
    body { font-family: sans-serif; margin: 1rem; }
    h1 { font-size: 1.4rem; margin: 0 0 0.25rem; }
    .scenario { color: #444; max-width: 50rem; margin: 0 0 0.75rem; white-space: pre-wrap; }
    .note { color: #444; font-size: 0.9rem; }
    .scroll { overflow-x: auto; }
    pre { background: #f4f4f4; padding: 0.5rem; overflow-x: auto; }
    @media print {
      .noprint { display: none; }
      .scroll { overflow: visible; }
      svg { max-width: 100%; height: auto; }
    }
  </style>
</head>
<body>
  <p class="noprint">
    <button type="button" onclick="window.print()">Print</button>
    <button type="button" onclick="saveText('sd', '{{.FileBase}}.uml')">Save for sequencediagram.org…</button>
    <button type="button" onclick="saveText('puml', '{{.FileBase}}.puml')">Save as PlantUML…</button>
  </p>
  <h1>{{.Title}}</h1>
  {{if .Scenario}}<p class="scenario">{{.Scenario}}</p>{{end}}
  {{if .Note}}<p class="note noprint">{{.Note}}</p>{{end}}
  <div class="scroll">{{.SVG}}</div>
  <details class="noprint">
    <summary>sequencediagram.org</summary>
    <pre id="sd">{{.SequenceDiagram}}</pre>
  </details>
  <details class="noprint">
    <summary>PlantUML</summary>
    <pre id="puml">{{.PlantUML}}</pre>
  </details>
  <script>
    function saveText(id, name) {
      const blob = new Blob([document.getElementById(id).textContent], { type: 'text/plain' })
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = name
      a.click()
      setTimeout(() => URL.revokeObjectURL(a.href), 1000)
    }
  </script>
</body>
</html>
`))

// servePostGenTrainingFlowDiagram handles POST /gentrain-flow-diagram
// requests, which have a flow= form value holding a scenarioFlow as JSON. It
// shows the flow as a printable sequence diagram.
func (s *Server) servePostGenTrainingFlowDiagram(w http.ResponseWriter, r *http.Request) {
	var sf scenarioFlow
	if err := json.Unmarshal([]byte(r.FormValue("flow")), &sf); err != nil {
		s.ErrPage(w, "The scenario could not be read: "+err.Error(), http.StatusBadRequest)
		return
	}
	d, err := genmsg.FlowDiagram(sf.Flow)
	if err != nil {
		s.ErrPage(w, "The scenario can't be drawn: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.writeDiagramPage(w, d, "scenario", sf.Scenario,
		"Times are at the left. Message numbers the scenario doesn't give are those the messages get in an incident with no earlier messages from their stations.")
}

// serveGetIncidentDiagram handles GET /incident-diagram requests, which have
// a dir= parameter and an optional name= parameter (the net name, by
// default the incident's). It shows the incident's messages as a printable
// sequence diagram (see genmsg.IncidentDiagram).
func (s *Server) serveGetIncidentDiagram(w http.ResponseWriter, r *http.Request) {
	var d genmsg.Diagram
	name := strings.TrimSpace(r.FormValue("name"))
	if err := incident.Read(r.FormValue("dir"), func(i *incident.Incident) (err error) {
		if name == "" {
			name = i.Config.IncidentName
		}
		d, err = genmsg.IncidentDiagram(i, name)
		return err
	}); err != nil {
		s.ErrPage(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.writeDiagramPage(w, d, "incident", "",
		"Principals, hand-off groups, and net events are known only for messages generated from a multi-party scenario.")
}

func (s *Server) writeDiagramPage(w http.ResponseWriter, d genmsg.Diagram, fileBase, scenario, note string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, private")
	if err := flowDiagramTemplate.Execute(w, map[string]any{
		"Title":           d.Title(),
		"FileBase":        fileBase,
		"Scenario":        strings.TrimSpace(scenario),
		"Note":            note,
		"SVG":             template.HTML(diagramSVG(d)), // diagramSVG escapes all text
		"SequenceDiagram": d.SequenceDiagram(),
		"PlantUML":        d.PlantUML(scenario),
	}); err != nil {
		slog.Error("render flow diagram", "err", err)
	}
}

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

// diagramSVG draws d as an SVG sequence diagram: a box and a lifeline for
// each participant (rounded for principals), notes, and labeled arrows,
// dashed for hand-offs and replies. Arrows drawn in parallel share a row.
// Message times are in a column at the left, on their messages' first row.
// A label line too long for its arrow is shortened, with the whole label
// shown on hover.
func diagramSVG(d genmsg.Diagram) string {
	col := sdMinColumn
	for _, p := range d.Participants {
		col = max(col, len([]rune(p.Label))*sdCharWidth+24)
	}
	left := sdMargin
	if slices.ContainsFunc(d.Items, func(it genmsg.DiagramItem) bool { return it.Time != "" }) {
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
		rows := [][]genmsg.DiagramArrow{}
		for _, a := range it.Arrows {
			if it.Block == "parallel" && len(rows) > 0 && !overlaps(rows[len(rows)-1], a) {
				rows[len(rows)-1] = append(rows[len(rows)-1], a)
			} else {
				rows = append(rows, []genmsg.DiagramArrow{a})
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
func overlaps(row []genmsg.DiagramArrow, a genmsg.DiagramArrow) bool {
	lo, hi := min(a.From, a.To), max(a.From, a.To)
	for _, r := range row {
		if min(r.From, r.To) < hi && lo < max(r.From, r.To) {
			return true
		}
	}
	return false
}

// drawArrow draws a from x1 to x2 at height y, with its label above it.
func drawArrow(b *strings.Builder, a genmsg.DiagramArrow, x1, x2, y, col int) {
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
