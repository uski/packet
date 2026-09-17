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
	"strings"
	"time"

	"github.com/rothskeller/packet/v4/errors"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/pdf/v2"
)

// unsafeFileChars matches runs of characters not to put in a file name.
var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// serveGetUnsentPDFs handles GET /unsent-pdfs requests, which have a dir=
// parameter and a format= parameter. They render a fresh PDF of each unsent
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
	tmp, err := os.MkdirTemp("", "packet-unsent-pdfs")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmp)
	var pdfs []renderedPDF
	err = incident.Read(r.FormValue("dir"), func(i *incident.Incident) (err error) {
		pdfs, err = renderUnsentPDFs(i, tmp)
		return err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if len(pdfs) == 0 {
		http.Error(w, "There are no unsent messages.", http.StatusNotFound)
		return
	}
	var (
		data     []byte
		ctype    string
		filename string
	)
	if format == "pdf" {
		data, err = concatenatePDFs(pdfs, filepath.Join(tmp, "all.pdf"))
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

// renderUnsentPDFs renders the PDF of every unsent message in i, in log
// order, using directory tmp for scratch files.
func renderUnsentPDFs(i *incident.Incident, tmp string) (pdfs []renderedPDF, err error) {
	used := map[string]bool{}
	for _, le := range i.Log {
		if le.Status != incident.StatusDraft && le.Status != incident.StatusQueued || le.Flags&incident.FIsReceipt != 0 {
			continue
		}
		msg, err := i.GetMessageFromLogEntry(le)
		if err != nil || msg == nil {
			return nil, fmt.Errorf("reading message %s: %v", le.LocalMsgID, err)
		}
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
		pdfs = append(pdfs, renderedPDF{name: pdfName(le, used), data: data})
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
func concatenatePDFs(pdfs []renderedPDF, fname string) ([]byte, error) {
	fh, err := os.Create(fname)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	out := pdf.New(fh)
	out.Info["Title"] = "Unsent messages"
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
