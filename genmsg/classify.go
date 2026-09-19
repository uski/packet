package genmsg

import (
	"slices"
	"strings"

	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
)

// classifyField reports which proword categories a field is naturally
// suited to satisfy on its own, based on its Common name and its
// Label/Help text -- e.g. a "Contact Info (phone number, email, etc.)"
// field can hold an EMAIL ADDRESS or TELEPHONE FIGURES value directly, and
// a "From Name"/"To Name" field can hold a person's name (an I SPELL
// trigger). This lets selectFields route a message's assigned categories
// into fields whose whole purpose is to hold exactly that kind of content,
// instead of only being able to weave them into the free-text body --
// keeping the body, and so the whole message, shorter and more realistic.
// Many such fields (contact info, names) are marked "optional and rarely
// provided" by the forms that have them, so generatableFields alone would never
// select them.
func classifyField(spec fieldSpec) []prowords.Category {
	text := strings.ToLower(spec.Label + " " + spec.Help)
	if spec.Common == "fromName" || spec.Common == "toName" || strings.Contains(text, "name of the person") {
		return []prowords.Category{prowords.ISpell}
	}
	var cats []prowords.Category
	if strings.Contains(text, "email") || strings.Contains(text, "e-mail") {
		cats = append(cats, prowords.EmailAddress)
	}
	if strings.Contains(text, "phone") || strings.Contains(text, "telephone") {
		cats = append(cats, prowords.TelephoneFigures)
	}
	if strings.Contains(text, "gps") || strings.Contains(text, "coordinate") {
		cats = append(cats, prowords.GPSCoordinates)
	}
	if strings.Contains(text, "call sign") || strings.Contains(text, "callsign") {
		cats = append(cats, prowords.AmateurCall)
	}
	if strings.Contains(text, "packet address") || strings.Contains(text, "ax.25") {
		cats = append(cats, prowords.PacketAddress)
	}
	if strings.Contains(text, "website") || strings.Contains(text, "web site") || strings.Contains(text, "internet address") || strings.Contains(text, "url") {
		cats = append(cats, prowords.InternetAddress)
	}
	return cats
}

// selectFields extends generatableFields's base selection (the fields the LLM
// must fill regardless of proword assignment) with whatever additional
// fields, per classifyField, give categories in the message's plan a
// dedicated home -- so a category with such a field never needs to be
// crammed into the free-text body as well. routed maps each category that
// got a dedicated field to that field's Tag, for buildPrompt to point
// Claude at it explicitly instead of (or as well as, for a field that also
// happens to be otherwise required) issuing a generic "weave this into your
// writing" instruction.
func selectFields(all []fieldSpec, categories []prowords.Category) (selected []fieldSpec, routed map[prowords.Category]string) {
	selected = generatableFields(all)
	included := make(map[string]bool, len(selected))
	for _, s := range selected {
		included[s.Tag] = true
	}
	routed = make(map[prowords.Category]string)
	used := make(map[string]bool)
	for _, cat := range categories {
		best, bestScore := -1, -1
		for i, s := range all {
			if skipCommon[s.Common] || !slices.Contains(classifyField(s), cat) {
				continue
			}
			// Spread categories across separate fields before
			// doubling up, and prefer the author's own (From) name and
			// contact fields over the recipient's.
			score := 0
			if included[s.Tag] {
				score += 4 // a field the form requires anyway, rather than adding one
			}
			if !used[s.Tag] {
				score += 2
			}
			if strings.HasPrefix(s.Common, "from") {
				score++
			}
			if score > bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			continue
		}
		s := all[best]
		routed[cat] = s.Tag
		used[s.Tag] = true
		if !included[s.Tag] {
			selected = append(selected, s)
			included[s.Tag] = true
		}
	}
	return selected, routed
}

// promptFields returns, in form order, the fields of msg (a draft with the
// tool's own values already applied) to show Claude: those selectFields
// says it must fill, and every other field it may fill, marked Optional.
// Seeing the whole form lets Claude put each piece of information in the
// field made for it. Fields the tool already filled (party positions and
// locations, dates, times) are left out so Claude can't overwrite them.
func promptFields(msg message.Message, categories []prowords.Category) (specs []fieldSpec, routed map[prowords.Category]string) {
	all := describeFields(msg)
	must, routed := selectFields(all, categories)
	mustTag := make(map[string]bool, len(must))
	for _, s := range must {
		mustTag[s.Tag] = true
	}
	for _, s := range all {
		if skipCommon[s.Common] || s.DateTime {
			continue // dates and times are the tool's to fill
		}
		if !mustTag[s.Tag] {
			if strings.HasPrefix(s.Label, "Operator") {
				continue // relay and other operator bookkeeping, not message content
			}
			if f := FindField(msg, s.Tag); f != nil && f.Value(msg) != "" {
				continue
			}
			s.Optional = true
		}
		specs = append(specs, s)
	}
	for _, g := range failingCheckboxGroups(msg) {
		for i := range specs {
			if slices.Contains(g.keys, specs[i].Tag) {
				specs[i].Optional, specs[i].Group = false, g.label
			}
		}
	}
	return specs, routed
}
