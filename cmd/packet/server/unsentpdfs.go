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
)

// unsafeFileChars matches runs of characters not to put in a file name.
var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// serveGetUnsentPDFs handles GET /unsent-pdfs requests, which have a dir=
// parameter. They respond with a ZIP file holding a separate, freshly
// rendered PDF of each unsent message (drafts and queued messages, not
// receipts) in the incident, named after its subject line, or with a 404
// if there are none.
func (s *Server) serveGetUnsentPDFs(w http.ResponseWriter, r *http.Request) {
	var (
		buf   bytes.Buffer
		count int
	)
	tmp, err := os.MkdirTemp("", "packet-unsent-pdfs")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmp)
	zw := zip.NewWriter(&buf)
	err = incident.Read(r.FormValue("dir"), func(i *incident.Incident) error {
		used := map[string]bool{}
		for _, le := range i.Log {
			if le.Status != incident.StatusDraft && le.Status != incident.StatusQueued || le.Flags&incident.FIsReceipt != 0 {
				continue
			}
			msg, err := i.GetMessageFromLogEntry(le)
			if err != nil || msg == nil {
				return fmt.Errorf("reading message %s: %v", le.LocalMsgID, err)
			}
			fname := filepath.Join(tmp, fmt.Sprintf("%d.pdf", le.Ident))
			if err = msg.Type().RenderPDF(msg, fname, ""); errors.IsType[message.Warning](err) {
				slog.Warn("RenderPDF", "id", le.LocalMsgID, "warn", err)
			} else if err != nil {
				return fmt.Errorf("creating the PDF of %s: %w", le.LocalMsgID, err)
			}
			data, err := os.ReadFile(fname)
			if err != nil {
				return err
			}
			name := pdfName(le, used)
			fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
			if err != nil {
				return err
			}
			if _, err = fw.Write(data); err != nil {
				return err
			}
			count++
		}
		return nil
	})
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if count == 0 {
		http.Error(w, "There are no unsent messages.", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="unsent-messages.zip"`)
	w.Header().Set("Cache-Control", "no-store, private")
	w.Write(buf.Bytes())
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
