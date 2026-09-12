package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// servePostGenTraining handles POST /gentrain-messages requests. They have a
// dir= form value with the incident directory, a level= form value ("f3" or
// "full"), an optional scenario= form value, and one or more repeated
// msgtype= form values, one per message to generate (a training session can
// mix message types). It creates the generated messages as draft messages
// in the incident, exactly as the "New Message" dialog would for a
// hand-created message.
func (s *Server) servePostGenTraining(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	level := strings.ToLower(r.FormValue("level"))
	if level != prowords.LevelF3 && level != prowords.LevelFull {
		level = prowords.LevelFull
	}
	scenario := r.FormValue("scenario")
	tags := r.Form["msgtype"]
	if len(tags) == 0 {
		http.Error(w, "at least one message type is required", http.StatusBadRequest)
		return
	}
	msgtypes := make([]message.EditableMType, len(tags))
	for idx, tag := range tags {
		var found message.EditableMType
		for mt := range message.AllTypes() {
			if emt, ok := mt.(message.EditableMType); ok {
				if strings.EqualFold(tag, emt.CreateTag()) || strings.EqualFold(tag, emt.CreateKey()) {
					found = emt
					break
				}
			}
		}
		if found == nil {
			http.Error(w, fmt.Sprintf("no such message type %q", tag), http.StatusBadRequest)
			return
		}
		msgtypes[idx] = found
	}
	client := &genmsg.ClaudeClient{}
	if !client.HasAPIKey() {
		http.Error(w, genmsg.ErrNoAPIKey.Error(), http.StatusBadRequest)
		return
	}
	err := incident.Write(r.FormValue("dir"), func(i *incident.Incident) error {
		results, err := genmsg.Generate(r.Context(), client, genmsg.Request{
			MsgTypes: msgtypes,
			Level:    level,
			Scenario: scenario,
		})
		if err != nil {
			return err
		}
		_, incomplete, err := genmsg.Apply(i, msgtypes, results)
		if err != nil {
			return err
		}
		if len(incomplete) != 0 {
			var names []string
			for _, res := range incomplete {
				for _, cat := range res.Missing {
					names = append(names, prowords.ProwordName(cat))
				}
			}
			w.Header().Set("X-Packet-Warning", "Some generated messages may be missing content for: "+strings.Join(names, ", ")+".  Review them before use.")
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
