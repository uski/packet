package server

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// genTrainingProwordCount is one row of a message's proword-count table, for
// the JSON response to the client.
type genTrainingProwordCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// genTrainingMessage describes one generated message in the JSON response.
type genTrainingMessage struct {
	ID       string                    `json:"id"`
	Type     string                    `json:"type"`
	Prowords []genTrainingProwordCount `json:"prowords"`
	Missing  []string                  `json:"missing,omitempty"`
}

// genTrainingResponse is the JSON response body for a successful
// POST /gentrain-messages.
type genTrainingResponse struct {
	Messages []genTrainingMessage `json:"messages"`
}

// servePostGenTraining handles POST /gentrain-messages requests. They have a
// dir= form value with the incident directory, a level= form value ("f3" or
// "full"), an optional scenario= form value, and one or more repeated
// msgtype= form values, one per message to generate (a training session can
// mix message types). It creates the generated messages as draft messages
// in the incident, exactly as the "New Message" dialog would for a
// hand-created message, and responds with a JSON summary (see
// genTrainingResponse) of which prowords each message's content exercises,
// for the dialog to display as a table.
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
	var resp genTrainingResponse
	err := incident.Write(r.FormValue("dir"), func(i *incident.Incident) error {
		results, err := genmsg.Generate(r.Context(), client, genmsg.Request{
			Incident: i,
			MsgTypes: msgtypes,
			Level:    level,
			Scenario: scenario,
		})
		if err != nil {
			return err
		}
		applied, err := genmsg.Apply(i, msgtypes, results)
		if err != nil {
			return err
		}
		resp.Messages = make([]genTrainingMessage, len(applied))
		for j, a := range applied {
			gm := genTrainingMessage{ID: a.ID, Type: a.MsgType.Name()}
			cats := slices.Collect(maps.Keys(a.Result.Counts))
			slices.SortFunc(cats, func(x, y prowords.Category) int {
				return cmp.Compare(prowords.ProwordName(x), prowords.ProwordName(y))
			})
			for _, cat := range cats {
				gm.Prowords = append(gm.Prowords, genTrainingProwordCount{
					Name:  prowords.ProwordName(cat),
					Count: a.Result.Counts[cat],
				})
			}
			for _, cat := range a.Result.Missing {
				gm.Missing = append(gm.Missing, prowords.ProwordName(cat))
			}
			resp.Messages[j] = gm
		}
		return nil
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(&resp)
}
