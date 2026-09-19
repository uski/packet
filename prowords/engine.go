package prowords

import (
	"cmp"
	"slices"
	"strings"
)

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

// Match is one proword usage found in text: the byte span of text it covers
// and the proword category it calls for.
type Match struct {
	Start, End int
	Category   Category
}

// Find analyzes text and returns every proword usage in it, in text order.
// No two matches overlap.
func Find(text string) []Match {
	var matches []Match
	var plain []Match // text calling for no proword, such as times and dates
	overlaps := func(s, e int) bool {
		for _, m := range slices.Concat(matches, plain) {
			if s < m.End && e > m.Start {
				return true
			}
		}
		return false
	}
	claimPatterns := func(cats []Category) {
		for _, cat := range cats {
			for _, loc := range catalog[cat].re.FindAllStringIndex(text, -1) {
				// A call sign with "/..." is a MIXED GROUP, per the Procedures.
				if cat == AmateurCall && strings.HasPrefix(text[loc[1]:], "/") {
					continue
				}
				// An initial's period is punctuation of its own ("Fire
				// Capt." is spoken "INITIALS charlie alpha papa
				// tango period"), so it is left out of the match.
				if cat == Initials && text[loc[1]-1] == '.' {
					loc[1]--
				}
				if !overlaps(loc[0], loc[1]) {
					matches = append(matches, Match{loc[0], loc[1], cat})
				}
			}
		}
	}
	claimPatterns(categoryPriority)
	matches = splitCasedPaths(text, matches)
	for _, loc := range groupRE.FindAllStringIndex(text, -1) {
		if overlaps(loc[0], loc[1]) {
			continue
		}
		g := text[loc[0]:loc[1]]
		if s, e := timeOrDateBody(g); timeOrDateRE.MatchString(g[s:e]) {
			plain = append(plain, Match{Start: loc[0] + s, End: loc[0] + e})
			continue
		}
		if cat, s, e, ok := classifyGroup(g); ok {
			matches = append(matches, Match{loc[0] + s, loc[0] + e, cat})
		}
	}
	claimPatterns(wordCategories)
	slices.SortFunc(matches, func(a, b Match) int { return cmp.Compare(a.Start, b.Start) })
	return matches
}

// splitCasedPaths splits an internet address whose path (what follows the
// domain name) has a capital letter into two matches: the address through
// its domain, which calls for INTERNET ADDRESS, and the path, which the
// sender must spell out with the UPPERCASE and LOWERCASE prowords, since a
// path's capitalization matters (unlike a domain name's).
func splitCasedPaths(text string, matches []Match) []Match {
	var out []Match
	for _, m := range matches {
		if m.Category == InternetAddress {
			if slash := pathStart(text[m.Start:m.End]); slash > 0 && strings.ContainsAny(text[m.Start+slash:m.End], upperLetters) {
				out = append(out, Match{m.Start, m.Start + slash, InternetAddress})
				m = Match{m.Start + slash, m.End, CaseSensitive}
			}
		}
		out = append(out, m)
	}
	return out
}

const upperLetters = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

// pathStart returns the index in internet address addr where its path
// begins (the "/" after its domain name), or -1 if it has none.
func pathStart(addr string) int {
	rest := addr
	var offset int
	if i := strings.Index(addr, "://"); i >= 0 {
		offset = i + 3
		rest = addr[offset:]
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return offset + i
	}
	return -1
}

// Count analyzes text and returns, for each proword category found, how
// many times it appears. Categories with zero occurrences are omitted from
// the result.
func Count(text string) map[Category]int {
	counts := map[Category]int{}
	for _, m := range Find(text) {
		counts[m.Category]++
	}
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
