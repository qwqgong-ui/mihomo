package common

import (
	"path/filepath"
	"strings"

	"github.com/metacubex/mihomo/component/wildcard"
	C "github.com/metacubex/mihomo/constant"

	"github.com/dlclark/regexp2"
)

type Process struct {
	Base
	pattern  string
	adapter  string
	ruleType C.RuleType
	regexp   *regexp2.Regexp
}

func (ps *Process) Payload() string {
	return ps.pattern
}

func (ps *Process) Adapter() string {
	return ps.adapter
}

func (ps *Process) RuleType() C.RuleType {
	return ps.ruleType
}

func (ps *Process) Match(metadata *C.Metadata, helper C.RuleMatchHelper) (bool, string) {
	if helper.FindProcess != nil {
		helper.FindProcess()
	}
	var target string
	switch ps.ruleType {
	case C.ProcessName, C.ProcessNameRegex, C.ProcessNameWildcard:
		target = metadata.Process
	default:
		target = metadata.ProcessPath
	}
	return ps.matchTarget(target), ps.adapter
}

func (ps *Process) matchTarget(target string) bool {
	switch ps.ruleType {
	case C.ProcessNameRegex, C.ProcessPathRegex:
		match, _ := ps.regexp.MatchString(target)
		return match
	case C.ProcessNameWildcard, C.ProcessPathWildcard:
		return wildcard.Match(strings.ToLower(ps.pattern), strings.ToLower(target))
	default:
		return strings.EqualFold(target, ps.pattern)
	}
}

func (ps *Process) HasProcessRule() bool { return true }

// MatchProcess checks an executable path against this process rule without
// resolving socket ownership. Linux uses it to avoid scanning fd directories
// of processes that cannot possibly match the rule.
func (ps *Process) MatchProcess(processPath string) bool {
	target := processPath
	switch ps.ruleType {
	case C.ProcessName, C.ProcessNameRegex, C.ProcessNameWildcard:
		target = filepath.Base(processPath)
	}
	return ps.matchTarget(target)
}

// MatchProcessName checks an already resolved Android package/process name.
// Path rules conservatively return true so they retain the old path lookup.
func (ps *Process) MatchProcessName(name string) bool {
	switch ps.ruleType {
	case C.ProcessName, C.ProcessNameRegex, C.ProcessNameWildcard:
		return ps.matchTarget(name)
	default:
		return true
	}
}

func NewProcess(pattern string, adapter string, ruleType C.RuleType) (*Process, error) {
	ps := &Process{
		Base:     Base{},
		pattern:  pattern,
		adapter:  adapter,
		ruleType: ruleType,
	}
	switch ps.ruleType {
	case C.ProcessNameRegex, C.ProcessPathRegex:
		r, err := regexp2.Compile(pattern, regexp2.IgnoreCase)
		if err != nil {
			return nil, err
		}
		ps.regexp = r
	default:
	}
	return ps, nil
}

var _ C.Rule = (*Process)(nil)
