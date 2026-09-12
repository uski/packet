package prowords

import "testing"

func TestProfileF3IsSubsetOfFull(t *testing.T) {
	f3, err := Profile(LevelF3)
	if err != nil {
		t.Fatal(err)
	}
	full, err := Profile(LevelFull)
	if err != nil {
		t.Fatal(err)
	}
	full3 := map[Category]bool{}
	for _, c := range full {
		full3[c] = true
	}
	for _, c := range f3 {
		if !full3[c] {
			t.Errorf("category %q is in f3 profile but not full profile", c)
		}
	}
	if len(full) <= len(f3) {
		t.Errorf("full profile (%d categories) should be strictly larger than f3 profile (%d categories)", len(full), len(f3))
	}
}

func TestProfileUnknownLevel(t *testing.T) {
	if _, err := Profile("bogus"); err == nil {
		t.Error("expected error for unknown level, got nil")
	}
}

func TestProfileReturnsFreshSlice(t *testing.T) {
	a, _ := Profile(LevelF3)
	a[0] = "mutated"
	b, _ := Profile(LevelF3)
	if b[0] == "mutated" {
		t.Error("Profile should return a fresh copy each call")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		cat  Category
		text string
		want bool
	}{
		{Figures, "Send 5 dozen jelly donuts", true},
		{Figures, "Send jelly donuts", false},
		{EmailAddress, "Contact harry@aol.com for details", true},
		{EmailAddress, "Contact the front desk", false},
		{TelephoneFigures, "Call 408-555-1212 now", true},
		{TelephoneFigures, "Call the office now", false},
		{AmateurCall, "This is W6XSC reporting", true},
		{AmateurCall, "This is the reporting station", false},
		{Punctuation, "Deliver blankets, cots, and pillows.", true},
		{Punctuation, "Deliver blankets cots and pillows", false},
	}
	for _, c := range cases {
		if got := Validate(c.cat, c.text); got != c.want {
			t.Errorf("Validate(%q, %q) = %v, want %v", c.cat, c.text, got, c.want)
		}
	}
}
