package common

import (
	"testing"

	C "github.com/metacubex/mihomo/constant"
)

func TestProcessMatchProcess(t *testing.T) {
	tests := []struct {
		name     string
		ruleType C.RuleType
		pattern  string
		path     string
		want     bool
	}{
		{name: "exact name", ruleType: C.ProcessName, pattern: "curl", path: "/usr/bin/curl", want: true},
		{name: "exact name mismatch", ruleType: C.ProcessName, pattern: "curl", path: "/usr/bin/wget", want: false},
		{name: "wildcard name", ruleType: C.ProcessNameWildcard, pattern: "chrom*", path: "/usr/bin/chromium", want: true},
		{name: "exact path", ruleType: C.ProcessPath, pattern: "/usr/bin/curl", path: "/usr/bin/curl", want: true},
		{name: "path regex", ruleType: C.ProcessPathRegex, pattern: `^/usr/(?:local/)?bin/curl$`, path: "/usr/bin/curl", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, err := NewProcess(tt.pattern, "DIRECT", tt.ruleType)
			if err != nil {
				t.Fatal(err)
			}
			if got := rule.MatchProcess(tt.path); got != tt.want {
				t.Fatalf("MatchProcess(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestProcessMatchProcessName(t *testing.T) {
	nameRule, err := NewProcess("com.tencent.mm", "DIRECT", C.ProcessName)
	if err != nil {
		t.Fatal(err)
	}
	if !nameRule.MatchProcessName("com.tencent.mm") || nameRule.MatchProcessName("com.example.other") {
		t.Fatal("Android package candidate matching does not follow PROCESS-NAME")
	}

	pathRule, err := NewProcess("/data/local/tmp/curl", "DIRECT", C.ProcessPath)
	if err != nil {
		t.Fatal(err)
	}
	if !pathRule.MatchProcessName("com.example.other") {
		t.Fatal("PROCESS-PATH must conservatively retain Android path lookup")
	}
}
