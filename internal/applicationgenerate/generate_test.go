package applicationgenerate_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
)

func TestGenerateChecksAndInstallsEmptyApplicationWithoutJavaScriptIdentity(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/Acme/empty")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "timeouts:\n  startup: 17s\n")
	environment := goEnvironment(nil)
	before := snapshotTree(t, root)

	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Generate check: %v", err)
	}
	if !checked.Checked() || checked.Module().Path() != root || checked.Module().ModulePath() != "example.com/Acme/empty" {
		t.Fatalf("checked result = %#v", checked)
	}
	if got, want := checked.Report().Missing(), []string{generatedfiles.ManifestPath, "generated/go/application/main_gen.go", "generated/go/assembly/compatibility_gen.go", "generated/go/assembly/invocations_gen.go", "generated/go/assembly/providers_gen.go", "generated/go/bootstrap/bootstrap_gen.go", "generated/manifest.json", "generated/proto/descriptor-set.pb", "generated/proto/wire-map.json"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("missing files = %v, want %v", got, want)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("check mode mutated application:\nbefore: %#v\nafter:  %#v", before, after)
	}

	installed, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate:    func(_ context.Context, _ string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Generate install: %v", err)
	}
	if installed.Checked() || !installed.Report().Clean() {
		t.Fatalf("installed result = checked %t, changes %#v", installed.Checked(), installed.Report().Changes())
	}
	applicationManifest := readFile(t, root, "generated/manifest.json")
	for _, required := range []string{
		`"capability_aliases":[]`,
		`"configuration":{"version":4,"mode":"default"`,
		`"root":{"path":"plystra.yaml","digest":"sha256:`,
		`"dependency_baselines":[{"mode":"default","path":"plystra.yaml"`,
		`"dependency_composition_digest":"sha256:`,
		`"dependency_baseline":[]`,
		`"protobuf_wire_map_digest":"sha256:`,
		`"application_model_digest":"sha256:`,
	} {
		if !bytes.Contains(applicationManifest, []byte(required)) {
			t.Fatalf("generated application manifest omits %q: %s", required, applicationManifest)
		}
	}
	assertFileExists(t, root, generatedfiles.ManifestPath)
	assertFileExists(t, root, "generated/go/application/main_gen.go")
	assertFileExists(t, root, "generated/go/assembly/compatibility_gen.go")
	assertFileExists(t, root, "generated/go/assembly/invocations_gen.go")
	assertFileExists(t, root, "generated/go/assembly/providers_gen.go")
	assertFileExists(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	assertFileExists(t, root, "generated/proto/descriptor-set.pb")
	assertFileExists(t, root, "generated/proto/wire-map.json")
	bootstrap := readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	if bytes.Contains(bootstrap, []byte("17s")) {
		t.Fatalf("generated bootstrap embeds application-specific startup timeout:\n%s", bootstrap)
	}
	for _, required := range [][]byte{
		[]byte(`defaultRuntimeDocument = "plystra.yaml"`),
		[]byte("func New(ctx context.Context, options RuntimeOptions)"),
		[]byte("compiledApplicationModelCompatibilityJSON"),
		[]byte("compiledApplicationModelCompatibilityDigest"),
		[]byte("validateRuntimeApplicationModel(document)"),
		[]byte(`runtimeEnvironmentVariable   = "PLYSTRA_ENV"`),
		[]byte(`runtimeConfigurationVariable = "PLYSTRA_CONFIG"`),
		[]byte(`case "--env":`),
		[]byte(`case "--config":`),
		[]byte("runtimeProjectRelativeConfigurationPath"),
		[]byte("normalizeRuntimeDocument"),
	} {
		if !bytes.Contains(bootstrap, required) {
			t.Fatalf("generated bootstrap omits default configuration selection %q:\n%s", required, bootstrap)
		}
	}
	entrypoint := readFile(t, root, "generated/go/application/main_gen.go")
	if !bytes.Contains(entrypoint, []byte("applicationbootstrap.New(ctx, applicationbootstrap.RuntimeOptions{")) || !bytes.Contains(entrypoint, []byte("os.Environ()")) || bytes.Contains(entrypoint, []byte("plystra.yaml")) {
		t.Fatalf("generated entrypoint does not delegate default configuration selection to bootstrap:\n%s", entrypoint)
	}
	assertFileMissing(t, root, "generated/sdk/javascript/package.json")

	writeFile(t, filepath.Join(root, "generated", "manifest.json"), "drift\n")
	driftedBefore := snapshotTree(t, root)
	drifted, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !reflect.DeepEqual(drifted.Report().Changed(), []string{"generated/manifest.json"}) {
		t.Fatalf("drift check = %#v, %v", drifted.Report().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, driftedBefore) {
		t.Fatalf("drift check mutated application:\nbefore: %#v\nafter:  %#v", driftedBefore, after)
	}
	if repaired, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: func(_ context.Context, _ string) error { return nil }}); err != nil || !repaired.Report().Clean() {
		t.Fatalf("repair drift = %#v, %v", repaired.Report().Changes(), err)
	}

	clean, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !clean.Report().Clean() {
		t.Fatalf("clean check = %#v, %v", clean.Report().Changes(), err)
	}
}

func TestModuleRequirementRejectsInvalidPathOrMinimumVersion(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		modulePath string
		version    string
		valid      bool
	}{
		{modulePath: "connectrpc.com/connect", version: "v1.20.0", valid: true},
		{modulePath: "example.com/runtime/v2", version: "v2.1.0", valid: true},
		{modulePath: "", version: "v1.0.0"},
		{modulePath: "example.com/runtime", version: "1.0.0"},
		{modulePath: "example.com/runtime/v2", version: "v1.0.0"},
		{modulePath: "example.com/runtime", version: "v1.0.0+metadata"},
	} {
		requirement, err := applicationgenerate.NewModuleRequirement(test.modulePath, test.version)
		if test.valid {
			if err != nil || requirement.Path() != test.modulePath || requirement.MinimumVersion() != test.version {
				t.Fatalf("NewModuleRequirement(%q, %q) = %#v, %v", test.modulePath, test.version, requirement, err)
			}
			continue
		}
		if !errors.Is(err, applicationgenerate.ErrRuntimeDependency) {
			t.Fatalf("NewModuleRequirement(%q, %q) error = %v", test.modulePath, test.version, err)
		}
	}
}

