package command_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const inspectProgress = "Resolving selected application model...\n"

type inspectCommandEnvelope struct {
	Schema                 string `json:"schema"`
	SchemaVersion          int    `json:"schema_version"`
	ConfigurationMode      string `json:"configuration_mode"`
	ApplicationModelDigest string `json:"application_model_digest"`
	Result                 struct {
		Project struct {
			Module string `json:"module"`
		} `json:"project"`
		Configuration struct {
			Mode        string `json:"mode"`
			Environment string `json:"environment"`
			Path        string `json:"path"`
		} `json:"configuration"`
		Summary struct {
			SelectedPluginCount      int      `json:"selected_plugin_count"`
			AvailableCapabilityCount int      `json:"available_capability_count"`
			RequiredCapabilityCount  int      `json:"required_capability_count"`
			ExposedCapabilityCount   int      `json:"exposed_capability_count"`
			CapabilityAliasCount     int      `json:"capability_alias_count"`
			AuthNActive              bool     `json:"authn_active"`
			AuthZActive              bool     `json:"authz_active"`
			Transports               []string `json:"transports"`
		} `json:"summary"`
		Readiness struct {
			State        string `json:"state"`
			ProblemCount int    `json:"problem_count"`
			NextAction   string `json:"next_action"`
		} `json:"readiness"`
		ResolutionEvidence json.RawMessage `json:"resolution_evidence"`
	} `json:"result"`
}

type inspectGraphCommandEnvelope struct {
	Schema                 string `json:"schema"`
	SchemaVersion          int    `json:"schema_version"`
	ConfigurationMode      string `json:"configuration_mode"`
	ApplicationModelDigest string `json:"application_model_digest"`
	Result                 struct {
		Type               string             `json:"type"`
		Nodes              []inspectGraphNode `json:"nodes"`
		Edges              []inspectGraphEdge `json:"edges"`
		ResolutionEvidence json.RawMessage    `json:"resolution_evidence"`
	} `json:"result"`
}

type inspectGraphNode struct {
	ID      string               `json:"id"`
	Kind    string               `json:"kind"`
	Label   string               `json:"label"`
	Sources []inspectGraphSource `json:"sources"`
}

type inspectGraphEdge struct {
	ID      string               `json:"id"`
	Kind    string               `json:"kind"`
	From    string               `json:"from"`
	To      string               `json:"to"`
	Reason  string               `json:"reason"`
	Sources []inspectGraphSource `json:"sources"`
}

type inspectGraphSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

func TestInspectHumanOutputIsConciseAndReadOnlyFromNestedDirectory(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	before := snapshotInspectProject(t, root)
	exitCode, stdout, stderr := runCommand(t, []string{"inspect"}, nested, inspectCommandEnvironment(nil))
	want := inspectProgress +
		"Project: example.com/acme/inspect\n" +
		"Configuration: default (plystra.yaml)\n" +
		"Plugins: 0 selected\n" +
		"Capabilities: 2 available, 0 required, 0 exposed, 0 aliases\n" +
		"AuthN: inactive\n" +
		"AuthZ: inactive\n" +
		"Transports: none\n" +
		"Readiness: ready (0 problems)\n" +
		"Next action: Run plystra check to validate the selected model.\n"
	if exitCode != 0 || stdout != want || stderr != "" {
		t.Fatalf("inspect = exit %d, stdout %q, stderr %q; want stdout %q", exitCode, stdout, stderr, want)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("inspect mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInspectJSONOwnsStdoutAndIsDeterministic(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	environment := inspectCommandEnvironment(nil)
	firstExit, firstStdout, firstStderr := runCommand(t, []string{"inspect", "--format", "json"}, nested, environment)
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"inspect", "--verbose", "--format", "json"}, root, environment)
	if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress {
		t.Fatalf("JSON inspect = first (%d, %q) second (%d, %q)", firstExit, firstStderr, secondExit, secondStderr)
	}
	if firstStdout != secondStdout || !strings.HasSuffix(firstStdout, "\n") || strings.Count(firstStdout, "\n") != 1 {
		t.Fatalf("JSON stdout is not one deterministic document:\nfirst:  %q\nsecond: %q", firstStdout, secondStdout)
	}
	document := decodeInspectCommandEnvelope(t, firstStdout)
	if document.Schema != "plystra.inspect" || document.SchemaVersion != 1 || document.ConfigurationMode != "default" || document.ApplicationModelDigest == "" {
		t.Fatalf("inspect envelope identity = %#v", document)
	}
	if document.Result.Project.Module != "example.com/acme/inspect" || document.Result.Configuration.Mode != "default" || document.Result.Configuration.Path != "plystra.yaml" {
		t.Fatalf("inspect result identity = %#v", document.Result)
	}
	if document.Result.Summary.SelectedPluginCount != 0 || document.Result.Summary.AvailableCapabilityCount != 2 || !reflect.DeepEqual(document.Result.Summary.Transports, []string{}) {
		t.Fatalf("inspect summary = %#v", document.Result.Summary)
	}
	if document.Result.Readiness.State != "ready" || document.Result.Readiness.ProblemCount != 0 || document.Result.Readiness.NextAction != "Run plystra check to validate the selected model." || len(document.Result.ResolutionEvidence) == 0 {
		t.Fatalf("inspect readiness/evidence = %#v, %s", document.Result.Readiness, document.Result.ResolutionEvidence)
	}
	if strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
		t.Fatalf("inspect JSON leaked a Project path or unrestricted configuration: %s", firstStdout)
	}
}

