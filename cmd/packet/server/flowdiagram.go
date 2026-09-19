package server

import (
	"encoding/json"
	"html/template"
	"io"
	"log/slog"
	"net/http"
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
		"SVG":             template.HTML(d.SVG()), // Diagram.SVG escapes all text
		"SequenceDiagram": d.SequenceDiagram(),
		"PlantUML":        d.PlantUML(scenario),
	}); err != nil {
		slog.Error("render flow diagram", "err", err)
	}
}
