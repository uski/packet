package prowords

import "strings"

// This file implements the "proword engine": given arbitrary message text,
// it reports which prowords a sending station would need to use to voice
// it correctly, and how many times each would be used. It is a best-effort
// heuristic built on the same regular expressions used by Validate, not a
// substitute for an evaluator's own judgment or a real message-passing
// parser -- it exists to give an evaluator (or the credential-evaluation
// tooling) a quick, at-a-glance sense of proword coverage in a message,
// generated or hand-written.

// categoryPriority lists categories in the order they claim matching text
// when two categories' patterns overlap the same span, most specific and
// structured first. This keeps e.g. a phone number's digits from being
// counted once as TELEPHONE FIGURES and then a second time as bare
// FIGURE(S): TELEPHONE FIGURES claims that span first, so FIGURE(S) never
// sees it.
var categoryPriority = []Category{
	GPSCoordinates,
	PacketAddress,
	EmailAddress,
	InternetAddress,
	TelephoneFigures,
	AmateurCall,
	SubscriptSuperscript,
	CaseSensitive,
	MixedGroupSymbols,
	MixedGroupFigures,
	MixedGroup,
	Initials,
	Symbols,
	Figures,
	Punctuation,
	ISpell,
	Newline,
}

// Count analyzes text and returns, for each proword category found, how
// many times it appears. Categories with zero occurrences are omitted from
// the result.
func Count(text string) map[Category]int {
	type span struct{ start, end int }
	var claimed []span
	overlaps := func(s, e int) bool {
		for _, c := range claimed {
			if s < c.end && e > c.start {
				return true
			}
		}
		return false
	}
	counts := map[Category]int{}
	for _, cat := range categoryPriority {
		re := catalog[cat].re
		if re == nil {
			continue
		}
		for _, loc := range re.FindAllStringIndex(text, -1) {
			if overlaps(loc[0], loc[1]) {
				continue
			}
			claimed = append(claimed, span{loc[0], loc[1]})
			counts[cat]++
		}
	}
	return counts
}

// CountFields is a convenience wrapper that concatenates the given field
// values (e.g. a message's Subject and Message body) before counting, for
// analyzing a whole message at once.
func CountFields(values map[string]string) map[Category]int {
	var b strings.Builder
	for _, v := range values {
		b.WriteString(v)
		b.WriteString("\n")
	}
	return Count(b.String())
}

// Total returns the sum of all category counts, i.e. the total number of
// proword usages found.
func Total(counts map[Category]int) int {
	var n int
	for _, c := range counts {
		n += c
	}
	return n
}
