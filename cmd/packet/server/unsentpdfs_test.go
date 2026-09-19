package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/form/formdefs"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/pdf/v2"
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
	get := func(format ...string) *httptest.ResponseRecorder {
		query := url.Values{"dir": {dir}, "format": format}
		rr := httptest.NewRecorder()
		s.serveGetUnsentPDFs(rr, httptest.NewRequest(http.MethodGet, "/unsent-pdfs?"+query.Encode(), nil))
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
	var (
		names []string
		pages int
	)
	for _, f := range zr.File {
		names = append(names, f.Name)
		fr, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(fr)
		fr.Close()
		if err != nil {
			t.Fatal(err)
		}
		pages += pdfPages(t, f.Name, data)
	}
	if len(names) != 3 || !slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, "ICS213") }) {
		t.Errorf("files = %q, want 3 PDFs, one of them the ICS-213", names)
	}
	t.Logf("files: %q", names)
	slices.Sort(names)
	if len(slices.Compact(names)) != 3 {
		t.Errorf("file names should be unique: %q", names)
	}

	rr = get("pdf")
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/pdf" {
		t.Fatalf("single PDF: status %d, content type %q: %s", rr.Code, rr.Header().Get("Content-Type"), rr.Body)
	}
	if n := pdfPages(t, "single PDF", rr.Body.Bytes()); n != pages || n < 3 {
		t.Errorf("single PDF has %d pages, want the %d of the separate PDFs", n, pages)
	}
	if rr := get("doc"); rr.Code != http.StatusBadRequest {
		t.Errorf("unknown format: status %d, want 400", rr.Code)
	}
}

// pdfPages parses data as a PDF and returns its number of pages.
func pdfPages(t *testing.T, name string, data []byte) int {
	t.Helper()
	p, err := pdf.Open(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s is not a valid PDF: %v", name, err)
	}
	n, err := p.NumPages()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return n
}

func TestServeGetUnsentMessagesAndSelection(t *testing.T) {
	formdefs.UseInternalForms = true
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "YUY-100P"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var idents []int
	if err := incident.Write(dir, func(i *incident.Incident) error {
		for _, summary := range []string{"First message", "Second message"} {
			draft := message.PlainMessage.NewDraft().(*message.DraftMessage)
			i.ApplyDefaults(draft)
			for f := range draft.Fields() {
				switch f.Common() {
				case "subjectHandling":
					f.SetValue(draft, "ROUTINE")
				case "subjectSummary":
					f.SetValue(draft, summary)
				case "headerTo":
					f.SetValue(draft, "eoc@w6xsc.ampr.org")
				}
			}
			le, err := i.AddDraftMessage(draft)
			if err != nil {
				return err
			}
			idents = append(idents, le.Ident)
		}
		i.AddLogEntry(&incident.LogEntry{Subject: "manual"}) // not a message
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	s := &Server{stop: make(chan struct{})}
	rr := httptest.NewRecorder()
	s.serveGetUnsentMessages(rr, httptest.NewRequest(http.MethodGet, "/unsent-messages?"+url.Values{"dir": {dir}}.Encode(), nil))
	var list []unsentMessage
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil || rr.Code != http.StatusOK {
		t.Fatalf("status %d, decode %v", rr.Code, err)
	}
	if len(list) != 2 {
		t.Fatalf("listed %d messages, want the 2 unsent ones: %+v", len(list), list)
	}
	if list[0].Ident != idents[0] || list[0].ID != "YUY-100P" || list[0].Status != "DRAFT" || list[0].To != "EOC" ||
		list[0].Subject != "R_First message" {
		t.Errorf("first message = %+v", list[0])
	}

	// Only the selected message is downloaded.
	query := url.Values{"dir": {dir}, "format": {"zip"}, "id": {strconv.Itoa(idents[1])}}
	rr = httptest.NewRecorder()
	s.serveGetUnsentPDFs(rr, httptest.NewRequest(http.MethodGet, "/unsent-pdfs?"+query.Encode(), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(rr.Body.Bytes()), int64(rr.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || !strings.Contains(zr.File[0].Name, "Second") {
		t.Errorf("downloaded %d file(s): %+v", len(zr.File), zr.File)
	}
}
