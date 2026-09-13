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
