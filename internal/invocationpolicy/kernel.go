package invocationpolicy

import (
	"go/constant"
	"go/types"
)

// KernelSupport is the selected runtime's exported compiled-policy contract.
type KernelSupport struct {
	compatible  bool
	duration    int64
	concurrency int64
	attempts    int64
	replaySafe  string
}

// ReadKernelSupport inspects compiler constants without running application code.
func ReadKernelSupport(pkg *types.Package) KernelSupport {
	if pkg == nil || pkg.Path() != "github.com/plystra/kernel/invocation" || !validKernelPolicyTypes(pkg) {
		return KernelSupport{}
	}
	integer := func(name string) (int64, bool) {
		value, ok := pkg.Scope().Lookup(name).(*types.Const)
		if !ok || value.Val().Kind() != constant.Int {
			return 0, false
		}
		return constant.Int64Val(value.Val())
	}
	defaults := Default()
	for name, expected := range map[string]int64{
		"PolicySchemaVersion":     int64(defaults.SchemaVersion),
		"PolicyCompilerVersion":   int64(defaults.CompilerVersion),
		"PolicyDefaultsVersion":   int64(defaults.DefaultsVersion),
		"DefaultConcurrencyLimit": int64(defaults.ConcurrencyLimit),
	} {
		if value, ok := integer(name); !ok || value != expected {
			return KernelSupport{}
		}
	}
	duration, durationOK := integer("MaximumPolicyDuration")
	concurrency, concurrencyOK := integer("MaximumConcurrencyLimit")
	attempts, attemptsOK := integer("MaximumRetryAttempts")
	replay, replayOK := pkg.Scope().Lookup("RetryReplaySafe").(*types.Const)
	if !durationOK || duration <= 0 || !concurrencyOK || concurrency < int64(defaults.ConcurrencyLimit) || !attemptsOK || attempts < 1 || !replayOK || replay.Val().Kind() != constant.String {
		return KernelSupport{}
	}
	return KernelSupport{compatible: true, duration: duration, concurrency: concurrency, attempts: attempts, replaySafe: constant.StringVal(replay.Val())}
}

func validKernelPolicyTypes(pkg *types.Package) bool {
	for name, fields := range map[string]map[string]string{
		"Policy":        {"SchemaVersion": "int", "CompilerVersion": "int", "DefaultsVersion": "int", "Timeout": "duration", "ConcurrencyLimit": "int", "QueueLimit": "int", "Retry": "RetryPolicy", "Circuit": "CircuitPolicy"},
		"RetryPolicy":   {"Eligibility": "string", "MaxAttempts": "int", "Backoff": "duration"},
		"CircuitPolicy": {"FailureThreshold": "int", "OpenFor": "duration", "ProbeLimit": "int"},
	} {
		object, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok || object.IsAlias() {
			return false
		}
		named, ok := object.Type().(*types.Named)
		if !ok || named.TypeParams().Len() != 0 {
			return false
		}
		structure, ok := named.Underlying().(*types.Struct)
		if !ok || structure.NumFields() != len(fields) {
			return false
		}
		for index := range structure.NumFields() {
			field := structure.Field(index)
			expected := fields[field.Name()]
			if field.Embedded() || expected == "" {
				return false
			}
			switch expected {
			case "int":
				if !types.Identical(field.Type(), types.Typ[types.Int]) {
					return false
				}
			case "string":
				if !types.Identical(field.Type(), types.Typ[types.String]) {
					return false
				}
			case "duration":
				named, ok := types.Unalias(field.Type()).(*types.Named)
				if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "time" || named.Obj().Name() != "Duration" {
					return false
				}
			default:
				object := pkg.Scope().Lookup(expected)
				if object == nil || !types.Identical(field.Type(), object.Type()) {
					return false
				}
			}
		}
	}
	return true
}

// UnsupportedField returns the first normalized field the selected Kernel
// cannot enforce, or an empty string when the complete policy is compatible.
func (s KernelSupport) UnsupportedField(policy Policy) string {
	if !s.compatible || policy.Validate() != nil || int64(policy.Timeout) > s.duration || int64(policy.ConcurrencyLimit) > s.concurrency {
		return "timeout"
	}
	if policy.Retry.Eligibility != "" && policy.Retry.Eligibility != s.replaySafe {
		return "retry.eligibility"
	}
	if int64(policy.Retry.MaxAttempts) > s.attempts {
		return "retry.max_attempts"
	}
	if int64(policy.Retry.Backoff) > s.duration {
		return "retry.backoff"
	}
	return ""
}
