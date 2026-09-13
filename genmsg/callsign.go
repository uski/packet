package genmsg

import (
	"regexp"
	"strings"
)

// realCallSignRE matches a group in the format of a real FCC amateur call sign
// (the same pattern forms validate call sign fields with), in any case, as a
// whole alphanumeric group so that e.g. an email address's user part matches
// but the "W6XRL" in a fictitious "W6XRL4" doesn't.
var realCallSignRE = regexp.MustCompile(`(?i)\b(?:A[A-L][0-9][A-Z]{1,3}|[KNW][0-9][A-Z]{2,3}|[KNW][A-Z][0-9][A-Z]{1,3})\b`)

// fictitiousCallSignRE matches a whole value that is a fictitious call sign:
// a real call sign's format with a trailing digit, like "W6XRL4".
var fictitiousCallSignRE = regexp.MustCompile(`(?i)^(?:A[A-L][0-9][A-Z]{1,3}|[KNW][0-9][A-Z]{2,3}|[KNW][A-Z][0-9][A-Z]{1,3})[0-9]$`)

// fictionalizeCallSigns returns values with every group in a real call sign's
// format given a trailing "4" (e.g. "KJ6ABC" becomes "KJ6ABC4"), so no
// generated message names a station that could actually exist. Real call
// signs never end with a digit, and the same call sign always becomes the
// same fictitious one, so messages stay consistent with each other.
func fictionalizeCallSigns(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for k, v := range values {
		out[k] = realCallSignRE.ReplaceAllString(v, "${0}4")
	}
	return out
}

// isFictitiousCallSignError reports whether err is only a form's complaint that
// a call sign field doesn't hold a real FCC call sign, when it holds a
// fictitious one on purpose.
func isFictitiousCallSignError(value string, err error) bool {
	return strings.Contains(err.Error(), "FCC call sign") && fictitiousCallSignRE.MatchString(strings.TrimSpace(value))
}
