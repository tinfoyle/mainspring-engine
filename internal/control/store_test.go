package control

import "testing"

func TestNormalizeSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: " Acme-HVAC ", want: "acme-hvac", ok: true},
		{input: "abc", want: "abc", ok: true},
		{input: "ab", ok: false},
		{input: "-acme", ok: false},
		{input: "acme-", ok: false},
		{input: "account", ok: false},
		{input: "has spaces", ok: false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := NormalizeSlug(test.input)
			if (err == nil) != test.ok {
				t.Fatalf("NormalizeSlug() error = %v, ok = %v", err, test.ok)
			}
			if got != test.want {
				t.Fatalf("NormalizeSlug() = %q, want %q", got, test.want)
			}
		})
	}
}
