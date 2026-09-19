package genmsg

import (
	"iter"

	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/message/field"
)

// fieldSpec describes one editable field of a message type, for building an
// LLM prompt and for later looking up the field to set its value.
type fieldSpec struct {
	Tag       string   // the field's PackItForms tag, or its Common name if it has no tag (see describeFields)
	Common    string   // the well-known common-field name, if any
	Label     string   // the human-readable field label
	Help      string   // help text describing the field's purpose
	Multiline bool     // whether the field expects multi-line text
	Choices   []string // allowed/recommended values, if the field is restricted
	Required  bool     // true if the field currently has no value and fails validation without one
	Problem   string   // on a repair round, why the field's current value fails validation
	Optional  bool     // offered to Claude to fill only if the message has information that belongs in it
	Group     string   // label of the required checkbox group this checkbox belongs to, of which at least one must be checked
	DateTime  bool     // a date or time field, which the tool fills in itself
}

// describeFields returns the settable, addressable fields of msg (which should be
// a freshly created draft with the incident's defaults already applied --
// see incident.Incident.ApplyDefaults), in field order, for use in building
// an LLM prompt. Not every message type is PackItForms-based (e.g. a plain
// text message has no PIFO tags at all), so a field's Common name is used
// as its Tag/key when it has no PIFO tag of its own; see FindField for the
// matching lookup used when applying values back.
func describeFields(msg message.Message) []fieldSpec {
	var specs []fieldSpec
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

func newFieldSpec(msg message.Message, f field.Field, key string) fieldSpec {
	spec := fieldSpec{
		Tag:       key,
		Common:    f.Common(),
		Label:     f.Label(),
		Help:      f.EditHelp(),
		Multiline: f.Multiline(),
		Required:  f.Value(msg) == "" && f.Validate(msg, f, 0) != nil,
		DateTime:  isDateOrTime(f),
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

// FindField returns the field of msg whose key (as computed by describeFields:
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

// allFieldValues returns the value of every field of msg that counts
// toward proword coverage, by the key describeFields uses (its PackItForms tag,
// or its Common name if it has none). Unlike describeFields, which lists what
// Claude might fill in, this measures what the finished message holds, so
// a requirement a pre-filled field already satisfies needn't be woven into
// the body as well. Two kinds of field are left out: the administrative
// ones the incident fills in itself (see contentFields), and the ICS
// position and location names, which a candidate reads aloud but which
// would falsely read as I SPELL names here.
func allFieldValues(msg message.Message) map[string]string {
	values := make(map[string]string)
	for f := range contentFields(msg) {
		// ICS position and location role names like "Shelter Manager"
		// would falsely read as I SPELL names, so they don't count
		// toward proword coverage either.
		if shortNameField[f.Common()] {
			continue
		}
		if v := f.Value(msg); v != "" {
			values[fieldKey(f)] = v
		}
	}
	return values
}

// contentFields iterates the fields of msg that hold message content: all
// of them but the administrative ones the incident fills in itself (message
// numbers, dates, times, operator and envelope data, see skipCommon) and
// any with no key to name them by. Callers add their own rule on top: what
// counts toward proword coverage leaves out the party names as well
// (allFieldValues, ProwordFields), and what counts toward the word budget
// leaves out dates and times (messageWordCount).
func contentFields(msg message.Message) iter.Seq[field.Field] {
	return func(yield func(field.Field) bool) {
		for f := range msg.Fields() {
			if fieldKey(f) == "" || skipCommon[f.Common()] {
				continue
			}
			if !yield(f) {
				return
			}
		}
	}
}

// setFieldValues writes each non-empty entry of values onto msg, keyed the
// same way as describeFields (PIFO tag, or Common name if a field has none);
// entries with no matching field, or an empty value, are ignored. This is
// shared by Generate (to keep its working draft in sync with what's been
// generated so far, for allFieldValues-based coverage checks) and Apply (to
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

// generatableFields filters specs down to the small set the LLM should actually
// be asked to produce a value for: fields already handled by the
// incident's default-filling machinery are excluded outright, and of what
// remains, only fields that are actually required (or the message body, or
// a field in alwaysInclude) are kept. This keeps generated messages short
// and avoids padding out optional, rarely-used fields (contact info,
// reply/take-action toggles, references, and the like) purely because they
// exist -- exactly the fields most forms mark optional because they're
// "rarely provided" in practice.
func generatableFields(specs []fieldSpec) []fieldSpec {
	var out []fieldSpec
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

// shortNameField lists common field names for ICS position and location
// fields, which should be kept short (a role or place name, not a
// sentence) to read naturally over voice and fit real-world form fields.
var shortNameField = map[string]bool{
	"toICSPosition":   true,
	"fromICSPosition": true,
	"toLocation":      true,
	"fromLocation":    true,
}
