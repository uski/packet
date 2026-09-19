package server

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
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
	Ident         int                       `json:"ident"`
	From          string                    `json:"from"` // the sending party, empty if none
	F3            bool                      `json:"f3"`   // the sender is evaluated on the reduced F3 list
	ID            string                    `json:"id"`
	Type          string                    `json:"type"`
	Prowords      []genTrainingProwordCount `json:"prowords"`
	Missing       []string                  `json:"missing,omitempty"`
	Invalid       []string                  `json:"invalid,omitempty"`
	MissingFields []string                  `json:"missingFields,omitempty"`
	Words         int                       `json:"words"`
	OverLimit     bool                      `json:"overLimit,omitempty"`
}

// genTrainingResult is the final payload of a finished job: the generated
// messages, or an error.
type genTrainingResult struct {
	Messages []genTrainingMessage `json:"messages"`
	// Prowords lists every proword of the complete list, in handbook order
	// (the reduced F3 list first), for the per-party table.
	Prowords []genTrainingProwordRow `json:"prowords"`
}

type genTrainingProwordRow struct {
	Name string `json:"name"`
	F3   bool   `json:"f3"` // on the reduced F3 list
}

// genTrainingJob tracks one in-progress (or just-finished) call to
// packet gentrain from the GUI, so that a long-polling client can watch its
// progress instead of the request blocking for the whole, possibly
// tens-of-seconds-long, generation. This mirrors the bbsConnection /
// connect-progress pattern in connect.go. It's shared by both the simple
// (POST /gentrain-messages) and multi-party flow (POST /gentrain-flow)
// endpoints, and by their common progress poll (GET /gentrain-progress).
type genTrainingJob struct {
	mutex   sync.Mutex
	ProgMsg string `json:"progress"`
	// Activities are the generation's activities (see genmsg.Activity),
	// in the order they were first reported, each in its latest state.
	Activities []genmsg.Activity  `json:"activities,omitempty"`
	ErrMsg     string             `json:"error"`
	Seq        int                `json:"seq"`
	Done       bool               `json:"done"`
	Result     *genTrainingResult `json:"result,omitempty"`
	notify     chan struct{}
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

// Activity records the latest state of one of the generation's parallel
// activities: called from the generation goroutine.
func (j *genTrainingJob) Activity(a genmsg.Activity) {
	j.mutex.Lock()
	if i := slices.IndexFunc(j.Activities, func(b genmsg.Activity) bool { return b.Key == a.Key }); i >= 0 {
		j.Activities[i] = a
	} else {
		j.Activities = append(j.Activities, a)
	}
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

// normalizeLevel returns level lowercased, defaulting to the full proword
// list if it's not a recognized level.
func normalizeLevel(level string) string {
	level = strings.ToLower(level)
	if level != prowords.LevelF3 && level != prowords.LevelFull {
		return prowords.LevelFull
	}
	return level
}

// startGenTrainingJob registers a new job for dir (failing if one is
// already running for it) and starts the generation in a background
// goroutine.
func startGenTrainingJob(dir string, client *genmsg.ClaudeClient, specs []genmsg.MessageSpec, level, scenario string) error {
	genTrainingJobsMutex.Lock()
	if genTrainingJobs[dir] != nil {
		genTrainingJobsMutex.Unlock()
		return fmt.Errorf("a training message generation is already running for this incident")
	}
	job := &genTrainingJob{}
	genTrainingJobs[dir] = job
	genTrainingJobsMutex.Unlock()

	go runGenTraining(job, dir, client, specs, level, scenario)
	return nil
}

// servePostGenTraining handles POST /gentrain-messages requests. They have a
// dir= form value with the incident directory, a level= form value ("f3" or
// "full"), an optional scenario= form value, and one or more repeated
// msgtype= form values, one per message to generate (a training session can
// mix message types, but each message is independent -- no parties or
// replies; see POST /gentrain-flow for that). It validates the request,
// then starts generation in a background goroutine and returns immediately
// (204); progress and the final result are retrieved via
// GET /gentrain-progress.
func (s *Server) servePostGenTraining(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dir := r.FormValue("dir")
	level := normalizeLevel(r.FormValue("level"))
	scenario := r.FormValue("scenario")
	tags := r.Form["msgtype"]
	if len(tags) == 0 {
		http.Error(w, "at least one message type is required", http.StatusBadRequest)
		return
	}
	specs := make([]genmsg.MessageSpec, len(tags))
	for idx, tag := range tags {
		mt, ok := genmsg.FindMsgType(tag)
		if !ok {
			http.Error(w, fmt.Sprintf("no such message type %q", tag), http.StatusBadRequest)
			return
		}
		specs[idx] = genmsg.MessageSpec{MsgType: mt}
	}
	client := &genmsg.ClaudeClient{}
	if !client.HasAPIKey() {
		http.Error(w, genmsg.ErrNoAPIKey.Error(), http.StatusBadRequest)
		return
	}
	if err := startGenTrainingJob(dir, client, specs, level, scenario); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// genTrainingFlowRequest is the JSON body of POST /gentrain-flow: a
// genmsg.Flow (parties and messages referencing them) plus the incident
// directory and the same level/scenario options the simple endpoint takes.
type genTrainingFlowRequest struct {
	Dir      string `json:"dir"`
	Level    string `json:"level"`
	Scenario string `json:"scenario"`
	genmsg.Flow
}

// servePostGenTrainingFlow handles POST /gentrain-flow requests: a JSON
// body (see genTrainingFlowRequest) describing a multi-party message flow,
// for the "Generate Multi-Party Training Messages" dialog. Like
// POST /gentrain-messages, it validates the request, starts generation in a
// background goroutine, and returns 204; progress and the result are
// retrieved the same way, via GET /gentrain-progress.
func (s *Server) servePostGenTrainingFlow(w http.ResponseWriter, r *http.Request) {
	var req genTrainingFlowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	specs, err := genmsg.ResolveFlow(req.Flow)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	client := &genmsg.ClaudeClient{}
	if !client.HasAPIKey() {
		http.Error(w, genmsg.ErrNoAPIKey.Error(), http.StatusBadRequest)
		return
	}
	if err := startGenTrainingJob(req.Dir, client, specs, normalizeLevel(req.Level), req.Scenario); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// servePostGenTrainingFlowCheck handles POST /gentrain-flow-check requests,
// whose JSON body is a genmsg.Flow. It responds with how each party's
// traffic compares with its credential's minimums (see genmsg.CheckFlow).
func (s *Server) servePostGenTrainingFlowCheck(w http.ResponseWriter, r *http.Request) {
	var fl genmsg.Flow
	if err := json.NewDecoder(r.Body).Decode(&fl); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	report, err := genmsg.CheckFlow(fl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"parties": report})
}

// servePostGenTrainingFlowComplete handles POST /gentrain-flow-complete
// requests, whose JSON body is a genmsg.Flow. It responds with the flow
// with messages added so every party meets its credential's minimums (see
// genmsg.CompleteFlow), how many were added, and the resulting compliance.
func (s *Server) servePostGenTrainingFlowComplete(w http.ResponseWriter, r *http.Request) {
	var fl genmsg.Flow
	if err := json.NewDecoder(r.Body).Decode(&fl); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	out, added, err := genmsg.CompleteFlow(fl)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	report, err := genmsg.CheckFlow(out)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"flow": out, "added": added, "parties": report})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode JSON response", "err", err)
	}
}

