package prowords

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// groupRE matches a group: a run of non-space characters, the unit the
// Message Handling Procedures classify as a word, FIGURE(S), SYMBOL(S), or
// one of the MIXED GROUP kinds.
var groupRE = regexp.MustCompile(`\S+`)

// Characters that bracket or end a group in running text without being part
// of it (e.g. the comma in "5kW, 12V").
const (
	groupLeadTrim  = "([{\"'‘“"
	groupTrailTrim = ".,;:!?)]}\"'’”"
)

// classifyGroup classifies group g the way the Message Handling Procedures
// do. A group of only symbols is SYMBOL(S). Otherwise a group holding at
// least two of letters, numbers, and symbols is MIXED GROUP FIGURE(S) if it
// starts with a number, MIXED GROUP SYMBOL(S) if it starts with a symbol,
// and MIXED GROUP if it starts with a letter. start and end give the part of
// g the category covers; ok is false for a plain word or number.
func classifyGroup(g string) (cat Category, start, end int, ok bool) {
	if !strings.ContainsFunc(g, isAlnum) {
		return Symbols, 0, len(g), true
	}
	start, end = 0, len(g)
	for start < end {
		r, n := utf8.DecodeRuneInString(g[start:end])
		if !strings.ContainsRune(groupLeadTrim, r) {
			break
		}
		start += n
	}
	for end > start {
		r, n := utf8.DecodeLastRuneInString(g[start:end])
		if !strings.ContainsRune(groupTrailTrim, r) {
			break
		}
		end -= n
	}
	body := g[start:end]
	var letters, digits, symbols, wordSymbols bool
	for _, r := range body {
		switch {
		case unicode.IsLetter(r):
			letters = true
		case unicode.IsDigit(r):
			digits = true
		case r == '-' || r == '\'' || r == '’':
			wordSymbols = true
		default:
			symbols = true
		}
	}
	first, _ := utf8.DecodeRuneInString(body)
	switch {
	case unicode.IsDigit(first):
		return MixedGroupFigures, start, end, letters || symbols || wordSymbols
	case unicode.IsLetter(first):
		// Hyphenated words and contractions ("well-being", "they're") are
		// words, not mixed groups.
		return MixedGroup, start, end, digits || symbols
	default:
		return MixedGroupSymbols, start, end, letters || digits
	}
}

func isAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
