package genmsg

import (
	"strings"

	"github.com/rothskeller/packet/v4/prowords"
)

// This file answers "who is this party?": how it is named, what credential
// it is evaluated for, and whether it is the net's control station. Flows
// name parties by role and prefix, while reports and diagrams rebuild them
// from what an incident holds, so both go through the same rules here.

// PartyName is how a party is named in records, reports, and diagrams: its
// role, then its message number prefix if it has one, e.g. "Shelter S21".
func PartyName(role, prefix string) string {
	role, prefix = strings.TrimSpace(role), strings.TrimSpace(prefix)
	if prefix == "" {
		return role
	}
	if role == "" {
		return prefix
	}
	return role + " " + prefix
}

// partyName is PartyName for a flow party.
func partyName(p FlowParty) string { return PartyName(p.Role, p.Prefix) }

// partyCredential returns the credential p is evaluated for, if any. A
// party marked "f3" without a credential of its own is evaluated for F3,
// whether it is sending the message or receiving it.
func partyCredential(p FlowParty) string {
	if p.Credential == "" && p.F3 {
		return "F3"
	}
	return p.Credential
}

// partyLevel returns the proword level a party's messages are evaluated at:
// the reduced F3 list for an F3 party, else the full list.
func partyLevel(p FlowParty) string {
	return credentialLevel(partyCredential(p))
}

// credentialLevel returns the proword level a credential is evaluated at.
// Only F3 has a reduced list; every other credential uses the complete one.
func credentialLevel(credential string) string {
	if credential == "F3" {
		return prowords.LevelF3
	}
	return prowords.LevelFull
}

// isNetControl says whether a party with this credential and role is the
// net's control station: any Net Control credential, or failing that, a
// role that says so.
func isNetControl(credential, role string) bool {
	return strings.HasPrefix(credential, "N") || strings.Contains(strings.ToLower(role), "net control")
}

// netControlParty returns the index of the Net Control party among
// parties. A party with a Net Control credential wins over one that only
// has the role, so a flow can name a station "Net Control 2" without
// confusing the two.
func netControlParty(parties []FlowParty) (int, bool) {
	for i, p := range parties {
		if strings.HasPrefix(p.Credential, "N") {
			return i, true
		}
	}
	for i, p := range parties {
		if isNetControl("", p.Role) {
			return i, true
		}
	}
	return 0, false
}
