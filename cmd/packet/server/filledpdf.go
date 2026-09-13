package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/rothskeller/packet/v4/errors"
	"github.com/rothskeller/packet/v4/message"
)

// servePostFilledPDF handles POST /filled-pdf requests, sent by the Filled PDF
// button of a message edit window.  They have a tag= parameter naming the
// message type, and the form's current field values as shown on screen.  The
// response is a PDF of the message filled in with those values; nothing is
// saved.
func (s *Server) servePostFilledPDF(w http.ResponseWriter, r *http.Request) {
	tag := r.FormValue("tag")
	mt := message.FindCreateTag(tag)
	if mt == nil {
		s.ErrPage(w, fmt.Sprintf("The message type tag %q is not recognized.", tag), http.StatusBadRequest)
		return
	}
	msg, err := mt.FromPOST(r)
	if err != nil {
		s.ErrPage(w, err.Error(), http.StatusBadRequest)
		return
	}
	dir, err := os.MkdirTemp("", "packet-filled-pdf")
	if err != nil {
		s.ErrPage(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(dir)
	fname := filepath.Join(dir, "filled.pdf")
	if err = msg.Type().RenderPDF(msg, fname, ""); errors.IsType[message.Warning](err) {
		slog.Warn("RenderPDF", "tag", tag, "warn", err)
	} else if err != nil {
		slog.Error("RenderPDF", "tag", tag, "err", err)
		s.ErrPage(w, fmt.Sprintf("Unable to create PDF: %s", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", "inline; filename=filled.pdf")
	w.Header().Set("Cache-Control", "no-store, private")
	http.ServeFile(w, r, fname)
}
