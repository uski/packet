package genmsg

import (
	"encoding/json"
	"fmt"
	"strings"
)

// This file turns Claude's reply into field values: finding the JSON in it,
// matching each value to a field of the message it belongs to, and naming
// the fields that still need work.

// parseResponse parses Claude's JSON array response, validating each
// object's keys against the field tags of the corresponding pending
// message (by position). For a restricted (dropdown) field, the value must
// match one of the field's Choices (case/whitespace-insensitively); if it
// doesn't, the value is dropped (the field is left unset, so whatever
// default it already had stands) rather than writing free-form text into a
// field meant to hold one of a fixed set of choices, and the field's label
// is reported in the corresponding entry of invalid.
func parseResponse(text string, specsPerMsg [][]FieldSpec, pending []int) (values []map[string]string, invalid [][]string, err error) {
	if strings.HasPrefix(text, "{") {
		text = "[" + text + "]" // a single message sent as a bare object
	}
	var raw []map[string]any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, nil, fmt.Errorf("parsing Claude's JSON response: %w", err)
	}
	values = make([]map[string]string, 0, len(raw))
	invalid = make([][]string, 0, len(raw))
	for pos, obj := range raw {
		if pos >= len(pending) {
			break // extra objects beyond what we asked for are ignored
		}
		specByTag := make(map[string]FieldSpec, len(specsPerMsg[pending[pos]]))
		for _, s := range specsPerMsg[pending[pos]] {
			specByTag[s.Tag] = s
		}
		vals := make(map[string]string, len(obj))
		var bad []string
		for k, v := range obj {
			spec, ok := specByTag[k]
			if !ok {
				continue
			}
			var sval string
			if s, ok := v.(string); ok {
				sval = s
			} else {
				sval = fmt.Sprint(v)
			}
			if isCheckbox(spec) {
				switch strings.ToLower(strings.TrimSpace(sval)) {
				case "true", "yes", "x", "1", "on":
					sval = "checked"
				case "", "false", "no", "unchecked", "0", "off":
					continue
				}
			}
			if len(spec.Choices) > 0 {
				canon, ok := matchChoice(sval, spec.Choices)
				if !ok {
					bad = append(bad, spec.Label)
					continue
				}
				sval = canon
			}
			vals[k] = sval
		}
		values = append(values, vals)
		invalid = append(invalid, bad)
	}
	return values, invalid, nil
}

// isCheckbox reports whether s is a checkbox, whose only value is "checked".
func isCheckbox(s FieldSpec) bool {
	return len(s.Choices) == 1 && s.Choices[0] == "checked"
}

// matchChoice returns the canonical form (as declared in choices) matching
// value case- and whitespace-insensitively, if any.
func matchChoice(value string, choices []string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	for _, c := range choices {
		if strings.EqualFold(strings.TrimSpace(c), trimmed) {
			return c, true
		}
	}
	return "", false
}

// fieldLabels returns the human-readable labels of specs, for reporting
// which required fields a message is still missing; a checkbox group is
// reported once, by its own label.
func fieldLabels(specs []FieldSpec) []string {
	var labels []string
	seen := map[string]bool{}
	for _, s := range specs {
		label := s.Label
		if s.Group != "" {
			label = s.Group
		}
		if !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	return labels
}

// extractJSON strips leading/trailing markdown code fences (```json ... ```
// or ``` ... ```) that models sometimes add despite instructions not to.
func extractJSON(s string) string {
	// Take the outermost JSON array or object, dropping any fences or
	// commentary around it.
	start := strings.IndexAny(s, "[{")
	end := strings.LastIndexAny(s, "]}")
	if start < 0 || end < start {
		return strings.TrimSpace(s)
	}
	return s[start : end+1]
}