// runGenTraining does the actual generation work for job, in its own
// goroutine, reporting progress via job.Progress and the final outcome via
// job.finish.
func runGenTraining(job *genTrainingJob, dir string, client *genmsg.ClaudeClient, specs []genmsg.MessageSpec, level, scenario string) {
	var result genTrainingResult
	err := incident.Write(dir, func(i *incident.Incident) error {
		results, err := genmsg.Generate(context.Background(), client, genmsg.Request{
			Incident: i,
			Messages: specs,
			Level:    level,
			Scenario: scenario,
			Progress: job.Progress,
			Activity: job.Activity,
		})
		if err != nil {
			return err
		}
		applied, err := genmsg.Apply(i, specs, results)
		if err != nil {
			return err
		}
		result = buildGenTrainingResult(applied, specs)
		return nil
	})
	if err != nil {
		job.finish(nil, err.Error())
	} else {
		job.finish(&result, "")
	}
}

// buildGenTrainingResult turns the low-level genmsg.Applied results into the
// JSON shape the dialog renders as a table.
func buildGenTrainingResult(applied []genmsg.Applied, specs []genmsg.MessageSpec) genTrainingResult {
	var result genTrainingResult
	full, _ := prowords.Profile(prowords.LevelFull)
	f3, _ := prowords.Profile(prowords.LevelF3)
	for _, cat := range full {
		result.Prowords = append(result.Prowords, genTrainingProwordRow{Name: prowords.ProwordName(cat), F3: slices.Contains(f3, cat)})
	}
	result.Messages = make([]genTrainingMessage, len(applied))
	for j, a := range applied {
		gm := genTrainingMessage{Ident: a.Ident, ID: a.ID, Type: a.MsgType.Name()}
		gm.From = strings.TrimSpace(specs[j].From + " " + specs[j].FromPrefix)
		gm.F3 = specs[j].Level == prowords.LevelF3
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
		gm.MissingFields = a.Result.MissingFields
		gm.Words = a.Result.Words
		gm.OverLimit = a.Result.Words > genmsg.MaxWords+genmsg.WordTolerance
		result.Messages[j] = gm
	}
	return result
}

// serveGetGenTrainingProgress handles GET /gentrain-progress requests, which
// have a dir= parameter identifying the incident and a seq= parameter with
// the sequence number of the last update the client already has. This is a
// long-polling request: it returns a job update (see genTrainingJob's JSON
// tags) as soon as one is available past seq, or 410 Gone if there is no
// job running or finished-but-unclaimed for dir (including after the client
// has already received its Done update once). It works the same regardless
// of whether the job was started by the simple or flow endpoint.
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
			delete(genTrainingJobs, dir)
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