func TestGenerateCheckReportsMissingConnectRuntimeRequirementsWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/missing-connect-runtime")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "http: {expose: [kernel.health/v1]}\n")
	before := snapshotTree(t, root)
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: goEnvironment(nil),
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrRuntimeDependency) {
		t.Fatalf("Generate check error = %v", err)
	}
	for _, want := range []string{"connectrpc.com/connect", "v1.20.0", "run plystra generate"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Generate check error %q omits %q", err, want)
		}
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Generate check mutated missing-runtime Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestGenerateDetectsDescriptorEvidenceDriftWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/descriptor-drift")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	environment := goEnvironment(nil)
	options := applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate:    func(_ context.Context, _ string) error { return nil },
	}
	if generated, err := applicationgenerate.Generate(t.Context(), options); err != nil || !generated.Report().Clean() {
		t.Fatalf("initial Generate = %#v, %v", generated.Report().Changes(), err)
	}
	descriptorPath := filepath.Join(root, filepath.FromSlash("generated/proto/descriptor-set.pb"))
	writeFile(t, descriptorPath, "manual descriptor drift")
	changedBefore := snapshotTree(t, root)
	checkOptions := options
	checkOptions.Check = true
	checkOptions.Validate = nil
	changed, err := applicationgenerate.Generate(t.Context(), checkOptions)
	if err != nil || !reflect.DeepEqual(changed.Report().Changed(), []string{"generated/proto/descriptor-set.pb"}) {
		t.Fatalf("changed descriptor check = %#v, %v", changed.Report().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, changedBefore) {
		t.Fatal("changed descriptor check mutated the Project")
	}

	if err := os.Remove(descriptorPath); err != nil {
		t.Fatalf("Remove descriptor set: %v", err)
	}
	missingBefore := snapshotTree(t, root)
	missing, err := applicationgenerate.Generate(t.Context(), checkOptions)
	if err != nil || !reflect.DeepEqual(missing.Report().Missing(), []string{"generated/proto/descriptor-set.pb"}) {
		t.Fatalf("missing descriptor check = %#v, %v", missing.Report().Changes(), err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, missingBefore) {
		t.Fatal("missing descriptor check mutated the Project")
	}
}

func TestGenerateMaintainsStableOwnedProtobufFieldHistoryTransactionally(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConnectApplicationModule(t, root, "example.com/acme/wire-history")
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  transports: {connect: true, rest: false}
  expose: [customer.enroll/v1]
capabilities:
  require: [customer.enroll/v1]
`)
	writePlugin(t, root, "customer", "id: acme.customer\nprovides: [customer.enroll/v1]\n")
	capabilityPath := filepath.Join(root, "customer", "capabilities", "customer.enroll", "v1", "capability.yaml")
	initialContract := `id: customer.enroll/v1
request:
  beta: {type: string}
  alpha: {type: string}
response:
  customer_id: {type: string}
errors: []
`
	writeCapability(t, root, "customer", "customer.enroll/v1", initialContract)
	environment := goEnvironment(nil)
	validate := func(_ context.Context, _ string) error { return nil }
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: validate}); err != nil || !result.Report().Clean() {
		t.Fatalf("initial Generate = %#v, %v", result.Report().Changes(), err)
	}
	initialMap := readFile(t, root, protobufwiremap.Path)
	initial := decodeWireAssignments(t, initialMap, "customer.enroll/v1")
	if initial.request["alpha"] != 1 || initial.request["beta"] != 2 || initial.response["customer_id"] != 1 {
		t.Fatalf("initial assignments = %#v", initial)
	}
	initialManifest, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil || initialManifest.ProtobufWireMapDigest() != sha256Text(initialMap) {
		t.Fatalf("initial wire-map provenance = %q, %v", initialManifest.ProtobufWireMapDigest(), err)
	}

	writeFile(t, capabilityPath, withQuerySemantics(`response: {customer_id: {type: string}}
errors: []
request:
  alpha: {type: string}
  beta: {type: string}
id: customer.enroll/v1
`))
	beforeReorder := snapshotTree(t, root)
	reordered, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment})
	if err != nil || !reordered.Report().Clean() || !reflect.DeepEqual(snapshotTree(t, root), beforeReorder) {
		t.Fatalf("reordered check = %#v, %v", reordered.Report().Changes(), err)
	}

	writeFile(t, capabilityPath, withQuerySemantics(`id: customer.enroll/v1
request:
  gamma: {type: integer}
  beta: {type: string}
  alpha: {type: string}
response: {customer_id: {type: string}}
errors: []
`))
	beforeAdditionCheck := snapshotTree(t, root)
	drift, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment})
	if err != nil || drift.Report().Clean() || !slicesContains(drift.Report().Changed(), protobufwiremap.Path) || !reflect.DeepEqual(snapshotTree(t, root), beforeAdditionCheck) {
		t.Fatalf("added-field check = %#v, %v", drift.Report().Changes(), err)
	}
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: validate}); err != nil || !result.Report().Clean() {
		t.Fatalf("added-field Generate = %#v, %v", result.Report().Changes(), err)
	}
	addedMap := readFile(t, root, protobufwiremap.Path)
	added := decodeWireAssignments(t, addedMap, "customer.enroll/v1")
	if added.request["alpha"] != 1 || added.request["beta"] != 2 || added.request["gamma"] != 3 {
		t.Fatalf("added assignments = %#v", added)
	}

	writeFile(t, capabilityPath, withQuerySemantics(`id: customer.enroll/v1
request:
  delta: {type: boolean}
  gamma: {type: integer}
  alpha: {type: string}
response: {customer_id: {type: string}}
errors: []
`))
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: validate}); err != nil || !result.Report().Clean() {
		t.Fatalf("removed-field Generate = %#v, %v", result.Report().Changes(), err)
	}
	removedMap := readFile(t, root, protobufwiremap.Path)
	removed := decodeWireAssignments(t, removedMap, "customer.enroll/v1")
	if removed.request["alpha"] != 1 || removed.request["gamma"] != 3 || removed.request["delta"] != 4 || !reflect.DeepEqual(removed.reservedNumbers, []int{2}) || !reflect.DeepEqual(removed.reservedNames, []string{"beta"}) {
		t.Fatalf("removed assignments = %#v", removed)
	}

	writeFile(t, capabilityPath, withQuerySemantics(`id: customer.enroll/v1
request:
  epsilon: {type: number}
  delta: {type: boolean}
  gamma: {type: integer}
  alpha: {type: string}
response: {customer_id: {type: string}}
errors: []
`))
	generatedBeforeRollback := snapshotGenerated(t, root)
	forced := errors.New("forced validation failure")
	if _, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: root, Environment: environment,
		Validate: func(_ context.Context, _ string) error { return forced },
	}); !errors.Is(err, forced) {
		t.Fatalf("rollback Generate error = %v", err)
	}
	if after := snapshotGenerated(t, root); !reflect.DeepEqual(after, generatedBeforeRollback) {
		t.Fatalf("wire-history rollback changed generated tree:\nbefore: %#v\nafter: %#v", generatedBeforeRollback, after)
	}
	assertNoTransactions(t, root)

	writeFile(t, filepath.Join(root, filepath.FromSlash(protobufwiremap.Path)), "manual drift\n")
	driftedBefore := snapshotTree(t, root)
	if _, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment}); !errors.Is(err, generatedfiles.ErrManifest) || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("manually modified wire map error = %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, driftedBefore) {
		t.Fatal("failed managed-history check mutated the Project")
	}
}

func TestGenerateMaintainsStableOwnedProtobufEnumHistoryTransactionally(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeConnectApplicationModule(t, root, "example.com/acme/enum-wire-history")
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  transports: {connect: true, rest: false}
  expose: [delivery.route/v1]
capabilities:
  require: [delivery.route/v1]
`)
	writePlugin(t, root, "delivery", "id: acme.delivery\nprovides: [delivery.route/v1]\n")
	capabilityPath := filepath.Join(root, "delivery", "capabilities", "delivery.route", "v1", "capability.yaml")
	writeCapability(t, root, "delivery", "delivery.route/v1", `id: delivery.route/v1
request:
  mode: {type: string, enum: [slow, fast]}
response: {}
errors: []
`)
	environment := goEnvironment(nil)
	validate := func(_ context.Context, _ string) error { return nil }
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: validate}); err != nil || !result.Report().Clean() {
		t.Fatalf("initial Generate = %#v, %v", result.Report().Changes(), err)
	}
	initialMap := readFile(t, root, protobufwiremap.Path)
	initial := decodeWireEnumAssignments(t, initialMap, "delivery.route/v1", "mode")
	if initial.members[`"fast"`].number != 1 || initial.members[`"slow"`].number != 2 || initial.sentinelNumber != 0 {
		t.Fatalf("initial enum assignments = %#v", initial)
	}

	writeFile(t, capabilityPath, withQuerySemantics(`response: {}
errors: []
request:
  mode: {enum: [fast, slow], type: string}
id: delivery.route/v1
`))
	beforeReorder := snapshotTree(t, root)
	reordered, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment})
	if err != nil || !reordered.Report().Clean() || !reflect.DeepEqual(snapshotTree(t, root), beforeReorder) {
		t.Fatalf("reordered enum check = %#v, %v", reordered.Report().Changes(), err)
	}

	writeFile(t, capabilityPath, withQuerySemantics(`id: delivery.route/v1
request:
  mode: {type: string, enum: [express, fast, slow]}
response: {}
errors: []
`))
	beforeAdditionCheck := snapshotTree(t, root)
	drift, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment})
	if err != nil || drift.Report().Clean() || !slicesContains(drift.Report().Changed(), protobufwiremap.Path) || !reflect.DeepEqual(snapshotTree(t, root), beforeAdditionCheck) {
		t.Fatalf("added-enum-member check = %#v, %v", drift.Report().Changes(), err)
	}
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: validate}); err != nil || !result.Report().Clean() {
		t.Fatalf("added-enum-member Generate = %#v, %v", result.Report().Changes(), err)
	}
	added := decodeWireEnumAssignments(t, readFile(t, root, protobufwiremap.Path), "delivery.route/v1", "mode")
	if added.members[`"fast"`].number != 1 || added.members[`"slow"`].number != 2 || added.members[`"express"`].number != 3 {
		t.Fatalf("added enum assignments = %#v", added)
	}

	writeFile(t, capabilityPath, withQuerySemantics(`id: delivery.route/v1
request:
  mode: {type: string, enum: [express, later, slow]}
response: {}
errors: []
`))
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: validate}); err != nil || !result.Report().Clean() {
		t.Fatalf("removed-enum-member Generate = %#v, %v", result.Report().Changes(), err)
	}
	removedMap := readFile(t, root, protobufwiremap.Path)
	removed := decodeWireEnumAssignments(t, removedMap, "delivery.route/v1", "mode")
	if removed.members[`"express"`].number != 3 || removed.members[`"slow"`].number != 2 || removed.members[`"later"`].number != 4 || !reflect.DeepEqual(removed.reservedNumbers, []int{1}) || len(removed.reservedNames) != 1 || removed.reservedNames[0] != initial.members[`"fast"`].name {
		t.Fatalf("removed enum assignments = %#v", removed)
	}

	writeFile(t, capabilityPath, withQuerySemantics(`id: delivery.route/v1
request:
  mode: {type: string, enum: [express, fast, later, slow]}
response: {}
errors: []
`))
	beforeReaddition := snapshotTree(t, root)
	if _, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment}); !errors.Is(err, protobufwiremap.ErrHistory) || !strings.Contains(err.Error(), "permanently occupied generated name") {
		t.Fatalf("re-added enum member error = %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, beforeReaddition) {
		t.Fatal("failed enum re-addition check mutated the Project")
	}

	writeFile(t, capabilityPath, withQuerySemantics(`id: delivery.route/v1
request:
  mode: {type: string, enum: [express, later, slow, urgent]}
response: {}
errors: []
`))
	generatedBeforeRollback := snapshotGenerated(t, root)
	forced := errors.New("forced enum validation failure")
	if _, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: root, Environment: environment,
		Validate: func(_ context.Context, _ string) error { return forced },
	}); !errors.Is(err, forced) {
		t.Fatalf("enum rollback Generate error = %v", err)
	}
	if after := snapshotGenerated(t, root); !reflect.DeepEqual(after, generatedBeforeRollback) {
		t.Fatalf("enum-history rollback changed generated tree:\nbefore: %#v\nafter: %#v", generatedBeforeRollback, after)
	}
	assertNoTransactions(t, root)
}

