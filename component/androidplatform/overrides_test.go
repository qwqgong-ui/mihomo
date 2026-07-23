package androidplatform

import "testing"

func TestParseTunStackOverride(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		expected  TunStackOverride
		wantError bool
	}{
		{name: "empty follows config", value: "", expected: TunStackOverrideConfig},
		{name: "explicit config", value: "config", expected: TunStackOverrideConfig},
		{name: "gvisor", value: "gVisOr", expected: TunStackOverrideGVisor},
		{name: "system unavailable", value: "system", wantError: true},
		{name: "unknown", value: "mixed", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := ParseTunStackOverride(test.value)
			if test.wantError {
				if err == nil {
					t.Fatalf("expected error for %q", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTunStackOverride(%q): %v", test.value, err)
			}
			if actual != test.expected {
				t.Fatalf("ParseTunStackOverride(%q) = %q, want %q", test.value, actual, test.expected)
			}
		})
	}
}
