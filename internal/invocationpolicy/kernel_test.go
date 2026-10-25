package invocationpolicy_test

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/invocationpolicy"
)

func TestSelectedKernelPolicyContract(t *testing.T) {
	const contract = `package invocation
import "time"
type Policy struct {
SchemaVersion, CompilerVersion, DefaultsVersion int
Timeout time.Duration
ConcurrencyLimit, QueueLimit int
Retry RetryPolicy
Circuit CircuitPolicy
}
type RetryPolicy struct { Eligibility string; MaxAttempts int; Backoff time.Duration }
type CircuitPolicy struct { FailureThreshold int; OpenFor time.Duration; ProbeLimit int }
const (
PolicySchemaVersion = 1
PolicyCompilerVersion = 1
PolicyDefaultsVersion = 1
DefaultConcurrencyLimit = 64
MaximumConcurrencyLimit = 65536
MaximumPolicyDuration = 9223372036854775807
MaximumRetryAttempts = 16
RetryReplaySafe = "replay_safe"
)
`
	policy := invocationpolicy.Default()
	policy.Timeout = 5 * time.Second
	policy.Retry = invocationpolicy.Retry{Eligibility: "replay_safe", MaxAttempts: 3, Backoff: time.Second}
	for _, test := range []struct {
		name, old, replacement, field string
	}{
		{name: "compatible"},
		{name: "missing policy type", old: "type Policy struct", replacement: "type Other struct", field: "timeout"},
		{name: "generic policy type", old: "type Policy struct", replacement: "type Policy[T any] struct", field: "timeout"},
		{name: "wrong retry type", old: "Retry RetryPolicy", replacement: "Retry string", field: "timeout"},
		{name: "wrong duration type", old: "Timeout time.Duration", replacement: "Timeout int64", field: "timeout"},
		{name: "schema", old: "PolicySchemaVersion = 1", replacement: "PolicySchemaVersion = 2", field: "timeout"},
		{name: "compiler", old: "PolicyCompilerVersion = 1", replacement: "PolicyCompilerVersion = 2", field: "timeout"},
		{name: "defaults", old: "PolicyDefaultsVersion = 1", replacement: "PolicyDefaultsVersion = 2", field: "timeout"},
		{name: "default concurrency", old: "DefaultConcurrencyLimit = 64", replacement: "DefaultConcurrencyLimit = 32", field: "timeout"},
		{name: "missing", old: "PolicySchemaVersion = 1", replacement: "OtherVersion = 1", field: "timeout"},
		{name: "variable", old: "PolicySchemaVersion = 1", replacement: "", field: "timeout"},
		{name: "wrong kind", old: "PolicySchemaVersion = 1", replacement: "PolicySchemaVersion = \"1\"", field: "timeout"},
		{name: "overflow", old: "MaximumPolicyDuration = 9223372036854775807", replacement: "MaximumPolicyDuration = 1 << 80", field: "timeout"},
		{name: "timeout bound", old: "MaximumPolicyDuration = 9223372036854775807", replacement: "MaximumPolicyDuration = 4000000000", field: "timeout"},
		{name: "retry bound", old: "MaximumRetryAttempts = 16", replacement: "MaximumRetryAttempts = 2", field: "retry.max_attempts"},
		{name: "replay identity", old: `RetryReplaySafe = "replay_safe"`, replacement: `RetryReplaySafe = "other"`, field: "retry.eligibility"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := contract
			if test.old != "" {
				source = strings.Replace(source, test.old, test.replacement, 1)
			}
			if test.name == "variable" {
				source += "var PolicySchemaVersion = 1\n"
			}
			files := token.NewFileSet()
			parsed, err := parser.ParseFile(files, "policy.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			pkg, err := (&types.Config{Importer: importer.Default()}).Check("github.com/plystra/kernel/invocation", files, []*ast.File{parsed}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if field := invocationpolicy.ReadKernelSupport(pkg).UnsupportedField(policy); field != test.field {
				t.Fatalf("unsupported field = %q; want %q", field, test.field)
			}
		})
	}
	if invocationpolicy.ReadKernelSupport(nil).UnsupportedField(policy) != "timeout" {
		t.Fatal("nil runtime was accepted")
	}
}
