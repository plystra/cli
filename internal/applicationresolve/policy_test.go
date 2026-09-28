package applicationresolve_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/commandschema"
)

func TestResolveExecutablePoliciesRejectsReachableDependencies(t *testing.T) {
	root := writeResolvedInterfaceProject(t)
	options := applicationresolve.Options{
		Start:                     root,
		Environment:               goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"}),
		RequireExecutablePolicies: true,
	}
	before := snapshotTree(t, filepath.Dir(root))
	var previous string
	for range 2 {
		_, err := applicationresolve.Resolve(t.Context(), options)
		var policy *applicationresolve.PolicyNotEnforcedError
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, applicationresolve.ErrPolicyNotEnforced) || !errors.As(err, &policy) {
			t.Fatalf("policy error = %v", err)
		}
		if policy.InterfaceID().String() != "audit.write/v1" || policy.Field() != "timeout" ||
			policy.Support().Generated() != commandschema.SupportYes || policy.Support().Executed() != commandschema.SupportNo {
			t.Fatalf("policy facts = %v", policy)
		}
		sources := policy.Sources()
		if len(sources) != 1 || sources[0].ModulePath != "example.com/interface-app" || sources[0].Path != "plystra.yaml" {
			t.Fatalf("sources = %#v", sources)
		}
		sources[0].Path = "mutated"
		if policy.Sources()[0].Path != "plystra.yaml" {
			t.Fatal("policy exposes mutable sources")
		}
		if previous != "" && previous != err.Error() {
			t.Fatal("policy error is not deterministic")
		}
		previous = err.Error()
	}
	options.RequireExecutablePolicies = false
	if _, err := applicationresolve.Resolve(t.Context(), options); err != nil {
		t.Fatalf("read-only resolution = %v", err)
	}
	if after := snapshotTree(t, filepath.Dir(root)); !reflect.DeepEqual(after, before) {
		t.Fatal("resolution changed the Project or its dependencies")
	}
}

func TestResolveExecutablePoliciesPreservesAdoptedSourcesAndDormantIntent(t *testing.T) {
	root := writeResolvedInterfaceProject(t)
	dependency := filepath.Join(filepath.Dir(root), "cache", "plystra.yaml")
	writeFile(t, dependency, "composition:\n  exports:\n    defaults:\n      interfaces:\n        policies:\n          audit.write/v1: {timeout: 5s}\n")
	configuration := "composition:\n  adopt: [{module: example.com/interface-cache, export: defaults}]\ninterfaces:\n  require: [app.run/v1]\n  use: {audit.write/v1: example.com/interface-app/auditone.New}\n"
	writeFile(t, filepath.Join(root, "plystra.yaml"), configuration)
	options := applicationresolve.Options{
		Start:                     root,
		Environment:               goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"}),
		RequireExecutablePolicies: true,
	}
	before := snapshotTree(t, filepath.Dir(root))
	_, err := applicationresolve.Resolve(t.Context(), options)
	var policy *applicationresolve.PolicyNotEnforcedError
	if !errors.As(err, &policy) {
		t.Fatalf("adopted policy = %v", err)
	}
	sources := policy.Sources()
	if len(sources) != 1 || sources[0].ModulePath != "example.com/interface-cache" || sources[0].Path != "plystra.yaml" ||
		!strings.Contains(sources[0].Reference, "composition.exports") {
		t.Fatalf("adopted sources = %#v", sources)
	}
	if after := snapshotTree(t, filepath.Dir(root)); !reflect.DeepEqual(after, before) {
		t.Fatal("adopted policy failure changed input")
	}
	// Removing the root leaves the same adopted policy dormant.
	writeFile(t, filepath.Join(root, "plystra.yaml"), strings.Replace(configuration, "require: [app.run/v1]", "require: []", 1))
	if _, err := applicationresolve.Resolve(t.Context(), options); err != nil {
		t.Fatalf("dormant adopted policy = %v", err)
	}
	// The selected overlay can explicitly remove an active inherited policy.
	writeFile(t, filepath.Join(root, "plystra.yaml"), configuration)
	writeFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces:\n  policies: {audit.write/v1: null}\n")
	options.EnvironmentName = "production"
	if _, err := applicationresolve.Resolve(t.Context(), options); err != nil {
		t.Fatalf("removed adopted policy = %v", err)
	}
}

func TestPolicyNotEnforcedErrorNilAccessors(t *testing.T) {
	var policy *applicationresolve.PolicyNotEnforcedError
	if policy.InterfaceID().String() != "" || policy.Field() != "" || policy.Support().Valid() || policy.Sources() != nil ||
		policy.Error() != applicationresolve.ErrPolicyNotEnforced.Error() || !errors.Is(policy, applicationresolve.ErrPolicyNotEnforced) {
		t.Fatal("nil policy error accessors are inconsistent")
	}
}
