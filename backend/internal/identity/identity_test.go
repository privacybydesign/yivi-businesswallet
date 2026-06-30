package identity

import "testing"

func TestKeyMatching(t *testing.T) {
	tests := []struct {
		name  string
		a, b  Name
		equal bool
	}{
		{
			name:  "passport MRZ matches ID-card mixed case",
			a:     Name{"JOSE", "VAN DER BERG"},
			b:     Name{"José", "van der Berg"},
			equal: true,
		},
		{
			name:  "diacritics fold to ASCII",
			a:     Name{"MULLER", "STRASSE"},
			b:     Name{"Müller", "Straße"},
			equal: true,
		},
		{
			name:  "case and whitespace are insignificant",
			a:     Name{"  anna   maria ", "DE VRIES"},
			b:     Name{"Anna Maria", "de Vries"},
			equal: true,
		},
		{
			name:  "different surnames differ",
			a:     Name{"Anna", "Berg"},
			b:     Name{"Anna", "Bakker"},
			equal: false,
		},
		{
			name:  "given/last boundary is significant",
			a:     Name{"Anna", "Maria"},
			b:     Name{"Anna Maria", ""},
			equal: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.Key() == tt.b.Key(); got != tt.equal {
				t.Errorf("Key()==Key() = %v, want %v (%q vs %q)", got, tt.equal, tt.a.Key(), tt.b.Key())
			}
		})
	}
}

func TestClean(t *testing.T) {
	tests := []struct {
		name string
		in   Name
		want Name
	}{
		{
			name: "MRZ all-caps surname lowercases particles",
			in:   Name{"JOSE", "VAN DER BERG"},
			want: Name{"Jose", "van der Berg"},
		},
		{
			name: "MRZ particle as first token is lowercased",
			in:   Name{"JAN", "DE VRIES"},
			want: Name{"Jan", "de Vries"},
		},
		{
			name: "mixed-case input is kept verbatim",
			in:   Name{"José", "van der Berg"},
			want: Name{"José", "van der Berg"},
		},
		{
			name: "diacritics are not recovered from MRZ",
			in:   Name{"JOSE", "MULLER"},
			want: Name{"Jose", "Muller"},
		},
		{
			name: "whitespace is trimmed and collapsed",
			in:   Name{"  jan  pieter ", "  van  gogh "},
			want: Name{"jan pieter", "van gogh"},
		},
		{
			name: "empty fields stay empty",
			in:   Name{"", ""},
			want: Name{"", ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Clean(); got != tt.want {
				t.Errorf("Clean() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReconcile(t *testing.T) {
	tests := []struct {
		name      string
		disclosed Name
		stored    *Name
		want      Decision
	}{
		{
			name:      "no stored name populates",
			disclosed: Name{"José", "Berg"},
			stored:    nil,
			want:      Populate,
		},
		{
			name:      "matching ASCII name proceeds",
			disclosed: Name{"Jose", "Berg"},
			stored:    &Name{"Jose", "Berg"},
			want:      Proceed,
		},
		{
			name:      "richer disclosure upgrades the stored form",
			disclosed: Name{"José", "Berg"},
			stored:    &Name{"Jose", "Berg"},
			want:      Upgrade,
		},
		{
			name:      "MRZ re-disclosure does not downgrade diacritics",
			disclosed: Name{"JOSE", "BERG"},
			stored:    &Name{"José", "Berg"},
			want:      Proceed,
		},
		{
			name:      "different legal name holds for review",
			disclosed: Name{"Anna", "Berg"},
			stored:    &Name{"José", "Berg"},
			want:      Review,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Reconcile(tt.disclosed, tt.stored); got != tt.want {
				t.Errorf("Reconcile() = %s, want %s", got, tt.want)
			}
		})
	}
}
