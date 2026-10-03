package applicationmeta_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/invocationpolicy"
)

func TestParseRetryPolicyDefaultsAndBounds(t *testing.T) {
	for _, test := range []struct {
		name, fields string
		attempts     int
		backoff      time.Duration
	}{
		{"absent", "", 1, 0},
		{"defaults", ", retry: {eligibility: replay_safe}", 2, 0},
		{"explicit", ", retry: {eligibility: replay_safe, max_attempts: 3, backoff: 25ms}", 3, 25 * time.Millisecond},
		{"bounds", ", retry: {eligibility: replay_safe, max_attempts: 16, backoff: 2562047h47m16.854775807s}", 16, time.Duration(1<<63 - 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := composeManifest(t, "interfaces: {policies: {email.send/v1: {timeout: 5s"+test.fields+"}}}\n")
			policy := manifest.InterfacePolicies()[0]
			compiled := invocationpolicy.Compile(policy)
			if policy.RetryMaxAttempts() != test.attempts || policy.RetryBackoff() != test.backoff || compiled.Validate() != nil || compiled.Retry.MaxAttempts != test.attempts || compiled.Retry.Backoff != test.backoff {
				t.Fatalf("retry policy = %#v, compiled %#v", policy, compiled)
			}
			if (policy.RetryEligibility() == "replay_safe") != (test.attempts > 1) || policy.Source() != `plystra.yaml interfaces.policies["email.send/v1"]` {
				t.Fatalf("assertion or whole-entry source lost: %#v", policy)
			}
		})
	}
}

func TestParseRetryPolicyRejectsInvalidForms(t *testing.T) {
	for _, retry := range []string{
		"null", "2", "[]", "{}", "{max_attempts: 2}", "{eligibility: true}", "{eligibility: idempotent}",
		"{eligibility: replay_safe, extra: true}",
		"{eligibility: replay_safe, max_attempts: 0}", "{eligibility: replay_safe, max_attempts: 1}",
		"{eligibility: replay_safe, max_attempts: 17}", "{eligibility: replay_safe, max_attempts: -2}",
		"{eligibility: replay_safe, max_attempts: '2'}", "{eligibility: replay_safe, max_attempts: 2.0}",
		"{eligibility: replay_safe, max_attempts: 0x2}", "{eligibility: replay_safe, max_attempts: 1_0}",
		"{eligibility: replay_safe, max_attempts: 99999999999999999999999999}",
		"{eligibility: replay_safe, backoff: -1ns}", "{eligibility: replay_safe, backoff: 0}",
		"{eligibility: replay_safe, backoff: ' 0s'}", "{eligibility: replay_safe, backoff: 2562047h47m16.854775808s}",
		"{eligibility: replay_safe, backoff: '" + strings.Repeat("0", 65) + "s'}",
		`{eligibility: replay_safe, backoff: "0s\0"}`,
	} {
		t.Run(retry, func(t *testing.T) {
			_, err := applicationmeta.Parse([]byte("interfaces: {policies: {email.send/v1: {timeout: 5s, retry: " + retry + "}}}\n"))
			if !errors.Is(err, applicationmeta.ErrInvalidManifest) {
				t.Fatalf("accepted retry %s: %v", retry, err)
			}
		})
	}
	if _, err := applicationmeta.Parse([]byte("interfaces: {policies: {email.send/v1: {retry: {eligibility: replay_safe}}}}\n")); !errors.Is(err, applicationmeta.ErrInvalidManifest) {
		t.Fatalf("retry without timeout = %v", err)
	}
}

func TestComposeAndMaintainRetryAsOnePolicyEntry(t *testing.T) {
	config := func(fields string) applicationmeta.Manifest {
		return composeManifest(t, "interfaces: {policies: {email.send/v1: {timeout: 5s, retry: {"+fields+"}}}}\n")
	}
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: config("eligibility: replay_safe")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.0.0", Manifest: config("backoff: 0ms, max_attempts: 2, eligibility: replay_safe")},
	}
	base, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	path := `interfaces.policies["email.send/v1"]`
	if evidence := findProvenance(t, base.Provenance(), path); len(evidence) != 1 || len(evidence[0].Sources()) != 2 {
		t.Fatalf("normalized retry did not deduplicate: %#v", evidence)
	}
	for _, fields := range []string{"eligibility: replay_safe, max_attempts: 3", "eligibility: replay_safe, backoff: 1ns"} {
		dependencies[1].Manifest = config(fields)
		if _, err := applicationmeta.Compose(dependencies, composeManifest(t, "{}\n"), composeSchemaLookup(nil)); err != nil {
			t.Fatalf("ordered retry failed: %v", err)
		}
		current := composeManifest(t, "interfaces: {policies: {email.send/v1: {timeout: 2s}}}\n")
		resolved, err := applicationmeta.Compose(dependencies, current, composeSchemaLookup(nil))
		if err != nil || resolved.Manifest().InterfacePolicies()[0].RetryMaxAttempts() != 1 {
			t.Fatalf("whole-entry override retained retry: %v", err)
		}
	}
	dependencies = dependencies[:1]
	maintained, err := applicationmeta.MaintainDependencyConfiguration([]byte("{}\n"), applicationmeta.DependencyBaseline{}, nil, dependencies, composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(maintained.Data(), []byte("{}\n")) {
		t.Fatal("inherited retry was materialized")
	}
	composition, err := applicationmeta.Compose(dependencies, composeManifest(t, string(maintained.Data())), composeSchemaLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	local := []byte("interfaces: {policies: {email.send/v1: {timeout: 5s, retry: {eligibility: replay_safe, max_attempts: 3}}}}\n")
	dependencies[0].Manifest = config("eligibility: replay_safe, max_attempts: 4")
	updated, err := applicationmeta.MaintainDependencyConfiguration(local, composition.DependencyBaseline(), maintained.LocalPaths(), dependencies, composeSchemaLookup(nil))
	if err != nil || !bytes.Equal(updated.Data(), local) {
		t.Fatalf("maintenance replaced local retry: %v\n%s", err, updated.Data())
	}
}

func FuzzRetryPolicyCompilation(f *testing.F) {
	for _, seed := range []struct {
		attempts uint8
		backoff  int64
	}{{2, 0}, {16, 1}, {1, -1}, {17, 1<<63 - 1}} {
		f.Add(seed.attempts, seed.backoff)
	}
	f.Fuzz(func(t *testing.T, attempts uint8, backoff int64) {
		data := fmt.Sprintf("interfaces: {policies: {email.send/v1: {timeout: 1s, retry: {eligibility: replay_safe, max_attempts: %d, backoff: %dns}}}}\n", attempts, backoff)
		manifest, err := applicationmeta.Parse([]byte(data))
		valid := attempts >= 2 && attempts <= 16 && backoff >= 0
		if (err == nil) != valid {
			t.Fatalf("parse = %v for %s", err, data)
		}
		if valid && invocationpolicy.Compile(manifest.InterfacePolicies()[0]).Validate() != nil {
			t.Fatal("parsed policy cannot compile")
		}
	})
}
