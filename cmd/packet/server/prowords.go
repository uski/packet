package server

import (
	"cmp"
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

//go:embed prowords.html
var prowordsHTML string

var prowordsTemplate = template.Must(template.New("prowords").Parse(prowordsHTML))

// prowordSegment is a run of a field's text: either text calling for the
// named proword, or (with Proword empty) text calling for none.
type prowordSegment struct {
	Text    string
	Proword string
}

type prowordField struct {
	Label    string
	Segments []prowordSegment
}

type prowordCount struct {
	Name  string
	Count int
}

type prowordPage struct {
	Title  string
	Counts []prowordCount
	Fields []prowordField
}

// serveGetViewProwords handles GET /view-prowords requests, which have dir=
// and id= parameters. It shows the message's fields with the text calling
// for each proword underlined, naming the proword when hovered.
func (s *Server) serveGetViewProwords(w http.ResponseWriter, r *http.Request) {
	var (
		msg message.Message
		lmi string
	)
	s.outpost = false
	dir := r.FormValue("dir")
	ident, _ := strconv.Atoi(r.FormValue("id"))
	err := incident.Read(dir, func(i *incident.Incident) (err error) {
		le := i.GetLogEntryByIdent(ident)
		if le == nil {
			return fmt.Errorf(" The message with ID %d was not found.", ident)
		}
		if msg, err = i.GetMessageFromLogEntry(le); msg == nil {
			if err == nil {
				err = fmt.Errorf(" Log entry %d does not have an associated message.", ident)
			}
			return err
		}
		lmi = le.LocalMsgID
		return nil
	})
	if err != nil {
		s.ErrPage(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := prowordsTemplate.Execute(w, buildProwordPage(lmi, msg)); err != nil {
		slog.Error("render prowords page", "err", err)
	}
}

// buildProwordPage splits each of msg's fields into segments at its proword
// usages, and tallies the usages.
func buildProwordPage(title string, msg message.Message) prowordPage {
	page := prowordPage{Title: title}
	counts := map[prowords.Category]int{}
	for _, f := range genmsg.ProwordFields(msg) {
		pf := prowordField{Label: f.Label}
		pos := 0
		for _, m := range f.Matches {
			if m.Start > pos {
				pf.Segments = append(pf.Segments, prowordSegment{Text: f.Value[pos:m.Start]})
			}
			text := f.Value[m.Start:m.End]
			if m.Category == prowords.Newline {
				text = "¶" + text // a blank line has nothing visible to underline
			}
			pf.Segments = append(pf.Segments, prowordSegment{Text: text, Proword: prowords.ProwordName(m.Category)})
			pos = m.End
			counts[m.Category]++
		}
		if pos < len(f.Value) {
			pf.Segments = append(pf.Segments, prowordSegment{Text: f.Value[pos:]})
		}
		page.Fields = append(page.Fields, pf)
	}
	for cat, n := range counts {
		page.Counts = append(page.Counts, prowordCount{Name: prowords.ProwordName(cat), Count: n})
	}
	slices.SortFunc(page.Counts, func(a, b prowordCount) int { return cmp.Compare(a.Name, b.Name) })
	return page
}
