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
		MixedGroup:           {"abc-123", "W6XRL4/VA"},
		MixedGroupFigures:    {"146.595", "14,135", "2C"},
		MixedGroupSymbols:    {"-10 degrees", "$32"},
		Initials:             {"EOC", "ARRL"},
		Symbols:              {"gate #4", "50%"},
		TelephoneFigures:     {"408-555-1212"},
		AmateurCall:          {"W6XSC", "KJ6ABC"},
		EmailAddress:         {"harry@xanadu-city.org"},
		GPSCoordinates:       {"37.336 N, 121.890 W", "37 20.16', 121 53.40'"},
		PacketAddress:        {"w6xrl4@w4xsc.#nca.ca.usa"},
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
