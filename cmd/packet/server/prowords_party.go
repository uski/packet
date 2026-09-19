package server

import (
	_ "embed"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/prowords"
)

//go:embed prowords_party.html
var prowordsPartyHTML string

var prowordsPartyTemplate = template.Must(template.New("prowords-party").Parse(prowordsPartyHTML))

type partyPage struct {
	Incident string
	Parties  []partyColumn
	Prowords []partyRow
	Traffic  []partyRow
	Reach    []partyRow
}

type partyColumn struct {
	Name       string
	Credential string
}

type partyRow struct {
	Label string
	Class string
	Cells []partyCell
}

type partyCell struct {
	Text  string
	Class string
}

// serveGetProwordsByParty handles GET /prowords-by-party requests, which have
// a dir= parameter. It recounts, from every message in the incident, the
// prowords and traffic of each party and which credentials that reaches
// (see genmsg.IncidentReport).
func (s *Server) serveGetProwordsByParty(w http.ResponseWriter, r *http.Request) {
	var (
		name    string
		reports []genmsg.PartyReport
	)
	dir := r.FormValue("dir")
	if err := incident.Read(dir, func(i *incident.Incident) (err error) {
		name = i.Config.IncidentName
		reports, err = genmsg.IncidentReport(i)
		return err
	}); err != nil {
		s.ErrPage(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, private")
	if err := prowordsPartyTemplate.Execute(w, buildPartyPage(name, reports)); err != nil {
		slog.Error("render prowords by party page", "err", err)
	}
}

func buildPartyPage(incidentName string, reports []genmsg.PartyReport) partyPage {
	page := partyPage{Incident: incidentName}
	for _, p := range reports {
		page.Parties = append(page.Parties, partyColumn{Name: p.Name, Credential: p.Credential})
	}
	full, _ := prowords.Profile(prowords.LevelFull)
	f3, _ := prowords.Profile(prowords.LevelF3)
	for _, cat := range full {
		onF3 := false
		for _, c := range f3 {
			onF3 = onF3 || c == cat
		}
		row := partyRow{Label: prowords.ProwordName(cat), Class: "pp-fullrow"}
		if onF3 {
			row.Class = "pp-f3row"
		}
		for _, p := range reports {
			n := p.Counts[cat]
			cell := partyCell{Text: strconv.Itoa(n)}
			switch {
			case n > 0:
			case p.Credential == "" || strings.HasPrefix(p.Credential, "N"):
				cell.Class = "pp-zero"
			case p.Credential == "F3" && !onF3:
				cell.Text = "–"
			default:
				cell.Class = "pp-bad" // required by the party's credential
			}
			row.Cells = append(row.Cells, cell)
		}
		page.Prowords = append(page.Prowords, row)
	}
	traffic := []struct {
		label string
		value func(genmsg.PartyReport) int
	}{
		{"Messages sent", func(p genmsg.PartyReport) int { return p.Messages }},
		{"3rd party sent", func(p genmsg.PartyReport) int { return p.Sent.ThirdParty }},
		{"Operator-to-operator sent", func(p genmsg.PartyReport) int { return p.Sent.OpToOp }},
		{"3rd party received", func(p genmsg.PartyReport) int { return p.Received.ThirdParty }},
		{"Operator-to-operator received", func(p genmsg.PartyReport) int { return p.Received.OpToOp }},
	}
	for _, t := range traffic {
		row := partyRow{Label: t.label}
		for _, p := range reports {
			row.Cells = append(row.Cells, partyCell{Text: strconv.Itoa(t.value(p))})
		}
		page.Traffic = append(page.Traffic, row)
	}
	for _, c := range genmsg.ReportCredentials {
		row := partyRow{Label: c.Label}
		for _, p := range reports {
			check, _ := p.Reached(c.Code)
			cell := partyCell{Text: "✓ reached", Class: "pp-good"}
			if !check.Met {
				cell = partyCell{Text: strings.Join(check.Missing, "; "), Class: "pp-short"}
			}
			if p.Credential == c.Code {
				cell.Class += " pp-declared"
			}
			row.Cells = append(row.Cells, cell)
		}
		page.Reach = append(page.Reach, row)
	}
	return page
}
