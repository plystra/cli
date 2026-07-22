package interfaceinventory_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/projectlocate"
)

func TestDiscoverLoadsOnlyActiveEligiblePackagesDeterministically(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProject(t, root, "example.com/app")
	writeFile(t, filepath.Join(root, "interfaces", "zeta", "v1", "interface.go"), interfaceSource("zetav1", "zeta.execute/v1", "Execute"))
	writeFile(t, filepath.Join(root, "deep", "domain", "records", "alpha", "v2", "interface.go"), interfaceSource("alphav2", "records.alpha.read/v2", "Read"))
	writeFile(t, filepath.Join(root, "tagged", "interface.go"), "//go:build inventory_active\n\n"+interfaceSource("tagged", "tagged.active.run/v1", "Run"))
	writeFile(t, filepath.Join(root, "inactive", "interface.go"), "//go:build inventory_inactive\n\n"+interfaceSource("inactive", "tagged.inactive.run/v1", "Run"))
	writeFile(t, filepath.Join(root, "inactive", interfacemeta.Name), "[\n")
	writeFile(t, filepath.Join(root, "testonly", "interface_test.go"), interfaceSource("testonly", "test.only.run/v1", "Run"))
	writeFile(t, filepath.Join(root, "ordinary", "ordinary.go"), "package ordinary\n\nconst markerText = \"//plystra:interface ignored.string.value/v1\"\n")

	for _, reserved := range []string{"generated", "vendor", "testdata", "fixture", "fixtures", ".hidden", "_hidden", "dist"} {
		writeFile(t, filepath.Join(root, reserved, "bad", "interface.go"), "package bad\n//plystra:interface invalid\ntype Interface interface{}\n")
	}
	nested := filepath.Join(root, "nested")
	writeProject(t, nested, "example.com/nested")
	writeFile(t, filepath.Join(nested, "interface.go"), "package nested\n//plystra:interface invalid\ntype Interface interface{}\n")

	before := snapshotFiles(t, root)
	environment := goEnvironment(map[string]string{
		"GOFLAGS": "-tags=inventory_active",
		"GOPROXY": "off",
		"GOSUMDB": "off",
		"GOWORK":  "off",
	})
	first := discover(t, root, environment)
	second := discover(t, root, environment)
	if err := interfaceinventory.ValidateUniqueIDs(first); err != nil {
		t.Fatalf("ValidateUniqueIDs: %v", err)
	}

	wantIDs := []string{"records.alpha.read/v2", "tagged.active.run/v1", "zeta.execute/v1"}
	if got := interfaceIDs(first); !slices.Equal(got, wantIDs) {
		t.Fatalf("Interface IDs = %v, want %v", got, wantIDs)
	}
	if got := inventorySummary(first); !reflect.DeepEqual(got, inventorySummary(second)) {
		t.Fatalf("repeated discovery changed inventory:\nfirst: %#v\nsecond: %#v", got, inventorySummary(second))
	}
	for _, discovered := range first.Interfaces() {
		if !discovered.Local() || discovered.ModulePath() != "example.com/app" || discovered.ModuleVersion() != "" {
			t.Fatalf("local module provenance = %#v", discovered)
		}
		if !strings.HasPrefix(discovered.PackagePath(), "example.com/app/") || !strings.HasSuffix(discovered.SourcePath(), "interface.go") {
			t.Fatalf("package provenance = package %q source %q", discovered.PackagePath(), discovered.SourcePath())
		}
		contract := discovered.Contract()
		if contract.ID().String() != discovered.ID() || contract.PackagePath() != discovered.PackagePath() || len(contract.RequestFields()) != 1 || len(contract.ResponseFields()) != 1 {
			t.Fatalf("normalized contract = %#v", contract)
		}
		request := contract.RequestFields()[0]
		if request.Name() != "Value" || request.Number() != 1 || !request.Required() || request.JSONName() != "value" || !request.HasExplicitJSONName() || request.Type().Canonical() != "string" {
			t.Fatalf("request field = %#v", request)
		}
		position := discovered.Declaration().Position()
		if !strings.Contains(discovered.Source(), discovered.ModulePath()+"@local/") || !strings.HasSuffix(discovered.Source(), fmt.Sprintf(":%d:%d", position.Line, position.Column)) {
			t.Fatalf("stable source = %q", discovered.Source())
		}
	}
	view := first.Interfaces()
	view[0] = interfaceinventory.Interface{}
	if first.Interfaces()[0].ID() != wantIDs[0] {
		t.Fatal("Interfaces exposed mutable inventory storage")
	}
	if after := snapshotFiles(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("discovery mutated Project files:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestDiscoverIncludesDirectAndTransitiveDependencyProjectsButNotOrdinaryModules(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	directRoot := filepath.Join(root, "direct")
	transitiveRoot := filepath.Join(root, "transitive")
	ordinaryRoot := filepath.Join(root, "ordinary")

	writeProject(t, transitiveRoot, "example.com/transitive")
	writeFile(t, filepath.Join(transitiveRoot, "interfaces", "transitive", "v1", "interface.go"), interfaceSource("transitivev1", "dependency.transitive.run/v1", "Run"))
	writeProject(t, directRoot, "example.com/direct")
	writeFile(t, filepath.Join(directRoot, "go.mod"), fmt.Sprintf("module example.com/direct\n\ngo 1.26\n\nrequire example.com/transitive v1.2.0\n"))
	writeFile(t, filepath.Join(directRoot, "api", "interface.go"), interfaceSource("api", "dependency.direct.run/v1", "Run"))
	writeFile(t, filepath.Join(directRoot, "internal", "private", "interface.go"), interfaceSource("private", "dependency.private.run/v1", "Run"))

	writeFile(t, filepath.Join(ordinaryRoot, "go.mod"), "module example.com/ordinary\n\ngo 1.26\n")
	writeFile(t, filepath.Join(ordinaryRoot, "interface.go"), interfaceSource("ordinary", "dependency.ordinary.run/v1", "Run"))
	writeFile(t, filepath.Join(ordinaryRoot, interfacemeta.Name), "[\n")

	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/direct v1.1.0
	example.com/ordinary v1.0.0
)

replace example.com/direct => ../direct
replace example.com/transitive => ../transitive
replace example.com/ordinary => ../ordinary
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(appRoot, "local", "interface.go"), interfaceSource("local", "dependency.local.run/v1", "Run"))

	directBefore := snapshotFiles(t, directRoot)
	transitiveBefore := snapshotFiles(t, transitiveRoot)
	ordinaryBefore := snapshotFiles(t, ordinaryRoot)
	index := discover(t, appRoot, goEnvironment(map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off"}))

	wantIDs := []string{"dependency.direct.run/v1", "dependency.local.run/v1", "dependency.transitive.run/v1"}
	if got := interfaceIDs(index); !slices.Equal(got, wantIDs) {
		t.Fatalf("Interface IDs = %v, want %v", got, wantIDs)
	}
	byID := make(map[string]interfaceinventory.Interface)
	for _, discovered := range index.Interfaces() {
		byID[discovered.ID()] = discovered
	}
	if !byID["dependency.local.run/v1"].Local() || byID["dependency.local.run/v1"].ModuleVersion() != "" {
		t.Fatalf("local Interface provenance = %#v", byID["dependency.local.run/v1"])
	}
	if direct := byID["dependency.direct.run/v1"]; direct.Local() || direct.ModulePath() != "example.com/direct" || direct.ModuleVersion() != "v1.1.0" || direct.PackagePath() != "example.com/direct/api" {
		t.Fatalf("direct Interface provenance = %#v", direct)
	}
	if transitive := byID["dependency.transitive.run/v1"]; transitive.Local() || transitive.ModulePath() != "example.com/transitive" || transitive.ModuleVersion() != "v1.2.0" || transitive.PackagePath() != "example.com/transitive/interfaces/transitive/v1" {
		t.Fatalf("transitive Interface provenance = %#v", transitive)
	}
	for name, comparison := range map[string]struct {
		root   string
		before map[string]fileState
	}{
		"direct":     {root: directRoot, before: directBefore},
		"transitive": {root: transitiveRoot, before: transitiveBefore},
		"ordinary":   {root: ordinaryRoot, before: ordinaryBefore},
	} {
		if after := snapshotFiles(t, comparison.root); !reflect.DeepEqual(after, comparison.before) {
			t.Fatalf("discovery mutated %s dependency:\nbefore: %#v\nafter:  %#v", name, comparison.before, after)
		}
	}
}

func TestDiscoverUsesExplicitWorkspaceProjectsWithoutAssigningPriority(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "workspace-dependency")
	writeProject(t, appRoot, "example.com/app")
	writeProject(t, dependencyRoot, "example.com/workspace-dependency")
	writeFile(t, filepath.Join(appRoot, "local", "interface.go"), interfaceSource("local", "workspace.shared.run/v1", "Run"))
	writeFile(t, filepath.Join(dependencyRoot, "remote", "interface.go"), interfaceSource("remote", "workspace.shared.run/v1", "Run"))
	workspacePath := filepath.Join(root, "go.work")
	writeFile(t, workspacePath, "go 1.26\n\nuse (\n\t./app\n\t./workspace-dependency\n)\n")
	before := snapshotFiles(t, root)

	index := discover(t, appRoot, goEnvironment(map[string]string{
		"GOPROXY": "off",
		"GOSUMDB": "off",
		"GOWORK":  workspacePath,
	}))
	interfaces := index.Interfaces()
	if len(interfaces) != 2 || interfaces[0].ID() != "workspace.shared.run/v1" || interfaces[1].ID() != "workspace.shared.run/v1" {
		t.Fatalf("workspace Interfaces = %#v", interfaces)
	}
	if interfaces[0].PackagePath() != "example.com/app/local" || !interfaces[0].Local() || interfaces[1].PackagePath() != "example.com/workspace-dependency/remote" || interfaces[1].Local() || interfaces[1].ModuleVersion() != "" {
		t.Fatalf("workspace provenance = %#v", inventorySummary(index))
	}
	if after := snapshotFiles(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("workspace discovery mutated source:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestValidateUniqueIDsRejectsEveryDuplicateDefinition(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProject(t, root, "example.com/duplicates")
	writeFile(t, filepath.Join(root, "zeta", "interface.go"), interfaceSource("zeta", "duplicate.visible.run/v1", "Run"))
	writeFile(t, filepath.Join(root, "alpha", "interface.go"), interfaceSource("alpha", "duplicate.visible.run/v1", "Run"))
	writeFile(t, filepath.Join(root, "middle", "interface.go"), interfaceSource("middle", "duplicate.visible.run/v1", "Run"))

	index := discover(t, root, goEnvironment(map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off"}))
	interfaces := index.Interfaces()
	if len(interfaces) != 3 || interfaces[0].PackagePath() != "example.com/duplicates/alpha" || interfaces[1].PackagePath() != "example.com/duplicates/middle" || interfaces[2].PackagePath() != "example.com/duplicates/zeta" {
		t.Fatalf("duplicate definitions = %#v", inventorySummary(index))
	}
	err := interfaceinventory.ValidateUniqueIDs(index)
	if !errors.Is(err, interfaceinventory.ErrDuplicateID) {
		t.Fatalf("ValidateUniqueIDs error = %v", err)
	}
	var duplicate *interfaceinventory.DuplicateIDError
	if !errors.As(err, &duplicate) || duplicate.ID() != "duplicate.visible.run/v1" {
		t.Fatalf("duplicate error = %#v, %v", duplicate, err)
	}
	if definitions := duplicate.Definitions(); !reflect.DeepEqual(definitions, interfaces) {
		t.Fatalf("duplicate definitions = %#v, want %#v", definitions, interfaces)
	}
	for _, definition := range interfaces {
		if !strings.Contains(err.Error(), fmt.Sprintf("package %q at %s", definition.PackagePath(), definition.Source())) {
			t.Fatalf("duplicate error omits %s at %s: %v", definition.PackagePath(), definition.Source(), err)
		}
	}
	definitions := duplicate.Definitions()
	definitions[0] = interfaceinventory.Interface{}
	if duplicate.Definitions()[0].PackagePath() != "example.com/duplicates/alpha" {
		t.Fatal("DuplicateIDError exposed mutable definition storage")
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("duplicate error exposed private Project root: %v", err)
	}
}

func TestDiscoverSupportsSingleComponentProjectModulePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProject(t, root, "my-app")
	writeFile(t, filepath.Join(root, "interfaces", "local", "interface.go"), interfaceSource("local", "local.project.run/v1", "Run"))

	index := discover(t, root, goEnvironment(map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off"}))
	interfaces := index.Interfaces()
	if len(interfaces) != 1 || interfaces[0].ModulePath() != "my-app" || interfaces[0].PackagePath() != "my-app/interfaces/local" {
		t.Fatalf("single-component Project Interface = %#v", inventorySummary(index))
	}
}

func TestDiscoverLoadsOptionalColocatedMetadataWithStableProvenance(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "dependency")
	writeProject(t, dependencyRoot, "example.com/dependency")
	writeFile(t, filepath.Join(dependencyRoot, "api", "interface.go"), interfaceSource("api", "dependency.records.list/v1", "List"))
	dependencyMetadata := "description: Lists dependency records.\nsemantics:\n  kind: query\nerrors:\n  - code: dependency_unavailable\nconstraints:\n  request.value: {min_length: 1}\n"
	writeFile(t, filepath.Join(dependencyRoot, "api", interfacemeta.Name), dependencyMetadata)

	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/dependency v1.2.3

replace example.com/dependency => ../dependency
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(appRoot, "interfaces", "local", "interface.go"), interfaceSource("local", "local.records.list/v1", "List"))
	localMetadata := "# local metadata\ndescription: Lists local records.\nsemantics:\n  kind: command\nerrors:\n  - code: records_unavailable\n  - code: invalid_filter\n    description: The filter is invalid.\nconstraints:\n  response.accepted: {}\n  request.value: {min_length: 1}\n"
	writeFile(t, filepath.Join(appRoot, "interfaces", "local", interfacemeta.Name), localMetadata)
	writeFile(t, filepath.Join(appRoot, "interfaces", "plain", "interface.go"), interfaceSource("plain", "local.records.get/v1", "Get"))
	writeFile(t, filepath.Join(appRoot, "interfaces", "described", "interface.go"), interfaceSource("described", "local.records.describe/v1", "Describe"))
	writeFile(t, filepath.Join(appRoot, "interfaces", "described", interfacemeta.Name), "description: Describes local records.\n")

	beforeApp := snapshotFiles(t, appRoot)
	beforeDependency := snapshotFiles(t, dependencyRoot)
	environment := goEnvironment(map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off"})
	first := discover(t, appRoot, environment)
	second := discover(t, appRoot, environment)
	if !reflect.DeepEqual(inventorySummary(first), inventorySummary(second)) {
		t.Fatalf("metadata discovery was not deterministic:\nfirst: %#v\nsecond: %#v", inventorySummary(first), inventorySummary(second))
	}
	byID := make(map[string]interfaceinventory.Interface)
	for _, discovered := range first.Interfaces() {
		byID[discovered.ID()] = discovered
	}
	local, present := byID["local.records.list/v1"].Metadata()
	if !present || local.Path() != "interfaces/local/interface.yaml" || string(local.Data()) != localMetadata || byID["local.records.list/v1"].MetadataSource() != "example.com/app@local/interfaces/local/interface.yaml" {
		t.Fatalf("local metadata = %#v, %t, source %q", local, present, byID["local.records.list/v1"].MetadataSource())
	}
	localSemantics, present := byID["local.records.list/v1"].Semantics()
	if !present || localSemantics.Kind() != interfacemeta.OperationKindCommand {
		t.Fatalf("local semantics = %#v, %t", localSemantics, present)
	}
	localErrors := byID["local.records.list/v1"].SemanticErrors()
	if len(localErrors) != 2 || localErrors[0].Code() != "invalid_filter" || localErrors[1].Code() != "records_unavailable" {
		t.Fatalf("local semantic errors = %#v", localErrors)
	}
	localConstraints := byID["local.records.list/v1"].ConstraintTargets()
	if len(localConstraints) != 2 || localConstraints[0].Path() != "request.value" || localConstraints[0].GoPath() != "Request.Value" || localConstraints[1].Path() != "response.accepted" || localConstraints[1].GoPath() != "Response.Accepted" {
		t.Fatalf("local constraint targets = %#v", localConstraints)
	}
	dependency, present := byID["dependency.records.list/v1"].Metadata()
	if !present || dependency.Path() != "api/interface.yaml" || string(dependency.Data()) != dependencyMetadata || byID["dependency.records.list/v1"].MetadataSource() != "example.com/dependency@v1.2.3/api/interface.yaml" {
		t.Fatalf("dependency metadata = %#v, %t, source %q", dependency, present, byID["dependency.records.list/v1"].MetadataSource())
	}
	dependencySemantics, present := byID["dependency.records.list/v1"].Semantics()
	if !present || dependencySemantics.Kind() != interfacemeta.OperationKindQuery {
		t.Fatalf("dependency semantics = %#v, %t", dependencySemantics, present)
	}
	dependencyErrors := byID["dependency.records.list/v1"].SemanticErrors()
	if len(dependencyErrors) != 1 || dependencyErrors[0].Code() != "dependency_unavailable" {
		t.Fatalf("dependency semantic errors = %#v", dependencyErrors)
	}
	dependencyConstraints := byID["dependency.records.list/v1"].ConstraintTargets()
	if len(dependencyConstraints) != 1 || dependencyConstraints[0].Path() != "request.value" || dependencyConstraints[0].GoPath() != "Request.Value" {
		t.Fatalf("dependency constraint targets = %#v", dependencyConstraints)
	}
	if metadata, present := byID["local.records.get/v1"].Metadata(); present || metadata.Path() != "" || len(metadata.Data()) != 0 || byID["local.records.get/v1"].MetadataSource() != "" {
		t.Fatalf("absent metadata = %#v, %t", metadata, present)
	}
	if semantics, present := byID["local.records.get/v1"].Semantics(); present || semantics.Kind() != "" {
		t.Fatalf("absent semantics = %#v, %t", semantics, present)
	}
	described, hasMetadata := byID["local.records.describe/v1"].Metadata()
	if !hasMetadata || described.Path() != "interfaces/described/interface.yaml" {
		t.Fatalf("description-only metadata = %#v, %t", described, hasMetadata)
	}
	if semantics, present := byID["local.records.describe/v1"].Semantics(); present || semantics.Kind() != "" {
		t.Fatalf("description-only semantics = %#v, %t", semantics, present)
	}
	if semanticErrors := byID["local.records.describe/v1"].SemanticErrors(); len(semanticErrors) != 0 {
		t.Fatalf("description-only semantic errors = %#v", semanticErrors)
	}
	if constraints := byID["local.records.describe/v1"].ConstraintTargets(); len(constraints) != 0 {
		t.Fatalf("description-only constraint targets = %#v", constraints)
	}
	localErrors[0] = interfacemeta.SemanticError{}
	if byID["local.records.list/v1"].SemanticErrors()[0].Code() != "invalid_filter" {
		t.Fatal("SemanticErrors exposed mutable metadata storage")
	}
	localConstraints[0] = interfacemeta.ConstraintTarget{}
	if byID["local.records.list/v1"].ConstraintTargets()[0].Path() != "request.value" {
		t.Fatal("ConstraintTargets exposed mutable inventory storage")
	}
	view := local.Data()
	view[0] = 'x'
	again, _ := byID["local.records.list/v1"].Metadata()
	if string(again.Data()) != localMetadata {
		t.Fatal("Metadata exposed mutable document bytes")
	}
	if after := snapshotFiles(t, appRoot); !reflect.DeepEqual(after, beforeApp) {
		t.Fatalf("metadata discovery mutated current Project:\nbefore: %#v\nafter:  %#v", beforeApp, after)
	}
	if after := snapshotFiles(t, dependencyRoot); !reflect.DeepEqual(after, beforeDependency) {
		t.Fatalf("metadata discovery mutated dependency Project:\nbefore: %#v\nafter:  %#v", beforeDependency, after)
	}
}

func TestDiscoverRejectsUnsafeOrMalformedOptionalMetadataWithoutMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		data      string
		directory bool
		wantError error
		want      string
	}{
		{name: "empty", want: "document is empty"},
		{name: "comments only", data: "# empty\n", want: "expected one YAML document"},
		{name: "malformed", data: "[\n", want: "decode YAML"},
		{name: "multiple documents", data: "{}\n---\n{}\n", want: "multiple YAML documents"},
		{name: "sequence root", data: "- description\n", want: "root must be a mapping"},
		{name: "anchor", data: "description: &text value\n", want: "anchors and aliases"},
		{name: "duplicate key", data: "description: one\ndescription: two\n", want: "duplicate mapping key"},
		{name: "authoritative field", data: "id: records.invalid.list/v1\n", wantError: interfacemeta.ErrAuthoritativeField, want: "Interface ID is authoritative"},
		{name: "unknown field", data: "custom: value\n", wantError: interfacemeta.ErrUnknownField, want: "unknown top-level field"},
		{name: "invalid semantics shape", data: "semantics: []\n", wantError: interfacemeta.ErrInvalidSemantics, want: "semantics must be a mapping"},
		{name: "invalid semantics field", data: "semantics:\n  kind: query\n  retry: {}\n", wantError: interfacemeta.ErrInvalidSemantics, want: "semantics.retry"},
		{name: "invalid semantics kind", data: "semantics:\n  kind: event\n", wantError: interfacemeta.ErrInvalidSemantics, want: "expected query or command"},
		{name: "invalid semantic errors shape", data: "errors: invalid_value\n", wantError: interfacemeta.ErrInvalidSemanticErrors, want: "errors must be a sequence"},
		{name: "invalid semantic error field", data: "errors:\n  - code: invalid_value\n    retryable: true\n", wantError: interfacemeta.ErrInvalidSemanticErrors, want: "errors[0].retryable"},
		{name: "duplicate semantic error", data: "errors:\n  - code: invalid_value\n  - code: invalid_value\n", wantError: interfacemeta.ErrInvalidSemanticErrors, want: "duplicates"},
		{name: "invalid constraints shape", data: "constraints: []\n", wantError: interfacemeta.ErrInvalidConstraints, want: "constraints must be a mapping"},
		{name: "invalid constraint rules", data: "constraints:\n  request.value: []\n", wantError: interfacemeta.ErrInvalidConstraints, want: "must be a mapping"},
		{name: "unknown constraint path", data: "constraints:\n  request.missing: {}\n", wantError: interfacemeta.ErrInvalidConstraints, want: "does not identify a canonical"},
		{name: "directory", directory: true, want: "regular non-symbolic file"},
		{name: "oversized", data: strings.Repeat("x", interfacemeta.MaximumSize+1), want: "exceeds"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeProject(t, root, "example.com/invalid-metadata")
			packageRoot := filepath.Join(root, "interfaces", "records")
			writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceSource("records", "records.invalid.list/v1", "List"))
			metadataPath := filepath.Join(packageRoot, interfacemeta.Name)
			if test.directory {
				if err := os.Mkdir(metadataPath, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, metadataPath, test.data)
			}
			before := snapshotFiles(t, root)
			_, err := discoverResult(t, root, goEnvironment(map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off"}))
			if !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalid) || test.wantError != nil && !errors.Is(err, test.wantError) || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "interfaces/records/interface.yaml") {
				t.Fatalf("Discover error = %v, want Interface metadata error containing %q", err, test.want)
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
				t.Fatalf("metadata error exposed private Project root: %v", err)
			}
			if after := snapshotFiles(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed metadata discovery mutated Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestDiscoverRejectsMalformedActiveDeclarationsAndPackages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		source   string
		want     error
		wantText string
	}{
		{
			name: "malformed directive",
			source: `package broken
//plystra:interface broken
type Interface interface{}
`,
			want:     interfacedecl.ErrInvalid,
			wantText: "interfaces/broken/interface.go:2:1",
		},
		{
			name: "invalid contract",
			source: `package broken

import "context"

//plystra:interface broken.contract.run/v1
type Interface interface { Run(context.Context, Request) (Response, error) }
type Request struct { Value string }
type Response struct { Value string ` + "`plystra:\"1\"`" + ` }
`,
			want:     interfacecontract.ErrInvalid,
			wantText: "missing plystra field-number tag",
		},
		{
			name: "Go type error",
			source: `package broken

import "context"

//plystra:interface broken.types.run/v1
type Interface interface { Run(context.Context, Missing) (Response, error) }
type Response struct { Value string ` + "`plystra:\"1\"`" + ` }
`,
			want:     interfaceinventory.ErrPackage,
			wantText: "Missing",
		},
		{
			name: "program package",
			source: `package main

import "context"

//plystra:interface broken.program.run/v1
type Interface interface { Run(context.Context, Request) (Response, error) }
type Request struct { Value string ` + "`plystra:\"1\"`" + ` }
type Response struct { Value string ` + "`plystra:\"1\"`" + ` }
func main() {}
`,
			want:     interfaceinventory.ErrPackage,
			wantText: "cannot define an importable Interface",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeProject(t, root, "example.com/broken")
			writeFile(t, filepath.Join(root, "interfaces", "broken", "interface.go"), test.source)
			_, err := discoverResult(t, root, goEnvironment(map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOWORK": "off"}))
			if !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, test.want) || !strings.Contains(err.Error(), test.wantText) {
				t.Fatalf("Discover error = %v, want %v containing %q", err, test.want, test.wantText)
			}
			if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
				t.Fatalf("error exposed private Project root: %v", err)
			}
		})
	}
}

func discover(t testing.TB, root string, environment []string) interfaceinventory.Index {
	t.Helper()
	index, err := discoverResult(t, root, environment)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return index
}

func discoverResult(t testing.TB, root string, environment []string) (interfaceinventory.Index, error) {
	t.Helper()
	project, err := projectlocate.Find(root)
	if err != nil {
		t.Fatalf("locate Project: %v", err)
	}
	dependencies, err := moduledependency.Discover(t.Context(), project, moduledependency.Options{Environment: environment})
	if err != nil {
		t.Fatalf("discover modules: %v", err)
	}
	return interfaceinventory.Discover(t.Context(), project, dependencies, interfaceinventory.Options{Environment: environment})
}

func interfaceSource(packageName, id, method string) string {
	return fmt.Sprintf(`package %s

import "context"

// %s is the canonical operation.
//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

type Request struct {
	Value string `+"`plystra:\"1,required\" json:\"value\"`"+`
}

type Response struct {
	Accepted bool `+"`plystra:\"1\" json:\"accepted\"`"+`
}
`, packageName, method, id, method)
}

func writeProject(t testing.TB, root, modulePath string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf("module %s\n\ngo 1.26\n", modulePath))
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
}

func writeFile(t testing.TB, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func goEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	order := make([]string, 0)
	for _, entry := range os.Environ() {
		name, value, exists := strings.Cut(entry, "=")
		if !exists {
			continue
		}
		key := strings.ToUpper(name)
		if _, duplicate := values[key]; !duplicate {
			order = append(order, key)
		}
		values[key] = name + "=" + value
	}
	for name, value := range overrides {
		key := strings.ToUpper(name)
		if _, exists := values[key]; !exists {
			order = append(order, key)
		}
		values[key] = name + "=" + value
	}
	sort.Strings(order)
	result := make([]string, 0, len(order))
	for _, key := range order {
		result = append(result, values[key])
	}
	return result
}

func interfaceIDs(index interfaceinventory.Index) []string {
	interfaces := index.Interfaces()
	result := make([]string, len(interfaces))
	for position, discovered := range interfaces {
		result[position] = discovered.ID()
	}
	return result
}

type interfaceSummary struct {
	ID              string
	ModulePath      string
	ModuleVersion   string
	PackagePath     string
	SourcePath      string
	Source          string
	Local           bool
	Method          string
	Request         string
	Response        string
	MetadataPath    string
	MetadataData    string
	MetadataSource  string
	SemanticsKind   interfacemeta.OperationKind
	HasSemantics    bool
	ErrorCodes      []string
	ConstraintPaths []string
}

func inventorySummary(index interfaceinventory.Index) []interfaceSummary {
	interfaces := index.Interfaces()
	result := make([]interfaceSummary, len(interfaces))
	for position, discovered := range interfaces {
		contract := discovered.Contract()
		metadata, _ := discovered.Metadata()
		semantics, hasSemantics := discovered.Semantics()
		semanticErrors := discovered.SemanticErrors()
		errorCodes := make([]string, len(semanticErrors))
		for index, semanticError := range semanticErrors {
			errorCodes[index] = semanticError.Code()
		}
		constraintTargets := discovered.ConstraintTargets()
		constraintPaths := make([]string, len(constraintTargets))
		for index, target := range constraintTargets {
			constraintPaths[index] = target.Path() + "=" + target.GoPath()
		}
		result[position] = interfaceSummary{
			ID:              discovered.ID(),
			ModulePath:      discovered.ModulePath(),
			ModuleVersion:   discovered.ModuleVersion(),
			PackagePath:     discovered.PackagePath(),
			SourcePath:      discovered.SourcePath(),
			Source:          discovered.Source(),
			Local:           discovered.Local(),
			Method:          contract.MethodName(),
			Request:         contract.RequestName(),
			Response:        contract.ResponseName(),
			MetadataPath:    metadata.Path(),
			MetadataData:    string(metadata.Data()),
			MetadataSource:  discovered.MetadataSource(),
			SemanticsKind:   semantics.Kind(),
			HasSemantics:    hasSemantics,
			ErrorCodes:      errorCodes,
			ConstraintPaths: constraintPaths,
		}
	}
	return result
}

type fileState struct {
	Mode    fs.FileMode
	Size    int64
	ModTime time.Time
	Data    []byte
}

func snapshotFiles(t testing.TB, root string) map[string]fileState {
	t.Helper()
	result := make(map[string]fileState)
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = fileState{Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime(), Data: bytes.Clone(data)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
