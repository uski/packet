// Package prowords defines the SCCo ARES/RACES message-passing prowords and
// which ones each credential level's evaluation exercises, per the "Santa
// Clara County ARES/RACES Message Handling Procedures" and "Credentialing
// Program Handbook" documents.
//
// It is used to steer and validate LLM-generated training messages for
// credential evaluations: given a credential level, Profile returns the set
// of content categories a message should contain so that voicing it
// correctly requires the candidate to use the corresponding prowords.
package prowords

import (
	"fmt"
	"regexp"
)

// Category identifies a distinct proword, or a written-content pattern that
// triggers use of a proword when the message is voiced aloud.
type Category string

const (
	ISpell               Category = "i-spell"
	Figures              Category = "figures"
	MixedGroup           Category = "mixed-group"
	MixedGroupFigures    Category = "mixed-group-figures"
	MixedGroupSymbols    Category = "mixed-group-symbols"
	Initials             Category = "initials"
	Symbols              Category = "symbols"
	TelephoneFigures     Category = "telephone-figures"
	AmateurCall          Category = "amateur-call"
	EmailAddress         Category = "email-address"
	Punctuation          Category = "punctuation"
	GPSCoordinates       Category = "gps-coordinates"
	PacketAddress        Category = "packet-address"
	InternetAddress      Category = "internet-address"
	CaseSensitive        Category = "case-sensitive"
	SubscriptSuperscript Category = "subscript-superscript"
	Newline              Category = "newline"
)

// Credential levels accepted by Profile.  Per the Credentialing Program
// Handbook, only the Field Communicator Type III (F3) credential is
// evaluated on a reduced proword list; every other credential (F2, F1, and
// every tier of Net Control, Packet Operator, and Shadow Communicator) is
// evaluated on the complete list.
const (
	LevelF3   = "f3"
	LevelFull = "full"
)

// info holds the static metadata for a Category: the proword name it
// corresponds to, the instruction to give an LLM for how to satisfy it in
// generated text, and the regular expression used to check that generated
// text actually satisfies it.
type info struct {
	Proword string
	Prompt  string
	re      *regexp.Regexp
}

var catalog = map[Category]info{
	ISpell: {
		Proword: "I SPELL",
		Prompt:  `Include a hard-to-spell two-word proper name with both words capitalized, such as a person's full name or a street name (e.g. "Diego Marchetti", "Kaczmarek Street"), so the sender must use the I SPELL proword.`,
		re:      regexp.MustCompile(`\b[A-Z][a-z]{2,}\s+[A-Z][a-z]{2,}\b`),
	},
	Figures: {
		Proword: "FIGURE(S)",
		Prompt:  `Include at least one number written with digits only, standing alone (e.g. "100 containers"), so the sender must use the FIGURE(S) proword.`,
		re:      regexp.MustCompile(`\d`),
	},
	MixedGroup: {
		Proword: "MIXED GROUP",
		Prompt:  `Include at least one group that STARTS WITH A LETTER and also holds numbers or symbols (e.g. a truck model like "F150", a call sign with a slash like "W6XRL4/VA", or "abc-123"), so the sender must use the MIXED GROUP proword.`,
	},
	MixedGroupFigures: {
		Proword: "MIXED GROUP FIGURE(S)",
		Prompt:  `Include at least one group that STARTS WITH A DIGIT and also holds letters or symbols (e.g. "2C", a decimal measurement like "2.5", a rating like "5kW", a temperature like "28°F", or "50%"), so the sender must use the MIXED GROUP FIGURE(S) proword.`,
	},
	MixedGroupSymbols: {
		Proword: "MIXED GROUP SYMBOL(S)",
		Prompt:  `Include at least one group that STARTS WITH A SYMBOL and also holds numbers or letters (e.g. "-10 degrees", "$32", "#4", or "-32°F"), so the sender must use the MIXED GROUP SYMBOL(S) proword.`,
	},
	Initials: {
		Proword: "INITIAL(S)",
		Prompt:  `Include at least one abbreviation or acronym written as two to five capital letters with no periods (e.g. "EOC", "ARRL"), so the sender must use the INITIAL(S) proword.`,
		re:      regexp.MustCompile(`\b[A-Z]{2,5}\b`),
	},
	Symbols: {
		Proword: "SYMBOL(S)",
		Prompt:  `Include a symbol standing alone, apart from any word or number (e.g. "Replace all ? with a value", "This != that", "Smith & Jones"), so the sender must use the SYMBOL(S) proword.`,
	},
	TelephoneFigures: {
		Proword: "TELEPHONE FIGURES",
		Prompt:  `Include a properly formatted US phone number with area code, e.g. "408-555-1212", so the sender must use the TELEPHONE FIGURES proword.`,
		re:      regexp.MustCompile(`\b\d{3}[-.]?\d{3}[-.]?\d{4}\b|\(\d{3}\)\s?\d{3}-\d{4}`),
	},
	AmateurCall: {
		Proword: "AMATEUR CALL",
		Prompt:  `Include a fictitious amateur radio call sign that ends with a digit, so it can't be anyone's real call sign (e.g. "W6XRL4", "K6ABC2"), so the sender must use the AMATEUR CALL proword.`,
		// A real call sign, or a fictitious one with a trailing digit.
		re: regexp.MustCompile(`\b[AKNW][A-Z]?\d[A-Z]{1,3}\d?\b`),
	},
	EmailAddress: {
		Proword: "EMAIL ADDRESS",
		Prompt:  `Include a plausible email address, so the sender must use the EMAIL ADDRESS proword. ALWAYS use "@xanadu-city.org" as the domain (e.g. "harry@xanadu-city.org") -- never a real-world domain like gmail.com or aol.com.`,
		re:      regexp.MustCompile(`\S+@\S+\.\S+`),
	},
	Punctuation: {
		Proword: "(punctuation)",
		Prompt:  `Use normal punctuation (comma, period, colon, semicolon, question mark, or exclamation point) in at least one field value -- a description or short phrase is enough, no full sentence is needed -- which the sender must voice using the punctuation-symbol names (e.g. "comma", "period").`,
		re:      regexp.MustCompile(`[,.:;?!]`),
	},
	GPSCoordinates: {
		Proword: "GPS COORDINATES",
		Prompt:  `Include a set of GPS coordinates with degree/minute/second or N/S/E/W markers (e.g. "37.336 N, 121.890 W" or "37 20.16', 121 53.40'"), so the sender must use the GPS COORDINATES proword.`,
		re:      regexp.MustCompile(`\d+(\.\d+)?\s*(°|deg\b)\s*(\d|[NSEW]\b)|\b\d{1,3}\.\d+\s*[NSEW]\b|\b\d{1,3}\s+\d{1,2}\.\d+['’]`),
	},
	PacketAddress: {
		Proword: "PACKET ADDRESS",
		Prompt:  `Include a packet BBS address of the form callsign@bbscall.#region.state.country (e.g. "w6xrl4@w6bbs4.#nca.ca.usa"), using fictitious call signs that end with a digit, so the sender must use the PACKET ADDRESS proword.`,
		re:      regexp.MustCompile(`\S+@\S+\.#\S+`),
	},
	InternetAddress: {
		Proword: "INTERNET ADDRESS",
		Prompt:  `Include a web address, so the sender must use the INTERNET ADDRESS proword. It MUST start with "https://", and ALWAYS use "xanadu-city.org" as the domain (e.g. "https://www.xanadu-city.org" or "https://xanadu-city.org/shelters") -- never a real-world domain.`,
		re:      regexp.MustCompile(`https?://\S+|\bwww\.\S+\.\S+|\b[a-zA-Z0-9-]+\.(org|com|net|gov)/\S+`),
	},
	CaseSensitive: {
		Proword: "UPPERCASE/LOWERCASE",
		Prompt:  `Include a case-sensitive value where capitalization matters, such as a password or a mixed-case portmanteau word (e.g. "PackItForms", "pasSWOrd"), so the sender must use the UPPERCASE and LOWERCASE prowords.`,
		re:      regexp.MustCompile(`[a-z][A-Z]|[A-Z][a-z]+[A-Z]`),
	},
	SubscriptSuperscript: {
		Proword: "SUBSCRIPT/SUPERSCRIPT",
		Prompt:  `Include a chemical formula or numeric expression with a subscript or superscript, written using Unicode subscript/superscript characters (e.g. "H₂O", "10⁵"), so the sender must use the SUBSCRIPT and SUPERSCRIPT prowords.`,
		re:      regexp.MustCompile(`[\x{2070}-\x{209F}\x{00B2}\x{00B3}\x{00B9}]`),
	},
	Newline: {
		Proword: "NEWLINE",
		Prompt:  `Write the message body as two or more short paragraphs separated by a blank line, so the sender must use the NEWLINE proword between them.`,
		re:      regexp.MustCompile(`\n[ \t]*\n`),
	},
}

