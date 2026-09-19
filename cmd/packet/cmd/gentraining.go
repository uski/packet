package cmd

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/rothskeller/packet/v4/cmd/packet/cio"
	"github.com/rothskeller/packet/v4/genmsg"
	"github.com/rothskeller/packet/v4/incident"
	"github.com/rothskeller/packet/v4/prowords"
	"github.com/spf13/pflag"
)

const (
	gentrainingSlug = `Generate realistic training messages for a credential evaluation`
	gentrainingHelp = `
usage: packet gentrain ⇥[-flags] «msg-type» [«msg-type» ...]
       packet gentrain ⇥--flow «file.json» [-flags]
       packet gentrain ⇥--uml|--plantuml [--flow «file.json»] [--name «net-name»]
  -l, --level «level»      ⇥Proword profile: "f3" or "full" (default "full"); ignored with --flow, where each party has its own "f3" flag instead
  -s, --scenario «text»    ⇥Scenario to steer the generated content
  -f, --flow «file.json»   ⇥Generate a multi-party message flow from a JSON file
      --uml                ⇥Print the flow (or without --flow, the current incident's messages) as a sequencediagram.org diagram instead
      --plantuml           ⇥Likewise, as a PlantUML sequence diagram
      --name «net-name»    ⇥With --uml or --plantuml and no --flow, the diagram title's net name (default: the incident name)

The "packet gentrain" (or "gentraining") command asks Claude to draft one realistic 3rd-party message for each «msg-type» given on the command line (see "packet forms list" for the supported tags/keys, or "plain" for a plain text message), suitable for handing to a candidate during an SCCo RACES credential evaluation.

A training session doesn't need to use the same form for every message: for example, "packet gentrain ICS213 plain RoadCl" generates three messages -- one ICS-213, one plain text message, and one Road Closure form -- as a single batch with a shared scenario. Repeat a «msg-type» to get more than one message of that type, e.g. "packet gentrain ICS213 ICS213 plain" for two ICS-213s and a plain text message.

For a coherent multi-party exchange -- e.g. one message asking all stations for a status update, answered by several reply messages that are actually consistent with the question and with each other -- use --flow with a JSON file shaped like:

  {
    "parties": [
      {"role": "Net Control", "location": "County EOC", "prefix": "EOC"},
      {"role": "Shelter Manager", "location": "Roosevelt MS", "prefix": "S24", "f3": true}
    ],
    "messages": [
      {"msgType": "ICS213", "from": 0, "to": -1, "toLabel": "All Stations",
       "purpose": "request shelter capacity status"},
      {"msgType": "ICS213", "from": 1, "to": 0, "replyTo": 1,
       "purpose": "report current shelter status"}
    ]
  }

A party's optional "credential" names the credential it is evaluated for ("F3", "F2", "F1", "S3", "S2", "S1", "P3", "P2", "P1", "N3", "N2", or "N1"); "F3" also selects the reduced proword list, as "f3": true does. The GUI dialog's "Check credential criteria" button compares each party's traffic with its credential's minimums from the Credentialing Program Handbook, and "Auto-add traffic to meet criteria" adds messages until they're met, all through the Net Control party (the one with a Net Control credential, or a role containing "Net Control"): Net Control to a station or to All Stations, and a station to Net Control, often as a reply. A message's optional "opToOp": true marks it as operator-to-operator traffic (e.g. a status report between radio operators) rather than a served agency's message; plain text, check-in, and check-out messages always count as operator-to-operator. Auto-added operator-to-operator traffic is ICS-213s marked this way, never plain text.

An optional top-level "packet": true says the messages are sent by packet, so their numbers get the "P" suffix (e.g. "ABC-123P"); without it, they have no suffix (e.g. "ABC-123"). A number a message gives itself keeps its own suffix, if it has one. An optional top-level "name" names the net or exercise (e.g. "Evaluation Net") for diagram titles. A party's optional "principal" names who hands its messages to its operator and receives the messages delivered to it (e.g. "NetMgr" or "FieldMgr"); diagrams show those hand-offs and deliveries for 3rd-party traffic. A message's optional "handling" ("R", "P", or "I") sets its handling order instead of letting Claude choose, and its optional "group" (a number) puts it in a hand-off group: the messages with the same group are handed to their operators together, and the diagram shows them sent in handling order, most urgent first -- a prioritization drill.

An entry with "event" instead of "msgType" is a scenario event: drawn on the diagram, never generated, and not counted as traffic. The events are "note" (a section heading, with its "text"), "open-net", "check-ins", "hw-check" (health and welfare check), "shift-change" (Net Control shift change), "closing" (announcing the net is closing), "check-outs", and "net-closed"; an optional "text" replaces the default wording. Every event but "note", "hw-check", and "shift-change" needs a Net Control party. Events count as entries for "replyTo" numbering, but a message can't reply to one.

--uml prints the flow in the syntax of sequencediagram.org, and --plantuml in PlantUML's, without generating anything. Without --flow, they draw the messages in the current incident instead, with their actual numbers and handling orders; the principals, hand-off groups, and events are known for the messages generated from a flow. The GUI's Incident menu has the same diagram under "Message Flow Diagram".

An optional top-level "date" gives the incident date (MM/DD/YYYY or YYYY-MM-DD, default today): every date field of the generated messages gets it. A message's optional "msgNo" gives its message number instead of letting the tool pick one, as PPP-NNN: the sender's prefix and a number (e.g. "XND-101"). A "from": -1 message can't have one; give each station its own message to number it. Without a suffix letter, the number gets "P", like the tool's own numbers. The numbers the tool picks skip those, and ignore deleted messages; a number already used by a message in the incident is refused. A message's optional "time" (HH:MM) fills its message time field (and any other required time field, such as a form's prepared time), and Claude keeps the message consistent with it; without one, the time fields are left blank.

An optional top-level "scenario" gives the scenario text, used when --scenario isn't given. The "Save scenario…" button of the GUI's Generate Multi-Party Training Messages dialog writes a file in this format, and its "Load scenario…" button reads one.

"parties" are referenced by 0-based index from each message's "from"/"to". A party's optional "prefix" is its three-character message number prefix (e.g. "S24" for Shelter 24): its messages are numbered with it (e.g. "S24-101P", continuing after the highest such number already in the incident) and addressed to the receiving party's prefix, and a reply's Reference field gets the number of the message it answers. Use "to": -1 with "toLabel" for a broadcast recipient that isn't one of the defined parties (e.g. "All Stations"). "replyTo" is the 1-based index of another message in the same file that this one replies to; Claude is given that message's content so the reply is directly consistent with it, not just generated independently. The From/To ICS Position and Location on each message are set directly from the parties (never left for Claude to invent), so they stay perfectly consistent across the whole flow.

Use "from": -1 to fan a single message entry out into one message from EVERY party, e.g. so several field stations can each independently reply to one "All Stations" broadcast without listing each reply by hand. The fan-out leaves out the recipient party, and the sender of the message it replies to (via "replyTo"), since no station sends a message to itself or replies to its own broadcast. An ordinary message's "from" and "to" must be different parties. A message may not itself reply to a "from": -1 entry, since there is no single message to point at.

Each party's "f3" (boolean, default false) selects which proword list that party's own messages are evaluated on: true for the reduced Field Communicator Type III list, false (or omitted) for the complete list required by every other credential (F2, F1, and all Net Control, Packet Operator, and Shadow Communicator tiers). This lets one flow mix parties at different credential levels -- each message's required prowords are drawn only from its own sender's list, and spread across that sender's messages rather than crammed into every one. --level (below) only applies when no «msg-type» flow is used. Each party's own messages exercise its whole list, because a candidate is evaluated on the traffic that candidate transmits, so give every party at least two messages (three for Field Type II and Shadow Type II, per the Credentialing Program Handbook).

If --scenario is given, its text is used to steer the emergency-response scenario the messages are based on (e.g. "a downed power line on Almaden Expressway"). If omitted, a generic SCCo emergency-response scenario is invented (utility outage, fallen tree, road closure, traffic congestion, etc.).

Generating messages requires the ANTHROPIC_API_KEY environment variable to be set to a valid Anthropic API key. Messages are drafted several at a time, sharing a cached prompt, with Claude's reasoning effort set to "low" for speed; set PACKET_CLAUDE_EFFORT (e.g. to "medium") to change it, or PACKET_CLAUDE_MODEL to use another model.

The new messages are created as unsent draft messages in the current incident, exactly as "packet new" would create them. For each one, its local message ID and a table of which prowords its final content exercises (and how many times) are printed, so you can spot-check coverage before handing it to a candidate. Review and edit the messages themselves (see "packet help edit") as needed.
`
)

