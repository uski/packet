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

// Sentence punctuation that ends a group in running text without being part
// of it: the Procedures voice it with its own name ("Sacramento, CA" is
// spoken "Sacramento COMMA <pause> INITIALS charlie alpha"). Everything
// else -- brackets, braces, parentheses, quotes -- belongs to the group and
// makes it a mixed group, as "[220V]" and "$32" do.
const groupTrailTrim = ".,;:!?"

// Brackets and quotes around a group. They belong to the group (see
// groupTrailTrim), but are ignored when looking for a time or date, which
// is spoken as it is written whether or not it is bracketed.
const (
	bracketLeadTrim  = "([{\"'‘“"
	bracketTrailTrim = ")]}\"'’”"
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
	start, end = groupBody(g)
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

// timeOrDateBody returns the part of group g to test for a time or date:
// its body without the brackets or quotes around it.
func timeOrDateBody(g string) (start, end int) {
	start, end = groupBody(g)
	for start < end {
		r, n := utf8.DecodeRuneInString(g[start:end])
		if !strings.ContainsRune(bracketLeadTrim, r) {
			break
		}
		start += n
	}
	for end > start {
		r, n := utf8.DecodeLastRuneInString(g[start:end])
		if !strings.ContainsRune(bracketTrailTrim, r) {
			break
		}
		end -= n
	}
	return start, end
}

// groupBody returns the part of group g without the sentence punctuation
// after it.
func groupBody(g string) (start, end int) {
	start, end = 0, len(g)
	for end > start {
		r, n := utf8.DecodeLastRuneInString(g[start:end])
		if !strings.ContainsRune(groupTrailTrim, r) {
			break
		}
		end -= n
	}
	return start, end
}

// timeOrDateRE matches a time (16:41, 16:41:05, 4:30pm) or a date
// (09/16/2026, 9/16/26, 2026-09-16). These are voiced digit by digit with no
// proword.
var timeOrDateRE = regexp.MustCompile(`(?i)^(?:(?:[01]?\d|2[0-4]):[0-5]\d(?::[0-5]\d)?(?:[ap]m)?|\d{1,2}/\d{1,2}/(?:\d{2}|\d{4})|\d{4}-\d{2}-\d{2})$`)
