package server

import (
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rothskeller/packet/v4/genmsg"
)

// scenarioFlow is a flow with its scenario text, as the multi-party dialog
// sends it.
type scenarioFlow struct {
	genmsg.Flow
	Scenario string `json:"scenario"`
}

// servePostGenTrainingFlowUML handles POST /gentrain-flow-uml requests, whose
// JSON body is a scenarioFlow. It responds with the flow as a PlantUML
// sequence diagram, to be saved as a file.
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
	w.Header().Set("Content-Disposition", `attachment; filename="scenario.puml"`)
	io.WriteString(w, d.PlantUML(sf.Scenario))
}

var flowDiagramTemplate = template.Must(template.New("flow-diagram").Parse(`<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Training scenario, {{.Date}}</title>
  <style>
    body { font-family: sans-serif; margin: 1rem; }
    h1 { font-size: 1.4rem; margin: 0 0 0.25rem; }
    .scenario { color: #444; max-width: 50rem; margin: 0 0 0.75rem; }
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
  <p class="noprint"><button type="button" onclick="window.print()">Print</button></p>
  <h1>Training scenario, {{.Date}}</h1>
  {{if .Scenario}}<p class="scenario">{{.Scenario}}</p>{{end}}
  <div class="scroll">{{.SVG}}</div>
  <details class="noprint">
    <summary>PlantUML</summary>
    <pre>{{.PlantUML}}</pre>
  </details>
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
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, private")
	if err := flowDiagramTemplate.Execute(w, map[string]any{
		"Date":     d.Date,
		"Scenario": strings.TrimSpace(sf.Scenario),
		"SVG":      template.HTML(diagramSVG(d)), // diagramSVG escapes all text
		"PlantUML": d.PlantUML(sf.Scenario),
	}); err != nil {
		slog.Error("render flow diagram", "err", err)
	}
}

// Sequence diagram layout, in pixels.
const (
	sdMargin     = 20
	sdMinColumn  = 150
	sdCharWidth  = 7
	sdHeadHeight = 32
	sdRowHeight  = 36
)

// diagramSVG draws d as an SVG sequence diagram: a box and a lifeline for
// each participant, and a labeled arrow per message, dashed for replies.
// A label too long for its arrow is shortened, with the whole label shown
// on hover.
func diagramSVG(d genmsg.Diagram) string {
	col := sdMinColumn
	for _, p := range d.Participants {
		col = max(col, len([]rune(p))*sdCharWidth+24)
	}
	width := 2*sdMargin + col*len(d.Participants)
	lifeTop := sdMargin + sdHeadHeight
	height := lifeTop + sdRowHeight*(len(d.Arrows)+1) + sdMargin
	x := func(i int) int { return sdMargin + col*i + col/2 }

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" font-family="sans-serif" font-size="12">`, width, height, width, height)
	b.WriteString(`<defs><marker id="arrowhead" viewBox="0 0 10 10" refX="10" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="#333"/></marker></defs>`)
	for i, p := range d.Participants {
		cx := x(i)
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#999" stroke-dasharray="4 3"/>`, cx, lifeTop, cx, height-sdMargin)
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" rx="4" fill="#dff6df" stroke="#555"/>`, cx-col/2+8, sdMargin, col-16, sdHeadHeight)
		fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle" dominant-baseline="middle" font-weight="bold">%s</text>`, cx, sdMargin+sdHeadHeight/2, html.EscapeString(p))
	}
	for k, a := range d.Arrows {
		y := lifeTop + sdRowHeight*(k+1)
		x1, x2 := x(a.From), x(a.To)
		dash := ""
		if a.Reply {
			dash = ` stroke-dasharray="6 4"`
		}
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#333"%s marker-end="url(#arrowhead)"/>`, x1, y, x2, y, dash)
		label := []rune(a.Label)
		if room := (max(x1, x2) - min(x1, x2)) / 6; len(label) > room && room > 1 {
			label = append(label[:room-1], '…')
		}
		fmt.Fprintf(&b, `<text x="%d" y="%d" text-anchor="middle"><title>%s</title>%s</text>`, (x1+x2)/2, y-6, html.EscapeString(a.Label), html.EscapeString(string(label)))
	}
	b.WriteString(`</svg>`)
	return b.String()
}
