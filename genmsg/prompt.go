package genmsg

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/rothskeller/packet/v4/prowords"
)

// This file holds the text sent to Claude: the system prompts, the rules
// every message must follow, the exercise brief that a batch's messages are
// written from, and the per-message prompt.

const briefSystemPrompt = `You are helping a Santa Clara County ARES/RACES credential evaluator plan a short, realistic emergency-communications training exercise whose messages will be written one at a time from your plan. The exercise is set in the fictitious Xanadu City, Xanadu County. Respond with plain text only.`

const systemPrompt = `You are helping a Santa Clara County ARES/RACES credential evaluator generate realistic, short, third-party emergency-communications training messages. The exercises are set in the fictitious Xanadu City, Xanadu County, never in a real place. A candidate being evaluated will read these messages aloud over amateur radio using proper message-passing procedure, so the written content must naturally require specific prowords when voiced correctly (for example, a message won't require the EMAIL ADDRESS proword unless it actually contains an email address). Some batches are a single coherent multi-party exchange (e.g. one message requesting a status update, answered by several reply messages) rather than independent messages -- when a message says it replies to another, its content must directly and consistently respond to that message, as a real reply would. Respond with ONLY a single JSON array (no markdown code fences, no commentary before or after) containing one object per requested message, each object mapping the given field tags to string values.`

// incidentDate returns the incident date req's messages use, if any.
func incidentDate(req Request) string {
	var date string
	for _, m := range req.Messages {
		switch {
		case m.Date == "":
		case date == "":
			date = m.Date
		case m.Date != date:
			// The messages of one request come from one scenario
			// and share its date; if they somehow disagree, say
			// nothing rather than tell Claude the wrong one.
			return ""
		}
	}
	return date
}

// buildBriefPrompt asks Claude to plan the exercise every message in req
// will be written from: the incident, the facts they must agree on, and
// what each message says.
func buildBriefPrompt(req Request) string {
	var b strings.Builder
	if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario: %s\n\n", req.Scenario)
	} else {
		b.WriteString("No scenario was given: invent a plausible emergency-response scenario set in the fictitious Xanadu City (e.g. a downed power line, a fallen tree blocking a road, traffic congestion near a shelter, storm damage, a utility outage).\n\n")
	}
	if date := incidentDate(req); date != "" {
		fmt.Fprintf(&b, "The incident takes place on %s.\n\n", date)
	}
	fmt.Fprintf(&b, "The exercise has %d messages. Each will be written separately later, by someone who sees only your brief and that one message's details:\n", len(req.Messages))
	for i, m := range req.Messages {
		fmt.Fprintf(&b, "Message %d: %s", i+1, m.MsgType.Name())
		if isContentless(m) {
			fmt.Fprintf(&b, " (sent as is, with no content to write)")
		}
		if m.From != "" {
			fmt.Fprintf(&b, ", from %s", partyLabel(m.From, m.FromLocation))
		}
		if m.To != "" {
			fmt.Fprintf(&b, ", to %s", partyLabel(m.To, m.ToLocation))
		}
		if m.Time != "" {
			fmt.Fprintf(&b, ", written at %s", m.Time)
		}
		if m.Purpose != "" {
			fmt.Fprintf(&b, ", purpose: %s", m.Purpose)
		}
		if m.ReplyTo > 0 {
			fmt.Fprintf(&b, ", replying to message %d", m.ReplyTo)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nWrite a concise exercise brief of at most 250 words, in plain text:\n" +
		"1. The incident: what happened, where, and when.\n" +
		"2. Shared facts every message must agree on: names of people, places and addresses, quantities, times, amateur call signs, and the specific details that make requests realistic (e.g. a generator's make, model number, and power rating). Use only the xanadu-city.org domain for any email or web address, and only fictitious call signs ending with a digit, like W6XRL4, never a real call sign.\n" +
		"   " + fictionalPlacesPrompt + "\n" +
		"   " + contentRulesPrompt + "\n" +
		"3. One line per message saying specifically what it reports, requests, or answers, so each reply answers what was actually asked (nothing for a message sent as is).\n" +
		"Do not write the messages themselves. Each message will be at most about 50 words.\n")
	return b.String()
}

// fictionalPlacesPrompt keeps exercise traffic from naming real places, so
// it can't be mistaken for a report about one.
const fictionalPlacesPrompt = `Every place must be fictitious, so exercise traffic can't be confused with a real incident: invent street names, cross streets, building names, and street numbers freely, but the city is ALWAYS "Xanadu City" and the county, whenever one is mentioned, is ALWAYS "Xanadu County" -- even when the scenario mentions a real place. Never name a real city, county, street address, highway, or facility (such as a real school, hospital, or business location).`