func TestInspectSelectorsUseOneSharedSelectedModel(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		mode        string
		selectedEnv string
		path        string
		transports  []string
		nextAction  string
	}{
		{
			name:        "explicit environment",
			arguments:   []string{"inspect", "--format", "json", "--env", "production"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "environment",
			selectedEnv: "production",
			path:        "plystra.production.yaml",
			transports:  []string{"connect"},
			nextAction:  "Run plystra check --env \"production\" to validate the selected model.",
		},
		{
			name:        "ambient environment",
			arguments:   []string{"inspect", "--format", "json"},
			environment: map[string]string{"PLYSTRA_ENV": "production"},
			mode:        "environment",
			selectedEnv: "production",
			path:        "plystra.production.yaml",
			transports:  []string{"connect"},
			nextAction:  "Run plystra check --env \"production\" to validate the selected model.",
		},
		{
			name:        "explicit configuration",
			arguments:   []string{"inspect", "--format", "json", "--config", "deploy/customer.yaml"},
			environment: map[string]string{"PLYSTRA_ENV": "ignored", "PLYSTRA_CONFIG": "ignored.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
			transports:  []string{"connect"},
			nextAction:  "Run plystra check --config \"deploy/customer.yaml\" to validate the selected model.",
		},
		{
			name:        "ambient configuration",
			arguments:   []string{"inspect", "--format", "json"},
			environment: map[string]string{"PLYSTRA_CONFIG": "deploy/customer.yaml"},
			mode:        "explicit-config",
			path:        "deploy/customer.yaml",
			transports:  []string{"connect"},
			nextAction:  "Run plystra check --config \"deploy/customer.yaml\" to validate the selected model.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			if exitCode != 0 || stderr != inspectProgress {
				t.Fatalf("inspect = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			document := decodeInspectCommandEnvelope(t, stdout)
			if document.ConfigurationMode != test.mode || document.Result.Configuration.Mode != test.mode || document.Result.Configuration.Environment != test.selectedEnv || document.Result.Configuration.Path != test.path || !reflect.DeepEqual(document.Result.Summary.Transports, test.transports) || document.Result.Readiness.NextAction != test.nextAction {
				t.Fatalf("selected model = mode %q/%q env %q path %q transports %#v next %q", document.ConfigurationMode, document.Result.Configuration.Mode, document.Result.Configuration.Environment, document.Result.Configuration.Path, document.Result.Summary.Transports, document.Result.Readiness.NextAction)
			}
			if strings.Contains(stdout, root) {
				t.Fatalf("inspect JSON contains absolute Project path: %s", stdout)
			}
		})
	}
}

func TestInspectVerboseIncludesCompleteIndentedEvidence(t *testing.T) {
	t.Parallel()

	root, _ := createInspectCommandProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"inspect", "--verbose", "--env", "production"}, root, inspectCommandEnvironment(nil))
	for _, fragment := range []string{
		"Configuration: environment \"production\" (plystra.production.yaml)\n",
		"Transports: connect\n",
		"Resolution evidence:\n  {\n",
		"    \"modules\": [",
		"    \"configuration_selection\": {",
		"    \"static_assembly\": {",
		"    \"http_transports\": {",
	} {
		if !strings.Contains(stdout, fragment) {
			t.Fatalf("verbose inspect omits %q:\n%s", fragment, stdout)
		}
	}
	if exitCode != 0 || stderr != "" || strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("verbose inspect = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
}

