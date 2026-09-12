package cmd

import (
	"context"
	"fmt"
	"strconv"
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
usage: packet gentrain ⇥[-flags] «msg-type» «count»
  -l, --level «level»      ⇥Proword profile: "f3" or "full" (default "full")
  -s, --scenario «text»    ⇥Scenario to steer the generated content

The "packet gentrain" (or "gentraining") command asks Claude to draft «count» realistic 3rd-party messages of the given «msg-type» (see "packet forms list" for the supported tags/keys, or "plain" for a plain text message), suitable for handing to a candidate during an SCCo RACES credential evaluation.

The messages are generated to exercise the message-passing prowords required for the given --level: "f3" is the reduced proword list evaluated only for the Field Communicator Type III credential; "full" (the default) is the complete proword list required for every other credential (F2, F1, and all Net Control, Packet Operator, and Shadow Communicator tiers). The set of required prowords is spread across the generated messages rather than crammed into every one.

If --scenario is given, its text is used to steer the emergency-response scenario the messages are based on (e.g. "a downed power line on Almaden Expressway"). If omitted, a generic SCCo emergency-response scenario is invented (utility outage, fallen tree, road closure, traffic congestion, etc.).

Generating messages requires the ANTHROPIC_API_KEY environment variable to be set to a valid Anthropic API key.

The new messages are created as unsent draft messages in the current incident, exactly as "packet new" would create them, and their local message IDs are printed. Review and edit them (see "packet help edit") before handing them to a candidate.
`
)

func cmdGentraining(args []string) (err error) {
	var (
		level    string
		scenario string
		msgtype  message.EditableMType
		count    int
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
	if flags.NArg() != 2 {
		return usage(gentrainingHelp)
	}
	level = strings.ToLower(level)
	if level != prowords.LevelF3 && level != prowords.LevelFull {
		c.ErrorF(`--level must be %q or %q, not %q.`, prowords.LevelF3, prowords.LevelFull, level)
		return usage(gentrainingHelp)
	}
	if count, err = strconv.Atoi(flags.Arg(1)); err != nil || count < 1 {
		c.ErrorF(`%q is not a valid message count; it must be a positive integer.`, flags.Arg(1))
		return usage(gentrainingHelp)
	}
	registerForms() // needed to recognize a message type
	mtarg := flags.Arg(0)
	for mt := range message.AllTypes() {
		if emt, ok := mt.(message.EditableMType); ok {
			if strings.EqualFold(mtarg, emt.CreateTag()) || strings.EqualFold(mtarg, emt.CreateKey()) {
				msgtype = emt
				break
			}
		}
	}
	if msgtype == nil {
		c.ErrorF(`There is no editable message type %q.  Use "packet forms list" to get a list of message types.`, mtarg)
		return usage(gentrainingHelp)
	}
	client := &genmsg.ClaudeClient{}
	if !client.HasAPIKey() {
		c.Error(genmsg.ErrNoAPIKey)
		return genmsg.ErrNoAPIKey
	}
	var (
		ids        []string
		incomplete []genmsg.Result
	)
	if err = incWrite(true, func(i *incident.Incident) error {
		if err := requiredConfig(i, "OpCall", "OpName", "TxMessageID"); err != nil {
			return err
		}
		results, err := genmsg.Generate(context.Background(), client, genmsg.Request{
			MsgType:  msgtype,
			Count:    count,
			Level:    level,
			Scenario: scenario,
		})
		if err != nil {
			return err
		}
		ids, incomplete, err = genmsg.Apply(i, msgtype, results)
		return err
	}); err != nil {
		return err
	}
	for _, id := range ids {
		fmt.Println(id)
	}
	for _, res := range incomplete {
		var names []string
		for _, cat := range res.Missing {
			names = append(names, prowords.ProwordName(cat))
		}
		c.ErrorF("Warning: a generated message may be missing content for: %s.  Review it before use.", strings.Join(names, ", "))
	}
	return nil
}
