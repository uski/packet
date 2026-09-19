package genmsg

import "github.com/rothskeller/packet/v4/prowords"

// MessagePlan is the assignment of proword categories to a single message
// in a generation batch, plus (on a repair round) any required fields
// Claude's previous response left empty that it must fill in this time.
type MessagePlan struct {
	Categories    []prowords.Category
	Unmet         []prowords.Category // on a repair round, the Categories the previous version didn't satisfy
	MissingFields []FieldSpec
	Words         int         // on a repair round, the message's total words if it went over budget, else 0
	CheckOne      bool        // the form has many checkboxes, so the message must check at least one
	CheckOneUnmet bool        // on a repair round, the previous version checked none
	LongFields    []FieldSpec // on a repair round, subject-like fields the previous version made too long
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

// planByParty groups req.Messages by sender and effective proword level (a
// message's own Level if set, else req.Level) and runs Plan independently
// within each group, so that messages from parties evaluated at different
// credential levels each only draw proword requirements from their own
// level's profile -- an F3 party's messages never get saddled with a
// full-list-only category, and a full-list party's messages aren't limited
// to the reduced list just because they share a batch with an F3 party.
//
// Grouping by sender matters because each candidate is evaluated on what
// that candidate transmits: the Credentialing Program Handbook requires
// each credential's whole proword list, so a party's own messages have to
// cover it rather than the batch covering it between them. The returned
// slice is in req.Messages order.
func planByParty(req Request) ([]MessagePlan, error) {
	type party struct{ level, from, prefix string }
	count := len(req.Messages)
	groups := map[party][]int{}
	for i, m := range req.Messages {
		if isContentless(m) {
			continue // no content to hold proword requirements
		}
		lvl := m.Level
		if lvl == "" {
			lvl = req.Level
		}
		p := party{lvl, m.From, m.FromPrefix}
		groups[p] = append(groups[p], i)
	}
	plans := make([]MessagePlan, count)
	for p, idxs := range groups {
		profile, err := prowords.Profile(p.level)
		if err != nil {
			return nil, err
		}
		groupPlans, _ := Plan(profile, len(idxs))
		for j, idx := range idxs {
			plans[idx] = groupPlans[j]
		}
	}
	return plans, nil
}