// f3Profile is the reduced proword list evaluated for the Field
// Communicator Type III (F3) credential.  See the Credentialing Program
// Handbook, "Field Communicator Type III (F3)", Operator Skills: "F3s must
// be able to send and receive the following message contents: ... This
// reduced list is only applicable to the F3 Credential, all other
// credentials require use of the complete list of Prowords."
var f3Profile = []Category{
	ISpell, Figures, MixedGroup, MixedGroupFigures, MixedGroupSymbols,
	Initials, Symbols, TelephoneFigures, AmateurCall, EmailAddress,
	Punctuation,
}

// fullProfile is the complete proword list from the Message Handling
// Procedures document, Appendix B, minus the purely procedural
// control/clarification prowords (ROGER, BREAK, STAND BY, CONTINUE/GO, SAY
// AGAIN, MESSAGE ENDS, I SAY AGAIN) that arise during the live exchange
// rather than being embeddable in written message content.
var fullProfile = append(append([]Category{}, f3Profile...),
	GPSCoordinates, PacketAddress, InternetAddress, CaseSensitive,
	SubscriptSuperscript, Newline,
)

// Profile returns the ordered list of proword categories that should be
// exercised by a training message for the given credential level ("f3" or
// "full"). The returned slice is a fresh copy safe for the caller to
// mutate.
func Profile(level string) ([]Category, error) {
	switch level {
	case LevelF3:
		return append([]Category{}, f3Profile...), nil
	case LevelFull:
		return append([]Category{}, fullProfile...), nil
	default:
		return nil, fmt.Errorf("unknown proword level %q (want %q or %q)", level, LevelF3, LevelFull)
	}
}

// Prompt returns the natural-language instruction to give an LLM describing
// how to satisfy the given category in generated text.
func Prompt(cat Category) string { return catalog[cat].Prompt }

// ProwordName returns the human-readable proword (or prowords) that the
// given category exercises, for display to the evaluator.
func ProwordName(cat Category) string { return catalog[cat].Proword }

// Validate reports whether text appears to satisfy the given category. It
// is a best-effort heuristic check (used to decide whether to retry
// generation), not a substitute for the evaluator's own judgment.
func Validate(cat Category, text string) bool {
	return Count(text)[cat] > 0
}
