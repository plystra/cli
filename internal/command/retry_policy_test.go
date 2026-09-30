package command_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/invocationpolicy"
)

func TestPublicGenerationCompilesRetryPoliciesAcrossSelections(t *testing.T) {
	root := writeCommandPolicyProject(t)
	dependency := t.TempDir()
	writeCommandFile(t, filepath.Join(dependency, "go.mod"), "module example.com/policy-export\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(dependency, "plystra.yaml"), "composition: {exports: {defaults: {interfaces: {policies: {email.send/v1: {timeout: 5s, retry: {eligibility: replay_safe}}}}}}}\n")
	mod := string(readCommandFile(t, root, "go.mod"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), mod+"\nrequire example.com/policy-export v1.0.0\nreplace example.com/policy-export => "+filepath.ToSlash(dependency)+"\n")
	configuration := "composition: {adopt: [{module: example.com/policy-export, export: defaults}]}\ninterfaces: {require: [email.send/v1]}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), configuration)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces: {policies: {email.send/v1: {timeout: 5s, retry: {eligibility: replay_safe, max_attempts: 3, backoff: 25ms}}}}\n")
	writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), "interfaces: {require: [email.send/v1], policies: {email.send/v1: {timeout: 5s, retry: {eligibility: replay_safe, max_attempts: 16}}}}\n")
	dependencyBefore := commandTree(t, dependency)
	digests := make(map[string]string)
	for _, mode := range []struct {
		name, sameAs string
		selector     []string
		environment  map[string]string
		attempts     int
		backoff      time.Duration
	}{
		{name: "adopted defaults", attempts: 2},
		{name: "environment", selector: []string{"--env", "production"}, attempts: 3, backoff: 25 * time.Millisecond},
		{name: "replacement", selector: []string{"--config", "deploy/customer.yaml"}, attempts: 16},
		{name: "ambient environment", sameAs: "environment", environment: map[string]string{"PLYSTRA_ENV": "production"}, attempts: 3, backoff: 25 * time.Millisecond},
		{name: "ambient replacement", sameAs: "replacement", environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"}, attempts: 16},
		{name: "explicit override", sameAs: "replacement", selector: []string{"--config", "deploy/customer.yaml"}, environment: map[string]string{"PLYSTRA_ENV": "missing", "PLYSTRA_CONFIG": "missing.yaml"}, attempts: 16},
	} {
		t.Run(mode.name, func(t *testing.T) {
			want := invocationpolicy.Default()
			want.Timeout = 5 * time.Second
			want.Retry = invocationpolicy.Retry{Eligibility: "replay_safe", MaxAttempts: mode.attempts, Backoff: mode.backoff}
			assertCommandInvocationPolicy(t, root, mode.selector, commandGoEnvironmentWith(mode.environment), want)
			manifest, err := applicationgen.DecodeManifestProvenance(readCommandFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			digest := manifest.ApplicationModelDigest()
			if mode.sameAs != "" {
				if digest != digests[mode.sameAs] {
					t.Fatal("equivalent selector changed retry identity")
				}
			} else {
				for _, previous := range digests {
					if previous == digest {
						t.Fatal("changed retry did not change executable identity")
					}
				}
				digests[mode.name] = digest
			}
		})
	}
	// Root ownership replaces the entire adopted entry, including its retry.
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(configuration, "require: [email.send/v1]", "require: [email.send/v1], policies: {email.send/v1: {timeout: 5s}}", 1))
	assertCommandTimeoutPolicy(t, root, nil, commandGoEnvironment(), 5*time.Second)
	if !reflect.DeepEqual(commandTree(t, dependency), dependencyBefore) {
		t.Fatal("retry generation changed adopted source")
	}
}
