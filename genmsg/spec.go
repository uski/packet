package genmsg

import (
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// This file is the package's vocabulary: what a caller asks Generate for
// (Request and MessageSpec), what it gets back (Result), and what it can
// watch while it waits (Activity).

// MessageSpec describes one message to generate. In the simple case it's
// just a message type; for a multi-party flow (see Flow/ResolveFlow) it
// also carries who the message is from/to and how it relates to the other
// messages in the same Request, so Claude can generate a whole coherent
// exchange in one call while the tool -- not Claude -- keeps the mechanical
// per-message routing (who's sending to whom) perfectly consistent.
type MessageSpec struct {
	MsgType message.EditableMType

	// From, FromLocation, To, and ToLocation, when non-empty, are used
	// directly as the message's From/To ICS Position and Location (set
	// deterministically by Generate/Apply, not asked of Claude, so they
	// stay exactly consistent across a flow -- e.g. message 2's From
	// matches message 1's To when message 2 replies to message 1) and
	// are also given to Claude as context for message types that have no
	// first-class ICS Position/Location fields (e.g. plain text). Leave
	// all empty to let Claude invent a plausible position/location
	// itself, as in the original single-batch (non-flow) mode.
	From         string
	FromLocation string
	To           string
	ToLocation   string

	// FromPrefix and ToPrefix, when non-empty, are the three-character
	// message number prefixes of the sending and receiving stations (e.g.
	// "S24" for Shelter 24). Apply numbers the message with FromPrefix and
	// addresses it to ToPrefix.
	FromPrefix string
	ToPrefix   string

	// Date, if set (MM/DD/YYYY), is the incident date: every filled date
	// field gets it, and a time field gets Time, or is left blank.
	// Otherwise dates and times are the current ones. Date and Packet
	// describe the exercise rather than this one message, so the messages
	// of one request carry the same values (ResolveFlow sets them from
	// the flow); Claude is told the date only when they agree.
	Date string

	// Training, if set, is saved with the incident when the message is
	// created (see Apply), for IncidentReport. ResolveFlow sets it.
	Training *TrainingRecord

	// Purpose, if non-empty, is a short hint of what this specific
	// message is about (e.g. "request shelter capacity status"). Leave
	// empty to let Claude infer it from the scenario and its place in
	// the flow.
	Purpose string

	// ReplyTo, if non-zero, is the 1-based index into the same Request's
	// Messages of another message this one replies to. Claude is told to
	// make this message's content directly and consistently respond to
	// that message's content, even across repair rounds where the
	// referenced message might not itself need regenerating.
	ReplyTo int

	// Level, if non-empty, overrides Request.Level for this one message:
	// prowords.LevelF3 or prowords.LevelFull. This lets a multi-party
	// flow evaluate different parties at different credential levels
	// (e.g. an F3 candidate's messages drawing only from the reduced
	// proword list, while a Net Control party's messages in the same
	// batch draw from the full list) -- each message is graded only
	// against its own sender's level, never mixed with another party's.
	// Leave empty to use Request.Level, as in the original single-batch
	// (non-flow) mode where every message shares one level.
	Level string

	// Handling, if non-empty, is the message's handling order: "R", "P",
	// or "I" (or ROUTINE, PRIORITY, IMMEDIATE). It is set by the tool,
	// never asked of Claude.
	Handling string

	// MsgNo, if non-empty, is the message's number, used instead of the
	// one Apply would pick (see FromPrefix).
	MsgNo string

	// Packet says the message is sent by packet, so the number Apply picks
	// for it gets the "P" suffix (see Date for why it is per message).
	Packet bool

	// Time, if non-empty, is the time the message is written, as HH:MM. It
	// fills the message's time fields (see setIncidentDate), which are
	// otherwise left blank when Date is set.
	Time string
}

// Request describes one batch of training messages to generate. The
// messages need not all be the same type, and (via MessageSpec's From/To/
// Purpose/ReplyTo) need not be independent: a training session might be a
// single ICS-213 asking all stations for a status update, answered by
// several situation-report replies, all generated together for coherence.
type Request struct {
	Incident *incident.Incident // used to apply the incident's own defaults before deciding what the LLM needs to fill in
	Messages []MessageSpec
	Level    string // prowords.LevelF3 or prowords.LevelFull; fallback for any MessageSpec that doesn't set its own Level
	Scenario string // optional user-supplied scenario; if empty, a generic one is invented

	// Progress, if non-nil, is called with human-readable status updates
	// as Generate works. Claude calls in particular can take tens of
	// seconds, so Progress is also called periodically (heartbeatInterval)
	// while one is in flight, not just between calls, so a caller showing
	// these to a user (a CLI status line, a GUI progress panel) always has
	// something recent to display.
	Progress func(string)

	// Activity, if non-nil, is called whenever one of the activities
	// Generate runs, several at a time, changes: planning the scenario, and
	// drafting each message. Every message's activity is reported, waiting,
	// before any starts, in the order they're drafted.
	Activity func(Activity)
}

// Activity is the state of one activity of Generate (see Request.Activity).
type Activity struct {
	Key    string `json:"key"`    // stable identifier: "plan", or "message-N" (1-based message index)
	Label  string `json:"label"`  // what it is, e.g. "Message 3 of 12: ICS-213 from Shelter S21"
	Status string `json:"status"` // what it's doing, e.g. "drafting (8s)"
	State  string `json:"state"`  // ActivityWaiting, ActivityWorking, ActivityDone, or ActivityFailed
}

// Result is one generated message: field tag -> human-readable value, plus
// bookkeeping about which proword categories it was asked to exercise, how
// many times each proword category was actually detected in the final
// values (see the prowords engine, prowords.CountFields), which assigned
// categories (if any) could not be confirmed after generation, the labels
// of any restricted (dropdown/choice) fields where Claude returned a value
// outside the field's allowed choices (dropped rather than written in, so
// the field keeps whatever default it already had), and the labels of any
// required fields still left empty after all repair rounds.
type Result struct {
	Values        map[string]string
	Assigned      []prowords.Category
	Counts        map[prowords.Category]int
	Missing       []prowords.Category
	InvalidFields []string
	MissingFields []string
	Words         int // total words across the message's content fields
}

// DrillTrafficPhrase is the fixed marker every generated message must
// contain somewhere in its content, so it's unmistakably training/exercise
// traffic rather than a real report. Apply guarantees its presence
// deterministically (see ensureDrillTraffic), rather than only asking for
// it in the prompt: a plain, fixed literal like this doesn't need an LLM's
// creativity, and guessing wrong here would be a training-safety issue, not
// just a cosmetic one.
const DrillTrafficPhrase = "This is drill traffic"
