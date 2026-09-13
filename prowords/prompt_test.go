package prowords

import (
	"strings"
	"testing"
)

// TestPromptExamplesAreDetected checks that each example a prompt gives
// Claude is recognized by that category's detector. When they disagree,
// Claude follows the prompt, the detector misses it, and the message is
// needlessly sent back for revision.
func TestPromptExamplesAreDetected(t *testing.T) {
	examples := map[Category][]string{
		ISpell:               {"Diego Marchetti", "Kaczmarek Street"},
		Figures:              {"100 containers"},
		MixedGroup:           {"F150", "W6XRL4/VA", "abc-123"},
		MixedGroupFigures:    {"2C", "146.595", "5kW", "28°F", "50%"},
		MixedGroupSymbols:    {"-10 degrees", "$32", "#4", "-32°F"},
		Initials:             {"EOC", "ARRL"},
		Symbols:              {"Replace all ? with a value", "This != that", "Smith & Jones"},
		TelephoneFigures:     {"408-555-1212"},
		AmateurCall:          {"W6XRL4", "K6ABC2"},
		EmailAddress:         {"harry@xanadu-city.org"},
		GPSCoordinates:       {"37.336 N, 121.890 W", "37 20.16', 121 53.40'"},
		PacketAddress:        {"w6xrl4@w6bbs4.#nca.ca.usa"},
		InternetAddress:      {"https://www.xanadu-city.org", "xanadu-city.org/shelters"},
		CaseSensitive:        {"PackItForms", "pasSWOrd"},
		SubscriptSuperscript: {"H₂O", "10⁵"},
	}
	for cat, exs := range examples {
		for _, ex := range exs {
			if !strings.Contains(Prompt(cat), `"`+ex+`"`) {
				t.Errorf("%s prompt no longer gives the example %q; update this test", cat, ex)
			}
			if Count(ex)[cat] == 0 {
				t.Errorf("%s prompt example %q is not detected as %s (counts: %v)", cat, ex, cat, Count(ex))
			}
		}
	}
	if Count("First paragraph.\n\nSecond paragraph.")[Newline] == 0 {
		t.Error("two paragraphs separated by a blank line should be detected as NEWLINE")
	}
}