// contentRulesPrompt gives rules for what exercise traffic may contain.
const contentRulesPrompt = `Never mention an amateur radio frequency (no frequencies, repeaters, or channels in MHz or kHz). Every web address (URL) MUST start with "https://".`

// realismPrompt asks for the specific details real requests and reports
// carry. Such details (model numbers, ratings) also tend to call for mixed
// group and figures prowords naturally.
const realismPrompt = `Make every message realistic, with the specific details a real served agency would give so the recipient can act on it: when asking for or reporting equipment or supplies, identify them precisely -- e.g. a generator with its make, model number, or power rating ("Honda EU7000is, 7 kW"), cots or blankets with a quantity and type, a pump with its capacity, a vehicle with its type and unit number -- and give realistic-looking places (street addresses, cross streets, building and room names, following the place rules below), quantities with units, and names of responsible people. Keep these details plausible, and within the word budget.`

// sharedPrompt returns the part of the prompt that is the same for every
// message of a batch -- the scenario, the rules, and how to meet each proword
// requirement -- which is sent as a cached prefix, so each message's call
// only pays for, and waits on, its own details (messagePrompt).
func sharedPrompt(req Request, brief string) string {
	var b strings.Builder
	if brief != "" {
		fmt.Fprintf(&b, "This message belongs to a training exercise of %d messages, each written separately from the exercise brief below. Keep every name, place, number, and fact consistent with the brief, and write what the brief says this message is about.\n\nEXERCISE BRIEF:\n%s\n\n", len(req.Messages), brief)
	} else if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario to base all of the messages on: %s\n\n", req.Scenario)
	} else {
		b.WriteString("No specific scenario was given. Invent a plausible emergency-response scenario set in the fictitious Xanadu City (e.g. a downed power line, a fallen tree blocking a road, traffic congestion near a shelter, storm damage assessment, a utility outage) and use it consistently across all the messages in this batch.\n\n")
	}
	if date := incidentDate(req); date != "" {
		fmt.Fprintf(&b, "The incident takes place on %s; any date a message mentions must be consistent with that. Date and time fields are filled in separately, so they aren't listed.\n\n", date)
	}
	b.WriteString("Messages may be different form types with different fields (a training session can mix, for example, an ICS-213, a plain text message, and a Road Closure form). Weave each message's listed requirements naturally into that message's own field values -- they must fit the scenario and read like real, professional emergency radio traffic, not like a checklist. Proword content in ANY field counts, so each requirement only needs to be met ONCE, in the single field that suits it best (a person's name in a name field, an email address or phone number in a contact field) -- never repeat it in another field, and never add a sentence to the free-text body just to carry it (e.g. not \"Contact Jane Doe at jane@xanadu-city.org for logistics.\" when there are name and contact fields to hold them). Requirements already satisfied by pre-filled fields have been left out. Fill each form the way a trained operator fills out the real form: put every piece of information in the field made for it -- for example each requested item in its own item row (Item 1's name and quantity, then Item 2's), a person in a name field, a phone number in a phone field -- and use a free-text field such as Comments or Special Instructions only for information no other field holds, never to restate other fields (e.g. not \"Need 50 blankets, generator\" in Comments when the form has item fields). A subject, title, or summary field is only a short headline of a few words: the message's details go in its message body or the form's other fields, never in the subject.\n")
	b.WriteString(realismPrompt + "\n\n" + fictionalPlacesPrompt + "\n\n" + contentRulesPrompt + "\n\n")
	b.WriteString("How to meet each proword requirement a message lists:\n")
	full, _ := prowords.Profile(prowords.LevelFull)
	for _, cat := range full {
		fmt.Fprintf(&b, "  - %s: %s\n", prowords.ProwordName(cat), prowords.Prompt(cat))
	}
	fmt.Fprintf(&b, "\nEvery message must also include the exact phrase %q somewhere in its content, to clearly mark it as training/exercise traffic rather than a real report.\n\n", DrillTrafficPhrase)
	b.WriteString("Respond with ONLY a JSON array holding one object per requested message, in the order they are listed, each mapping THAT message's own field tags to their string values. Keep every message SHORT: real emergency radio traffic is deliberately terse, and each message's word budget covers ALL of its fields together, so a free-text field should be one or two short sentences at most -- include only what's needed to satisfy the listed requirements. Only use the field tags listed for each message: give every MUST field a non-empty value, fill an optional field only when the message's information belongs there, and never add other keys. For any field marked as a dropdown, its value must be one of the listed choices, verbatim -- do not invent your own wording for it. Everywhere else, use normal sentence capitalization: capitalize only the first word of a sentence or phrase, proper names, and acronyms, and never Title Case ordinary words (write \"Generator runtime is 8 hours\", not \"Generator Runtime is 8 hours\"), because capitalized ordinary words read as names that call for I SPELL. Every amateur radio call sign, anywhere in a message (including inside email and packet addresses), must be fictitious so it can't belong to a real station: write it in the format of a real call sign followed by one extra digit, like \"W6XRL4\" or \"K6ABC2\", never a real-format call sign like \"KJ6ABC\". Before answering, check your values against every requirement and the word budget.\n")
	return b.String()
}

