package server

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/form/formdefs"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
)

func TestServeGetUnsentPDFs(t *testing.T) {
	formdefs.UseInternalForms = true
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	ics213 := message.FindCreateTag("ICS213")
	if ics213 == nil {
		t.Skip("ICS213 not registered (build without -tags sccopifo?)")
	}
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "YUY-100P"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{stop: make(chan struct{})}
	get := func() *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		s.serveGetUnsentPDFs(rr, httptest.NewRequest(http.MethodGet, "/unsent-pdfs?"+url.Values{"dir": {dir}}.Encode(), nil))
		return rr
	}
	if rr := get(); rr.Code != http.StatusNotFound {
		t.Errorf("with no messages, status = %d, want 404", rr.Code)
	}

	if err := incident.Write(dir, func(i *incident.Incident) error {
		for _, mt := range []message.EditableMType{ics213.(message.EditableMType), message.PlainMessage, message.PlainMessage} {
			draft := mt.NewDraft().(*message.DraftMessage)
			i.ApplyDefaults(draft)
			for f := range draft.Fields() {
				switch f.Common() {
				case "subjectHandling", "handling":
					f.SetValue(draft, "ROUTINE")
				case "subjectSummary":
					f.SetValue(draft, "Generator status")
				}
			}
			if _, err := i.AddDraftMessage(draft); err != nil {
				return err
			}
		}
		i.AddLogEntry(&incident.LogEntry{Subject: "manual"}) // not a message
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rr := get()
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("status %d, content type %q: %s", rr.Code, rr.Header().Get("Content-Type"), rr.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		fr, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		head := make([]byte, 4)
		fr.Read(head)
		fr.Close()
		if string(head) != "%PDF" {
			t.Errorf("%s is not a PDF", f.Name)
		}
	}
	if len(names) != 3 || !slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, "ICS213") }) {
		t.Errorf("files = %q, want 3 PDFs, one of them the ICS-213", names)
	}
	t.Logf("files: %q", names)
	slices.Sort(names)
	if len(slices.Compact(names)) != 3 {
		t.Errorf("file names should be unique: %q", names)
	}
}
