package genmsg

import "github.com/rothskeller/packet/v4/prowords"

// MessagePlan is the assignment of proword categories to a single message
// in a generation batch, plus (on a repair round) any required fields
// Claude's previous response left empty that it must fill in this time.
type MessagePlan struct {
	Categories    []prowords.Category
	Unmet         []prowords.Category // on a repair round, the Categories the previous version didn't satisfy
	MissingFields []FieldSpec
	Words         int  // on a repair round, the message's total words if it went over budget, else 0
	CheckOne      bool // the form has many checkboxes, so the message must check at least one
	CheckOneUnmet bool // on a repair round, the previous version checked none
}

// alwaysEvery lists categories common enough that every generated message
// should include them, matching how real message traffic actually reads
// (nearly every message has a number and a punctuated sentence in it).
var alwaysEvery = map[prowords.Category]bool{
	prowords.ISpell:      true,
	prowords.Figures:     true,
	prowords.Punctuation: true,
}

// Plan distributes the categories in profile across count messages:
// categories in alwaysEvery are assigned to every message; the rest are
// round-robined across the messages so that each appears in at least one
// message. If count is less than the number of "rare" categories, some
// messages simply end up with more than one rare category assigned to them
// -- nothing is dropped. Plan only fails to place every category when count
// is zero, in which case the entire profile is returned as unfit.
func Plan(profile []prowords.Category, count int) (plans []MessagePlan, unfit []prowords.Category) {
	if count <= 0 {
		return nil, append([]prowords.Category{}, profile...)
	}
	plans = make([]MessagePlan, count)
	var rare []prowords.Category
	for _, c := range profile {
		if alwaysEvery[c] {
			for i := range plans {
				plans[i].Categories = append(plans[i].Categories, c)
			}
		} else {
			rare = append(rare, c)
		}
	}
	for i, c := range rare {
		idx := i % count
		plans[idx].Categories = append(plans[idx].Categories, c)
	}
	return plans, nil
}
