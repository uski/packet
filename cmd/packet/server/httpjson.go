package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/rothskeller/packet/v4/genmsg"
)

// This file holds what the JSON-speaking handlers share: how a request body
// is read, how a response is written, and the shape the multi-party
// scenario dialog posts.

// scenarioFlow is a multi-party scenario as the dialog sends it: the flow
// itself, the scenario text steering its content, and (when messages are to
// be generated) the incident to create them in and the proword level to
// fall back on.
type scenarioFlow struct {
	genmsg.Flow
	Scenario string `json:"scenario"`
	Dir      string `json:"dir"`
	Level    string `json:"level"`
}

// decodeJSON reads the request body into v, answering with a 400 and
// reporting false if it isn't the JSON expected.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

// writeJSON answers the request with v as JSON.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode JSON response", "err", err)
	}
}
