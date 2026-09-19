package genmsg

import (
	"testing"

	"github.com/rothskeller/packet/v4/prowords"
)

func TestPlanEveryCategoryPlaced(t *testing.T) {
	profile, err := prowords.Profile(prowords.LevelFull)
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{1, 3, len(profile), len(profile) * 2} {
		plans, unfit := planCategories(profile, count)
		if len(unfit) != 0 {
			t.Errorf("count=%d: unexpected unfit categories: %v", count, unfit)
		}
		if len(plans) != count {
			t.Fatalf("count=%d: got %d plans, want %d", count, len(plans), count)
		}
		seen := map[prowords.Category]bool{}
		for _, p := range plans {
			for _, c := range p.Categories {
				seen[c] = true
			}
		}
		for _, c := range profile {
			if !seen[c] {
				t.Errorf("count=%d: category %q was not placed in any message", count, c)
			}
		}
	}
}

func TestPlanCommonCategoriesInEveryMessage(t *testing.T) {
	profile, _ := prowords.Profile(prowords.LevelFull)
	plans, _ := planCategories(profile, 4)
	for i, p := range plans {
		has := map[prowords.Category]bool{}
		for _, c := range p.Categories {
			has[c] = true
		}
		for common := range alwaysEvery {
			if !has[common] {
				t.Errorf("message %d is missing common category %q", i, common)
			}
		}
	}
}

func TestPlanZeroCount(t *testing.T) {
	profile, _ := prowords.Profile(prowords.LevelF3)
	plans, unfit := planCategories(profile, 0)
	if plans != nil {
		t.Errorf("expected nil plans for count=0, got %v", plans)
	}
	if len(unfit) != len(profile) {
		t.Errorf("expected all %d categories unfit for count=0, got %d", len(profile), len(unfit))
	}
}
