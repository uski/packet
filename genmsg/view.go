package genmsg

import (
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// FieldProwords is one filled-in field of a message, with the proword usages
// found in its value.
type FieldProwords struct {
	Label   string
	Value   string
	Matches []prowords.Match // nil for a field not counted toward proword coverage
}

// ProwordFields returns the filled-in fields of msg in form order, each with
// the proword usages in its value, found and counted the same way as a
// generated message's proword table (see AllFieldValues). Administrative
// fields are left out, and ICS position and location names are shown
// without matches, as is every field of a check-in or check-out message
// (see IsContentlessType).
func ProwordFields(msg message.Message) []FieldProwords {
	var out []FieldProwords
	contentless := IsContentlessType(msg.Type())
	for f := range msg.Fields() {
		if fieldKey(f) == "" || skipCommon[f.Common()] {
			continue
		}
		v := f.Value(msg)
		if v == "" {
			continue
		}
		fp := FieldProwords{Label: f.Label(), Value: v}
		if !shortNameField[f.Common()] && !contentless {
			fp.Matches = prowords.Find(v)
		}
		out = append(out, fp)
	}
	return out
}
