package emailchannel

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// How much of an event's audit metadata a notification repeats.
//
// The metadata is handed to the channels verbatim, and the notification catalog
// (internal/notifications) is the gate that decides which actions may leave the
// audit log at all — so what is left here is presentation, not another filter.
// The rules below keep that presentation honest:
//
//   - one "field: value" per line, sorted by field, so two notifications of the
//     same action read the same way;
//   - an {before, after} update shows the new value, with the old one before an
//     arrow when both are set. A field the update cleared is left out rather than
//     printed as its old value, which would read as if it were still in force;
//   - a value is one line and bounded in length, because it lands in a mail.
const (
	// maxDetailLines caps how many fields one notification lists. Audit metadata is
	// written by this codebase and is a handful of fields per action, so this is a
	// backstop against a future record carrying a large map, not routine truncation.
	maxDetailLines = 16
	// maxDetailValue caps one value's length in runes, so a long free-text field (a
	// message subject, a covering note) cannot take the message over.
	maxDetailValue = 120
	// ellipsis marks a value this cut short.
	ellipsis = "…"
	// changeArrow separates an updated field's old value from its new one. It is a
	// symbol rather than a word because this line carries no copy to translate.
	changeArrow = " → "
)

// summarize renders an event's audit metadata as the lines the notification mail
// shows. It returns "" when there is nothing worth stating, in which case the
// template's details paragraph drops out of the message.
func summarize(metadata map[string]any) string {
	before, after, updated := envelope(metadata)

	names := make([]string, 0, len(before)+len(after))
	seen := make(map[string]bool, len(before)+len(after))
	for _, side := range []map[string]any{before, after} {
		for name := range side {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)

	lines := make([]string, 0, len(names))
	for _, name := range names {
		if len(lines) == maxDetailLines {
			break
		}
		old, current := formatValue(before[name]), formatValue(after[name])
		if current == "" {
			// Nothing is in force for this field: either it was never set, or the
			// update cleared it. Neither is worth a line.
			continue
		}
		if updated && old != "" && old != current {
			lines = append(lines, name+": "+old+changeArrow+current)
			continue
		}
		lines = append(lines, name+": "+current)
	}
	return strings.Join(lines, "\n")
}

// envelope unwraps the {before, after} shape audit.Created/Updated/Deleted record
// their metadata in. The "after" side is what is in force, so a deletion's before
// side is returned as the after side: the fields it lists are what was removed,
// and that is the state the notification is about. Metadata that is not an
// envelope at all is taken as-is.
func envelope(metadata map[string]any) (before, after map[string]any, updated bool) {
	oldSide, hasOld := asMap(metadata["before"])
	newSide, hasNew := asMap(metadata["after"])
	switch {
	case hasOld && hasNew:
		return oldSide, newSide, true
	case hasNew:
		return nil, newSide, false
	case hasOld:
		return nil, oldSide, false
	default:
		return nil, metadata, false
	}
}

func asMap(value any) (map[string]any, bool) {
	m, ok := value.(map[string]any)
	return m, ok && len(m) > 0
}

// formatValue renders one metadata value on a single bounded line. An empty
// result means "nothing to say", which is what drops the field from the list.
// Numbers arrive as float64 because the outbox round-trips metadata through JSON.
func formatValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return clip(oneLine(typed))
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return clip(oneLine(string(encoded)))
	}
}

// oneLine collapses whitespace, so one field stays one line of the list.
func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func clip(value string) string {
	if utf8.RuneCountInString(value) <= maxDetailValue {
		return value
	}
	return string([]rune(value)[:maxDetailValue]) + ellipsis
}
