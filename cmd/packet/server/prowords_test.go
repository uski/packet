package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/rothskeller/packet/v4/form/formdefs"
	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
)

func TestBuildProwordPage(t *testing.T) {
	msg := message.PlainMessage.NewDraft().(*message.DraftMessage)
	genmsg.FindField(msg, "defaultBody").SetValue(msg, "Call 408-555-1212 now.\n\nThanks <all>")
	page := buildProwordPage("S24-101P", msg)

	var segs []prowordSegment
	for _, f := range page.Fields {
		if len(f.Segments) > 1 {
			segs = f.Segments
		}
	}
	var phone, newline bool
	for _, s := range segs {
		phone = phone || (s.Text == "408-555-1212" && s.Proword == "TELEPHONE FIGURES")
		newline = newline || (strings.HasPrefix(s.Text, "¶") && s.Proword == "NEWLINE")
	}
	if !phone || !newline {
		t.Errorf("expected phone number and newline segments, got %+v", segs)
	}

	var buf bytes.Buffer
	if err := prowordsTemplate.Execute(&buf, page); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `<span class="pw"><span class="pwtext">408-555-1212</span><span class="pwname">TELEPHONE FIGURES</span></span>`) {
		t.Errorf("expected an underlined phone number span with its proword named below:\n%s", out)
	}
	if strings.Contains(out, "<all>") {
		t.Error("message text must be HTML-escaped")
	}
}

func TestServeGetViewProwords(t *testing.T) {
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "LOC-100P"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var ident int
	if err := incident.Write(dir, func(i *incident.Incident) error {
		msg := message.PlainMessage.NewDraft().(*message.DraftMessage)
		i.ApplyDefaults(msg)
		genmsg.FindField(msg, "defaultBody").SetValue(msg, "Call 408-555-1212.")
		le, err := i.AddDraftMessage(msg)
		if err == nil {
			ident = le.Ident
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}

	s := &Server{stop: make(chan struct{})}
	params := url.Values{"dir": {dir}, "id": {strconv.Itoa(ident)}}
	rr := httptest.NewRecorder()
	s.serveGetViewProwords(rr, httptest.NewRequest(http.MethodGet, "/view-prowords?"+params.Encode(), nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `<span class="pwname">TELEPHONE FIGURES</span>`) {
		t.Errorf("status %d, body:\n%s", rr.Code, rr.Body)
	}

	rr = httptest.NewRecorder()
	s.serveGetProwordsByParty(rr, httptest.NewRequest(http.MethodGet, "/prowords-by-party?"+url.Values{"dir": {dir}}.Encode(), nil))
	body := rr.Body.String()
	for _, want := range []string{"Prowords by Party", "(unknown sender)", "Field III (F3)", "TELEPHONE FIGURES", `class="pp-f3row"`} {
		if !strings.Contains(body, want) {
			t.Errorf("prowords by party page (status %d) lacks %q:\n%s", rr.Code, want, body)
		}
	}
}

func TestBuildProwordPageCheckIn(t *testing.T) {
	formdefs.UseInternalForms = true
	if err := formdefs.RegisterForms(); err != nil {
		t.Fatal(err)
	}
	mt := message.FindCreateTag("Check-In")
	if mt == nil {
		t.Skip("Check-In not registered (build without -tags sccopifo?)")
	}
	msg := mt.(message.EditableMType).NewDraft().(*message.DraftMessage)
	for f := range msg.Fields() {
		switch f.Common() {
		case "operatorCall":
			f.SetValue(msg, "W6XRL4")
		case "operatorName":
			f.SetValue(msg, "Diego Marchetti")
		}
	}
	page := buildProwordPage("S21-101P", msg)
	if !page.Contentless || len(page.Counts) != 0 || len(page.Fields) == 0 {
		t.Errorf("a check-in should show its fields but count no prowords: %+v", page)
	}
	var buf bytes.Buffer
	if err := prowordsTemplate.Execute(&buf, page); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "no proword is counted") || strings.Contains(buf.String(), `class="pw"`) {
		t.Errorf("page:\n%s", buf.String())
	}
}