func TestGenerateRejectsProtobufNamingCollisionsWithoutMutation(t *testing.T) {
	t.Parallel()

	for _, check := range []bool{false, true} {
		check := check
		name := "generate"
		if check {
			name = "generate check"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeApplicationModule(t, root, "example.com/acme/protobuf-collision")
			writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  transports: {connect: true, rest: false}
  expose: [naming.collision/v1]
capabilities:
  require: [naming.collision/v1]
`)
			writePlugin(t, root, "naming", "id: acme.naming\nprovides: [naming.collision/v1]\n")
			writeCapability(t, root, "naming", "naming.collision/v1", `id: naming.collision/v1
request:
  foo_1: {type: string}
  foo1: {type: string}
response: {}
errors: []
`)
			before := snapshotTree(t, root)
			_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
				Start:       root,
				Check:       check,
				Environment: goEnvironment(nil),
				Validate:    func(_ context.Context, _ string) error { return nil },
			})
			if !errors.Is(err, protobufidentity.ErrCollision) {
				t.Fatalf("Generate error = %v", err)
			}
			for _, want := range []string{"Capability naming.collision/v1", "request", `"foo1"`, `"foo_1"`, `Protobuf JSON name "foo1"`} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("Generate error %q omits %q", err, want)
				}
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed generation mutated Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
			assertNoTransactions(t, root)
		})
	}
}

func TestGenerateSupportsUnaryCommandConnectExposure(t *testing.T) {
	for _, selection := range []struct {
		name         string
		rootData     string
		selectedPath string
		selectedData string
		configure    func(*applicationgenerate.Options)
	}{
		{
			name:         "default",
			selectedPath: "plystra.yaml",
			selectedData: "http: {expose: [records.archive/v1]}\n",
		},
		{
			name:         "environment",
			rootData:     "http: {expose: [records.archive/v1]}\n",
			selectedPath: "plystra.production.yaml",
			selectedData: "{}\n",
			configure: func(options *applicationgenerate.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:         "full replacement",
			rootData:     "{}\n",
			selectedPath: "deploy/customer-a.yaml",
			selectedData: "http: {expose: [records.archive/v1]}\n",
			configure: func(options *applicationgenerate.Options) {
				options.ConfigurationPath = "deploy/customer-a.yaml"
			},
		},
	} {
		selection := selection
		for _, check := range []bool{false, true} {
			check := check
			name := selection.name + "/generate"
			if check {
				name = selection.name + "/generate check"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				root := t.TempDir()
				writeConnectApplicationModule(t, root, "example.com/acme/connect-command-"+strings.ReplaceAll(selection.name, " ", "-"))
				rootData := selection.rootData
				if selection.selectedPath == "plystra.yaml" {
					rootData = selection.selectedData
				}
				writeFile(t, filepath.Join(root, "plystra.yaml"), rootData)
				if selection.selectedPath != "plystra.yaml" {
					writeFile(t, filepath.Join(root, filepath.FromSlash(selection.selectedPath)), selection.selectedData)
				}
				writePlugin(t, root, "records", "id: acme.records\nprovides: [records.archive/v1]\n")
				writeCapability(t, root, "records", "records.archive/v1", `id: records.archive/v1
request: {record_id: {type: string, required: true}}
response: {archived: {type: boolean, required: true}}
errors: [archive_failed]
semantics:
  kind: command
  effects: external-write
  idempotency: {mode: none}
  retry: {safety: never}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`)

				before := snapshotTree(t, root)
				options := applicationgenerate.Options{
					Start:       root,
					Check:       check,
					Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
					Validate:    func(_ context.Context, _ string) error { return nil },
				}
				if selection.configure != nil {
					selection.configure(&options)
				}
				result, err := applicationgenerate.Generate(t.Context(), options)
				if err != nil {
					t.Fatalf("Generate(command, check=%t): %v", check, err)
				}
				for _, path := range []string{
					"generated/go/adapters/connect/records/archive/v1/handler_gen.go",
					"generated/proto/plystra/generated/records/archive/v1/capability.proto",
					"generated/sdk/javascript/src/operations/records/archive/v1.ts",
				} {
					if check {
						if !slicesContains(result.Report().Missing(), path) {
							t.Fatalf("Generate check(command) missing = %v; want %s", result.Report().Missing(), path)
						}
						continue
					}
					assertFileExists(t, root, path)
				}
				if check {
					if result.Report().Clean() {
						t.Fatal("Generate check(command) reported an ungenerated Project as clean")
					}
					if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
						t.Fatalf("command generation check mutated Project:\nbefore: %#v\nafter:  %#v", before, after)
					}
				} else if !result.Report().Clean() {
					t.Fatalf("Generate(command) changes = %#v", result.Report().Changes())
				}
				assertNoTransactions(t, root)
			})
		}
	}
}

func TestGenerateRejectsEventAndStreamConnectExposureWithoutMutation(t *testing.T) {
	kinds := []struct {
		name      string
		semantics string
	}{
		{
			name: "event",
			semantics: `semantics:
  kind: event
  effects: external-write
  idempotency: {mode: none}
  retry: {safety: never}
  cancellation: {mode: best-effort}
  completion: {mode: accepted-for-processing}
  ordering: {mode: none}
  data: {request: public, response: public}
`,
		},
		{
			name: "stream",
			semantics: `semantics:
  kind: stream
  effects: none
  idempotency: {mode: none}
  retry: {safety: never}
  cancellation: {mode: best-effort}
  completion: {mode: accepted-for-processing}
  ordering: {mode: none}
  data: {request: public, response: public}
`,
		},
	}
	selections := []struct {
		name         string
		selectedPath string
		configure    func(*applicationgenerate.Options)
	}{
		{name: "default", selectedPath: "plystra.yaml"},
		{
			name:         "environment",
			selectedPath: "plystra.production.yaml",
			configure: func(options *applicationgenerate.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:         "full replacement",
			selectedPath: "deploy/customer-a.yaml",
			configure: func(options *applicationgenerate.Options) {
				options.ConfigurationPath = "deploy/customer-a.yaml"
			},
		},
	}
	surfaces := []struct {
		name string
		yaml string
	}{
		{name: "canonical", yaml: "http: {expose: [records.archived/v1]}\n"},
		{
			name: "canonical with Alias",
			yaml: `http: {expose: [records.archived/v1]}
capabilities:
  aliases: {records.archive-notification/v1: records.archived/v1}
`,
		},
	}

	for _, kind := range kinds {
		kind := kind
		for _, selection := range selections {
			selection := selection
			for _, surface := range surfaces {
				surface := surface
				for _, check := range []bool{false, true} {
					check := check
					name := kind.name + "/" + selection.name + "/" + surface.name + "/generate"
					if check {
						name += " check"
					}
					t.Run(name, func(t *testing.T) {
						t.Parallel()

						root := t.TempDir()
						writeApplicationModule(t, root, "example.com/acme/connect-"+kind.name)
						rootData := "{}\n"
						if selection.selectedPath == "plystra.yaml" {
							rootData = surface.yaml
						}
						writeFile(t, filepath.Join(root, "plystra.yaml"), rootData)
						if selection.selectedPath != "plystra.yaml" {
							writeFile(t, filepath.Join(root, filepath.FromSlash(selection.selectedPath)), surface.yaml)
						}
						writePlugin(t, root, "records", "id: acme.records\nprovides: [records.archived/v1]\n")
						writeCapability(t, root, "records", "records.archived/v1", "id: records.archived/v1\nrequest: {record_id: {type: string, required: true}}\nresponse: {}\nerrors: []\n"+kind.semantics)
						before := snapshotTree(t, root)
						options := applicationgenerate.Options{
							Start:       root,
							Check:       check,
							Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
							Validate:    func(_ context.Context, _ string) error { return nil },
						}
						if selection.configure != nil {
							selection.configure(&options)
						}
						_, err := applicationgenerate.Generate(t.Context(), options)
						if !errors.Is(err, protobufmodel.ErrOperationKind) {
							t.Fatalf("Generate(check=%t) error = %v", check, err)
						}
						for _, want := range []string{
							"Capability records.archived/v1",
							`semantics.kind "` + kind.name + `"`,
							"requested Connect surface",
							"unary boundary",
							`semantics.kind "query"`,
							`"command"`,
							"remove records.archived/v1 from http.expose",
						} {
							if !strings.Contains(err.Error(), want) {
								t.Fatalf("Generate error %q omits %q", err, want)
							}
						}
						if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
							t.Fatalf("failed generation mutated Project:\nbefore: %#v\nafter:  %#v", before, after)
						}
						assertNoTransactions(t, root)
					})
				}
			}
		}
	}
}

type wireAssignments struct {
	request         map[string]int
	response        map[string]int
	reservedNumbers []int
	reservedNames   []string
}

type wireEnumMember struct {
	name   string
	number int
}

type wireEnumAssignments struct {
	sentinelNumber  int
	members         map[string]wireEnumMember
	reservedNumbers []int
	reservedNames   []string
}

func decodeWireEnumAssignments(t testing.TB, data []byte, capabilityID, fieldName string) wireEnumAssignments {
	t.Helper()
	var document struct {
		Capabilities map[string]struct {
			Request struct {
				Enums map[string]struct {
					Sentinel struct {
						Number int `json:"number"`
					} `json:"sentinel"`
					Members []struct {
						Canonical json.RawMessage `json:"canonical"`
						Name      string          `json:"name"`
						Number    int             `json:"number"`
					} `json:"members"`
					ReservedNumbers []int    `json:"reserved_numbers"`
					ReservedNames   []string `json:"reserved_names"`
				} `json:"enums"`
			} `json:"request"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode wire map: %v", err)
	}
	record, exists := document.Capabilities[capabilityID]
	if !exists {
		t.Fatalf("wire map omits %s: %s", capabilityID, data)
	}
	assignment, exists := record.Request.Enums[fieldName]
	if !exists {
		t.Fatalf("wire map omits enum %s.%s: %s", capabilityID, fieldName, data)
	}
	result := wireEnumAssignments{
		sentinelNumber:  assignment.Sentinel.Number,
		members:         make(map[string]wireEnumMember, len(assignment.Members)),
		reservedNumbers: append([]int(nil), assignment.ReservedNumbers...),
		reservedNames:   append([]string(nil), assignment.ReservedNames...),
	}
	for _, member := range assignment.Members {
		result.members[string(member.Canonical)] = wireEnumMember{name: member.Name, number: member.Number}
	}
	return result
}

func decodeWireAssignments(t testing.TB, data []byte, capabilityID string) wireAssignments {
	t.Helper()
	var document struct {
		Capabilities map[string]struct {
			Request struct {
				Fields map[string]struct {
					Number int `json:"number"`
				} `json:"fields"`
				ReservedNumbers []int    `json:"reserved_numbers"`
				ReservedNames   []string `json:"reserved_names"`
			} `json:"request"`
			Response struct {
				Fields map[string]struct {
					Number int `json:"number"`
				} `json:"fields"`
			} `json:"response"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode wire map: %v", err)
	}
	record, exists := document.Capabilities[capabilityID]
	if !exists {
		t.Fatalf("wire map omits %s: %s", capabilityID, data)
	}
	result := wireAssignments{
		request:         make(map[string]int, len(record.Request.Fields)),
		response:        make(map[string]int, len(record.Response.Fields)),
		reservedNumbers: append([]int(nil), record.Request.ReservedNumbers...),
		reservedNames:   append([]string(nil), record.Request.ReservedNames...),
	}
	for name, field := range record.Request.Fields {
		result.request[name] = field.Number
	}
	for name, field := range record.Response.Fields {
		result.response[name] = field.Number
	}
	return result
}

func slicesContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sha256Text(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestGenerateChecksAndRepairsDependencyCompositionDriftTransactionally(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "platform")
	writeModule(t, dependencyRoot, "example.com/platform", "")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities:\n  require: [kernel.health/v1]\n")
	writeApplicationModule(t, appRoot, "example.com/acme/composed")
	goModPath := filepath.Join(appRoot, "go.mod")
	goMod := string(readAbsoluteFile(t, goModPath)) + fmt.Sprintf("\nrequire example.com/platform v1.0.0\n\nreplace example.com/platform => %s\n", filepath.ToSlash(dependencyRoot))
	writeFile(t, goModPath, goMod)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "# shared application settings\nhttp:\n  address: \":8080\" # keep process comment\ncapabilities:\n  require: []\n")
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
	noValidation := func(_ context.Context, _ string) error { return nil }

	initial, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate:    noValidation,
	})
	if err != nil || !initial.ConfigurationChanged() || !initial.Report().Clean() {
		t.Fatalf("initial Generate = changed %t, report %#v, %v", initial.ConfigurationChanged(), initial.Report().Changes(), err)
	}
	initialManifest := readFile(t, appRoot, "plystra.yaml")
	for _, required := range [][]byte{[]byte("kernel.health/v1"), []byte("# shared application settings"), []byte("# keep process comment")} {
		if !bytes.Contains(initialManifest, required) {
			t.Fatalf("initial maintained manifest omits %q:\n%s", required, initialManifest)
		}
	}
	beforeDrift := snapshotTree(t, appRoot)
	generatedBefore := snapshotGenerated(t, appRoot)

	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities:\n  require: [kernel.info/v1]\n")
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !checked.Checked() || !checked.ConfigurationChanged() {
		t.Fatalf("drift check = checked %t, configuration changed %t, report %#v, %v", checked.Checked(), checked.ConfigurationChanged(), checked.Report().Changes(), err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, beforeDrift) {
		t.Fatalf("dependency-composition check mutated application:\nbefore: %#v\nafter:  %#v", beforeDrift, after)
	}

	validationFailure := errors.New("reject recomposed application")
	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			return validationFailure
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, validationFailure) {
		t.Fatalf("recomposition validation failure = %v", err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, beforeDrift) {
		t.Fatalf("failed recomposition changed application:\nbefore: %#v\nafter:  %#v", beforeDrift, after)
	}

	concurrentManifest := append(append([]byte(nil), initialManifest...), []byte("# concurrent user comment\n")...)
	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			writeFile(t, filepath.Join(appRoot, "plystra.yaml"), string(concurrentManifest))
			return nil
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrConcurrentChange) {
		t.Fatalf("concurrent recomposition edit = %v", err)
	}
	if current := readFile(t, appRoot, "plystra.yaml"); !bytes.Equal(current, concurrentManifest) {
		t.Fatalf("concurrent manifest edit was overwritten:\n%s", current)
	}
	if after := snapshotGenerated(t, appRoot); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatalf("generated rollback after concurrent configuration edit:\nbefore: %#v\nafter:  %#v", generatedBefore, after)
	}
	cleanupRecoveryTransactions(t, appRoot)

	installed, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate:    noValidation,
	})
	if err != nil || !installed.ConfigurationChanged() || !installed.Report().Clean() {
		t.Fatalf("install recomposition = changed %t, report %#v, %v", installed.ConfigurationChanged(), installed.Report().Changes(), err)
	}
	updated := readFile(t, appRoot, "plystra.yaml")
	for _, required := range [][]byte{[]byte("kernel.info/v1"), []byte("# shared application settings"), []byte("# keep process comment"), []byte("# concurrent user comment")} {
		if !bytes.Contains(updated, required) {
			t.Fatalf("updated manifest omits %q:\n%s", required, updated)
		}
	}
	if bytes.Contains(updated, []byte("kernel.health/v1")) {
		t.Fatalf("updated manifest retained disappeared dependency requirement:\n%s", updated)
	}
	clean, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: appRoot, Check: true, Environment: environment})
	if err != nil || clean.ConfigurationChanged() || !clean.Report().Clean() {
		t.Fatalf("clean composed check = changed %t, report %#v, %v", clean.ConfigurationChanged(), clean.Report().Changes(), err)
	}
	assertNoTransactions(t, appRoot)
}

func TestGenerateMaintainsFullReplacementSelectionsIndependently(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "platform")
	writeModule(t, dependencyRoot, "example.com/platform", "")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {require: [kernel.health/v1]}\n")
	writeApplicationModule(t, appRoot, "example.com/acme/selected-config")
	goModPath := filepath.Join(appRoot, "go.mod")
	goMod := string(readAbsoluteFile(t, goModPath)) + fmt.Sprintf("\nrequire example.com/platform v1.0.0\n\nreplace example.com/platform => %s\n", filepath.ToSlash(dependencyRoot))
	writeFile(t, goModPath, goMod)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "# default document\ncapabilities: {require: []}\n")
	selectedPath := filepath.Join(appRoot, "deploy", "customer.yaml")
	writeFile(t, selectedPath, "# selected document\ncapabilities: {require: []}\n")
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
	validate := func(_ context.Context, _ string) error { return nil }

	defaultResult, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: appRoot, Environment: environment, Validate: validate})
	if err != nil || defaultResult.ConfigurationPath() != "plystra.yaml" || !defaultResult.ConfigurationChanged() {
		t.Fatalf("default Generate = path %q changed %t, error %v", defaultResult.ConfigurationPath(), defaultResult.ConfigurationChanged(), err)
	}
	rootAfterDefault := readFile(t, appRoot, "plystra.yaml")
	explicitResult, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:             filepath.Join(appRoot, "deploy"),
		ConfigurationPath: "deploy/customer.yaml",
		Environment:       environment,
		Validate:          validate,
	})
	if err != nil || explicitResult.ConfigurationPath() != "deploy/customer.yaml" || !explicitResult.ConfigurationChanged() {
		t.Fatalf("explicit Generate = path %q changed %t, error %v", explicitResult.ConfigurationPath(), explicitResult.ConfigurationChanged(), err)
	}
	if current := readFile(t, appRoot, "plystra.yaml"); !bytes.Equal(current, rootAfterDefault) {
		t.Fatalf("explicit generation rewrote root configuration:\n%s", current)
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, appRoot, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}
	if _, exists := provenance.BaselineForSelection(applicationgen.ConfigurationModeDefault, "plystra.yaml"); !exists {
		t.Fatal("generated manifest lost default dependency baseline")
	}
	if _, exists := provenance.BaselineForSelection(applicationgen.ConfigurationModeExplicit, "deploy/customer.yaml"); !exists {
		t.Fatal("generated manifest lost explicit dependency baseline")
	}

	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	if _, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: appRoot, Environment: environment, Validate: validate}); err != nil {
		t.Fatalf("remove dependency declaration from default: %v", err)
	}
	if current := readFile(t, appRoot, "plystra.yaml"); bytes.Contains(current, []byte("kernel.health/v1")) {
		t.Fatalf("default retained disappeared inherited requirement:\n%s", current)
	}
	writeFile(t, selectedPath, "# selected document\ncapabilities:\n  require: [kernel.health/v1, kernel.info/v1]\n")
	rootBeforeExplicit := readFile(t, appRoot, "plystra.yaml")
	if _, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:             appRoot,
		ConfigurationPath: "deploy/customer.yaml",
		Environment:       environment,
		Validate:          validate,
	}); err != nil {
		t.Fatalf("remove dependency declaration from explicit selection: %v", err)
	}
	selected := readAbsoluteFile(t, selectedPath)
	if bytes.Contains(selected, []byte("kernel.health/v1")) || !bytes.Contains(selected, []byte("kernel.info/v1")) || !bytes.Contains(selected, []byte("# selected document")) {
		t.Fatalf("explicit maintenance lost ownership or local edit:\n%s", selected)
	}
	if current := readFile(t, appRoot, "plystra.yaml"); !bytes.Equal(current, rootBeforeExplicit) {
		t.Fatalf("explicit maintenance changed root:\n%s", current)
	}

	beforeCheck := snapshotTree(t, appRoot)
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:             appRoot,
		Check:             true,
		ConfigurationPath: "deploy/customer.yaml",
		Environment:       environment,
	})
	if err != nil || !checked.Checked() || checked.ConfigurationChanged() || !checked.Report().Clean() {
		t.Fatalf("explicit check = changed %t report %#v, error %v", checked.ConfigurationChanged(), checked.Report().Changes(), err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, beforeCheck) {
		t.Fatal("explicit generate --check mutated the Project")
	}

	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {require: [kernel.health/v1]}\n")
	rollbackBefore := snapshotTree(t, appRoot)
	validationFailure := errors.New("reject selected configuration update")
	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:             appRoot,
		ConfigurationPath: "deploy/customer.yaml",
		Environment:       environment,
		Validate:          func(_ context.Context, _ string) error { return validationFailure },
	})
	if !errors.Is(err, validationFailure) {
		t.Fatalf("selected validation failure = %v", err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, rollbackBefore) {
		t.Fatal("selected validation failure did not roll back configuration and generated output")
	}

	selectedBeforeConcurrent := readAbsoluteFile(t, selectedPath)
	concurrent := append(append([]byte(nil), selectedBeforeConcurrent...), []byte("# concurrent selected edit\n")...)
	generatedBefore := snapshotGenerated(t, appRoot)
	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:             appRoot,
		ConfigurationPath: "deploy/customer.yaml",
		Environment:       environment,
		Validate: func(_ context.Context, _ string) error {
			writeFile(t, selectedPath, string(concurrent))
			return nil
		},
	})
	if !errors.Is(err, applicationgenerate.ErrConcurrentChange) {
		t.Fatalf("concurrent selected edit error = %v", err)
	}
	if !strings.Contains(err.Error(), "recovery data retained in .plystra-files-") {
		t.Fatalf("concurrent selected edit error does not identify retained recovery data: %v", err)
	}
	if current := readAbsoluteFile(t, selectedPath); !bytes.Equal(current, concurrent) {
		t.Fatalf("concurrent selected edit was overwritten:\n%s", current)
	}
	if after := snapshotGenerated(t, appRoot); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatal("generated output changed after concurrent selected edit")
	}
	backups, globErr := filepath.Glob(filepath.Join(appRoot, ".plystra-files-*", "backup", "*"))
	if globErr != nil {
		t.Fatalf("glob selected-configuration recovery backups: %v", globErr)
	}
	foundSelectedBackup := false
	for _, backup := range backups {
		data, readErr := os.ReadFile(backup)
		if readErr != nil {
			t.Fatalf("read recovery backup %s: %v", backup, readErr)
		}
		if bytes.Equal(data, selectedBeforeConcurrent) {
			foundSelectedBackup = true
			break
		}
	}
	if !foundSelectedBackup {
		t.Fatalf("retained recovery transaction omits the pre-transaction selected configuration; backups = %v", backups)
	}
	cleanupRecoveryTransactions(t, appRoot)
}

func TestGenerateDetectsDependencyPluginConfigurationSchemaDrift(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "smtp")
	writeApplicationModule(t, dependencyRoot, "example.com/smtp")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, dependencyRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\nconfig:\n  endpoint: {type: string}\n")
	writeCapability(t, dependencyRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")

	writeApplicationModule(t, appRoot, "example.com/application")
	goModPath := filepath.Join(appRoot, "go.mod")
	goMod := string(readAbsoluteFile(t, goModPath)) + fmt.Sprintf("\nrequire example.com/smtp v1.0.0\n\nreplace example.com/smtp => %s\n", filepath.ToSlash(dependencyRoot))
	writeFile(t, goModPath, goMod)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities: {require: [email.send/v1]}\n")
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
	validate := func(_ context.Context, _ string) error { return nil }

	initial, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate:    validate,
	})
	if err != nil || !initial.Report().Clean() {
		t.Fatalf("initial Generate = changes %#v, %v", initial.Report().Changes(), err)
	}
	initialProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, appRoot, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(initial): %v", err)
	}

	writeFile(t, filepath.Join(dependencyRoot, "smtp", "plugin.yaml"), "id: example.smtp\nprovides: [email.send/v1]\nconfig:\n  endpoint: {type: integer}\n")
	applicationBeforeCheck := snapshotTree(t, appRoot)
	dependencyBeforeCheck := snapshotTree(t, dependencyRoot)
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Check:       true,
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Generate check after dependency schema change: %v", err)
	}
	if checked.Report().Clean() || !strings.Contains(strings.Join(checked.Report().Changed(), "\n"), "generated/manifest.json") {
		t.Fatalf("dependency schema change report = %#v", checked.Report().Changes())
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, applicationBeforeCheck) {
		t.Fatal("dependency schema drift check mutated the application")
	}
	if after := snapshotTree(t, dependencyRoot); !reflect.DeepEqual(after, dependencyBeforeCheck) {
		t.Fatal("dependency schema drift check mutated the dependency Project")
	}

	updated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       appRoot,
		Environment: environment,
		Validate:    validate,
	})
	if err != nil || !updated.Report().Clean() {
		t.Fatalf("updated Generate = changes %#v, %v", updated.Report().Changes(), err)
	}
	updatedProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, appRoot, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(updated): %v", err)
	}
	if updatedProvenance.ApplicationModelDigest() == initialProvenance.ApplicationModelDigest() {
		t.Fatal("dependency Plugin configuration schema change preserved application_model_digest")
	}
	if after := snapshotTree(t, dependencyRoot); !reflect.DeepEqual(after, dependencyBeforeCheck) {
		t.Fatal("dependency schema regeneration mutated the dependency Project")
	}
}

func TestGenerateSelectedHTTPTransportsCauseApplicationModelDrift(t *testing.T) {
	tests := []struct {
		name         string
		rootData     string
		selectedPath string
		selectedData string
		changedData  string
		configure    func(*applicationgenerate.Options)
	}{
		{
			name:         "default",
			selectedPath: "plystra.yaml",
			selectedData: "http:\n  transports: {connect: true, rest: false}\n",
			changedData:  "http:\n  transports: {connect: false, rest: true}\n",
		},
		{
			name:         "environment",
			rootData:     "http:\n  transports: {connect: true, rest: false}\n",
			selectedPath: "plystra.production.yaml",
			selectedData: "http:\n  transports: {connect: true, rest: false}\n",
			changedData:  "http:\n  transports: {connect: false, rest: true}\n",
			configure: func(options *applicationgenerate.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:         "full replacement",
			rootData:     "http:\n  transports: {connect: true, rest: false}\n",
			selectedPath: "deploy/customer-a.yaml",
			selectedData: "http:\n  transports: {connect: true, rest: false}\n",
			changedData:  "http:\n  transports: {connect: false, rest: true}\n",
			configure: func(options *applicationgenerate.Options) {
				options.ConfigurationPath = "deploy/customer-a.yaml"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeApplicationModule(t, root, "example.com/acme/transport-"+strings.ReplaceAll(test.name, " ", "-"))
			rootData := test.rootData
			if test.selectedPath == "plystra.yaml" {
				rootData = test.selectedData
			}
			writeFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			selectedPath := filepath.Join(root, filepath.FromSlash(test.selectedPath))
			if test.selectedPath != "plystra.yaml" {
				writeFile(t, selectedPath, test.selectedData)
			}
			environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
			options := applicationgenerate.Options{
				Start:       root,
				Environment: environment,
				Validate:    func(_ context.Context, _ string) error { return nil },
			}
			if test.configure != nil {
				test.configure(&options)
			}

			generated, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !generated.Report().Clean() {
				t.Fatalf("initial Generate = changes %#v, %v", generated.Report().Changes(), err)
			}
			initialProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatalf("DecodeManifestProvenance(initial): %v", err)
			}

			writeFile(t, selectedPath, test.changedData)
			beforeCheck := snapshotTree(t, root)
			checkOptions := options
			checkOptions.Check = true
			checkOptions.Validate = nil
			checked, err := applicationgenerate.Generate(t.Context(), checkOptions)
			if err != nil {
				t.Fatalf("Generate check after transport change: %v", err)
			}
			if checked.Report().Clean() || !strings.Contains(strings.Join(checked.Report().Changed(), "\n"), "generated/manifest.json") {
				t.Fatalf("transport change report = %#v", checked.Report().Changes())
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, beforeCheck) {
				t.Fatal("transport drift check mutated the Project")
			}

			updated, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !updated.Report().Clean() {
				t.Fatalf("updated Generate = changes %#v, %v", updated.Report().Changes(), err)
			}
			updatedProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatalf("DecodeManifestProvenance(updated): %v", err)
			}
			if updatedProvenance.ApplicationModelDigest() == initialProvenance.ApplicationModelDigest() {
				t.Fatal("selected HTTP transport change preserved application_model_digest")
			}
		})
	}
}

func TestGenerateSelectedHTTPCORSCausesApplicationModelDrift(t *testing.T) {
	tests := []struct {
		name           string
		rootData       string
		selectedPath   string
		selectedData   string
		equivalentData string
		changedData    string
		configure      func(*applicationgenerate.Options)
	}{
		{
			name:           "default",
			selectedPath:   "plystra.yaml",
			selectedData:   "http:\n  cors:\n    allowed_origins: [https://B.example:443, https://a.example, https://a.example:443]\n  expose: [kernel.health/v1]\n",
			equivalentData: "http:\n  cors:\n    allowed_origins: [https://a.example, https://b.example]\n  expose: [kernel.health/v1]\n",
			changedData:    "http:\n  cors:\n    allowed_origins: [https://api.example]\n  expose: [kernel.health/v1]\n",
		},
		{
			name:           "environment",
			rootData:       "http:\n  cors:\n    allowed_origins: [https://root.example]\n  expose: [kernel.health/v1]\n",
			selectedPath:   "plystra.production.yaml",
			selectedData:   "http:\n  cors:\n    allowed_origins: [https://B.example:443, https://a.example, https://a.example:443]\n    allow_credentials: false\n",
			equivalentData: "http:\n  cors:\n    allowed_origins: [https://a.example, https://b.example]\n",
			changedData:    "http:\n  cors:\n    allowed_origins: [https://a.example, https://b.example]\n    allow_credentials: true\n",
			configure: func(options *applicationgenerate.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:           "full replacement",
			rootData:       "http:\n  cors:\n    allowed_origins: [https://root.example]\n",
			selectedPath:   "deploy/customer-a.yaml",
			selectedData:   "http:\n  cors:\n    allowed_origins: [https://B.example:443, https://a.example, https://a.example:443]\n  expose: [kernel.health/v1]\n",
			equivalentData: "http:\n  cors:\n    allowed_origins: [https://a.example, https://b.example]\n  expose: [kernel.health/v1]\n",
			changedData:    "http:\n  cors:\n    allowed_origins: [https://api.example]\n  expose: [kernel.health/v1]\n",
			configure: func(options *applicationgenerate.Options) {
				options.ConfigurationPath = "deploy/customer-a.yaml"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeConnectApplicationModule(t, root, "example.com/acme/cors-"+strings.ReplaceAll(test.name, " ", "-"))
			rootData := test.rootData
			if test.selectedPath == "plystra.yaml" {
				rootData = test.selectedData
			}
			writeFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			selectedPath := filepath.Join(root, filepath.FromSlash(test.selectedPath))
			if test.selectedPath != "plystra.yaml" {
				writeFile(t, selectedPath, test.selectedData)
			}
			environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
			options := applicationgenerate.Options{
				Start:       root,
				Environment: environment,
				Validate:    func(_ context.Context, _ string) error { return nil },
			}
			if test.configure != nil {
				test.configure(&options)
			}

			generated, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !generated.Report().Clean() {
				t.Fatalf("initial Generate = changes %#v, %v", generated.Report().Changes(), err)
			}
			const connectHandlerPath = "generated/go/adapters/connect/kernel/health/v1/handler_gen.go"
			if source := readFile(t, root, connectHandlerPath); !strings.Contains(string(source), "plystraServeCORS") || !strings.Contains(string(source), "Access-Control-Allow-Origin") {
				t.Fatalf("generated Connect handler omits selected CORS behavior:\n%s", source)
			}
			initialProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatalf("DecodeManifestProvenance(initial): %v", err)
			}

			writeFile(t, selectedPath, test.equivalentData)
			equivalent, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !equivalent.Report().Clean() {
				t.Fatalf("equivalent Generate = changes %#v, %v", equivalent.Report().Changes(), err)
			}
			equivalentProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatalf("DecodeManifestProvenance(equivalent): %v", err)
			}
			if equivalentProvenance.ApplicationModelDigest() != initialProvenance.ApplicationModelDigest() {
				t.Fatalf("equivalent normalized CORS changed application_model_digest: %q != %q", equivalentProvenance.ApplicationModelDigest(), initialProvenance.ApplicationModelDigest())
			}

			writeFile(t, selectedPath, test.changedData)
			beforeCheck := snapshotTree(t, root)
			checkOptions := options
			checkOptions.Check = true
			checkOptions.Validate = nil
			checked, err := applicationgenerate.Generate(t.Context(), checkOptions)
			if err != nil {
				t.Fatalf("Generate check after CORS change: %v", err)
			}
			changedPaths := strings.Join(checked.Report().Changed(), "\n")
			if checked.Report().Clean() || !strings.Contains(changedPaths, "generated/manifest.json") || !strings.Contains(changedPaths, connectHandlerPath) {
				t.Fatalf("CORS change report = %#v", checked.Report().Changes())
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, beforeCheck) {
				t.Fatal("CORS drift check mutated the Project")
			}

			updated, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !updated.Report().Clean() {
				t.Fatalf("updated Generate = changes %#v, %v", updated.Report().Changes(), err)
			}
			updatedProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatalf("DecodeManifestProvenance(updated): %v", err)
			}
			if updatedProvenance.ApplicationModelDigest() == initialProvenance.ApplicationModelDigest() {
				t.Fatal("selected HTTP CORS change preserved application_model_digest")
			}
		})
	}
}

func TestGenerateSelectedExposureCausesApplicationModelDrift(t *testing.T) {
	for _, test := range []struct {
		name         string
		rootData     string
		selectedPath string
		selectedData string
		changedData  string
		configure    func(*applicationgenerate.Options)
	}{
		{
			name:         "default",
			selectedPath: "plystra.yaml",
			selectedData: "http: {expose: [kernel.info/v1]}\n",
			changedData:  "http: {expose: [kernel.health/v1]}\n",
		},
		{
			name:         "environment",
			rootData:     "http: {expose: [kernel.info/v1]}\n",
			selectedPath: "plystra.production.yaml",
			selectedData: "{}\n",
			changedData:  "http:\n  expose: {add: [kernel.health/v1], remove: [kernel.info/v1]}\n",
			configure: func(options *applicationgenerate.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:         "full replacement",
			rootData:     "http: {expose: [kernel.health/v1]}\n",
			selectedPath: "deploy/customer-a.yaml",
			selectedData: "http: {expose: [kernel.info/v1]}\n",
			changedData:  "http: {expose: [kernel.health/v1]}\n",
			configure: func(options *applicationgenerate.Options) {
				options.ConfigurationPath = "deploy/customer-a.yaml"
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeConnectApplicationModule(t, root, "example.com/acme/exposure-"+strings.ReplaceAll(test.name, " ", "-"))
			rootData := test.rootData
			if test.selectedPath == "plystra.yaml" {
				rootData = test.selectedData
			}
			writeFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			selectedPath := filepath.Join(root, filepath.FromSlash(test.selectedPath))
			if test.selectedPath != "plystra.yaml" {
				writeFile(t, selectedPath, test.selectedData)
			}
			environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
			options := applicationgenerate.Options{
				Start:       root,
				Environment: environment,
				Validate:    func(_ context.Context, _ string) error { return nil },
			}
			if test.configure != nil {
				test.configure(&options)
			}

			generated, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !generated.Report().Clean() {
				t.Fatalf("initial Generate = changes %#v, %v", generated.Report().Changes(), err)
			}
			initialProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatalf("DecodeManifestProvenance(initial): %v", err)
			}
			assertFileExists(t, root, "generated/sdk/javascript/src/operations/kernel/info/v1.ts")
			assertFileMissing(t, root, "generated/sdk/javascript/src/operations/kernel/health/v1.ts")
			assertFileExists(t, root, "generated/proto/plystra/generated/kernel/info/v1/capability.proto")
			assertFileMissing(t, root, "generated/proto/plystra/generated/kernel/health/v1/capability.proto")

			writeFile(t, selectedPath, test.changedData)
			beforeCheck := snapshotTree(t, root)
			checkOptions := options
			checkOptions.Check = true
			checkOptions.Validate = nil
			checked, err := applicationgenerate.Generate(t.Context(), checkOptions)
			if err != nil {
				t.Fatalf("Generate check after exposure change: %v", err)
			}
			if checked.Report().Clean() ||
				!slicesContains(checked.Report().Changed(), generatedfiles.ManifestPath) ||
				!slicesContains(checked.Report().Changed(), "generated/manifest.json") ||
				!slicesContains(checked.Report().Changed(), "generated/proto/descriptor-set.pb") ||
				!slicesContains(checked.Report().Missing(), "generated/proto/plystra/generated/kernel/health/v1/capability.proto") ||
				!slicesContains(checked.Report().Obsolete(), "generated/proto/plystra/generated/kernel/info/v1/capability.proto") ||
				!slicesContains(checked.Report().Missing(), "generated/sdk/javascript/src/operations/kernel/health/v1.ts") ||
				!slicesContains(checked.Report().Obsolete(), "generated/sdk/javascript/src/operations/kernel/info/v1.ts") {
				t.Fatalf("exposure change report = %#v", checked.Report().Changes())
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, beforeCheck) {
				t.Fatal("exposure drift check mutated the Project")
			}

			updated, err := applicationgenerate.Generate(t.Context(), options)
			if err != nil || !updated.Report().Clean() {
				t.Fatalf("updated Generate = changes %#v, %v", updated.Report().Changes(), err)
			}
			updatedProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
			if err != nil {
				t.Fatalf("DecodeManifestProvenance(updated): %v", err)
			}
			if updatedProvenance.ApplicationModelDigest() == initialProvenance.ApplicationModelDigest() {
				t.Fatal("selected exposure change preserved application_model_digest")
			}
			assertFileExists(t, root, "generated/sdk/javascript/src/operations/kernel/health/v1.ts")
			assertFileMissing(t, root, "generated/sdk/javascript/src/operations/kernel/info/v1.ts")
			assertFileExists(t, root, "generated/proto/plystra/generated/kernel/health/v1/capability.proto")
			assertFileMissing(t, root, "generated/proto/plystra/generated/kernel/info/v1/capability.proto")
		})
	}
}

func TestGenerateRequiresConnectForSelectedJavaScriptSDK(t *testing.T) {
	tests := []struct {
		name         string
		rootData     string
		selectedPath string
		selectedData string
		configure    func(*applicationgenerate.Options)
	}{
		{
			name:         "default",
			selectedPath: "plystra.yaml",
			selectedData: "http: {transports: {connect: false, rest: true}, expose: [kernel.health/v1]}\n",
		},
		{
			name:         "environment",
			rootData:     "http: {expose: [kernel.health/v1]}\n",
			selectedPath: "plystra.production.yaml",
			selectedData: "http: {transports: {connect: false, rest: true}}\n",
			configure: func(options *applicationgenerate.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:         "full replacement",
			rootData:     "{}\n",
			selectedPath: "deploy/customer-a.yaml",
			selectedData: "http: {transports: {connect: false, rest: true}, expose: [kernel.health/v1]}\n",
			configure: func(options *applicationgenerate.Options) {
				options.ConfigurationPath = "deploy/customer-a.yaml"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeApplicationModule(t, root, "example.com/acme/javascript-connect-"+strings.ReplaceAll(test.name, " ", "-"))
			rootData := test.rootData
			if test.selectedPath == "plystra.yaml" {
				rootData = test.selectedData
			}
			writeFile(t, filepath.Join(root, "plystra.yaml"), rootData)
			if test.selectedPath != "plystra.yaml" {
				writeFile(t, filepath.Join(root, filepath.FromSlash(test.selectedPath)), test.selectedData)
			}
			options := applicationgenerate.Options{
				Start:       root,
				Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
				Validate:    func(_ context.Context, _ string) error { return nil },
			}
			if test.configure != nil {
				test.configure(&options)
			}

			for _, check := range []bool{true, false} {
				before := snapshotTree(t, root)
				selectedOptions := options
				selectedOptions.Check = check
				if check {
					selectedOptions.Validate = nil
				}
				result, err := applicationgenerate.Generate(t.Context(), selectedOptions)
				if !errors.Is(err, applicationgen.ErrJavaScriptTransport) || result.Module().Path() != "" {
					t.Fatalf("Generate(check=%t) = %#v, %v", check, result, err)
				}
				for _, want := range []string{
					`http.transports.connect is false for selected configuration "` + test.selectedPath + `"`,
					"official generated JavaScript SDK requires Connect for Capability kernel.health/v1",
					"enable http.transports.connect",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("Generate(check=%t) error %q does not contain %q", check, err, want)
					}
				}
				if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
					t.Fatalf("Generate(check=%t) mutated rejected Project:\nbefore: %#v\nafter:  %#v", check, before, after)
				}
			}
		})
	}
}

func TestGenerateApplicationModelDigestExcludesRuntimeValuesAndMachinePaths(t *testing.T) {
	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/private-runtime-values")
	writePlugin(t, root, "mailer", "id: acme.mailer\nprovides: [email.send/v1]\nconfig:\n  endpoint: {type: string, required: true}\n  password: {type: secret, required: true}\n")
	writeCapability(t, root, "mailer", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	configurationPath := filepath.Join(root, "plystra.yaml")
	writeFile(t, configurationPath, "http:\n  transports: {connect: true, rest: false}\ncapabilities: {require: [email.send/v1]}\nconfig:\n  acme.mailer:\n    endpoint: 'C:/private/machine-one'\n    password: {env: PRIVATE_TOKEN_ONE}\n")
	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PRIVATE_TOKEN_ONE": "resolved-super-secret-one", "PRIVATE_TOKEN_TWO": "resolved-super-secret-two"}),
		Validate:    func(_ context.Context, _ string) error { return nil },
	}
	initial, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !initial.Report().Clean() {
		t.Fatalf("initial Generate = changes %#v, %v", initial.Report().Changes(), err)
	}
	initialProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(initial): %v", err)
	}

	writeFile(t, configurationPath, "http:\n  transports: {connect: true, rest: false}\ncapabilities: {require: [email.send/v1]}\nconfig:\n  acme.mailer:\n    endpoint: 'D:/private/machine-two'\n    password: {env: PRIVATE_TOKEN_TWO}\n")
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !updated.Report().Clean() {
		t.Fatalf("updated Generate = changes %#v, %v", updated.Report().Changes(), err)
	}
	manifestData := readFile(t, root, "generated/manifest.json")
	bootstrapData := readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	updatedProvenance, err := applicationgen.DecodeManifestProvenance(manifestData)
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(updated): %v", err)
	}
	if updatedProvenance.ApplicationModelDigest() != initialProvenance.ApplicationModelDigest() {
		t.Fatalf("runtime-only values changed application_model_digest: %q != %q", updatedProvenance.ApplicationModelDigest(), initialProvenance.ApplicationModelDigest())
	}
	for _, required := range []string{
		"compiledConfigurationSelectionProvenanceJSON",
		"compiledConfigurationSelectionProvenanceDigest",
		"compiledApplicationModelCompatibilityJSON",
		"compiledApplicationModelCompatibilityDigest",
		"validateRuntimeApplicationModel(document)",
		updatedProvenance.RootDigest(),
		updatedProvenance.ApplicationModelDigest(),
	} {
		if !bytes.Contains(bootstrapData, []byte(required)) {
			t.Fatalf("generated bootstrap omits non-secret configuration provenance %q", required)
		}
	}
	for _, forbidden := range []string{
		root,
		"C:/private/machine-one",
		"D:/private/machine-two",
		"PRIVATE_TOKEN_ONE",
		"PRIVATE_TOKEN_TWO",
		"resolved-super-secret-one",
		"resolved-super-secret-two",
	} {
		if bytes.Contains(manifestData, []byte(forbidden)) || bytes.Contains(bootstrapData, []byte(forbidden)) {
			t.Fatalf("generated provenance leaked %q", forbidden)
		}
	}
}

func TestGenerateMaintainsRootAndTracksSelectedEnvironmentOverlay(t *testing.T) {
	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "platform")
	writeModule(t, dependencyRoot, "example.com/platform", "")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {require: [kernel.health/v1]}\n")
	writeApplicationModule(t, appRoot, "example.com/acme/environment")
	goModPath := filepath.Join(appRoot, "go.mod")
	goMod := string(readAbsoluteFile(t, goModPath)) + fmt.Sprintf("\nrequire example.com/platform v1.0.0\n\nreplace example.com/platform => %s\n", filepath.ToSlash(dependencyRoot))
	writeFile(t, goModPath, goMod)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "# shared root\ncapabilities: {require: [kernel.info/v1]}\n")
	overlayPath := filepath.Join(appRoot, "plystra.production.yaml")
	overlayData := []byte("# sparse production overlay\ncapabilities:\n  require: {remove: [kernel.info/v1]}\n")
	writeFile(t, overlayPath, string(overlayData))
	environment := goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
	validate := func(_ context.Context, _ string) error { return nil }

	generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:           appRoot,
		EnvironmentName: "production",
		Environment:     environment,
		Validate:        validate,
	})
	if err != nil || !generated.ConfigurationChanged() || generated.ConfigurationPath() != "plystra.production.yaml" || generated.ConfigurationMaintenancePath() != "plystra.yaml" || !generated.Report().Clean() {
		t.Fatalf("Generate environment = selection %q maintenance %q changed %t report %#v, %v", generated.ConfigurationPath(), generated.ConfigurationMaintenancePath(), generated.ConfigurationChanged(), generated.Report().Changes(), err)
	}
	if current := readAbsoluteFile(t, overlayPath); !bytes.Equal(current, overlayData) {
		t.Fatalf("environment generation rewrote sparse overlay:\n%s", current)
	}
	if current := readFile(t, appRoot, "plystra.yaml"); !bytes.Contains(current, []byte("kernel.health/v1")) || !bytes.Contains(current, []byte("# shared root")) {
		t.Fatalf("environment generation did not maintain root baseline:\n%s", current)
	}
	provenance, err := applicationgen.DecodeManifestProvenance(readFile(t, appRoot, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance: %v", err)
	}
	if provenance.Mode() != applicationgen.ConfigurationModeEnvironment || provenance.Environment() != "production" || provenance.RootPath() != "plystra.yaml" || provenance.SelectedPath() != "plystra.production.yaml" {
		t.Fatalf("environment provenance = mode %q environment %q root %q selected %q", provenance.Mode(), provenance.Environment(), provenance.RootPath(), provenance.SelectedPath())
	}
	environmentBaseline, environmentExists := provenance.BaselineForSelection(applicationgen.ConfigurationModeEnvironment, "plystra.production.yaml")
	defaultBaseline, defaultExists := provenance.BaselineForSelection(applicationgen.ConfigurationModeDefault, "plystra.yaml")
	if !environmentExists || !defaultExists || environmentBaseline.Digest() != defaultBaseline.Digest() {
		t.Fatalf("environment baseline = %q/%t default %q/%t", environmentBaseline.Digest(), environmentExists, defaultBaseline.Digest(), defaultExists)
	}

	beforeCheck := snapshotTree(t, appRoot)
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:           appRoot,
		Check:           true,
		EnvironmentName: "production",
		Environment:     environment,
	})
	if err != nil || checked.ConfigurationChanged() || !checked.Report().Clean() {
		t.Fatalf("clean environment check = changed %t report %#v, %v", checked.ConfigurationChanged(), checked.Report().Changes(), err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, beforeCheck) {
		t.Fatal("environment generate --check mutated the Project")
	}

	changedOverlay := []byte("# sparse production overlay\ncapabilities:\n  require: {add: [kernel.info/v1]}\n")
	writeFile(t, overlayPath, string(changedOverlay))
	beforeDriftCheck := snapshotTree(t, appRoot)
	drift, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:           appRoot,
		Check:           true,
		EnvironmentName: "production",
		Environment:     environment,
	})
	if err != nil || drift.Report().Clean() {
		t.Fatalf("changed environment check = report %#v, %v", drift.Report().Changes(), err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, beforeDriftCheck) {
		t.Fatal("environment drift check mutated the Project")
	}

	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	rollbackBefore := snapshotTree(t, appRoot)
	validationFailure := errors.New("reject environment root maintenance")
	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:           appRoot,
		EnvironmentName: "production",
		Environment:     environment,
		Validate:        func(_ context.Context, _ string) error { return validationFailure },
	})
	if !errors.Is(err, validationFailure) {
		t.Fatalf("environment validation failure = %v", err)
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, rollbackBefore) {
		t.Fatal("environment validation failure did not roll back root and generated output")
	}
	if current := readAbsoluteFile(t, overlayPath); !bytes.Equal(current, changedOverlay) {
		t.Fatalf("environment rollback rewrote sparse overlay:\n%s", current)
	}
	cleanupRecoveryTransactions(t, appRoot)
}

func TestGenerateRejectsOrdinaryGoModuleWithoutProjectMarker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/ordinary")
	writePlugin(t, root, "business", "id: acme.ordinary.business\n")
	before := snapshotTree(t, root)
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       filepath.Join(root, "business"),
		Environment: goEnvironment(nil),
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !strings.Contains(err.Error(), "has no root plystra.yaml") {
		t.Fatalf("Generate ordinary module error = %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("ordinary module generation mutated files:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestGenerateWiresLocalPluginRequirementsThroughPublicRuntime(t *testing.T) {
	const modulePath = "example.com/acme/wired-application"
	root := t.TempDir()
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [order.place/v1]\n")
	writePlugin(t, root, "catalog", "id: acme.catalog\nprovides: [catalog.lookup/v1]\n")
	writeCapability(t, root, "catalog", "catalog.lookup/v1", `id: catalog.lookup/v1
request:
  key: {type: string, required: true}
response:
  value: {type: string, required: true}
errors: []
`)
	writePlugin(t, root, "orders", "id: acme.orders\nprovides: [order.place/v1]\nrequires: [catalog.lookup/v1]\n")
	writeCapability(t, root, "orders", "order.place/v1", `id: order.place/v1
request:
  key: {type: string, required: true}
response:
  value: {type: string, required: true}
errors: []
`)
	writeFile(t, filepath.Join(root, "catalog", "plugin.go"), `package catalog

import (
	"context"

	configuration "example.com/acme/wired-application/generated/go/configuration"
	contract "example.com/acme/wired-application/generated/go/contracts/catalog/lookup/v1"
)

type Config = configuration.CatalogConfig
type Plugin struct{}

func New(Config) *Plugin { return &Plugin{} }

func (*Plugin) Lookup(_ context.Context, request contract.Request) (contract.Response, error) {
	return contract.Response{Value: "catalog:" + request.Key}, nil
}
`)
	writeFile(t, filepath.Join(root, "orders", "plugin.go"), `package orders

import (
	"context"

	lookupcontract "example.com/acme/wired-application/generated/go/contracts/catalog/lookup/v1"
	ordercontract "example.com/acme/wired-application/generated/go/contracts/order/place/v1"
	configuration "example.com/acme/wired-application/generated/go/configuration"
	dependencies "example.com/acme/wired-application/generated/go/dependencies/orders"
)

type Config = configuration.OrdersConfig
type Plugin struct{ clients dependencies.Dependencies }

func New(_ Config, clients dependencies.Dependencies) *Plugin {
	return &Plugin{clients: clients}
}

func (p *Plugin) Place(ctx context.Context, request ordercontract.Request) (ordercontract.Response, error) {
	response, err := p.clients.CatalogLookupV1().Lookup(ctx, lookupcontract.Request{Key: request.Key})
	if err != nil {
		return ordercontract.Response{}, err
	}
	return ordercontract.Response{Value: "order:" + response.Value}, nil
}
`)
	environment := goEnvironment(nil)
	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate wired application = %#v, %v", result.Report().Changes(), err)
	}
	for _, filePath := range []string{
		"generated/go/clients/catalog/lookup/v1/client_gen.go",
		"generated/go/dependencies/orders/dependencies_gen.go",
		"generated/go/invocation/catalog/lookup/v1/invocation_gen.go",
		"generated/go/invocation/order/place/v1/invocation_gen.go",
	} {
		assertFileExists(t, root, filePath)
	}
	writeFile(t, filepath.Join(root, "wiring_runtime_test.go"), wiredApplicationRuntimeTest)
	command := exec.CommandContext(t.Context(), "go", "test", "-mod=readonly", "-count=1", "./...")
	command.Dir = root
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated wired application runtime: %v\n%s", err, output)
	}
	if clean, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment}); err != nil || !clean.Report().Clean() {
		t.Fatalf("wired application Generate --check = %#v, %v", clean.Report().Changes(), err)
	}
}

func TestGenerateRequiresDirectKernelDependency(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/acme/missing-kernel", "")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: goEnvironment(nil),
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrKernelDependency) || !strings.Contains(err.Error(), "go.mod must directly require github.com/plystra/kernel") {
		t.Fatalf("Generate without Kernel dependency = %v", err)
	}
}

func TestGenerateCompilesUnrequiredLocalCapabilityWithoutRuntimeAssembly(t *testing.T) {
	const modulePath = "example.com/acme/unrequired-capability"
	root := t.TempDir()
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writePlugin(t, root, "business", "id: acme.business\nprovides: [email.send/v1]\n")
	writeCapability(t, root, "business", "email.send/v1", `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeFile(t, filepath.Join(root, "business", "plugin.go"), `package business

import (
	"context"

	configuration "example.com/acme/unrequired-capability/generated/go/configuration"
	contract "example.com/acme/unrequired-capability/generated/go/contracts/email/send/v1"
)

type Config = configuration.BusinessConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, request contract.Request) (contract.Response, error) {
	if request.To == "" {
		return contract.Response{}, contract.ErrInvalidRecipient
	}
	return contract.Response{Accepted: true}, nil
}
`)
	writeFile(t, filepath.Join(root, "unrequired_runtime_test.go"), `package unrequiredapp_test

import (
	"context"
	"testing"

	bootstrap "example.com/acme/unrequired-capability/generated/go/bootstrap"
)

func TestUnrequiredCapabilityIsNotRegistered(t *testing.T) {
	application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	bindings := application.Invocations().Catalog().Bindings()
	if len(bindings) != 2 || bindings[0].Capability().String() != "kernel.health/v1" || bindings[1].Capability().String() != "kernel.info/v1" {
		t.Fatalf("runtime catalog bindings = %#v, want only intrinsic Kernel Capabilities", bindings)
	}
}
`)
	environment := goEnvironment(nil)
	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       filepath.Join(root, "business"),
		Environment: environment,
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate unrequired local Capability = %#v, %v", result.Report().Changes(), err)
	}
	for _, filePath := range []string{
		"generated/go/configuration/business_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
	} {
		assertFileExists(t, root, filePath)
	}
	for _, filePath := range []string{
		"generated/docs/api.md",
		"generated/go/adapters/http/email/send/v1/handler_gen.go",
		"generated/go/clients/email/send/v1/client_gen.go",
		"generated/go/internal/invocationcontext/context_gen.go",
		"generated/go/invocation/email/send/v1/invocation_gen.go",
		"generated/sdk/javascript/package.json",
	} {
		assertFileMissing(t, root, filePath)
	}
	if assembly := readFile(t, root, "generated/go/assembly/invocations_gen.go"); bytes.Contains(assembly, []byte("email.send/v1")) {
		t.Fatalf("unrequired local Capability entered runtime assembly:\n%s", assembly)
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Check:       true,
		Environment: environment,
	})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("Generate --check = %#v, %v", checked.Report().Changes(), err)
	}
}

