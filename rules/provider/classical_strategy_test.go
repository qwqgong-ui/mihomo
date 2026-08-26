package provider

import (
	"fmt"
	"testing"

	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/rules/common"
)

func TestClassicalStrategyProcessCandidates(t *testing.T) {
	strategy := NewClassicalStrategy(func(tp, payload, target string, _ []string, _ map[string][]C.Rule) (C.Rule, error) {
		switch tp {
		case "PROCESS-NAME":
			return common.NewProcess(payload, target, C.ProcessName)
		case "DOMAIN":
			return common.NewDomain(payload, target), nil
		default:
			return nil, fmt.Errorf("unsupported test rule %s", tp)
		}
	})
	strategy.Insert("DOMAIN,example.com")
	strategy.Insert("PROCESS-NAME,curl")
	strategy.Insert("PROCESS-NAME,wget")

	if !strategy.HasProcessRule() {
		t.Fatal("strategy did not report its process rules")
	}
	if !strategy.MatchProcess("/usr/bin/wget") {
		t.Fatal("strategy did not include a later process rule in its candidate union")
	}
	if strategy.MatchProcess("/usr/bin/chromium") {
		t.Fatal("strategy matched a process absent from its rules")
	}
	if !strategy.MatchProcessName("curl") || strategy.MatchProcessName("chromium") {
		t.Fatal("strategy package candidates do not match the process rule union")
	}
}
