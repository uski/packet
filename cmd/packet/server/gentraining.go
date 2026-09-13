package server

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

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
	Invalid  []string                  `json:"invalid,omitempty"`
}

// genTrainingResult is the final payload of a finished job: the generated
// messages, or an error.
type genTrainingResult struct {
	Messages []genTrainingMessage `json:"messages"`
}

// genTrainingJob tracks one in-progress (or just-finished) call to
// packet gentrain from the GUI, so that a long-polling client can watch its
// progress instead of the request blocking for the whole, possibly
// tens-of-seconds-long, generation. This mirrors the bbsConnection /
// connect-progress pattern in connect.go.
type genTrainingJob struct {
	mutex   sync.Mutex
	ProgMsg string             `json:"progress"`
	ErrMsg  string             `json:"error"`
	Seq     int                `json:"seq"`
	Done    bool               `json:"done"`
	Result  *genTrainingResult `json:"result,omitempty"`
	notify  chan struct{}
}

var genTrainingJobs = map[string]*genTrainingJob{}
var genTrainingJobsMutex sync.Mutex

// Progress implements the update side of the job: called from the
// generation goroutine as work proceeds.
func (j *genTrainingJob) Progress(msg string) {
	j.mutex.Lock()
	j.ProgMsg = msg
	j.Seq++
	if j.notify != nil {
		close(j.notify)
		j.notify = nil
	}
	j.mutex.Unlock()
}

// finish marks the job done, with either a result or an error (exactly one
// of the two should be non-zero).
func (j *genTrainingJob) finish(result *genTrainingResult, errMsg string) {
	j.mutex.Lock()
	j.Result = result
	j.ErrMsg = errMsg
	j.Done = true
	j.Seq++
	if j.notify != nil {
		close(j.notify)
		j.notify = nil
	}
	j.mutex.Unlock()
}

// servePostGenTraining handles POST /gentrain-messages requests. They have a
// dir= form value with the incident directory, a level= form value ("f3" or
// "full"), an optional scenario= form value, and one or more repeated
// msgtype= form values, one per message to generate (a training session can
// mix message types). It validates the request, then starts generation in a
// background goroutine and returns immediately (204); progress and the
// final result are retrieved via GET /gentrain-progress.
func (s *Server) servePostGenTraining(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("dir")
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

	genTrainingJobsMutex.Lock()
	if genTrainingJobs[dir] != nil {
		genTrainingJobsMutex.Unlock()
		http.Error(w, "a training message generation is already running for this incident", http.StatusConflict)
		return
	}
	job := &genTrainingJob{}
	genTrainingJobs[dir] = job
	genTrainingJobsMutex.Unlock()

	go runGenTraining(job, dir, client, msgtypes, level, scenario)
	w.WriteHeader(http.StatusNoContent)
}

// runGenTraining does the actual generation work for job, in its own
// goroutine, reporting progress via job.Progress and the final outcome via
// job.finish.
func runGenTraining(job *genTrainingJob, dir string, client *genmsg.ClaudeClient, msgtypes []message.EditableMType, level, scenario string) {
	var result genTrainingResult
	err := incident.Write(dir, func(i *incident.Incident) error {
		results, err := genmsg.Generate(context.Background(), client, genmsg.Request{
			Incident: i,
			MsgTypes: msgtypes,
			Level:    level,
			Scenario: scenario,
			Progress: job.Progress,
		})
		if err != nil {
			return err
		}
		applied, err := genmsg.Apply(i, msgtypes, results)
		if err != nil {
			return err
		}
		result.Messages = make([]genTrainingMessage, len(applied))
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
			gm.Invalid = a.Result.InvalidFields
			result.Messages[j] = gm
		}
		return nil
	})
	if err != nil {
		job.finish(nil, err.Error())
	} else {
		job.finish(&result, "")
	}
}

// serveGetGenTrainingProgress handles GET /gentrain-progress requests, which
// have a dir= parameter identifying the incident and a seq= parameter with
// the sequence number of the last update the client already has. This is a
// long-polling request: it returns a job update (see genTrainingJob's JSON
// tags) as soon as one is available past seq, or 410 Gone if there is no
// job running or finished-but-unclaimed for dir (including after the client
// has already received its Done update once).
func (s *Server) serveGetGenTrainingProgress(w http.ResponseWriter, r *http.Request) {
	dir := r.FormValue("dir")
	seq, _ := strconv.Atoi(r.FormValue("seq"))
	var update []byte
	var notify chan struct{}

RESTART:
	genTrainingJobsMutex.Lock()
	job := genTrainingJobs[dir]
	genTrainingJobsMutex.Unlock()
	if job == nil {
		w.WriteHeader(http.StatusGone)
		return
	}
	job.mutex.Lock()
	if job.Seq > seq {
		update, _ = json.Marshal(job)
		done := job.Done
		job.mutex.Unlock()
		if done {
			genTrainingJobsMutex.Lock()
			genTrainingJobs[dir] = nil
			genTrainingJobsMutex.Unlock()
		}
	} else {
		if job.notify == nil {
			job.notify = make(chan struct{})
		}
		notify = job.notify
		job.mutex.Unlock()
	}
	if notify != nil {
		select {
		case <-s.stop:
			return
		case <-r.Context().Done():
			return
		case <-notify:
			notify = nil
			goto RESTART
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Write(update)
}
