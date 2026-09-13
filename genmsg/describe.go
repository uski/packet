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
	Problem   string   // on a repair round, why the field's current value fails validation
	Optional  bool     // offered to Claude to fill only if the message has information that belongs in it
	Group     string   // label of the required checkbox group this checkbox belongs to, of which at least one must be checked
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
		if !f.Settable() {
			continue
		}
		// A form field that isn't editable only because the field it
		// depends on is still empty (e.g. Item 2 until Item 1 is filled)
		// is included, so a message can use it.
		blockedForNow := f.Tag() != "" && f.EditHelp() != "" && !isDateTimePart(f)
		if !f.Editable(msg, false) && !blockedForNow {
			continue
		}
		// Fields with neither a PIFO tag nor a common name (e.g. virtual
		// date/time combinations) aren't independently addressable.
		if key := fieldKey(f); key != "" {
			specs = append(specs, newFieldSpec(msg, f, key))
		}
	}
	return specs
}

// isDateTimePart reports whether f is the date or time half of a combined
// date/time field, which is edited through its parent.
func isDateTimePart(f field.Field) bool {
	p := f.Parent()
	return p != nil && p.EditHint() == "mm/dd/yyyy hh:mm"
}

// fieldKey returns the key a field is addressed by: its PIFO tag, or its
// Common name if it has no tag.
func fieldKey(f field.Field) string {
	if f.Tag() != "" {
		return f.Tag()
	}
	return f.Common()
}

func newFieldSpec(msg message.Message, f field.Field, key string) FieldSpec {
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
	return spec
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
	"reference":            true, // set by Apply for a reply
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

// AllFieldValues returns the current value of every field of msg that has
// an addressable key (as computed by Describe: its PIFO tag, or its Common
// name if it has none), regardless of whether the field is editable or was
// ever asked of the LLM. Unlike Describe, which only looks at what the LLM
// might need to fill in, this is for measuring what the message actually
// contains once finished -- the incident's own defaults (message ID, date,
// operator name and call, etc.) and the deterministic party fields are just
// as much a part of what a candidate reads aloud as the LLM-generated
// content, so a proword category already satisfied by one of them doesn't
// need to be redundantly woven into the free-text body too.
func AllFieldValues(msg message.Message) map[string]string {
	values := make(map[string]string)
	for f := range msg.Fields() {
		// Administrative fields (dates, times, operator and envelope
		// data) aren't message content, and ICS position/location role
		// names like "Shelter Manager" would falsely read as I SPELL
		// names, so neither counts toward proword coverage.
		if skipCommon[f.Common()] || shortNameField[f.Common()] {
			continue
		}
		key := f.Tag()
		if key == "" {
			key = f.Common()
		}
		if key == "" {
			continue
		}
		if v := f.Value(msg); v != "" {
			values[key] = v
		}
	}
	return values
}

// setFieldValues writes each non-empty entry of values onto msg, keyed the
// same way as Describe (PIFO tag, or Common name if a field has none);
// entries with no matching field, or an empty value, are ignored. This is
// shared by Generate (to keep its working draft in sync with what's been
// generated so far, for AllFieldValues-based coverage checks) and Apply (to
// build the final message).
func setFieldValues(msg *message.DraftMessage, values map[string]string) {
	// In form order, and twice: setting a field clears any field it makes
	// disallowed, so a field allowed only once an earlier one is filled
	// (e.g. Item 2 after Item 1) could otherwise lose its value.
	for range 2 {
		for f := range msg.Fields() {
			v := values[fieldKey(f)]
			if v == "" || !f.Settable() {
				continue
			}
			if nv := f.FromHuman(msg, v); f.Value(msg) != nv {
				f.SetValue(msg, nv)
			}
		}
	}
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
		// A free-text field such as Comments isn't forced unless the form
		// requires it, or Claude would put content there that belongs in
		// the form's own fields (e.g. a Resource Request's item rows).
		if s.Required || alwaysInclude[s.Common] {
			out = append(out, s)
		}
	}
	return out
}