func TestGenerateRunsIntrinsicApplicationWithoutOrdinaryPlugins(t *testing.T) {
	const modulePath = "example.com/acme/intrinsic-app"
	root := t.TempDir()
	writeConnectApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose: [kernel.health/v1]
capabilities:
  require: [kernel.info/v1]
  aliases:
    health.status/v1: kernel.health/v1
`)
	environment := goEnvironment(nil)
	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate intrinsic application = %#v, %v", result.Report().Changes(), err)
	}

	for _, filePath := range []string{
		"generated/go/adapters/http/kernel/health/v1/handler_gen.go",
		"generated/go/adapters/http/health/status/v1/handler_gen.go",
		"generated/go/clients/kernel/health/v1/client_gen.go",
		"generated/go/clients/kernel/info/v1/client_gen.go",
		"generated/go/contracts/kernel/health/v1/contract_gen.go",
		"generated/go/contracts/kernel/info/v1/contract_gen.go",
		"generated/go/invocation/kernel/health/v1/invocation_gen.go",
		"generated/go/invocation/kernel/info/v1/invocation_gen.go",
		"generated/sdk/javascript/src/operations/kernel/health/v1.ts",
	} {
		assertFileExists(t, root, filePath)
	}
	for _, filePath := range []string{
		"generated/go/adapters/http/kernel/info/v1/handler_gen.go",
		"generated/sdk/javascript/src/operations/kernel/info/v1.ts",
	} {
		assertFileMissing(t, root, filePath)
	}

	healthContract := readFile(t, root, "generated/go/contracts/kernel/health/v1/contract_gen.go")
	for _, required := range [][]byte{
		[]byte("type Request = kernelintrinsic.HealthRequest"),
		[]byte("type Response = kernelintrinsic.HealthResponse"),
		[]byte("type ResponseStatus = kernelintrinsic.HealthStatus"),
	} {
		if !bytes.Contains(healthContract, required) {
			t.Fatalf("generated health contract omits %q:\n%s", required, healthContract)
		}
	}
	assembly := readFile(t, root, "generated/go/assembly/invocations_gen.go")
	for _, required := range [][]byte{
		[]byte("kernelintrinsic.NewBindings"),
		[]byte("kernelintrinsic.HealthContract()"),
		[]byte("kernelintrinsic.InfoContract()"),
	} {
		if !bytes.Contains(assembly, required) {
			t.Fatalf("generated intrinsic assembly omits %q:\n%s", required, assembly)
		}
	}

	writeFile(t, filepath.Join(root, "intrinsic_runtime_test.go"), intrinsicApplicationRuntimeTest)
	command := exec.CommandContext(t.Context(), "go", "test", "-mod=readonly", "-count=1", "./...")
	command.Dir = root
	command.Env = environment
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated intrinsic application runtime: %v\n%s", err, output)
	}
}

func TestGenerateRendersValidatesAndCleansCanonicalAliasSurfaces(t *testing.T) {
	root := t.TempDir()
	writeConnectApplicationModule(t, root, "github.com/acme/my-app")
	writePlugin(t, root, "business", "id: acme.business\nprovides: [email.send/v1]\n")
	writeCapability(t, root, "business", "email.send/v1", `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeFile(t, filepath.Join(root, "business", "plugin.go"), `package business

import (
	"context"

	configuration "github.com/acme/my-app/generated/go/configuration"
	contract "github.com/acme/my-app/generated/go/contracts/email/send/v1"
)

type Config = configuration.BusinessConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, request contract.Request) (contract.Response, error) {
	if request.To == "" {
		return contract.Response{}, contract.ErrInvalidRecipient
	}
	return contract.Response{Accepted: true}, nil
}
`)
	withAlias := `http:
  expose: [email.send/v1]
capabilities:
  aliases:
    mail.deliver/v1:
      target: email.send/v1
      deprecated:
        message: Use email.send/v1 instead.
`
	writeFile(t, filepath.Join(root, "plystra.yaml"), withAlias)
	environment := goEnvironment(nil)

	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       filepath.Join(root, "business"),
		Environment: environment,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !result.Report().Clean() {
		t.Fatalf("installed changes = %#v", result.Report().Changes())
	}
	for _, filePath := range []string{
		"generated/docs/api.md",
		"generated/docs/openapi.json",
		"generated/go/adapters/http/email/send/v1/handler_gen.go",
		"generated/go/adapters/http/mail/deliver/v1/handler_gen.go",
		"generated/go/application/main_gen.go",
		"generated/go/assembly/compatibility_gen.go",
		"generated/go/assembly/invocations_gen.go",
		"generated/go/assembly/providers_gen.go",
		"generated/go/bootstrap/bootstrap_gen.go",
		"generated/go/clients/email/send/v1/client_gen.go",
		"generated/go/clients/mail/deliver/v1/client_gen.go",
		"generated/go/contracts/email/send/v1/contract_gen.go",
		"generated/go/configuration/business_gen.go",
		"generated/go/invocation/email/send/v1/invocation_gen.go",
		"generated/go/providers/email/send/v1/provider_gen.go",
		"generated/manifest.json",
		"generated/proto/descriptor-set.pb",
		"generated/proto/plystra/generated/email/send/v1/capability.proto",
		"generated/proto/plystra/generated/mail/deliver/v1/capability.proto",
		"generated/sdk/javascript/package.json",
		"generated/sdk/javascript/src/operations/email/send/v1.ts",
		"generated/sdk/javascript/src/operations/mail/deliver/v1.ts",
		generatedfiles.ManifestPath,
	} {
		assertFileExists(t, root, filePath)
	}
	packageJSON := readFile(t, root, "generated/sdk/javascript/package.json")
	if !bytes.Contains(packageJSON, []byte(`"name": "@acme/my-app-sdk"`)) {
		t.Fatalf("package.json has wrong inferred identity:\n%s", packageJSON)
	}
	manifest := readFile(t, root, "generated/manifest.json")
	for _, value := range [][]byte{[]byte(`"id":"mail.deliver/v1"`), []byte(`"target":"email.send/v1"`), []byte(`"deprecated":"Use email.send/v1 instead."`)} {
		if !bytes.Contains(manifest, value) {
			t.Fatalf("manifest omits %s:\n%s", value, manifest)
		}
	}

	writeFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  expose: [email.send/v1]\n")
	withoutAlias, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
	})
	if err != nil || !withoutAlias.Report().Clean() {
		t.Fatalf("Generate without Alias = %#v, %v", withoutAlias.Report().Changes(), err)
	}
	for _, obsolete := range []string{
		"generated/go/adapters/http/mail/deliver/v1/handler_gen.go",
		"generated/go/clients/mail/deliver/v1/client_gen.go",
		"generated/proto/plystra/generated/mail/deliver/v1/capability.proto",
		"generated/sdk/javascript/src/operations/mail/deliver/v1.ts",
	} {
		assertFileMissing(t, root, obsolete)
	}
	assertFileExists(t, root, "generated/go/clients/email/send/v1/client_gen.go")
	clean, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: environment})
	if err != nil || !clean.Report().Clean() {
		t.Fatalf("final check = %#v, %v", clean.Report().Changes(), err)
	}
}

