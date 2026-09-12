// Package genmsg generates realistic third-party training messages for SCCo
// RACES credential evaluations, using Claude to draft content that exercises
// a given credential level's required prowords (see the prowords package),
// and materializes the results as normal draft messages in an incident.
//
// It works generically with any registered message.EditableMType: it
// introspects the type's editable fields at runtime rather than hard-coding
// knowledge of any particular form (ICS-213, Road Closure, Shelter, etc.).
package genmsg

import "github.com/rothskeller/packet/v4/message"

// FieldSpec describes one editable field of a message type, for building an
// LLM prompt and for later looking up the field to set its value.
type FieldSpec struct {
	Tag       string   // the PackItForms tag identifying the field
	Common    string   // the well-known common-field name, if any
	Label     string   // the human-readable field label
	Help      string   // help text describing the field's purpose
	Multiline bool     // whether the field expects multi-line text
	Choices   []string // allowed/recommended values, if the field is restricted
	Required  bool     // true if the field currently has no value and fails validation without one
}

// Describe returns the settable fields of msg (which should be a freshly
// created draft with the incident's defaults already applied -- see
// incident.Incident.ApplyDefaults) that have a PackItForms tag, in field
// order, for use in building an LLM prompt.
func Describe(msg message.Message) []FieldSpec {
	var specs []FieldSpec
	for f := range msg.Fields() {
		if !f.Editable(msg, false) || !f.Settable() {
			continue
		}
		tag := f.Tag()
		if tag == "" {
			// Fields without a PIFO tag (e.g. read-only computed
			// fields, or virtual date/time combinations) aren't
			// independently addressable; skip them.
			continue
		}
		spec := FieldSpec{
			Tag:       tag,
			Common:    f.Common(),
			Label:     f.Label(),
			Help:      f.EditHelp(),
			Multiline: f.Multiline(),
			Required:  f.Value(msg) == "" && f.Validate(msg, f, 0) != nil,
		}
		if f.Restricted() {
			for _, c := range f.Choices(msg) {
				if c.Human != "" {
					spec.Choices = append(spec.Choices, c.Human)
				}
			}
		}
		specs = append(specs, spec)
	}
	return specs
}

// skipCommon lists the well-known common fields that the incident's
// ApplyDefaults/AddDraftMessage machinery already populates (message IDs,
// dates, times, operator info, envelope headers) and that the LLM should
// not be asked to fill in.
var skipCommon = map[string]bool{
	"originMessageID":      true,
	"destinationMessageID": true,
	"messageDate":          true,
	"messageTime":          true,
	"formDate":             true,
	"operatorName":         true,
	"operatorCall":         true,
	"operatorDate":         true,
	"operatorTime":         true,
	"operatorMethod":       true,
	"operatorMethodOther":  true,
	"subjectMessageID":     true,
	"subjectFormTag":       true,
	"headerDate":           true,
	"headerFrom":           true,
	"headerTo":             true,
	"headerReceived":       true,
}

// alwaysInclude lists common fields worth asking the LLM to fill even when
// they aren't strictly required, because a message without them would look
// obviously unfinished.
var alwaysInclude = map[string]bool{
	"messageSummary": true, // Subject
}

// Generatable filters specs down to the small set the LLM should actually
// be asked to produce a value for: fields already handled by the
// incident's default-filling machinery are excluded outright, and of what
// remains, only fields that are actually required (or the message body, or
// a field in alwaysInclude) are kept. This keeps generated messages short
// and avoids padding out optional, rarely-used fields (contact info,
// reply/take-action toggles, references, and the like) purely because they
// exist -- exactly the fields most forms mark optional because they're
// "rarely provided" in practice.
func Generatable(specs []FieldSpec) []FieldSpec {
	var out []FieldSpec
	for _, s := range specs {
		if skipCommon[s.Common] {
			continue
		}
		if s.Required || s.Multiline || alwaysInclude[s.Common] {
			out = append(out, s)
		}
	}
	return out
}