func cmdGentraining(args []string) (err error) {
	var (
		level    string
		scenario string
		flowFile string
		uml      bool
		plantuml bool
		netName  string
		specs    []genmsg.MessageSpec
		c        = cio.Open()
	)
	flags := pflag.NewFlagSet("gentraining", pflag.ContinueOnError)
	flags.StringVarP(&level, "level", "l", prowords.LevelFull, `Proword profile: "f3" or "full"`)
	flags.StringVarP(&scenario, "scenario", "s", "", "Scenario to steer the generated content")
	flags.StringVarP(&flowFile, "flow", "f", "", "Generate a multi-party message flow from a JSON file")
	flags.BoolVar(&uml, "uml", false, "Print the flow or incident as a sequencediagram.org diagram")
	flags.BoolVar(&plantuml, "plantuml", false, "Print the flow or incident as a PlantUML sequence diagram")
	flags.StringVar(&netName, "name", "", "Net name for the incident diagram title")
	flags.Usage = func() {} // we do our own
	if err = flags.Parse(args); err == pflag.ErrHelp {
		return cmdHelp([]string{"gentraining"})
	} else if err != nil {
		c.Error(err)
		return usage(gentrainingHelp)
	}
	if uml && plantuml {
		c.ErrorF(`Give only one of --uml and --plantuml.`)
		return usage(gentrainingHelp)
	}
	if (uml || plantuml) && flowFile == "" {
		if flags.NArg() != 0 {
			c.ErrorF(`Do not give «msg-type» arguments together with --uml or --plantuml.`)
			return usage(gentrainingHelp)
		}
		registerForms()
		return incidentDiagram(netName, plantuml)
	}
	if (flowFile == "") == (flags.NArg() == 0) {
		if flowFile != "" {
			c.ErrorF(`Do not give «msg-type» arguments together with --flow.`)
		}
		return usage(gentrainingHelp)
	}
	level = strings.ToLower(level)
	if level != prowords.LevelF3 && level != prowords.LevelFull {
		c.ErrorF(`--level must be %q or %q, not %q.`, prowords.LevelF3, prowords.LevelFull, level)
		return usage(gentrainingHelp)
	}
	registerForms() // needed to recognize message types
	if flowFile != "" {
		data, err := os.ReadFile(flowFile)
		if err != nil {
			c.ErrorF(`Can't read flow file: %s.`, err)
			return usage(gentrainingHelp)
		}
		var fl struct {
			genmsg.Flow
			Scenario string `json:"scenario"`
		}
		if err := json.Unmarshal(data, &fl); err != nil {
			c.ErrorF(`%s does not contain valid JSON: %s.`, flowFile, err)
			return usage(gentrainingHelp)
		}
		if scenario == "" {
			scenario = fl.Scenario
		}
		if uml || plantuml {
			d, err := genmsg.FlowDiagram(fl.Flow)
			if err != nil {
				c.ErrorF(`%s: %s.`, flowFile, err)
				return usage(gentrainingHelp)
			}
			if plantuml {
				fmt.Print(d.PlantUML(scenario))
			} else {
				fmt.Print(d.SequenceDiagram())
			}
			return nil
		}
		if specs, err = genmsg.ResolveFlow(fl.Flow); err != nil {
			c.ErrorF(`%s: %s.`, flowFile, err)
			return usage(gentrainingHelp)
		}
	} else {
		specs = make([]genmsg.MessageSpec, flags.NArg())
		for argidx := range flags.NArg() {
			mtarg := flags.Arg(argidx)
			mt, ok := genmsg.FindMsgType(mtarg)
			if !ok {
				c.ErrorF(`There is no editable message type %q (message #%d).  Use "packet forms list" to get a list of message types.`, mtarg, argidx+1)
				return usage(gentrainingHelp)
			}
			specs[argidx] = genmsg.MessageSpec{MsgType: mt}
		}
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
			Messages: specs,
			Level:    level,
			Scenario: scenario,
			Progress: func(s string) { c.Status("%s", s) },
		})
		c.Status("")
		if err != nil {
			return err
		}
		applied, err = genmsg.Apply(i, specs, results)
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
		if len(a.Result.MissingFields) > 0 {
			c.ErrorF("Warning: message %s is missing a required value for: %s.  Review it before use.", a.ID, strings.Join(a.Result.MissingFields, ", "))
		}
		if a.Result.Words > genmsg.MaxWords+genmsg.WordTolerance {
			c.ErrorF("Warning: message %s is %d words, over the target of about %d.  Review it before use.", a.ID, a.Result.Words, genmsg.MaxWords)
		}
	}
	return nil
}

// incidentDiagram prints the current incident's messages as a sequence
// diagram (see genmsg.IncidentDiagram).
func incidentDiagram(name string, plantuml bool) error {
	return incRead(func(i *incident.Incident) error {
		if name == "" {
			name = i.Config.IncidentName
		}
		d, err := genmsg.IncidentDiagram(i, name)
		if err != nil {
			return err
		}
		if plantuml {
			fmt.Print(d.PlantUML(""))
		} else {
			fmt.Print(d.SequenceDiagram())
		}
		return nil
	})
}

// printProwordTable prints, for each generated message, which prowords the
// proword engine (see the prowords package) detected in its final content
// and how many times each appears.
func printProwordTable(applied []genmsg.Applied) {
	for _, a := range applied {
		fmt.Printf("%s (%s, %d words)\n", a.ID, a.MsgType.Name(), a.Result.Words)
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