func TestGenerateRollsBackValidationFailureAndPreservesConcurrentSourceEdit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/rollback-app")
	writePlugin(t, root, "business", "id: acme.business\nprovides: [email.send/v1]\n")
	writeCapability(t, root, "business", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	withoutAlias := "capabilities:\n  require: [email.send/v1]\n"
	withAlias := withoutAlias + "  aliases:\n    mail.deliver/v1: email.send/v1\n"
	manifestPath := filepath.Join(root, "plystra.yaml")
	writeFile(t, manifestPath, withoutAlias)
	environment := goEnvironment(nil)
	noValidation := func(_ context.Context, _ string) error { return nil }
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: noValidation}); err != nil || !result.Report().Clean() {
		t.Fatalf("initial Generate = %#v, %v", result.Report().Changes(), err)
	}
	generatedBefore := snapshotGenerated(t, root)

	writeFile(t, manifestPath, withAlias)
	validationFailure := errors.New("validation rejected generated tree")
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			return validationFailure
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, validationFailure) {
		t.Fatalf("validation failure = %v", err)
	}
	if after := snapshotGenerated(t, root); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatalf("generated tree changed after validation rollback:\nbefore: %#v\nafter:  %#v", generatedBefore, after)
	}

	_, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			writeFile(t, manifestPath, withoutAlias)
			return nil
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrConcurrentChange) {
		t.Fatalf("concurrent source edit = %v", err)
	}
	if got := string(readAbsoluteFile(t, manifestPath)); got != withoutAlias {
		t.Fatalf("concurrent manifest edit was not preserved: %q", got)
	}
	if after := snapshotGenerated(t, root); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatalf("generated tree changed after concurrent-edit rollback:\nbefore: %#v\nafter:  %#v", generatedBefore, after)
	}
	assertNoTransactions(t, root)
}

