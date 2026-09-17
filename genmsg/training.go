package genmsg

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

// trainingFileName is the file in an incident's directory recording, for
// each message generated from a multi-party flow, the details a report
// can't recover from the message itself (see IncidentReport).
const trainingFileName = ".training.json"

// TrainingRecord is what a multi-party flow says about one generated
// message: its sending party, the parties that receive it (an "All
// Stations" message lists every other party), and whether it is
// operator-to-operator traffic. Parties are named as in the flow: role,
// then message number prefix if any.
type TrainingRecord struct {
	From           string          `json:"from"`
	FromCredential string          `json:"fromCredential,omitempty"`
	To             []TrainingParty `json:"to,omitempty"`
	OpToOp         bool            `json:"opToOp,omitempty"`
	// FromPrincipal is the sending party's principal (see
	// FlowParty.Principal).
	FromPrincipal string `json:"fromPrincipal,omitempty"`
	// Batch identifies the flow generation that made the message, and Step
	// the 1-based flow entry it came from, so the messages of one "each
	// station" or "All Stations" entry can be drawn together.
	Batch string `json:"batch,omitempty"`
	Step  int    `json:"step,omitempty"`
	// Group is the message's hand-off group within its batch (see
	// FlowMessage.Group).
	Group int `json:"group,omitempty"`
	// Events are the flow events just before the message, and EventsAfter
	// those after it, for the last message of a flow.
	Events      []FlowMessage `json:"events,omitempty"`
	EventsAfter []FlowMessage `json:"eventsAfter,omitempty"`
}

// TrainingParty is a party that receives a message.
type TrainingParty struct {
	Name       string `json:"name"`
	Credential string `json:"credential,omitempty"`
	Principal  string `json:"principal,omitempty"`
}

// partyName is how a flow party is named in records and reports.
func partyName(p FlowParty) string {
	if p.Prefix == "" {
		return p.Role
	}
	return p.Role + " " + p.Prefix
}

// loadTrainingRecords reads the records in incident directory dir, keyed by
// log entry ident.
func loadTrainingRecords(dir string) (map[int]TrainingRecord, error) {
	data, err := os.ReadFile(filepath.Join(dir, trainingFileName))
	if errors.Is(err, os.ErrNotExist) {
		return map[int]TrainingRecord{}, nil
	} else if err != nil {
		return nil, err
	}
	var byKey map[string]TrainingRecord
	if err := json.Unmarshal(data, &byKey); err != nil {
		return nil, err
	}
	records := make(map[int]TrainingRecord, len(byKey))
	for k, r := range byKey {
		if ident, err := strconv.Atoi(k); err == nil {
			records[ident] = r
		}
	}
	return records, nil
}

// saveTrainingRecords adds records (keyed by log entry ident) to those
// already saved in incident directory dir.
func saveTrainingRecords(dir string, records map[int]TrainingRecord) error {
	if len(records) == 0 {
		return nil
	}
	all, err := loadTrainingRecords(dir)
	if err != nil {
		return err
	}
	byKey := make(map[string]TrainingRecord, len(all)+len(records))
	for ident, r := range all {
		byKey[strconv.Itoa(ident)] = r
	}
	for ident, r := range records {
		byKey[strconv.Itoa(ident)] = r
	}
	data, err := json.MarshalIndent(byKey, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, trainingFileName), data, 0o644)
}
