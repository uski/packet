package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
)

func TestServePostDeleteAllMessages(t *testing.T) {
	dir := t.TempDir()
	if err := incident.Create(dir, func(i *incident.Incident) error {
		i.Config.TxMessageID = "YUY-100P"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var draftIdent int
	if err := incident.Write(dir, func(i *incident.Incident) error {
		for range 2 {
			draft := message.PlainMessage.NewDraft().(*message.DraftMessage)
			i.ApplyDefaults(draft)
			le, err := i.AddDraftMessage(draft)
			if err != nil {
				return err
			}
			draftIdent = le.Ident
		}
		i.AddLogEntry(&incident.LogEntry{Subject: "manual"})
		sent := &incident.LogEntry{Subject: "sent"}
		i.AddLogEntry(sent)
		sent.Status = incident.StatusSent // a record that must be kept
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	records := filepath.Join(dir, ".training.json")
	if err := os.WriteFile(records, []byte(`{"`+strconv.Itoa(draftIdent)+`":{"from":"A"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{stop: make(chan struct{})}
	post := func(query url.Values) deleteAllCounts {
		t.Helper()
		rr := httptest.NewRecorder()
		s.servePostDeleteAllMessages(rr, httptest.NewRequest(http.MethodPost, "/delete-all-messages?"+query.Encode(), nil))
		var counts deleteAllCounts
		if rr.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rr.Code, rr.Body)
		}
		if err := json.NewDecoder(rr.Body).Decode(&counts); err != nil {
			t.Fatal(err)
		}
		return counts
	}
	want := deleteAllCounts{Deleted: 3, Kept: 1}
	if got := post(url.Values{"dir": {dir}, "dryrun": {"1"}}); got != want {
		t.Errorf("dry run counts = %+v, want %+v", got, want)
	}
	if got := post(url.Values{"dir": {dir}}); got != want {
		t.Errorf("counts = %+v, want %+v", got, want)
	}
	if got := post(url.Values{"dir": {dir}}); got != (deleteAllCounts{Kept: 1}) {
		t.Errorf("after deleting, counts = %+v", got)
	}
	if err := incident.Read(dir, func(i *incident.Incident) error {
		for _, le := range i.Log {
			if le.Status != incident.StatusDeleted && le.Status != incident.StatusSent {
				t.Errorf("entry %d (%s) was not deleted", le.Ident, le.Status)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(records); !os.IsNotExist(err) {
		t.Errorf("the deleted messages' training records should be gone: %v", err)
	}
}
