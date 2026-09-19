// Package genmsg writes realistic training messages for an SCCo ARES/RACES
// credential evaluation, by asking Claude for their content and creating
// them as draft messages in an incident.
//
// An evaluator hands the generated messages to a candidate, who voices them
// over the air using the message-passing procedure. Each message therefore
// has to call for the prowords the candidate's credential is evaluated on
// (see the prowords package), while reading like real emergency traffic.
//
// It works generically with any registered message.EditableMType: it
// introspects the type's editable fields at runtime rather than hard-coding
// knowledge of any particular form (ICS-213, Road Closure, Shelter, etc.).
//
// # The pipeline
//
// Generate takes a Request: one MessageSpec per message, saying its type,
// who sends it, who receives it, and what it is about. For each message it:
//
//   - plans which prowords that message must exercise (plan.go, and
//     planByParty, which gives each party its own share, since a candidate
//     is evaluated on what that candidate transmits);
//   - builds an empty draft and decides which of its fields Claude should
//     fill (draft.go, describe.go, classify.go);
//   - asks Claude, first for a brief the whole batch shares, then for each
//     message (prompt.go, claude.go, response.go);
//   - checks the answer and asks again while something is missing or too
//     long, keeping the best version (generate.go).
//
// Apply then creates the messages in the incident, numbering them and
// linking replies to what they answer (apply.go).
//
// # Multi-party scenarios
//
// A Flow describes a whole exercise: its parties, and the messages and
// events between them (flow.go). ResolveFlow turns it into the MessageSpecs
// Generate wants, CheckFlow compares each party's traffic with its
// credential's minimums from the Credentialing Program Handbook, and
// CompleteFlow adds traffic until they are met (criteria.go).
//
// A flow, or an incident's own messages, can be drawn as a sequence
// diagram: layoutDiagram builds it (diagram.go) from a flow (FlowDiagram)
// or from what an incident holds (incdiagram.go), and it renders as
// sequencediagram.org or PlantUML text.
//
// IncidentReport recounts, from an incident's current messages, what each
// party sent and which credentials that reaches (report.go), using the
// records Apply kept of who sent what (training.go).
package genmsg