func TestGenerateDetectsConcurrentPrivateConfigurationChange(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/config-change-app")
	writePlugin(t, root, "business", "id: acme.business\nconfig: {label: {type: string}}\n")
	manifestPath := filepath.Join(root, "plystra.yaml")
	first := "config:\n  acme.business:\n    label: private-one\n"
	second := "config:\n  acme.business:\n    label: private-two\n"
	writeFile(t, manifestPath, first)
	environment := goEnvironment(nil)
	noValidation := func(_ context.Context, _ string) error { return nil }
	if result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Environment: environment, Validate: noValidation}); err != nil || !result.Report().Clean() {
		t.Fatalf("initial Generate = %#v, %v", result.Report().Changes(), err)
	}
	generatedBefore := snapshotGenerated(t, root)
	configurationSource := readFile(t, root, "generated/go/configuration/business_gen.go")
	for _, forbidden := range []string{"private-one", "private-two"} {
		if bytes.Contains(configurationSource, []byte(forbidden)) {
			t.Fatalf("generated configuration source exposed %q:\n%s", forbidden, configurationSource)
		}
	}

	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: environment,
		Validate: func(_ context.Context, _ string) error {
			writeFile(t, manifestPath, second)
			return nil
		},
	})
	if !errors.Is(err, applicationgenerate.ErrGenerate) || !errors.Is(err, applicationgenerate.ErrConcurrentChange) {
		t.Fatalf("concurrent private configuration edit = %v", err)
	}
	if got := string(readAbsoluteFile(t, manifestPath)); got != second {
		t.Fatalf("concurrent private configuration edit was not preserved: %q", got)
	}
	if after := snapshotGenerated(t, root); !reflect.DeepEqual(after, generatedBefore) {
		t.Fatalf("generated tree changed after private configuration edit:\nbefore: %#v\nafter:  %#v", generatedBefore, after)
	}
	assertNoTransactions(t, root)
}

