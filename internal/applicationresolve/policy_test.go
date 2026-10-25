package applicationresolve_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/testkernel"
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
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "go.mod"), strings.Replace(string(module), "../kernel", filepath.ToSlash(testkernel.Root(t)), 1)+"\nrequire go.yaml.in/yaml/v3 v3.0.5\n")
	sums, err := os.ReadFile(filepath.Join("..", "..", "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "go.sum"), string(sums))
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

func TestResolveExecutablePoliciesRejectsMissingSelectedKernelRuntime(t *testing.T) {
	root := writeResolvedInterfaceProject(t)
	options := applicationresolve.Options{
		Start: root, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"}),
		RequireExecutablePolicies: true,
	}
	before := snapshotTree(t, filepath.Dir(root))
	_, err := applicationresolve.Resolve(t.Context(), options)
	var policy *applicationresolve.PolicyNotEnforcedError
	if !errors.As(err, &policy) || policy.InterfaceID().String() != "audit.write/v1" || policy.Field() != "timeout" ||
		policy.Support().Generated() != commandschema.SupportYes || policy.Support().Executed() != commandschema.SupportNo ||
		policy.KernelVersion() != "v0.0.0 (local replacement)" || strings.Contains(policy.Error(), filepath.Dir(root)) {
		t.Fatalf("missing selected runtime = %v", err)
	}
	options.RequireExecutablePolicies = false
	if _, err := applicationresolve.Resolve(t.Context(), options); err != nil {
		t.Fatalf("inspection of unsupported policy = %v", err)
	}
	if !reflect.DeepEqual(snapshotTree(t, filepath.Dir(root)), before) {
		t.Fatal("unsupported policy validation changed inputs")
	}
}

func TestResolveExecutablePoliciesChecksSelectedExportsWithoutExecutingKernel(t *testing.T) {
	const compatible = `package invocation
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
func init() { panic("PRIVATE_KERNEL_INIT_MUST_NOT_RUN") }
`
	for _, test := range []struct {
		name, source, kernel             string
		workspace, unverified, supported bool
	}{
		{name: "compatible local replacement", source: compatible, kernel: "v0.0.0 (local replacement)", supported: true},
		{name: "compatible workspace checksums", source: strings.Replace(compatible, `import "time"`, "import (\"time\"; _ \"go.yaml.in/yaml/v3\")", 1), workspace: true, supported: true},
		{name: "compatible workspace replacement", source: compatible, workspace: true, supported: true},
		{name: "incompatible local replacement", source: strings.Replace(compatible, "PolicyCompilerVersion = 1", "PolicyCompilerVersion = 2", 1), kernel: "v0.0.0 (local replacement)"},
		{name: "incompatible workspace", source: strings.Replace(compatible, "PolicyDefaultsVersion = 1", "PolicyDefaultsVersion = 2", 1), kernel: "workspace", workspace: true},
		{name: "missing constant", source: strings.Replace(compatible, "PolicySchemaVersion = 1", "OtherVersion = 1", 1), kernel: "v0.0.0 (local replacement)"},
		{name: "missing policy representation", source: strings.Replace(compatible, "type Policy struct", "type Other struct", 1), kernel: "v0.0.0 (local replacement)"},
		{name: "excluded target source", source: "//go:build plystra_policy_never\n\npackage invocation\n", kernel: "v0.0.0 (local replacement)"},
		{name: "documentation only", source: "", kernel: "v0.0.0 (local replacement)"},
		{name: "failed compilation", source: "package invocation\nvar _ = PRIVATE_UNDEFINED_VALUE\n", unverified: true},
		{name: "invalid and excluded sources", source: "//go:build (\n\npackage invocation\n", unverified: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResolvedInterfaceProject(t)
			parent := filepath.Dir(root)
			name := "policy.go"
			if test.name == "documentation only" {
				name = "README.md"
			}
			writeFile(t, filepath.Join(parent, "kernel", "invocation", name), test.source)
			if test.name == "invalid and excluded sources" {
				writeFile(t, filepath.Join(parent, "kernel", "invocation", "ignored.go"), "//go:build plystra_policy_never\n\npackage invocation\n")
			}
			if test.name == "compatible workspace checksums" {
				writeFile(t, filepath.Join(parent, "kernel", "go.mod"), "module github.com/plystra/kernel\ngo 1.26\nrequire go.yaml.in/yaml/v3 v3.0.5\n")
			}
			environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"})
			if test.workspace {
				work := filepath.Join(parent, "go.work")
				writeFile(t, work, "go 1.26\nuse (\n./application\n./kernel\n./cache\n)\n")
				if test.name == "compatible workspace replacement" {
					writeFile(t, work, "go 1.26\nuse (\n./application\n./cache\n)\nreplace github.com/plystra/kernel => ./kernel\n")
				}
				environment = goEnvironment(map[string]string{"GOWORK": work, "GOPROXY": "off", "GOSUMDB": "off", "GOFLAGS": "-mod=readonly"})
			}
			before := snapshotTree(t, parent)
			_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: environment, RequireExecutablePolicies: true})
			var policy *applicationresolve.PolicyNotEnforcedError
			switch {
			case test.unverified:
				if !errors.Is(err, applicationresolve.ErrPolicySupportUnverified) || errors.Is(err, applicationresolve.ErrPolicyNotEnforced) || strings.Contains(err.Error(), "PRIVATE_UNDEFINED_VALUE") {
					t.Fatalf("unverified runtime = %v", err)
				}
			case test.supported:
				if err != nil {
					t.Fatal(err)
				}
			default:
				if !errors.As(err, &policy) || policy.KernelVersion() != test.kernel || strings.Contains(err.Error(), parent) {
					t.Fatalf("selected runtime = %v; want Kernel %q", err, test.kernel)
				}
			}
			if !reflect.DeepEqual(snapshotTree(t, parent), before) {
				t.Fatal("selected export verification changed Project or dependency inputs")
			}
			if test.name == "compatible workspace checksums" {
				control := exec.CommandContext(t.Context(), "go", "list", "-deps", "-export", "-e", "-mod=readonly", "github.com/plystra/kernel/invocation")
				control.Dir, control.Env = root, environment
				if output, err := control.CombinedOutput(); err != nil {
					t.Fatalf("workspace checksum control: %v\n%s", err, output)
				}
				data, err := os.ReadFile(filepath.Join(parent, "go.work.sum"))
				if err != nil || len(data) == 0 {
					t.Fatalf("control did not exercise authored workspace checksum writes: %v", err)
				}
			}
		})
	}
}

func TestPolicyNotEnforcedErrorNilAccessors(t *testing.T) {
	var policy *applicationresolve.PolicyNotEnforcedError
	if policy.InterfaceID().String() != "" || policy.Field() != "" || policy.Support().Valid() || policy.Sources() != nil ||
		policy.Error() != applicationresolve.ErrPolicyNotEnforced.Error() || !errors.Is(policy, applicationresolve.ErrPolicyNotEnforced) {
		t.Fatal("nil policy error accessors are inconsistent")
	}
}
