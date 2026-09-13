package prowords

import "testing"

func TestCountBasic(t *testing.T) {
	text := "Please call 408-555-1212 or email harry@aol.com about 5 units at the shelter."
	counts := Count(text)
	if counts[TelephoneFigures] != 1 {
		t.Errorf("TelephoneFigures = %d, want 1", counts[TelephoneFigures])
	}
	if counts[EmailAddress] != 1 {
		t.Errorf("EmailAddress = %d, want 1", counts[EmailAddress])
	}
	// The phone number's digits must not also be separately counted as
	// bare FIGURE(S).
	if counts[Figures] != 1 {
		t.Errorf("Figures = %d, want 1 (just the standalone '5'), got counts=%v", counts[Figures], counts)
	}
}

func TestCountNoDoubleCountingMixedGroupSymbol(t *testing.T) {
	counts := Count("The temperature is -10 degrees outside.")
	if counts[MixedGroupSymbols] != 1 {
		t.Errorf("MixedGroupSymbols = %d, want 1", counts[MixedGroupSymbols])
	}
	if counts[Figures] != 0 {
		t.Errorf("Figures = %d, want 0 (the -10 should be fully claimed by MixedGroupSymbols), got counts=%v", counts[Figures], counts)
	}
}

func TestCountPunctuation(t *testing.T) {
	counts := Count("Deliver blankets, cots, and pillows.")
	if counts[Punctuation] != 3 {
		t.Errorf("Punctuation = %d, want 3 (two commas, one period)", counts[Punctuation])
	}
}

func TestCountEmpty(t *testing.T) {
	counts := Count("")
	if len(counts) != 0 {
		t.Errorf("expected no categories for empty text, got %v", counts)
	}
}

// TestCountMixedGroupsInRealisticText covers mixed groups the way Claude
// actually writes them, each of which used to go uncounted (or be counted
// as something else) and send a message back for revision.
func TestCountMixedGroupsInRealisticText(t *testing.T) {
	cases := []struct {
		text string
		want Category
	}{
		{"2kW generator", MixedGroupFigures},
		{"Unit 12-B", MixedGroupFigures},
		{"Bay 4/C", MixedGroupFigures},
		{"146.52MHz", MixedGroupFigures},
		{"temps near 28°F", MixedGroupFigures},
		{"low of -5°F", MixedGroupSymbols},
		{"+5 volunteers", MixedGroupSymbols},
		{"cost $1,500", MixedGroupSymbols},
		{"gate #4", MixedGroupSymbols},
		{"shelter 50% full", MixedGroupFigures},
		{"units 3-4 ready", MixedGroupFigures},
		{"Replace all ? with a value", Symbols},
	}
	for _, text := range []string{"well-being", "they're closed", "Call me at noon, please."} {
		counts := Count(text)
		if counts[MixedGroup]+counts[MixedGroupFigures]+counts[MixedGroupSymbols]+counts[Symbols] != 0 {
			t.Errorf("Count(%q) = %v: ordinary words and sentence punctuation are not groups", text, counts)
		}
	}
	for _, c := range cases {
		counts := Count(c.text)
		if counts[c.want] != 1 {
			t.Errorf("Count(%q) = %v, want one %s", c.text, counts, c.want)
		}
		if counts[GPSCoordinates] != 0 || counts[CaseSensitive] != 0 {
			t.Errorf("Count(%q) = %v, should not see GPS coordinates or case-sensitive text", c.text, counts)
		}
	}
	if Count("37°20' N")[GPSCoordinates] != 1 {
		t.Error("degree-minute coordinates should still be GPS COORDINATES")
	}
	if Count("PackItForms")[CaseSensitive] == 0 {
		t.Error("a mixed-case word should still be UPPERCASE/LOWERCASE")
	}
}

func TestFindReturnsSpansInOrder(t *testing.T) {
	text := "Gen 5kW, call 408-555-1212 now."
	matches := Find(text)
	want := []struct {
		text string
		cat  Category
	}{
		{"5kW", MixedGroupFigures},
		{",", Punctuation},
		{"408-555-1212", TelephoneFigures},
		{".", Punctuation},
	}
	if len(matches) != len(want) {
		t.Fatalf("Find(%q) = %+v, want %d matches", text, matches, len(want))
	}
	for i, m := range matches {
		if got := text[m.Start:m.End]; got != want[i].text || m.Category != want[i].cat {
			t.Errorf("match %d = %q (%s), want %q (%s)", i, got, m.Category, want[i].text, want[i].cat)
		}
	}
}

func TestCountFieldsNoMatchAcrossFields(t *testing.T) {
	// Each word alone is not an I SPELL name; only joined across the two
	// fields would they look like one.
	counts := CountFields(map[string]string{"a": "Kaczmarek", "b": "Street"})
	if counts[ISpell] != 0 {
		t.Errorf("ISpell = %d, want 0 (must not match across field boundaries)", counts[ISpell])
	}
}

func TestCountFieldsAndTotal(t *testing.T) {
	values := map[string]string{
		"subject": "Road closure on 5th Street",
		"body":    "Please detour via Main St. Call 408-555-1212 for details.",
	}
	counts := CountFields(values)
	if Total(counts) == 0 {
		t.Error("expected a non-zero total across both fields")
	}
	if counts[TelephoneFigures] != 1 {
		t.Errorf("TelephoneFigures = %d, want 1", counts[TelephoneFigures])
	}
}