func TestGenerateExecutesRealSelectedExtensionAndCleansHelpers(t *testing.T) {
	root := t.TempDir()
	temporaryParent := t.TempDir()
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(`module example.com/acme/extension-app

go 1.26

require (
	github.com/plystra/cli v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/cli => %s

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(cliRoot), filepath.ToSlash(kernelRoot))
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	downloadModuleDependencies(t, root)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [order.create/v1]\n")
	writePlugin(t, root, "business", "id: example.business\nprovides: [order.create/v1]\n")
	writePlugin(t, root, "authn", `id: example.authn
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writePlugin(t, root, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	writeCapability(t, root, "business", "order.create/v1", `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
`)
	writeCapability(t, root, "authn", "authn.session.verify/v1", "id: authn.session.verify/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCapability(t, root, "audit", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(root, "authn", "generation", "generate.go"), realExtensionSource)
	extensionBefore := readFile(t, root, "authn/generation/generate.go")

	result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:            root,
		Environment:      goEnvironment(map[string]string{"GOPROXY": "off"}),
		CompileTimeout:   2 * time.Minute,
		ExecutionTimeout: 10 * time.Second,
		TemporaryParent:  temporaryParent,
		Validate:         func(_ context.Context, _ string) error { return nil },
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate with extension = %#v, %v", result.Report().Changes(), err)
	}
	initialProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(initial extension output): %v", err)
	}
	for _, capability := range []string{"order/create/v1", "authn/session/verify/v1", "audit/write/v1"} {
		assertFileExists(t, root, "generated/go/clients/"+capability+"/client_gen.go")
		assertFileExists(t, root, "generated/go/invocation/"+capability+"/invocation_gen.go")
	}
	entries, err := os.ReadDir(temporaryParent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary extension helpers = %v, %v", entries, err)
	}
	if after := readFile(t, root, "authn/generation/generate.go"); !bytes.Equal(after, extensionBefore) {
		t.Fatal("extension source changed during generation")
	}

	changedExtension := strings.Replace(realExtensionSource, "authn.require-audit", "authn.require-audit-v2", 1)
	writeFile(t, filepath.Join(root, "authn", "generation", "generate.go"), changedExtension)
	result, err = applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:            root,
		Environment:      goEnvironment(map[string]string{"GOPROXY": "off"}),
		CompileTimeout:   2 * time.Minute,
		ExecutionTimeout: 10 * time.Second,
		TemporaryParent:  temporaryParent,
		Validate:         func(_ context.Context, _ string) error { return nil },
	})
	if err != nil || !result.Report().Clean() {
		t.Fatalf("Generate with changed extension contribution = %#v, %v", result.Report().Changes(), err)
	}
	changedProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(changed extension output): %v", err)
	}
	if initialProvenance.ApplicationModelDigest() == changedProvenance.ApplicationModelDigest() {
		t.Fatal("generation-extension output and contribution change did not alter application_model_digest")
	}
	entries, err = os.ReadDir(temporaryParent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary extension helpers after changed contribution = %v, %v", entries, err)
	}
}

