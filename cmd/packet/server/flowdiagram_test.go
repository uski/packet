package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const diagramTestFlow = `{"scenario":"Storm <b>","date":"2026-03-14",` +
	`"parties":[{"role":"Net Control"},{"role":"Shelter <A>","credential":"F3"}],` +
	`"messages":[{"msgType":"plain","from":0,"to":1,"purpose":"status & more"},` +
	`{"msgType":"plain","from":1,"to":0,"replyTo":1}]}`

func TestServePostGenTrainingFlowUML(t *testing.T) {
	s := &Server{stop: make(chan struct{})}
	rr := httptest.NewRecorder()
	s.servePostGenTrainingFlowUML(rr, httptest.NewRequest(http.MethodPost, "/gentrain-flow-uml", strings.NewReader(diagramTestFlow)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "@startuml") || !strings.Contains(rr.Header().Get("Content-Disposition"), "scenario.puml") {
		t.Errorf("status %d, headers %v, body:\n%s", rr.Code, rr.Header(), rr.Body)
	}

	rr = httptest.NewRecorder()
	s.servePostGenTrainingFlowUML(rr, httptest.NewRequest(http.MethodPost, "/gentrain-flow-uml", strings.NewReader(`{"parties":[{"role":"A"}],"messages":[{"msgType":"plain","from":0,"to":0}]}`)))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("an invalid flow should be a bad request, got %d", rr.Code)
	}
}

func TestServePostGenTrainingFlowDiagram(t *testing.T) {
	s := &Server{stop: make(chan struct{})}
	form := url.Values{"flow": {diagramTestFlow}}
	req := httptest.NewRequest(http.MethodPost, "/gentrain-flow-diagram", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.servePostGenTrainingFlowDiagram(rr, req)
	body := rr.Body.String()
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d, body:\n%s", rr.Code, body)
	}
	for _, want := range []string{"<svg", "Training scenario, 03/14/2026", "Shelter &lt;A&gt; [F3]", "status &amp; more", `stroke-dasharray="6 4"`, "window.print()", "Storm &lt;b&gt;"} {
		if !strings.Contains(body, want) {
			t.Errorf("diagram page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "<A>") || strings.Contains(body, "Storm <b>") {
		t.Error("scenario text must be escaped")
	}
}
