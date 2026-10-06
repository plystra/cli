package applicationresolve_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/commandschema"
)

func TestResolveExecutablePoliciesRejectsLegacyButAllowsDormantIntent(t *testing.T) {
	root := t.TempDir()
	writeModule(t, root, "example.com/legacy-policy")
	writePlugin(t, root, "business", "id: acme.business\nprovides: [audit.write/v1]\n")
	writeCapability(t, root, "business", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\n")
	configuration := "capabilities: {require: [audit.write/v1]}\ninterfaces: {policies: {audit.write/v1: {timeout: 5s}}}\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), configuration)
	options := applicationresolve.Options{
		Start:                     root,
		Environment:               goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"}),
		RequireExecutablePolicies: true,
	}
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), options)
	var policy *applicationresolve.PolicyNotEnforcedError
	if !errors.Is(err, applicationresolve.ErrPolicyNotEnforced) || !errors.As(err, &policy) {
		t.Fatalf("legacy executable policy = %v", err)
	}
	if policy.InterfaceID().String() != "audit.write/v1" || policy.Field() != "timeout" ||
		policy.Support().ID() != "legacy.capability-timeout" || policy.Support().Executed() != commandschema.SupportNo {
		t.Fatalf("legacy support = %#v", policy)
	}
	sources := policy.Sources()
	if len(sources) != 1 || sources[0].ModulePath != "example.com/legacy-policy" || sources[0].Path != "plystra.yaml" {
		t.Fatalf("sources = %#v", sources)
	}
	sources[0].Path = "changed"
	if policy.Sources()[0].Path != "plystra.yaml" {
		t.Fatal("sources were not defensively copied")
	}
	options.RequireExecutablePolicies = false
	if _, err := applicationresolve.Resolve(t.Context(), options); err != nil {
		t.Fatalf("read-only legacy policy = %v", err)
	}
	if !reflect.DeepEqual(snapshotTree(t, root), before) {
		t.Fatal("resolution mutated the Project")
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(configuration, "require: [audit.write/v1]", "require: []", 1))
	options.RequireExecutablePolicies = true
	if _, err := applicationresolve.Resolve(t.Context(), options); err != nil {
		t.Fatalf("dormant legacy policy = %v", err)
	}
}

func TestResolveExecutablePoliciesAcceptsReachableDependencies(t *testing.T) {
	root := writeResolvedInterfaceProject(t)
	options := applicationresolve.Options{
		Start:                     root,
		Environment:               goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"}),
		RequireExecutablePolicies: true,
	}
	before := snapshotTree(t, filepath.Dir(root))
	for range 2 {
		resolved, err := applicationresolve.Resolve(t.Context(), options)
		if err != nil {
			t.Fatalf("executable policy resolution = %v", err)
		}
		policies := resolved.Manifest().InterfacePolicies()
		if len(policies) != 1 || policies[0].InterfaceID().String() != "audit.write/v1" || policies[0].Timeout() != 5*time.Second {
			t.Fatalf("resolved policies = %#v", policies)
		}
	}
	options.RequireExecutablePolicies = false
	if _, err := applicationresolve.Resolve(t.Context(), options); err != nil {
		t.Fatalf("read-only resolution = %v", err)
	}
	if after := snapshotTree(t, filepath.Dir(root)); !reflect.DeepEqual(after, before) {
		t.Fatal("resolution changed the Project or its dependencies")
	}
}

func TestPolicyNotEnforcedErrorNilAccessors(t *testing.T) {
	var policy *applicationresolve.PolicyNotEnforcedError
	if policy.InterfaceID().String() != "" || policy.Field() != "" || policy.Support().Valid() || policy.Sources() != nil ||
		policy.Error() != applicationresolve.ErrPolicyNotEnforced.Error() || !errors.Is(policy, applicationresolve.ErrPolicyNotEnforced) {
		t.Fatal("nil policy error accessors are inconsistent")
	}
}