func writeApplicationModule(t testing.TB, root, modulePath string) {
	t.Helper()
	writeApplicationModuleDefinition(t, root, modulePath)
	downloadModuleDependencies(t, root)
}

func writeApplicationModuleDefinition(t testing.TB, root, modulePath string) {
	t.Helper()
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	extra := fmt.Sprintf(`require (
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4
	golang.org/x/mod v0.38.0 // indirect
)

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot))
	writeModule(t, root, modulePath, extra)
}

func writeConnectApplicationModule(t testing.TB, root, modulePath string) {
	t.Helper()
	writeApplicationModuleDefinition(t, root, modulePath)
	legacyProtobufRoot := filepath.Join(t.TempDir(), "legacy-protobuf")
	writeModule(t, legacyProtobufRoot, "github.com/golang/protobuf", "")
	goModPath := filepath.Join(root, "go.mod")
	data := string(readAbsoluteFile(t, goModPath)) + fmt.Sprintf(`
require (
	connectrpc.com/connect v1.20.0
	google.golang.org/protobuf v1.36.11
)

replace github.com/golang/protobuf => %s
`, filepath.ToSlash(legacyProtobufRoot))
	writeFile(t, goModPath, data)
	downloadModuleDependencies(t, root)
}

func downloadModuleDependencies(t testing.TB, root string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", "mod", "download", "all")
	command.Dir = root
	command.Env = mergedEnvironment(map[string]string{
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go mod download in %s: %v\n%s", root, err, output)
	}
}

func writeModule(t testing.TB, root, modulePath, extra string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n\n"+extra)
}

func writePlugin(t testing.TB, root, name, manifest string) {
	t.Helper()
	writeFile(t, filepath.Join(root, name, "plugin.yaml"), manifest)
	metadata, err := pluginmeta.Parse([]byte(manifest))
	if err != nil {
		t.Fatalf("pluginmeta.Parse(%s): %v", name, err)
	}
	module, err := modulelocate.Find(root)
	if err != nil {
		t.Fatalf("modulelocate.Find(%s): %v", root, err)
	}
	names, err := configurationgen.DeriveGoNames(name)
	if err != nil {
		t.Fatalf("configurationgen.DeriveGoNames(%s): %v", name, err)
	}
	packageName := strings.ReplaceAll(name, "-", "")
	if token.Lookup(packageName).IsKeyword() {
		packageName += "plugin"
	}
	source, err := format.Source([]byte(fmt.Sprintf(`package %s

import configuration %q

type Config = configuration.%s
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }
`, packageName, module.ModulePath()+"/generated/go/configuration", names.TypeName())))
	if err != nil {
		t.Fatalf("format %s/plugin.go for %s: %v", name, metadata.ID(), err)
	}
	writeFile(t, filepath.Join(root, name, "plugin.go"), string(source))
}

func writeCapability(t testing.TB, root, plugin, value, source string) {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("capabilityid.Parse(%s): %v", value, err)
	}
	writeFile(t, filepath.Join(root, plugin, "capabilities", filepath.FromSlash(identifier.Name()), "v"+strconv.FormatUint(identifier.Major(), 10), "capability.yaml"), withQuerySemantics(source))
}

func withQuerySemantics(source string) string {
	if strings.Contains(source, "\nsemantics:") {
		return source
	}
	if !strings.HasSuffix(source, "\n") {
		source += "\n"
	}
	return source + querySemanticsYAML
}

const querySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func writeFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func readFile(t testing.TB, root, name string) []byte {
	t.Helper()
	return readAbsoluteFile(t, filepath.Join(root, filepath.FromSlash(name)))
}

func readAbsoluteFile(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return data
}

func assertFileExists(t testing.TB, root, name string) {
	t.Helper()
	info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("%s is not a regular file: %v", name, err)
	}
}

func assertFileMissing(t testing.TB, root, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s exists: %v", name, err)
	}
}

type treeEntry struct {
	path string
	mode fs.FileMode
	data []byte
}

func snapshotTree(t testing.TB, root string) []treeEntry {
	t.Helper()
	return snapshotSubtree(t, root, ".")
}

func snapshotGenerated(t testing.TB, root string) []treeEntry {
	t.Helper()
	return snapshotSubtree(t, root, "generated")
}

func snapshotSubtree(t testing.TB, root, subtree string) []treeEntry {
	t.Helper()
	var result []treeEntry
	err := fs.WalkDir(os.DirFS(root), subtree, func(name string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, fs.ErrNotExist) {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := treeEntry{path: filepath.ToSlash(name), mode: info.Mode()}
		if info.Mode().IsRegular() {
			item.data, err = os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				return err
			}
		}
		result = append(result, item)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s): %v", subtree, err)
	}
	return result
}

func assertNoTransactions(t testing.TB, root string) {
	t.Helper()
	for _, pattern := range []string{".plystra-files-*", ".plystra-generation-*"} {
		matches, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil || len(matches) != 0 {
			t.Fatalf("transaction matches for %s = %v, %v", pattern, matches, err)
		}
	}
}

func cleanupRecoveryTransactions(t testing.TB, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".plystra-files-*"))
	if err != nil {
		t.Fatalf("glob recovery transactions: %v", err)
	}
	for _, match := range matches {
		if err := os.RemoveAll(match); err != nil {
			t.Fatalf("remove recovery transaction %s: %v", match, err)
		}
	}
}

func goEnvironment(overrides map[string]string) []string {
	values := map[string]string{
		"GOENV":       "off",
		"GOFLAGS":     "",
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	for key, value := range overrides {
		values[strings.ToUpper(key)] = value
	}
	return mergedEnvironment(values)
}

func mergedEnvironment(values map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := values[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	return environment
}

func repositoryRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository root): %v", err)
	}
	return root
}

const realExtensionSource = `package extension

import generation "github.com/plystra/cli/generation/v1"

func Generate(context generation.GenerationContext) (generation.Output, error) {
	order, _ := generation.ParseCapabilityID("order.create/v1")
	audit, _ := generation.ParseCapabilityID("audit.write/v1")
	if _, exists := context.Capability(order); !exists {
		return generation.Output{}, nil
	}
	return generation.Output{Requirements: []generation.Requirement{{
		RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit,
	}}}, nil
}
`

const intrinsicApplicationRuntimeTest = `package intrinsicapp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	bootstrap "example.com/acme/intrinsic-app/generated/go/bootstrap"
	healthcontract "example.com/acme/intrinsic-app/generated/go/contracts/kernel/health/v1"
	infocontract "example.com/acme/intrinsic-app/generated/go/contracts/kernel/info/v1"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestIntrinsicApplicationRuntime(t *testing.T) {
	application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	invocations := application.Invocations()
	bindings := invocations.Catalog().Bindings()
	if len(bindings) != 2 || bindings[0].Capability().String() != "kernel.health/v1" || bindings[1].Capability().String() != "kernel.info/v1" {
		t.Fatalf("intrinsic catalog = %#v", bindings)
	}
	for _, binding := range bindings {
		build := binding.ProviderBuild()
		if binding.ProviderKind() != kernelinvocation.ProviderKindKernel ||
			binding.ProviderID().String() != "" ||
			binding.ProviderPackage() != kernelintrinsic.ProviderPackage ||
			binding.SelectionReason() != kernelinvocation.SelectionReasonIntrinsic ||
			build.ModulePath() != kernelintrinsic.ModulePath ||
			build.ModuleVersion() != "v0.0.0" ||
			build.BuildIdentity() == "" || binding.SchemaDigest() == [32]byte{} {
			t.Fatalf("intrinsic provenance for %s is incomplete", binding.Capability())
		}
	}

	health, err := invocations.KernelHealthV1().Invoke(context.Background(), healthcontract.Request{})
	if err != nil || health.Status != healthcontract.ResponseStatusHealthy {
		t.Fatalf("kernel.health/v1 = %#v, %v", health, err)
	}
	info, err := invocations.KernelInfoV1().Invoke(context.Background(), infocontract.Request{})
	if err != nil || info.AssemblyAPI != "v1" || info.KernelModule != kernelintrinsic.ModulePath || info.KernelVersion != "v0.0.0" {
		t.Fatalf("kernel.info/v1 = %#v, %v", info, err)
	}
	formatted := fmt.Sprintf("%+v", info)
	for _, forbidden := range []string{"sha256:", "plystra.yaml", "intrinsic-app", "secret"} {
		if strings.Contains(strings.ToLower(formatted), strings.ToLower(forbidden)) {
			t.Fatalf("kernel.info/v1 exposed %q in %s", forbidden, formatted)
		}
	}
}
`

const wiredApplicationRuntimeTest = `package wiredapplication_test

import (
	"context"
	"testing"

	bootstrap "example.com/acme/wired-application/generated/go/bootstrap"
	ordercontract "example.com/acme/wired-application/generated/go/contracts/order/place/v1"
)

func TestGeneratedCrossPluginCall(t *testing.T) {
	application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	invocations := application.Invocations()
	response, err := invocations.OrderPlaceV1().Invoke(context.Background(), ordercontract.Request{Key: "item"})
	if err != nil || response.Value != "order:catalog:item" {
		t.Fatalf("OrderPlaceV1.Invoke = %#v, %v", response, err)
	}
	bindings := invocations.Catalog().Bindings()
	if len(bindings) != 4 {
		t.Fatalf("catalog bindings = %#v", bindings)
	}
}
`
