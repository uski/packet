package cmd

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/rothskeller/packet/v4/cmd/packet/cio"
	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/message"
	"github.com/rothskeller/packet/v4/prowords"
	"github.com/spf13/pflag"
)

const (
	gentrainingSlug = `Generate realistic training messages for a credential evaluation`
	gentrainingHelp = `
usage: packet gentrain ⇥[-flags] «msg-type» [«msg-type» ...]
  -l, --level «level»      ⇥Proword profile: "f3" or "full" (default "full")
  -s, --scenario «text»    ⇥Scenario to steer the generated content

The "packet gentrain" (or "gentraining") command asks Claude to draft one realistic 3rd-party message for each «msg-type» given on the command line (see "packet forms list" for the supported tags/keys, or "plain" for a plain text message), suitable for handing to a candidate during an SCCo RACES credential evaluation.

A training session doesn't need to use the same form for every message: for example, "packet gentrain ICS213 plain RoadCl" generates three messages -- one ICS-213, one plain text message, and one Road Closure form -- as a single batch with a shared scenario. Repeat a «msg-type» to get more than one message of that type, e.g. "packet gentrain ICS213 ICS213 plain" for two ICS-213s and a plain text message.

The messages are generated to exercise the message-passing prowords required for the given --level: "f3" is the reduced proword list evaluated only for the Field Communicator Type III credential; "full" (the default) is the complete proword list required for every other credential (F2, F1, and all Net Control, Packet Operator, and Shadow Communicator tiers). The set of required prowords is spread across the generated messages rather than crammed into every one.

If --scenario is given, its text is used to steer the emergency-response scenario the messages are based on (e.g. "a downed power line on Almaden Expressway"). If omitted, a generic SCCo emergency-response scenario is invented (utility outage, fallen tree, road closure, traffic congestion, etc.).

Generating messages requires the ANTHROPIC_API_KEY environment variable to be set to a valid Anthropic API key.

The new messages are created as unsent draft messages in the current incident, exactly as "packet new" would create them. For each one, its local message ID and a table of which prowords its final content exercises (and how many times) are printed, so you can spot-check coverage before handing it to a candidate. Review and edit the messages themselves (see "packet help edit") as needed.
`
)

func cmdGentraining(args []string) (err error) {
	var (
		level    string
		scenario string
		c        = cio.Open()
	)
	flags := pflag.NewFlagSet("gentraining", pflag.ContinueOnError)
	flags.StringVarP(&level, "level", "l", prowords.LevelFull, `Proword profile: "f3" or "full"`)
	flags.StringVarP(&scenario, "scenario", "s", "", "Scenario to steer the generated content")
	flags.Usage = func() {} // we do our own
	if err = flags.Parse(args); err == pflag.ErrHelp {
		return cmdHelp([]string{"gentraining"})
	} else if err != nil {
		c.Error(err)
		return usage(gentrainingHelp)
	}
	if flags.NArg() < 1 {
		return usage(gentrainingHelp)
	}
	level = strings.ToLower(level)
	if level != prowords.LevelF3 && level != prowords.LevelFull {
		c.ErrorF(`--level must be %q or %q, not %q.`, prowords.LevelF3, prowords.LevelFull, level)
		return usage(gentrainingHelp)
	}
	registerForms() // needed to recognize message types
	msgtypes := make([]message.EditableMType, flags.NArg())
	for argidx := range flags.NArg() {
		mtarg := flags.Arg(argidx)
		var found message.EditableMType
		for mt := range message.AllTypes() {
			if emt, ok := mt.(message.EditableMType); ok {
				if strings.EqualFold(mtarg, emt.CreateTag()) || strings.EqualFold(mtarg, emt.CreateKey()) {
					found = emt
					break
				}
			}
		}
		if found == nil {
			c.ErrorF(`There is no editable message type %q (message #%d).  Use "packet forms list" to get a list of message types.`, mtarg, argidx+1)
			return usage(gentrainingHelp)
		}
		msgtypes[argidx] = found
	}
	client := &genmsg.ClaudeClient{}
	if !client.HasAPIKey() {
		c.Error(genmsg.ErrNoAPIKey)
		return genmsg.ErrNoAPIKey
	}
	var applied []genmsg.Applied
	if err = incWrite(true, func(i *incident.Incident) error {
		if err := requiredConfig(i, "OpCall", "OpName", "TxMessageID"); err != nil {
			return err
		}
		results, err := genmsg.Generate(context.Background(), client, genmsg.Request{
			Incident: i,
			MsgTypes: msgtypes,
			Level:    level,
			Scenario: scenario,
			Progress: func(s string) { c.Status("%s", s) },
		})
		c.Status("")
		if err != nil {
			return err
		}
		applied, err = genmsg.Apply(i, msgtypes, results)
		return err
	}); err != nil {
		return err
	}
	printProwordTable(applied)
	for _, a := range applied {
		if len(a.Result.Missing) > 0 {
			var names []string
			for _, cat := range a.Result.Missing {
				names = append(names, prowords.ProwordName(cat))
			}
			c.ErrorF("Warning: message %s may be missing content for: %s.  Review it before use.", a.ID, strings.Join(names, ", "))
		}
		if len(a.Result.InvalidFields) > 0 {
			c.ErrorF("Warning: message %s had an unrecognized value for: %s; left at its default.  Review it before use.", a.ID, strings.Join(a.Result.InvalidFields, ", "))
		}
	}
	return nil
}

// printProwordTable prints, for each generated message, which prowords the
// proword engine (see the prowords package) detected in its final content
// and how many times each appears.
func printProwordTable(applied []genmsg.Applied) {
	for _, a := range applied {
		fmt.Printf("%s (%s)\n", a.ID, a.MsgType.Name())
		if len(a.Result.Counts) == 0 {
			fmt.Println("  (no prowords detected)")
			continue
		}
		cats := slices.Collect(maps.Keys(a.Result.Counts))
		slices.SortFunc(cats, func(x, y prowords.Category) int {
			return cmp.Compare(prowords.ProwordName(x), prowords.ProwordName(y))
		})
		for _, cat := range cats {
			fmt.Printf("  %-24s %d\n", prowords.ProwordName(cat), a.Result.Counts[cat])
		}
	}
}
