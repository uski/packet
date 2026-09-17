package server

import (
	"net/http"

	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
)

// deleteAllCounts says how many log entries "delete all messages" deletes,
// and how many it keeps.
type deleteAllCounts struct {
	Deleted int `json:"deleted"`
	Kept    int `json:"kept"`
}

// servePostDeleteAllMessages handles POST /delete-all-messages requests,
// which have a dir= parameter. They delete every unsent message (drafts and
// queued messages and receipts) and every manual log entry of the incident;
// sent and received messages are the incident's record and are kept. With
// dryrun=1, nothing is deleted. They respond with the deleteAllCounts as
// JSON, or an error status and text/plain error message.
func (s *Server) servePostDeleteAllMessages(w http.ResponseWriter, r *http.Request) {
	var counts deleteAllCounts
	s.outpost = false
	dryRun := r.FormValue("dryrun") == "1"
	open := incident.Write
	if dryRun {
		open = incident.Read
	}
	err := open(r.FormValue("dir"), func(i *incident.Incident) error {
		var deleted []int
		for _, le := range i.Log {
			switch le.Status {
			case incident.StatusDeleted:
				continue
			case incident.StatusDraft, incident.StatusQueued, incident.StatusHandEntered:
				counts.Deleted++
				if dryRun {
					continue
				}
				if err := i.DeleteMessage(le); err != nil {
					return err
				}
				deleted = append(deleted, le.Ident)
			default:
				counts.Kept++
			}
		}
		if dryRun {
			return nil
		}
		return genmsg.ForgetTrainingRecords(i.Dir, deleted)
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, counts)
}
