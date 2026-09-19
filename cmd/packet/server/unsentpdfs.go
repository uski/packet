package server

import (
	"archive/zip"
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rothskeller/packet/v4/errors"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/pdf/v2"
)

// unsafeFileChars matches runs of characters not to put in a file name.
var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// unsentMessage is one unsent message, as the download dialog lists it.
type unsentMessage struct {
	Ident   int    `json:"ident"`
	Status  string `json:"status"` // "DRAFT" or "READY"
	ID      string `json:"id"`     // local message number
	To      string `json:"to"`     // the recipient's message number or call sign
	Subject string `json:"subject"`
}

// unsentEntries returns the log entries of i's unsent messages (drafts and
// queued messages, not receipts), in log order.
func unsentEntries(i *incident.Incident) []*incident.LogEntry {
	var out []*incident.LogEntry
	for _, le := range i.Log {
		if (le.Status == incident.StatusDraft || le.Status == incident.StatusQueued) && le.Flags&incident.FIsReceipt == 0 {
			out = append(out, le)
		}
	}
	return out
}

// serveGetUnsentMessages handles GET /unsent-messages requests, which have a
// dir= parameter. It lists the incident's unsent messages, for the download
// dialog to offer.
func (s *Server) serveGetUnsentMessages(w http.ResponseWriter, r *http.Request) {
	msgs := []unsentMessage{}
	if err := incident.Read(r.FormValue("dir"), func(i *incident.Incident) error {
		for _, le := range unsentEntries(i) {
			m := unsentMessage{Ident: le.Ident, Status: "DRAFT", ID: le.LocalMsgID, Subject: le.Subject}
			if le.Status == incident.StatusQueued {
				m.Status = "READY"
			}
			if m.To = le.ToMsgID; m.To == "" {
				m.To = le.ToCall
			}
			// The subject line repeats the message number; the log
			// shows what follows it.
			if m.Subject != "" && m.ID != "" {
				m.Subject = strings.TrimPrefix(m.Subject, m.ID+"_")
			}
			msgs = append(msgs, m)
		}
		return nil
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, msgs)
}

// serveGetUnsentPDFs handles GET /unsent-pdfs requests, which have a dir=
// parameter, a format= parameter, optional repeated id= parameters naming
// the messages to include (by default, all of them), and hideOrigin= and
// hideDest= parameters ("1" to leave that message number off the PDFs),
// and an annotate= parameter ("1" to add each message's proword
// annotations after it). They render a fresh PDF of each unsent
// message (drafts and queued messages, not receipts) in the incident, and
// respond with them either as a ZIP file of separate PDFs named after their
// subject lines (format=zip, the default), or concatenated into a single
// PDF (format=pdf). They respond with a 404 if there are no unsent messages.
func (s *Server) serveGetUnsentPDFs(w http.ResponseWriter, r *http.Request) {
	format := r.FormValue("format")
	if format == "" {
		format = "zip"
	}
	if format != "zip" && format != "pdf" {
		http.Error(w, fmt.Sprintf("unknown format %q", format), http.StatusBadRequest)
		return
	}
	var only map[int]bool
	if ids := r.Form["id"]; len(ids) > 0 {
		only = make(map[int]bool, len(ids))
		for _, id := range ids {
			n, err := strconv.Atoi(id)
			if err != nil {
				http.Error(w, fmt.Sprintf("invalid message ident %q", id), http.StatusBadRequest)
				return
			}
			only[n] = true
		}
	}
	hide := hiddenNumbers{origin: r.FormValue("hideOrigin") == "1", destination: r.FormValue("hideDest") == "1"}
	annotate := r.FormValue("annotate") == "1"
	tmp, err := os.MkdirTemp("", "packet-unsent-pdfs")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmp)
	var pdfs []renderedPDF
	err = incident.Read(r.FormValue("dir"), func(i *incident.Incident) (err error) {
		pdfs, err = renderUnsentPDFs(i, tmp, only, hide, annotate)
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(pdfs) == 0 {
		msg := "There are no unsent messages to download."
		if only != nil {
			msg = "None of the selected messages could be downloaded; they may have been sent or deleted."
		}
		http.Error(w, msg, http.StatusNotFound)
		return
	}
	var (
		data     []byte
		ctype    string
		filename string
	)
	if format == "pdf" {
		data, err = concatenatePDFs(pdfs, "Unsent messages", filepath.Join(tmp, "all.pdf"))
		ctype, filename = "application/pdf", "unsent-messages.pdf"
	} else {
		data, err = zipPDFs(pdfs)
		ctype, filename = "application/zip", "unsent-messages.zip"
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Cache-Control", "no-store, private")
	w.Write(data)
}

// renderedPDF is the PDF of one message.
type renderedPDF struct {
	name string // file name
	data []byte
}

// hiddenNumbers says which message numbers to leave off the PDFs.
type hiddenNumbers struct{ origin, destination bool }

// hideNumbers blanks the message numbers hide names, on the copy of the
// message about to be rendered (never on the incident's own file). The
// origin number is both at the top of a form and in the page footers, and
// is part of a plain text message's subject line, so all three go.
func hideNumbers(msg message.Message, hide hiddenNumbers) {
	for f := range msg.Fields() {
		if !f.Settable() || f.Value(msg) == "" {
			continue
		}
		switch f.Common() {
		case "originMessageID", "subjectMessageID":
			if hide.origin {
				f.SetValue(msg, "")
			}
		case "destinationMessageID":
			if hide.destination {
				f.SetValue(msg, "")
			}
		}
	}
}

// renderUnsentPDFs renders the PDF of each unsent message in i, in log
// order, using directory tmp for scratch files. If only is not nil, just
// the messages whose log entry idents it holds are rendered; hide says
// which message numbers to leave off them, and annotate adds each
// message's proword annotations after it (see annotatedPDF).
func renderUnsentPDFs(i *incident.Incident, tmp string, only map[int]bool, hide hiddenNumbers, annotate bool) (pdfs []renderedPDF, err error) {
	used := map[string]bool{}
	for _, le := range unsentEntries(i) {
		if only != nil && !only[le.Ident] {
			continue
		}
		msg, err := i.GetMessageFromLogEntry(le)
		if err != nil {
			return nil, fmt.Errorf("reading message %s: %w", le.LocalMsgID, err)
		}
		if msg == nil {
			return nil, fmt.Errorf("message %s has no content to render", le.LocalMsgID)
		}
		name := pdfName(le, used)
		hideNumbers(msg, hide)
		fname := filepath.Join(tmp, fmt.Sprintf("%d.pdf", le.Ident))
		if err = msg.Type().RenderPDF(msg, fname, ""); errors.IsType[message.Warning](err) {
			slog.Warn("RenderPDF", "id", le.LocalMsgID, "warn", err)
		} else if err != nil {
			return nil, fmt.Errorf("creating the PDF of %s: %w", le.LocalMsgID, err)
		}
		data, err := os.ReadFile(fname)
		if err != nil {
			return nil, err
		}
		if annotate {
			notes := filepath.Join(tmp, fmt.Sprintf("%d-prowords.pdf", le.Ident))
			if err = annotatedPDF(msg, "Prowords in "+le.LocalMsgID, notes); err != nil {
				return nil, fmt.Errorf("annotating %s: %w", le.LocalMsgID, err)
			}
			annotated, err := os.ReadFile(notes)
			if err != nil {
				return nil, err
			}
			joined := filepath.Join(tmp, fmt.Sprintf("%d-with-prowords.pdf", le.Ident))
			if data, err = concatenatePDFs([]renderedPDF{{name: name, data: data}, {name: name, data: annotated}}, le.LocalMsgID, joined); err != nil {
				return nil, fmt.Errorf("annotating %s: %w", le.LocalMsgID, err)
			}
		}
		pdfs = append(pdfs, renderedPDF{name: name, data: data})
	}
	return pdfs, nil
}

// zipPDFs returns a ZIP file holding pdfs.
func zipPDFs(pdfs []renderedPDF) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, p := range pdfs {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: p.name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return nil, err
		}
		if _, err = fw.Write(p.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// concatenatePDFs returns a single PDF with the pages of pdfs, in order,
// using fname as its scratch file. The rendered PDFs have no form fields
// (their values are drawn on the pages), so their pages can be combined
// without their fields clashing.
func concatenatePDFs(pdfs []renderedPDF, title, fname string) ([]byte, error) {
	fh, err := os.Create(fname)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	out := pdf.New(fh)
	out.Info["Title"] = title
	out.Info["Producer"] = "https://github.com/rothskeller/packet"
	var pages int
	for _, p := range pdfs {
		src, err := pdf.Open(bytes.NewReader(p.data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.name, err)
		}
		n, err := src.NumPages()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.name, err)
		}
		imp, err := out.NewImporter(src)
		if err != nil {
			return nil, err
		}
		for page := 1; page <= n; page++ {
			if err = imp.ImportPage(page, pages+page); err != nil {
				return nil, fmt.Errorf("%s page %d: %w", p.name, page, err)
			}
		}
		pages += n
	}
	if err := out.Write(); err != nil {
		return nil, err
	}
	if err := fh.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(fname)
}

// pdfName returns a file name for le's PDF that isn't in used, and adds it
// there.
func pdfName(le *incident.LogEntry, used map[string]bool) string {
	base := strings.Trim(unsafeFileChars.ReplaceAllString(le.Subject, "_"), "_.")
	if base == "" {
		base = strings.Trim(unsafeFileChars.ReplaceAllString(le.LocalMsgID, "_"), "_.")
	}
	if base == "" {
		base = fmt.Sprintf("message-%d", le.Ident)
	}
	base = string([]rune(base)[:min(len([]rune(base)), 100)])
	name := base + ".pdf"
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s-%d.pdf", base, n)
	}
	used[name] = true
	return name
}
