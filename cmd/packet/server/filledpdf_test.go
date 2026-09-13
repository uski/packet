package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/form/formdefs"
	"github.com/rothskeller/packet/v4/message"
)

func TestFilledPDF(t *testing.T) {
	// Test the forms built into this binary, not a cached forms bundle.
	formdefs.UseInternalForms = true
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	mt := message.FindCreateTag("ICS213")
	if mt == nil {
		t.Skip("ICS213 not registered (build without -tags sccopifo?)")
	}

	// The edit page offers the button only when given its URL.
	draft := mt.NewDraft().(*message.DraftMessage)
	vars := message.EditHTMLVars{SubmitURL: "/send-message", SubmitLabel: "Send Message", AssetBase: "/assets/ICS213"}
	page, err := mt.EditHTML(draft, vars)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(page), "show-filled-PDF") {
		t.Error("no Filled PDF button should be shown without a FilledPDFURL")
	}
	vars.FilledPDFURL = "/filled-pdf?tag=ICS213"
	if page, err = mt.EditHTML(draft, vars); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), "show-filled-PDF") || !strings.Contains(string(page), "/filled-pdf?tag=ICS213") {
		t.Error("the edit page should have a Filled PDF button posting to its URL")
	}

	values := url.Values{"MsgNo": {"S24-101P"}, "5.": {"ROUTINE"}, "10.": {"Generator status"}, "12.": {"Generator runtime is 8 hours."}}
	req := httptest.NewRequest(http.MethodPost, "/filled-pdf?tag=ICS213", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	(&Server{stop: make(chan struct{})}).servePostFilledPDF(rr, req)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(rr.Body.Bytes(), []byte("%PDF")) {
		t.Errorf("status %d, content type %q, want a PDF; body starts %q", rr.Code, rr.Header().Get("Content-Type"), rr.Body.Bytes()[:min(rr.Body.Len(), 200)])
	}
}
