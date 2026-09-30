package invocationpolicy_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/invocationpolicy"
	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

func TestCompiledPolicyMatchesKernelValidation(t *testing.T) {
	endpoint, err := invocation.NewEndpoint(capability.MustParseContract[struct{}, struct{}]("test.policy/v1"), func(context.Context, struct{}) (struct{}, error) { return struct{}{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	build, err := invocation.NewModuleBuild("example.com/policy", "v1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		edit  func(*invocationpolicy.Policy)
		valid bool
	}{
		{"default", func(*invocationpolicy.Policy) {}, true},
		{"maximum timeout", func(p *invocationpolicy.Policy) { p.Timeout = invocation.MaximumPolicyDuration }, true},
		{"negative timeout", func(p *invocationpolicy.Policy) { p.Timeout = -1 }, false},
		{"schema", func(p *invocationpolicy.Policy) { p.SchemaVersion++ }, false},
		{"compiler", func(p *invocationpolicy.Policy) { p.CompilerVersion++ }, false},
		{"defaults", func(p *invocationpolicy.Policy) { p.DefaultsVersion++ }, false},
		{"zero limit", func(p *invocationpolicy.Policy) { p.ConcurrencyLimit = 0 }, false},
		{"maximum limit", func(p *invocationpolicy.Policy) { p.ConcurrencyLimit = invocation.MaximumConcurrencyLimit }, true},
		{"excessive limit", func(p *invocationpolicy.Policy) { p.ConcurrencyLimit = invocation.MaximumConcurrencyLimit + 1 }, false},
		{"queue", func(p *invocationpolicy.Policy) { p.QueueLimit = 1; p.Timeout = time.Second }, false},
		{"circuit", func(p *invocationpolicy.Policy) {
			p.Circuit = invocationpolicy.Circuit{FailureThreshold: 1, OpenFor: time.Second, ProbeLimit: 1}
		}, false},
		{"zero attempts", func(p *invocationpolicy.Policy) { p.Retry.MaxAttempts = 0 }, false},
		{"retry without assertion", func(p *invocationpolicy.Policy) { p.Retry.MaxAttempts = 2; p.Timeout = time.Second }, false},
		{"retry without timeout", func(p *invocationpolicy.Policy) {
			p.Retry = invocationpolicy.Retry{Eligibility: invocation.RetryReplaySafe, MaxAttempts: 2}
		}, false},
		{"retry", func(p *invocationpolicy.Policy) {
			p.Timeout = time.Second
			p.Retry = invocationpolicy.Retry{Eligibility: invocation.RetryReplaySafe, MaxAttempts: invocation.MaximumRetryAttempts, Backoff: time.Millisecond}
		}, true},
		{"disabled backoff", func(p *invocationpolicy.Policy) { p.Retry.Backoff = 1 }, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := invocationpolicy.Default()
			test.edit(&p)
			source, err := p.Source()
			if (err == nil) != test.valid || (p.Validate() == nil) != test.valid || (!test.valid && (!errors.Is(err, invocationpolicy.ErrInvalid) || source != "")) {
				t.Fatalf("compiled policy: %q, %v", source, err)
			}
			_, err = invocation.NewBinding(invocation.BindingOptions{
				Policy: invocation.Policy{SchemaVersion: p.SchemaVersion, CompilerVersion: p.CompilerVersion, DefaultsVersion: p.DefaultsVersion, Timeout: p.Timeout, ConcurrencyLimit: p.ConcurrencyLimit, QueueLimit: p.QueueLimit, Retry: invocation.RetryPolicy{Eligibility: p.Retry.Eligibility, MaxAttempts: p.Retry.MaxAttempts, Backoff: p.Retry.Backoff}, Circuit: invocation.CircuitPolicy{FailureThreshold: p.Circuit.FailureThreshold, OpenFor: p.Circuit.OpenFor, ProbeLimit: p.Circuit.ProbeLimit}},
				Kind:   invocation.BindingKindImplementation, Constructor: "example.com/policy.New", ModuleBuild: build,
				SelectionReason: invocation.SelectionReasonUniqueCompatible, ContractDigest: sha256.Sum256([]byte("test.policy/v1")),
			}, endpoint)
			if (err == nil) != test.valid {
				t.Fatalf("CLI and Kernel policy validation diverge: %v", err)
			}
		})
	}
}

func TestCompiledDefaultsHaveExplicitLiteralIdentity(t *testing.T) {
	policy := invocationpolicy.Default()
	if policy.Timeout != 0 || policy.ConcurrencyLimit != 64 || policy.QueueLimit != 0 || policy.Retry != (invocationpolicy.Retry{MaxAttempts: 1}) || policy.Circuit != (invocationpolicy.Circuit{}) {
		t.Fatalf("absence defaults = %#v", policy)
	}
	source, err := policy.Source()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"SchemaVersion: 1", "CompilerVersion: 1", "DefaultsVersion: 1", "Timeout: 0", "ConcurrencyLimit: 64", "QueueLimit: 0", `Eligibility: ""`, "MaxAttempts: 1", "Backoff: 0", "FailureThreshold: 0", "OpenFor: 0", "ProbeLimit: 0"} {
		if !strings.Contains(source, field) {
			t.Fatalf("compiled literal omitted %s", field)
		}
	}
	if strings.Contains(source, "kernelinvocation.PolicySchemaVersion") {
		t.Fatal("runtime constants replaced frozen identity")
	}
}
