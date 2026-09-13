package prowords

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
}

// wordCategories claim what's left after groups are classified (see
// classifyGroup). SYMBOL(S) and the MIXED GROUP kinds describe a whole
// group, so they're settled before CaseSensitive or FIGURE(S) could take
// part of one, such as the "kW" in "5kW".
var wordCategories = []Category{
	CaseSensitive,
	Initials,
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
	claimPatterns := func(cats []Category) {
		for _, cat := range cats {
			for _, loc := range catalog[cat].re.FindAllStringIndex(text, -1) {
				if overlaps(loc[0], loc[1]) {
					continue
				}
				claimed = append(claimed, span{loc[0], loc[1]})
				counts[cat]++
			}
		}
	}
	claimPatterns(categoryPriority)
	for _, loc := range groupRE.FindAllStringIndex(text, -1) {
		if overlaps(loc[0], loc[1]) {
			continue
		}
		if cat, s, e, ok := classifyGroup(text[loc[0]:loc[1]]); ok {
			claimed = append(claimed, span{loc[0] + s, loc[0] + e})
			counts[cat]++
		}
	}
	claimPatterns(wordCategories)
	return counts
}

// CountFields counts each of the given field values (e.g. a message's
// Subject and Message body) and adds up the results, for analyzing a whole
// message at once. Counting fields separately keeps a pattern from matching
// across two of them.
func CountFields(values map[string]string) map[Category]int {
	counts := map[Category]int{}
	for _, v := range values {
		for cat, n := range Count(v) {
			counts[cat] += n
		}
	}
	return counts
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
