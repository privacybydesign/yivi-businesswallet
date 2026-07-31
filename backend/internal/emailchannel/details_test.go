package emailchannel

import (
	"strings"
	"testing"
)

func TestSummarizeRendersTheStateAField(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		want     string
	}{
		{
			name: "a created record lists its fields, sorted",
			metadata: map[string]any{"after": map[string]any{
				"role": "member", "email": "sam@example.org",
			}},
			want: "email: sam@example.org\nrole: member",
		},
		{
			name: "a deleted record lists what was removed",
			metadata: map[string]any{"before": map[string]any{
				"recipient": "postbus@example.org",
			}},
			want: "recipient: postbus@example.org",
		},
		{
			name: "an update shows the change",
			metadata: map[string]any{
				"before": map[string]any{"role": "member", "jobTitle": "Developer"},
				"after":  map[string]any{"role": "admin", "jobTitle": "Developer"},
			},
			want: "jobTitle: Developer\nrole: member → admin",
		},
		{
			name: "an update that cleared a field leaves it out",
			metadata: map[string]any{
				"before": map[string]any{"role": "admin", "jobTitle": "Developer"},
				"after":  map[string]any{"role": "admin", "jobTitle": nil},
			},
			want: "role: admin",
		},
		{
			name: "a field set for the first time shows only the new value",
			metadata: map[string]any{
				"before": map[string]any{"role": "member"},
				"after":  map[string]any{"role": "member", "department": "Support"},
			},
			want: "department: Support\nrole: member",
		},
		{
			name:     "metadata that is not an envelope is taken as-is",
			metadata: map[string]any{"subject": "Quarterly report"},
			want:     "subject: Quarterly report",
		},
		{
			name:     "an empty record says nothing",
			metadata: map[string]any{"after": map[string]any{"jobTitle": nil}},
			want:     "",
		},
		{
			name:     "no metadata says nothing",
			metadata: nil,
			want:     "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarize(tc.metadata); got != tc.want {
				t.Errorf("summarize() =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

// Metadata comes back off the outbox as JSON, so a count is a float64 and a
// nested value is a map — neither may render as a Go value dump.
func TestSummarizeRendersJSONValues(t *testing.T) {
	got := summarize(map[string]any{"after": map[string]any{
		"attachmentCount": float64(2),
		"qualified":       true,
		"attributes":      map[string]any{"level": "gold"},
	}})
	want := "attachmentCount: 2\nattributes: {\"level\":\"gold\"}\nqualified: true"
	if got != want {
		t.Errorf("summarize() =\n%q\nwant\n%q", got, want)
	}
}

// A free-text field lands in a mail, so it is one line and bounded.
func TestSummarizeKeepsAValueToOneBoundedLine(t *testing.T) {
	got := summarize(map[string]any{"after": map[string]any{
		"subject": "Line one\nline  two",
	}})
	if got != "subject: Line one line two" {
		t.Errorf("summarize() = %q, want the value on one line", got)
	}

	got = summarize(map[string]any{"after": map[string]any{
		"subject": strings.Repeat("é", maxDetailValue+10),
	}})
	if !strings.HasSuffix(got, ellipsis) {
		t.Errorf("summarize() = %q, want a clipped value", got)
	}
	// The rune count is the cap plus the ellipsis; a byte-wise cut would exceed it.
	if runes := len([]rune(got)) - len("subject: "); runes != maxDetailValue+1 {
		t.Errorf("value is %d runes long, want %d", runes, maxDetailValue+1)
	}
}

func TestSummarizeCapsTheNumberOfFields(t *testing.T) {
	fields := map[string]any{}
	for _, name := range []string{
		"a01", "a02", "a03", "a04", "a05", "a06", "a07", "a08", "a09", "a10",
		"a11", "a12", "a13", "a14", "a15", "a16", "a17", "a18",
	} {
		fields[name] = "value"
	}
	lines := strings.Split(summarize(map[string]any{"after": fields}), "\n")
	if len(lines) != maxDetailLines {
		t.Errorf("summarize() rendered %d lines, want %d", len(lines), maxDetailLines)
	}
}