// messagePrompt returns the part of the prompt describing the pending
// messages themselves (see buildPrompt), which follows sharedPrompt.
func messagePrompt(req Request, specsPerMsg [][]FieldSpec, results []Result, pending []int, plans []MessagePlan, routedPerMsg []map[prowords.Category]string, baseWords []int, isRepair bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nGenerate exactly %d message(s), described below in order.\n\n", len(pending))
	pendingSet := make(map[int]bool, len(pending))
	for _, idx := range pending {
		pendingSet[idx] = true
	}
	for pos, idx := range pending {
		m := req.Messages[idx]
		label := idx + 1 // stable original 1-based number, independent of pending/round
		fmt.Fprintf(&b, "Message %d -- type: %s\n", label, m.MsgType.Name())
		if m.From != "" || m.To != "" {
			b.WriteString("This message is from ")
			b.WriteString(partyLabel(m.From, m.FromLocation))
			if m.To != "" {
				b.WriteString(" to ")
				b.WriteString(partyLabel(m.To, m.ToLocation))
			}
			b.WriteString(".\n")
		}
		if m.Time != "" {
			fmt.Fprintf(&b, "This message is written at %s (its time fields are filled in separately); any time it mentions must be consistent with that.\n", m.Time)
		}
		if m.Purpose != "" {
			fmt.Fprintf(&b, "Purpose of this message: %s\n", m.Purpose)
		}
		if m.ReplyTo > 0 {
			fmt.Fprintf(&b, "This is a direct reply to Message %d: its content must specifically and consistently respond to that message (answer what it asked, reference the specifics it mentioned), not just share the same general scenario.\n", m.ReplyTo)
			refIdx := m.ReplyTo - 1
			if !pendingSet[refIdx] && results[refIdx].Values != nil {
				b.WriteString("For reference, Message ")
				fmt.Fprintf(&b, "%d's already-generated content was:\n", m.ReplyTo)
				for _, v := range results[refIdx].Values {
					b.WriteString("  ")
					b.WriteString(v)
					b.WriteString("\n")
				}
			}
		}
		routed := routedPerMsg[idx]
		b.WriteString("Fields you MUST fill in (the JSON key is the field tag in quotes):\n")
		shownGroups := map[string]bool{}
		for _, s := range specsPerMsg[idx] {
			if s.Optional {
				continue
			}
			if s.Group != "" {
				if !shownGroups[s.Group] {
					shownGroups[s.Group] = true
					fmt.Fprintf(&b, "  - %q: check AT LEAST ONE of these checkboxes by giving it the value \"checked\", leaving the others out:", s.Group)
					for _, c := range specsPerMsg[idx] {
						if c.Group == s.Group {
							fmt.Fprintf(&b, " %q (%s);", c.Tag, c.Label)
						}
					}
					b.WriteString("\n")
				}
				continue
			}
			fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
			if s.Help != "" {
				fmt.Fprintf(&b, " -- %s", s.Help)
			}
			if isCheckbox(s) {
				b.WriteString(` [checkbox: give "checked" to check it]`)
			} else if len(s.Choices) > 0 {
				fmt.Fprintf(&b, " (this is a dropdown: the value MUST be EXACTLY one of these, verbatim, character for character -- never free-form text: %s)", strings.Join(s.Choices, ", "))
			}
			if s.Multiline {
				b.WriteString(" [this is a free-text body field; it may span multiple sentences]")
			}
			if shortNameField[s.Common] {
				b.WriteString(" [keep this SHORT: at most 3 words, ideally 2, e.g. \"EOC Net Control\" or \"Command Post\", not a full sentence]")
			}
			if alwaysInclude[s.Common] {
				fmt.Fprintf(&b, " [a short title of at most %d words, in sentence case; the message's details go in its message fields, never here]", maxSummaryWords)
			}
			if names := routedForTag(routed, s.Tag); len(names) > 0 {
				fmt.Fprintf(&b, " [put the %s content here -- do NOT also add it to the free-text body]", strings.Join(names, "/"))
			}
			b.WriteString("\n")
		}
		var optional []FieldSpec
		for _, s := range specsPerMsg[idx] {
			if s.Optional {
				optional = append(optional, s)
			}
		}
		if len(optional) > 0 {
			b.WriteString("Other fields on this form, in form order. Put the message's details in the fields they belong in, and leave the fields that don't apply empty:\n")
			for _, s := range optional {
				fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
				if isCheckbox(s) {
					b.WriteString(` [checkbox: give "checked" to check it]`)
				} else if len(s.Choices) > 0 {
					fmt.Fprintf(&b, " (dropdown: exactly one of: %s)", strings.Join(s.Choices, ", "))
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("Requirements for this message (see above for how to meet each proword):\n")
		for _, cat := range plans[pos].Categories {
			text := prowords.ProwordName(cat)
			if slices.Contains(plans[pos].Unmet, cat) {
				text = "NOT MET IN YOUR PREVIOUS VERSION: " + text
			}
			if tag, ok := routed[cat]; ok {
				fmt.Fprintf(&b, "  - %s (put this ONLY in the %q field above, not in the free-text body)\n", text, tag)
			} else {
				fmt.Fprintf(&b, "  - %s\n", text)
			}
		}
		if plans[pos].CheckOne {
			text := `Check at least one checkbox on this form that fits the message, by giving it the value "checked", so the sender and receiver must handle a checked box.`
			if plans[pos].CheckOneUnmet {
				text = "NOT MET IN YOUR PREVIOUS VERSION: " + text
			}
			fmt.Fprintf(&b, "  - %s\n", text)
		}
		budget := max(MaxWords-baseWords[idx], minValueWords)
		fmt.Fprintf(&b, "Word budget: this whole message must total about %d words or fewer across ALL of its fields. Its pre-filled fields already use %d, so the values you give for it must total at most %d words (a phone number, email address, call sign, or other group without spaces counts as one word). Optional fields may stay empty; leave them out rather than go over.\n", MaxWords, baseWords[idx], budget)
		if plans[pos].Words > 0 {
			fmt.Fprintf(&b, "Your previous response made this message %d words in total: cut it to at most %d by shortening text and leaving optional fields empty (never leave a required field empty).\n", plans[pos].Words, MaxWords)
		}
		for _, s := range plans[pos].LongFields {
			fmt.Fprintf(&b, "Your previous version's %q (%s) was %s: make it a short title of at most %d words and move the details into the message fields.\n", s.Tag, s.Label, s.Problem, maxSummaryWords)
		}
		if prev := results[idx].Values; isRepair && prev != nil {
			if data, err := json.Marshal(prev); err == nil {
				fmt.Fprintf(&b, "Your previous version of this message (field tag -> value). Revise it rather than starting over, keeping everything that already works:\n%s\n", data)
			}
		}
		if len(plans[pos].MissingFields) > 0 {
			b.WriteString("The following REQUIRED fields are empty or invalid. You MUST give every one of them a valid, non-empty value this time:\n")
			reported := map[string]bool{}
			for _, s := range plans[pos].MissingFields {
				if s.Group != "" {
					if !reported[s.Group] {
						reported[s.Group] = true
						fmt.Fprintf(&b, "  - %q: check at least one of its checkboxes -- problem: %s\n", s.Group, s.Problem)
					}
					continue
				}
				fmt.Fprintf(&b, "  - %q: %s", s.Tag, s.Label)
				if s.Problem != "" {
					fmt.Fprintf(&b, " -- problem: %s", s.Problem)
				}
				if len(s.Choices) > 0 {
					fmt.Fprintf(&b, " (must be exactly one of: %s)", strings.Join(s.Choices, ", "))
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}
	if isRepair {
		b.WriteString("Revise your previous version rather than starting over: fix what is flagged above, keep everything that already works, and return the complete field values again (not just the changed ones). Every field under \"Fields you MUST fill in\" needs a non-empty value in your response -- do not omit any of them.\n\n")
	}
	return b.String()
}

// buildPrompt is sharedPrompt and messagePrompt together, as one message's
// whole prompt. Generate sends the two separately, so that the shared half
// is cached across the batch's calls; this is for tests, which check the
// prompt a message gets as a whole.
func buildPrompt(req Request, brief string, specsPerMsg [][]FieldSpec, results []Result, pending []int, plans []MessagePlan, routedPerMsg []map[prowords.Category]string, baseWords []int, isRepair bool) string {
	return sharedPrompt(req, brief) + messagePrompt(req, specsPerMsg, results, pending, plans, routedPerMsg, baseWords, isRepair)
}

// routedForTag returns the (sorted, for deterministic prompt text) proword
// names of the categories routed to the field with the given tag, if any.
func routedForTag(routed map[prowords.Category]string, tag string) []string {
	if len(routed) == 0 {
		return nil
	}
	var names []string
	for cat, t := range routed {
		if t == tag {
			names = append(names, prowords.ProwordName(cat))
		}
	}
	slices.Sort(names)
	return names
}

// partyLabel formats a From/To party for the prompt, e.g. "Net Control
// (County EOC)", or just the name if no location was given.
func partyLabel(name, location string) string {
	if location == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, location)
}