func TestInspectModulesHumanAndJSONAreDeterministicAndReadOnly(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	before := snapshotInspectProject(t, root)
	humanExit, humanStdout, humanStderr := runCommand(t, []string{"inspect", "modules"}, nested, inspectCommandEnvironment(nil))
	wantHuman := inspectProgress +
		"Module graph: 1 modules, 0 dependencies\n" +
		"Module: example.com/acme/inspect (current)\n" +
		"  Source: example.com/acme/inspect:plystra.yaml:1:1 (project-marker)\n"
	if humanExit != 0 || humanStdout != wantHuman || humanStderr != "" {
		t.Fatalf("human module graph = exit %d, stdout %q, stderr %q; want stdout %q", humanExit, humanStdout, humanStderr, wantHuman)
	}

	firstExit, firstStdout, firstStderr := runCommand(t, []string{"inspect", "modules", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"inspect", "modules", "--verbose", "--format", "json"}, root, inspectCommandEnvironment(nil))
	if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress || firstStdout != secondStdout {
		t.Fatalf("JSON module graph = first (%d, %q, %q) second (%d, %q, %q)", firstExit, firstStdout, firstStderr, secondExit, secondStdout, secondStderr)
	}
	if strings.Count(firstStdout, "\n") != 1 || !strings.HasSuffix(firstStdout, "\n") {
		t.Fatalf("module graph JSON is not one document: %q", firstStdout)
	}
	document := decodeInspectGraphCommandEnvelope(t, firstStdout)
	if document.Schema != "plystra.graph" || document.SchemaVersion != 1 || document.ConfigurationMode != "default" || document.ApplicationModelDigest == "" {
		t.Fatalf("module graph envelope = %#v", document)
	}
	if document.Result.Type != "modules" || len(document.Result.Nodes) != 1 || len(document.Result.Edges) != 0 || len(document.Result.ResolutionEvidence) == 0 {
		t.Fatalf("module graph result = %#v", document.Result)
	}
	if document.Result.Nodes[0].ID != "module:example.com/acme/inspect" || document.Result.Nodes[0].Kind != "module" || document.Result.Nodes[0].Label != "example.com/acme/inspect" || len(document.Result.Nodes[0].Sources) != 1 || document.Result.Nodes[0].Sources[0].Path != "plystra.yaml" {
		t.Fatalf("module graph node = %#v", document.Result.Nodes[0])
	}
	if strings.Contains(firstStdout, root) || strings.Contains(firstStdout, "resolved-secret-marker") {
		t.Fatalf("module graph leaked a Project path or unrestricted configuration: %s", firstStdout)
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("module graph mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInspectModulesIncludesDeterministicDependencyEdges(t *testing.T) {
	t.Parallel()

	root, nested := createInspectModuleGraphProject(t)
	exitCode, stdout, stderr := runCommand(t, []string{"inspect", "modules", "--format", "json"}, nested, inspectCommandEnvironment(nil))
	if exitCode != 0 || stderr != inspectProgress {
		t.Fatalf("module graph dependency inspect = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
	}
	document := decodeInspectGraphCommandEnvelope(t, stdout)
	if document.ConfigurationMode != "default" || len(document.Result.Nodes) != 2 || len(document.Result.Edges) != 1 {
		t.Fatalf("module graph dependency shape = mode %q nodes %d edges %d", document.ConfigurationMode, len(document.Result.Nodes), len(document.Result.Edges))
	}
	if document.Result.Nodes[0].ID != "module:example.com/acme/inspect" || document.Result.Nodes[1].ID != "module:example.com/acme/library" {
		t.Fatalf("module graph node order = %#v", document.Result.Nodes)
	}
	edge := document.Result.Edges[0]
	if edge.ID != "requires:example.com/acme/inspect->example.com/acme/library" || edge.Kind != "requires" || edge.From != "module:example.com/acme/inspect" || edge.To != "module:example.com/acme/library" || edge.Reason != "replacement" {
		t.Fatalf("module graph edge = %#v", edge)
	}
	if strings.Contains(stdout, root) || strings.Contains(stdout, "resolved-secret-marker") {
		t.Fatalf("module graph dependency output leaked a path or secret: %s", stdout)
	}
}

func TestInspectInterfacesHumanAndJSONAreDeterministicAndReadOnly(t *testing.T) {
	t.Parallel()

	root, nested := createInspectInterfaceGraphProject(t)
	before := snapshotInspectProject(t, root)
	environment := inspectInterfaceGraphEnvironment()
	firstHumanExit, firstHumanStdout, firstHumanStderr := runCommand(t, []string{"inspect", "interfaces"}, nested, environment)
	secondHumanExit, secondHumanStdout, secondHumanStderr := runCommand(t, []string{"inspect", "interfaces"}, root, environment)
	if firstHumanExit != 0 || secondHumanExit != 0 || firstHumanStderr != "" || secondHumanStderr != "" || firstHumanStdout != secondHumanStdout {
		t.Fatalf("human Interface graph = first (%d, %q, %q) second (%d, %q, %q)", firstHumanExit, firstHumanStdout, firstHumanStderr, secondHumanExit, secondHumanStdout, secondHumanStderr)
	}
	for _, fragment := range []string{
		inspectProgress + "Interface graph: 6 Interfaces, 5 active, 2 constructors, 18 relationships\n",
		"Interface: app.run/v1 (active, authored)\n  Package: example.com/acme/interface-inspect/interfaces/app/run/v1\n  Owner: example.com/acme/interface-inspect\n",
		"  Source: example.com/acme/interface-inspect:interfaces/app/run/v1/interface.yaml:1:1 (interface-metadata)\n",
		"Interface: cache.read/v1 (inactive, authored)\n  Package: example.com/acme/interface-library/interfaces/cache/read/v1\n  Owner: example.com/acme/interface-library\n",
		"Interface: kernel.health/v1 (active, intrinsic)\n  Package: github.com/plystra/kernel/interfaces/kernel/health/v1\n  Owner: github.com/plystra/kernel\n",
		"  Source: github.com/plystra/kernel:interfaces/kernel/health/v1 (intrinsic-interface)\n",
		"Interface: reports.read/v1 (active, authored)\n",
		"Active constructors:\n  example.com/acme/interface-inspect/app.New\n",
		"Requirements:\n  example.com/acme/interface-inspect -> app.run/v1 (declaration)\n",
		"  example.com/acme/interface-inspect -> app.run/v1 (exposure)\n",
		"  github.com/plystra/kernel -> kernel.health/v1 (intrinsic)\n",
		"Selections:\n  app.run/v1 -> example.com/acme/interface-inspect/app.New (unique-compatible)\n",
		"  reports.read/v1 -> example.com/acme/interface-inspect/app.New (explicit)\n",
		"Dependencies:\n  example.com/acme/interface-inspect/app.New -> audit.write/v1 (required)\n",
		"  example.com/acme/interface-inspect/app.New -> cache.read/v1 (optional-unavailable)\n",
	} {
		if !strings.Contains(firstHumanStdout, fragment) {
			t.Fatalf("human Interface graph omits %q:\n%s", fragment, firstHumanStdout)
		}
	}

	firstExit, firstStdout, firstStderr := runCommand(t, []string{"inspect", "interfaces", "--format", "json"}, nested, environment)
	secondExit, secondStdout, secondStderr := runCommand(t, []string{"inspect", "interfaces", "--verbose", "--format", "json"}, root, environment)
	if firstExit != 0 || secondExit != 0 || firstStderr != inspectProgress || secondStderr != inspectProgress || firstStdout != secondStdout {
		t.Fatalf("JSON Interface graph = first (%d, %q, %q) second (%d, %q, %q)", firstExit, firstStdout, firstStderr, secondExit, secondStdout, secondStderr)
	}
	if strings.Count(firstStdout, "\n") != 1 || !strings.HasSuffix(firstStdout, "\n") {
		t.Fatalf("Interface graph JSON is not one document: %q", firstStdout)
	}
	document := decodeInspectGraphCommandEnvelope(t, firstStdout)
	if document.Schema != "plystra.graph" || document.SchemaVersion != 1 || document.ConfigurationMode != "default" || document.ApplicationModelDigest == "" {
		t.Fatalf("Interface graph envelope = %#v", document)
	}
	if document.Result.Type != "interfaces" || len(document.Result.Nodes) != 11 || len(document.Result.Edges) != 18 || len(document.Result.ResolutionEvidence) == 0 {
		t.Fatalf("Interface graph result = type %q nodes %d edges %d evidence %d", document.Result.Type, len(document.Result.Nodes), len(document.Result.Edges), len(document.Result.ResolutionEvidence))
	}
	appNode := inspectGraphNodeByID(t, document.Result.Nodes, "interface:app.run/v1")
	if appNode.Kind != "interface" || appNode.Label != "example.com/acme/interface-inspect/interfaces/app/run/v1" || len(appNode.Sources) != 2 || appNode.Sources[0].Kind != "interface-declaration" || appNode.Sources[1].Kind != "interface-metadata" {
		t.Fatalf("app Interface node = %#v", appNode)
	}
	kernelNode := inspectGraphNodeByID(t, document.Result.Nodes, "interface:kernel.health/v1")
	if kernelNode.Label != "github.com/plystra/kernel/interfaces/kernel/health/v1" || len(kernelNode.Sources) != 1 || kernelNode.Sources[0].Module != "github.com/plystra/kernel" || kernelNode.Sources[0].Path != "interfaces/kernel/health/v1" || kernelNode.Sources[0].Kind != "intrinsic-interface" || kernelNode.Sources[0].Line != 0 || kernelNode.Sources[0].Column != 0 {
		t.Fatalf("intrinsic Interface node = %#v", kernelNode)
	}
	constructorNode := inspectGraphNodeByID(t, document.Result.Nodes, "constructor:example.com/acme/interface-inspect/app.New")
	if constructorNode.Kind != "constructor" || constructorNode.Label != "example.com/acme/interface-inspect/app.New" || len(constructorNode.Sources) != 1 || constructorNode.Sources[0].Module != "example.com/acme/interface-inspect" || constructorNode.Sources[0].Path != "app/service.go" || constructorNode.Sources[0].Kind != "implementation-constructor" {
		t.Fatalf("shared constructor node = %#v", constructorNode)
	}
	assertInspectGraphEdge(t, document.Result.Edges, "defines-interface", "module:example.com/acme/interface-library", "interface:cache.read/v1", "authored", "example.com/acme/interface-library", "interfaces/cache/read/v1/interface.go", "interface-declaration")
	assertInspectGraphEdge(t, document.Result.Edges, "defines-interface", "module:github.com/plystra/kernel", "interface:kernel.health/v1", "intrinsic", "github.com/plystra/kernel", "interfaces/kernel/health/v1", "intrinsic-interface")
	assertInspectGraphEdge(t, document.Result.Edges, "requires-interface", "module:example.com/acme/interface-inspect", "interface:app.run/v1", "declaration", "example.com/acme/interface-inspect", "plystra.yaml", "declaration")
	assertInspectGraphEdge(t, document.Result.Edges, "requires-interface", "module:example.com/acme/interface-inspect", "interface:app.run/v1", "exposure", "example.com/acme/interface-inspect", "plystra.yaml", "exposure")
	assertInspectGraphEdge(t, document.Result.Edges, "requires-interface", "module:github.com/plystra/kernel", "interface:kernel.info/v1", "intrinsic", "github.com/plystra/kernel", "interfaces/kernel/info/v1", "intrinsic-interface")
	assertInspectGraphEdge(t, document.Result.Edges, "requires-interface", "module:example.com/acme/interface-inspect", "interface:kernel.health/v1", "declaration", "example.com/acme/interface-inspect", "plystra.yaml", "declaration")
	assertInspectGraphEdge(t, document.Result.Edges, "requires-interface", "module:example.com/acme/interface-inspect", "interface:kernel.health/v1", "exposure", "example.com/acme/interface-inspect", "plystra.yaml", "exposure")
	assertInspectGraphEdge(t, document.Result.Edges, "selects-constructor", "interface:app.run/v1", "constructor:example.com/acme/interface-inspect/app.New", "unique-compatible", "example.com/acme/interface-inspect", "app/service.go", "implementation-constructor")
	assertInspectGraphEdge(t, document.Result.Edges, "selects-constructor", "interface:reports.read/v1", "constructor:example.com/acme/interface-inspect/app.New", "explicit", "example.com/acme/interface-inspect", "plystra.yaml", "implementation-selection")
	assertInspectGraphEdge(t, document.Result.Edges, "depends-on-interface", "constructor:example.com/acme/interface-inspect/app.New", "interface:audit.write/v1", "required", "example.com/acme/interface-inspect", "app/service.go", "implementation-constructor")
	assertInspectGraphEdge(t, document.Result.Edges, "depends-on-interface", "constructor:example.com/acme/interface-inspect/app.New", "interface:cache.read/v1", "optional-unavailable", "example.com/acme/interface-inspect", "app/service.go", "implementation-constructor")
	definitionEdge, exists := findInspectGraphEdge(document.Result.Edges, "defines-interface", "module:example.com/acme/interface-inspect", "interface:app.run/v1", "authored")
	if !exists || len(definitionEdge.Sources) != 1 || definitionEdge.Sources[0].Kind != "interface-declaration" {
		t.Fatalf("authored Interface definition edge retained non-authoritative metadata: %#v", definitionEdge)
	}
	assertInspectInterfaceGraphRedacted(t, root, firstHumanStdout, firstStdout)
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Interface graph mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInspectInterfacesSelectorsChangeOptionalAvailabilityDeterministically(t *testing.T) {
	t.Parallel()

	root, nested := createInspectInterfaceGraphProject(t)
	before := snapshotInspectProject(t, root)
	environment := inspectInterfaceGraphEnvironment()
	tests := []struct {
		name          string
		arguments     []string
		mode          string
		sourcePath    string
		selectionPath string
		humanConfig   string
	}{
		{
			name:          "environment",
			arguments:     []string{"inspect", "interfaces", "--env", "production", "--format", "json"},
			mode:          "environment",
			sourcePath:    "plystra.production.yaml",
			selectionPath: "plystra.yaml",
			humanConfig:   "--env",
		},
		{
			name:          "complete replacement",
			arguments:     []string{"inspect", "interfaces", "--config", "deploy/customer.yaml", "--format", "json"},
			mode:          "explicit-config",
			sourcePath:    "deploy/customer.yaml",
			selectionPath: "deploy/customer.yaml",
			humanConfig:   "--config",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, environment)
			if exitCode != 0 || stderr != inspectProgress {
				t.Fatalf("selected Interface graph = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			document := decodeInspectGraphCommandEnvelope(t, stdout)
			if document.ConfigurationMode != test.mode || document.Result.Type != "interfaces" || len(document.Result.Nodes) != 12 || len(document.Result.Edges) != 20 {
				t.Fatalf("selected Interface graph = mode %q type %q nodes %d edges %d", document.ConfigurationMode, document.Result.Type, len(document.Result.Nodes), len(document.Result.Edges))
			}
			assertInspectGraphEdge(t, document.Result.Edges, "requires-interface", "module:example.com/acme/interface-inspect", "interface:cache.read/v1", "declaration", "example.com/acme/interface-inspect", test.sourcePath, "declaration")
			assertInspectGraphEdge(t, document.Result.Edges, "selects-constructor", "interface:cache.read/v1", "constructor:example.com/acme/interface-library/cache.New", "unique-compatible", "example.com/acme/interface-library", "cache/service.go", "implementation-constructor")
			assertInspectGraphEdge(t, document.Result.Edges, "selects-constructor", "interface:reports.read/v1", "constructor:example.com/acme/interface-inspect/app.New", "explicit", "example.com/acme/interface-inspect", test.selectionPath, "implementation-selection")
			assertInspectGraphEdge(t, document.Result.Edges, "depends-on-interface", "constructor:example.com/acme/interface-inspect/app.New", "interface:cache.read/v1", "optional-available", "example.com/acme/interface-inspect", "app/service.go", "implementation-constructor")
			if _, exists := findInspectGraphEdge(document.Result.Edges, "depends-on-interface", "constructor:example.com/acme/interface-inspect/app.New", "interface:cache.read/v1", "optional-unavailable"); exists {
				t.Fatalf("selected Interface graph retained unavailable optional edge: %#v", document.Result.Edges)
			}
			humanArguments := append([]string(nil), test.arguments[:len(test.arguments)-2]...)
			humanExit, humanStdout, humanStderr := runCommand(t, humanArguments, nested, environment)
			if humanExit != 0 || humanStderr != "" || !strings.Contains(humanStdout, "Interface graph: 6 Interfaces, 6 active, 3 constructors, 20 relationships\n") || !strings.Contains(humanStdout, "Interface: cache.read/v1 (active, authored)\n") || !strings.Contains(humanStdout, "example.com/acme/interface-inspect/app.New -> cache.read/v1 (optional-available)\n") {
				t.Fatalf("selected human Interface graph for %s = exit %d, stdout %q, stderr %q", test.humanConfig, humanExit, humanStdout, humanStderr)
			}
			assertInspectInterfaceGraphRedacted(t, root, humanStdout, stdout)
		})
	}
	if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("selected Interface graphs mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestInspectFailuresKeepJSONStdoutEmptyAndDoNotMutate(t *testing.T) {
	t.Parallel()

	root, nested := createInspectCommandProject(t)
	before := snapshotInspectProject(t, root)
	tests := []struct {
		name        string
		arguments   []string
		environment map[string]string
		want        string
	}{
		{name: "missing overlay", arguments: []string{"inspect", "--format", "json", "--env", "missing"}, want: "plystra.missing.yaml"},
		{name: "Interface graph missing overlay", arguments: []string{"inspect", "interfaces", "--format", "json", "--env", "missing"}, want: "plystra.missing.yaml"},
		{name: "unsafe environment", arguments: []string{"inspect", "--format", "json", "--env", "../test"}, want: "safe filename component"},
		{name: "ambient conflict", arguments: []string{"inspect", "--format", "json"}, environment: map[string]string{"PLYSTRA_ENV": "production", "PLYSTRA_CONFIG": "deploy/customer.yaml"}, want: "PLYSTRA_CONFIG and PLYSTRA_ENV cannot be used together"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.arguments, nested, inspectCommandEnvironment(test.environment))
			if exitCode != 1 || stdout != "" || !strings.HasPrefix(stderr, inspectProgress) || !strings.Contains(stderr, test.want) {
				t.Fatalf("inspect failure = exit %d, stdout %q, stderr %q", exitCode, stdout, stderr)
			}
			if after := snapshotInspectProject(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("failed inspect mutated the Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func createInspectCommandProject(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/inspect\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  address: resolved-secret-marker\n")
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "http: {expose: {kernel.health/v1: {transport: connect}}}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), "http: {expose: {kernel.info/v1: {transport: connect}}}\n")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return root, nested
}

func createInspectModuleGraphProject(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	writeCommandFile(t, filepath.Join(root, "go.mod"), "module example.com/acme/inspect\n\ngo 1.26\n\nrequire example.com/acme/library v0.0.0\n\nreplace example.com/acme/library => ./library\n")
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "http:\n  address: resolved-secret-marker\n")
	writeCommandFile(t, filepath.Join(root, "library", "go.mod"), "module example.com/acme/library\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "library", "plystra.yaml"), "{}\n")
	writeCommandFile(t, filepath.Join(root, "library", "library.go"), "package library\n")
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return root, nested
}

func createInspectInterfaceGraphProject(t testing.TB) (string, string) {
	t.Helper()
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/acme/interface-inspect

go 1.26

require (
	example.com/acme/interface-library v0.0.0
	github.com/plystra/kernel v0.0.0
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/mod v0.38.0 // indirect
)

replace example.com/acme/interface-library => ./library

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), `http:
  address: private-http-address-marker
  expose:
    app.run/v1: {transport: connect}
    kernel.health/v1: {transport: connect}
interfaces:
  require: [app.run/v1, kernel.health/v1, reports.read/v1]
  use:
    reports.read/v1: example.com/acme/interface-inspect/app.New
config:
  example.com/acme/interface-inspect/app.New:
    endpoint: private-endpoint-marker
    password: {env: INTERFACE_GRAPH_PASSWORD}
`)
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "interfaces: {require: [cache.read/v1]}\n")
	writeCommandFile(t, filepath.Join(root, "deploy", "customer.yaml"), `http:
  address: private-http-address-marker
  expose:
    app.run/v1: {transport: connect}
    kernel.health/v1: {transport: connect}
interfaces:
  require: [app.run/v1, cache.read/v1, kernel.health/v1, reports.read/v1]
  use:
    reports.read/v1: example.com/acme/interface-inspect/app.New
config:
  example.com/acme/interface-inspect/app.New:
    endpoint: private-endpoint-marker
    password: {env: INTERFACE_GRAPH_PASSWORD}
`)
	writeCommandGraphInterface(t, root, "app/run/v1", "runv1", "app.run/v1", "Run")
	writeCommandFile(t, filepath.Join(root, "interfaces", "app", "run", "v1", "interface.yaml"), "description: Runs the selected application.\nsemantics:\n  kind: command\n")
	writeCommandGraphInterface(t, root, "audit/write/v1", "writev1", "audit.write/v1", "Write")
	writeCommandGraphInterface(t, root, "reports/read/v1", "readv1", "reports.read/v1", "Read")
	writeCommandFile(t, filepath.Join(root, "app", "service.go"), `package app

import (
	"context"

	readv1 "example.com/acme/interface-library/interfaces/cache/read/v1"
	runv1 "example.com/acme/interface-inspect/interfaces/app/run/v1"
	writev1 "example.com/acme/interface-inspect/interfaces/audit/write/v1"
	reportsv1 "example.com/acme/interface-inspect/interfaces/reports/read/v1"
	plystra "github.com/plystra/kernel"
	"github.com/plystra/kernel/configuration"
)

type Config struct {
	Endpoint string
	Password configuration.Secret
}

type Service struct{}

//plystra:implements app.run/v1
//plystra:implements reports.read/v1
func New(_ Config, audit writev1.Interface, cache plystra.Optional[readv1.Interface]) (*Service, error) {
	return &Service{}, nil
}

func (*Service) Run(context.Context, runv1.Request) (runv1.Response, error) {
	return runv1.Response{}, nil
}

func (*Service) Read(context.Context, reportsv1.Request) (reportsv1.Response, error) {
	return reportsv1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(root, "audit", "service.go"), `package audit

import (
	"context"

	writev1 "example.com/acme/interface-inspect/interfaces/audit/write/v1"
)

type Service struct{}

//plystra:implements audit.write/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Write(context.Context, writev1.Request) (writev1.Response, error) {
	return writev1.Response{}, nil
}
`)
	writeCommandFile(t, filepath.Join(root, "library", "go.mod"), "module example.com/acme/interface-library\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(root, "library", "plystra.yaml"), "{}\n")
	writeCommandGraphInterface(t, filepath.Join(root, "library"), "cache/read/v1", "readv1", "cache.read/v1", "Read")
	writeCommandFile(t, filepath.Join(root, "library", "cache", "service.go"), `package cache

import (
	"context"

	readv1 "example.com/acme/interface-library/interfaces/cache/read/v1"
)

type Service struct{}

//plystra:implements cache.read/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) Read(context.Context, readv1.Request) (readv1.Response, error) {
	return readv1.Response{}, nil
}
`)
	nested := filepath.Join(root, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", nested, err)
	}
	return root, nested
}

func decodeInspectCommandEnvelope(t testing.TB, output string) inspectCommandEnvelope {
	t.Helper()
	var result inspectCommandEnvelope
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode inspect JSON: %v\n%s", err, output)
	}
	return result
}

func decodeInspectGraphCommandEnvelope(t testing.TB, output string) inspectGraphCommandEnvelope {
	t.Helper()
	var result inspectGraphCommandEnvelope
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode inspect graph JSON: %v\n%s", err, output)
	}
	return result
}

func inspectGraphNodeByID(t testing.TB, nodes []inspectGraphNode, id string) inspectGraphNode {
	t.Helper()
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("graph nodes omit %q: %#v", id, nodes)
	return inspectGraphNode{}
}

func assertInspectGraphEdge(t testing.TB, edges []inspectGraphEdge, kind, from, to, reason, sourceModule, sourcePath, sourceKind string) {
	t.Helper()
	edge, exists := findInspectGraphEdge(edges, kind, from, to, reason)
	if !exists {
		t.Fatalf("graph edges omit %s %s -> %s (%s): %#v", kind, from, to, reason, edges)
	}
	for _, source := range edge.Sources {
		if source.Module == sourceModule && source.Path == sourcePath && source.Kind == sourceKind {
			return
		}
	}
	t.Fatalf("graph edge %s sources omit %s:%s (%s): %#v", edge.ID, sourceModule, sourcePath, sourceKind, edge.Sources)
}

func findInspectGraphEdge(edges []inspectGraphEdge, kind, from, to, reason string) (inspectGraphEdge, bool) {
	for _, edge := range edges {
		if edge.Kind == kind && edge.From == from && edge.To == to && edge.Reason == reason {
			return edge, true
		}
	}
	return inspectGraphEdge{}, false
}

func inspectCommandEnvironment(values map[string]string) []string {
	environment := commandGoEnvironment()
	filtered := environment[:0]
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "PLYSTRA_ENV") || strings.EqualFold(key, "PLYSTRA_CONFIG") {
			continue
		}
		filtered = append(filtered, entry)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		filtered = append(filtered, key+"="+values[key])
	}
	return filtered
}

func inspectInterfaceGraphEnvironment() []string {
	return inspectCommandEnvironment(map[string]string{
		"INTERFACE_GRAPH_PASSWORD": "resolved-interface-password-marker",
	})
}

func assertInspectInterfaceGraphRedacted(t testing.TB, root string, outputs ...string) {
	t.Helper()
	for _, output := range outputs {
		for _, private := range []string{
			root,
			"private-http-address-marker",
			"private-endpoint-marker",
			"INTERFACE_GRAPH_PASSWORD",
			"resolved-interface-password-marker",
		} {
			if strings.Contains(output, private) {
				t.Fatalf("Interface graph leaked %q: %s", private, output)
			}
		}
	}
}

func snapshotInspectProject(t testing.TB, root string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[filepath.ToSlash(relative)+"/"] = nil
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relative)] = bytes.Clone(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot Project: %v", err)
	}
	return result
}
