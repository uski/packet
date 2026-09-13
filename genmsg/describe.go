// Package genmsg generates realistic third-party training messages for SCCo
// RACES credential evaluations, using Claude to draft content that exercises
// a given credential level's required prowords (see the prowords package),
// and materializes the results as normal draft messages in an incident.
//
// It works generically with any registered message.EditableMType: it
// introspects the type's editable fields at runtime rather than hard-coding
// knowledge of any particular form (ICS-213, Road Closure, Shelter, etc.).
package genmsg

import (
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/message/field"
)

// FieldSpec describes one editable field of a message type, for building an
// LLM prompt and for later looking up the field to set its value.
type FieldSpec struct {
	Tag       string   // the field's PackItForms tag, or its Common name if it has no tag (see Describe)
	Common    string   // the well-known common-field name, if any
	Label     string   // the human-readable field label
	Help      string   // help text describing the field's purpose
	Multiline bool     // whether the field expects multi-line text
	Choices   []string // allowed/recommended values, if the field is restricted
	Required  bool     // true if the field currently has no value and fails validation without one
}

// Describe returns the settable, addressable fields of msg (which should be
// a freshly created draft with the incident's defaults already applied --
// see incident.Incident.ApplyDefaults), in field order, for use in building
// an LLM prompt. Not every message type is PackItForms-based (e.g. a plain
// text message has no PIFO tags at all), so a field's Common name is used
// as its Tag/key when it has no PIFO tag of its own; see FindField for the
// matching lookup used when applying values back.
func Describe(msg message.Message) []FieldSpec {
	var specs []FieldSpec
	for f := range msg.Fields() {
		if !f.Editable(msg, false) || !f.Settable() {
			continue
		}
		key := f.Tag()
		if key == "" {
			key = f.Common()
		}
		if key == "" {
			// Fields with neither a PIFO tag nor a common name
			// (e.g. read-only computed fields, or virtual
			// date/time combinations) aren't independently
			// addressable; skip them.
			continue
		}
		spec := FieldSpec{
			Tag:       key,
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

// FindField returns the field of msg whose key (as computed by Describe:
// its PIFO tag, or its Common name if it has none) equals key, or nil if
// there is no such field. This is how generated LLM field values (keyed the
// same way) get matched back to a field; for looking a field up purely by
// its well-known Common name regardless of whether it also has a PIFO tag,
// use FindFieldByCommon instead.
func FindField(msg message.Message, key string) field.Field {
	for f := range msg.Fields() {
		fkey := f.Tag()
		if fkey == "" {
			fkey = f.Common()
		}
		if fkey == key {
			return f
		}
	}
	return nil
}

// FindFieldByCommon returns the field of msg whose Common name equals
// common, or nil if there is no such field. Unlike FindField, this matches
// on Common regardless of whether the field also has a PIFO tag -- needed
// for fields like fromICSPosition/toICSPosition/fromLocation/toLocation,
// which on a real PackItForms-based type are keyed by their form-specific
// tag (e.g. "7."), not their common name.
func FindFieldByCommon(msg message.Message, common string) field.Field {
	for f := range msg.Fields() {
		if f.Common() == common {
			return f
		}
	}
	return nil
}

// alwaysInclude lists common fields worth asking the LLM to fill even when
// they aren't strictly required, because a message without them would look
// obviously unfinished. Different message types use different common names
// for what is conceptually the same "subject line" field (form types use
// messageSummary; plain text messages use subjectSummary, which packs
// message ID/handling/summary into one SCCo-standard subject line).
var alwaysInclude = map[string]bool{
	"messageSummary": true,
	"subjectSummary": true,
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
