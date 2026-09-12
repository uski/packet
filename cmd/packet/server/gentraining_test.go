package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rothskeller/packet/v4/incident"
)

// These tests call the HTTP handlers directly (httptest.NewRecorder /
// httptest.NewRequest), without starting the real listening server, so they
// can't collide with a packet gui instance that might already be running
// for interactive use.

func TestServePostGenTrainingNoAPIKey(t *testing.T) {
	dir := t.TempDir()
	if err := incident.Create(dir, func(*incident.Incident) error { return nil }); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv("ANTHROPIC_API_KEY")

	s := &Server{stop: make(chan struct{})}
	form := url.Values{"dir": {dir}, "level": {"f3"}, "msgtype": {"plain"}}
	req := httptest.NewRequest(http.MethodPost, "/gentrain-messages", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	s.servePostGenTraining(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 with no API key, got %d: %s", rr.Code, rr.Body)
	}
	if !strings.Contains(rr.Body.String(), "ANTHROPIC_API_KEY") {
		t.Errorf("expected error to mention ANTHROPIC_API_KEY, got %q", rr.Body.String())
	}
}

func TestServeGetGenTrainingProgressNoJob(t *testing.T) {
	s := &Server{stop: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/gentrain-progress?dir=no-such-dir&seq=0", nil)
	rr := httptest.NewRecorder()
	s.serveGetGenTrainingProgress(rr, req)
	if rr.Code != http.StatusGone {
		t.Errorf("expected 410 for a dir with no job, got %d", rr.Code)
	}
}

func TestGenTrainingJobProgressAndFinish(t *testing.T) {
	job := &genTrainingJob{}
	job.Progress("step one")
	if job.Seq != 1 || job.ProgMsg != "step one" {
		t.Errorf("unexpected job state after Progress: %+v", job)
	}
	job.finish(&genTrainingResult{Messages: []genTrainingMessage{{ID: "LOC-1"}}}, "")
	if !job.Done || job.Seq != 2 || job.Result == nil || len(job.Result.Messages) != 1 {
		t.Errorf("unexpected job state after finish: %+v", job)
	}
}

// TestGenTrainingProgressLongPollWakesOnUpdate verifies the notify-channel
// wake-up: a poll blocked waiting for seq to advance must return promptly
// once Progress is called, not just on its own timeout.
func TestGenTrainingProgressLongPollWakesOnUpdate(t *testing.T) {
	dir := "test-dir-" + strconv.Itoa(int(time.Now().UnixNano()))
	job := &genTrainingJob{}
	genTrainingJobsMutex.Lock()
	genTrainingJobs[dir] = job
	genTrainingJobsMutex.Unlock()
	defer func() {
		genTrainingJobsMutex.Lock()
		delete(genTrainingJobs, dir)
		genTrainingJobsMutex.Unlock()
	}()

	s := &Server{stop: make(chan struct{})}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodGet, "/gentrain-progress?dir="+url.QueryEscape(dir)+"&seq=0", nil)
		rr := httptest.NewRecorder()
		s.serveGetGenTrainingProgress(rr, req)
		done <- rr
	}()
	time.Sleep(20 * time.Millisecond) // let the poll block on notify
	job.Progress("hello")

	select {
	case rr := <-done:
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", rr.Code, rr.Body)
		}
		if !strings.Contains(rr.Body.String(), "hello") {
			t.Errorf("expected body to contain the progress message, got %s", rr.Body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("long poll did not return promptly after Progress was called")
	}
}

// TestGenTrainingProgressLongPollReturnsGoneAfterDone verifies that once a
// finished job's last update has been delivered, the job entry is cleared
// so a subsequent poll gets 410 Gone instead of the same update forever.
func TestGenTrainingProgressLongPollReturnsGoneAfterDone(t *testing.T) {
	dir := "test-dir-" + strconv.Itoa(int(time.Now().UnixNano()))
	job := &genTrainingJob{}
	job.finish(&genTrainingResult{Messages: []genTrainingMessage{{ID: "LOC-1"}}}, "")
	genTrainingJobsMutex.Lock()
	genTrainingJobs[dir] = job
	genTrainingJobsMutex.Unlock()

	s := &Server{stop: make(chan struct{})}
	req1 := httptest.NewRequest(http.MethodGet, "/gentrain-progress?dir="+url.QueryEscape(dir)+"&seq=0", nil)
	rr1 := httptest.NewRecorder()
	s.serveGetGenTrainingProgress(rr1, req1)
	if rr1.Code != http.StatusOK || !strings.Contains(rr1.Body.String(), "LOC-1") {
		t.Fatalf("first poll: status=%d body=%s", rr1.Code, rr1.Body)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/gentrain-progress?dir="+url.QueryEscape(dir)+"&seq=2", nil)
	rr2 := httptest.NewRecorder()
	s.serveGetGenTrainingProgress(rr2, req2)
	if rr2.Code != http.StatusGone {
		t.Errorf("second poll after done: expected 410, got %d: %s", rr2.Code, rr2.Body)
	}
}
