package diagnosticschema

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func TestGraphV1BuildsExactTypedResult(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	moduleSource := graphDiagnosticSource(evidence.Modules()[0].Source())
	capabilitySource := intrinsicExplainSource(t, evidence, "kernel.health/v1")
	input := GraphInput{
		Evidence: evidence,
		Type:     GraphTypeCapabilities,
		Nodes: []GraphNode{
			{ID: "module:example.com/inspect", Kind: "module", Label: "example.com/inspect", Sources: []diagnosticjson.Source{moduleSource}},
			{ID: "capability:kernel.health/v1", Kind: "capability", Label: "kernel.health/v1", Sources: []diagnosticjson.Source{capabilitySource}},
		},
		Edges: []GraphEdge{{
			ID:      "declared-requirement:example.com/inspect->kernel.health/v1",
			Kind:    "declared-requirement",
			From:    "module:example.com/inspect",
			To:      "capability:kernel.health/v1",
			Reason:  "project-declaration",
			Sources: []diagnosticjson.Source{moduleSource},
		}},
		Diagnostics: []diagnosticjson.Diagnostic{{
			Code:     "PLYSTRA_GRAPH_READY",
			Severity: diagnosticjson.SeverityInfo,
			Message:  "The Capability graph is available.",
		}},
	}
	result, err := NewGraph(input)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	if !result.Valid() || result.Envelope().Schema() != GraphSchemaV1() || result.Envelope().SchemaVersion() != 1 || result.Envelope().ConfigurationMode() != generation.ConfigurationModeEnvironment || result.Envelope().ApplicationModelDigest() != evidence.BuildModelDigest() {
		t.Fatalf("graph identity = valid %t schema %#v mode %q digest %q", result.Valid(), result.Envelope().Schema(), result.Envelope().ConfigurationMode(), result.Envelope().ApplicationModelDigest())
	}
	if result.Type() != GraphTypeCapabilities || result.NodeCount() != 2 || result.EdgeCount() != 1 {
		t.Fatalf("graph summary = type %q nodes %d edges %d", result.Type(), result.NodeCount(), result.EdgeCount())
	}
	if !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("graph result did not retain complete resolution evidence")
	}

	wantResult := canonicalObject(t, `{
		"type":"capabilities",
		"nodes":[
			{"id":"capability:kernel.health/v1","kind":"capability","label":"kernel.health/v1","sources":[{"module":"`+capabilitySource.Module+`","path":"`+capabilitySource.Path+`","kind":"`+capabilitySource.Kind+`","line":1,"column":1}]},
			{"id":"module:example.com/inspect","kind":"module","label":"example.com/inspect","sources":[{"module":"`+moduleSource.Module+`","path":"`+moduleSource.Path+`","kind":"`+moduleSource.Kind+`","line":1,"column":1}]}
		],
		"edges":[{"id":"declared-requirement:example.com/inspect->kernel.health/v1","kind":"declared-requirement","from":"module:example.com/inspect","to":"capability:kernel.health/v1","reason":"project-declaration","sources":[{"module":"`+moduleSource.Module+`","path":"`+moduleSource.Path+`","kind":"`+moduleSource.Kind+`","line":1,"column":1}]}],
		"resolution_evidence":`+string(evidence.CanonicalJSON())+`
	}`)
	if got := result.Envelope().ResultJSON(); !bytes.Equal(got, wantResult) {
		t.Fatalf("graph result JSON:\ngot:  %s\nwant: %s", got, wantResult)
	}
	if !slices.Contains(result.Envelope().Sources(), moduleSource) || !slices.Contains(result.Envelope().Sources(), capabilitySource) {
		t.Fatalf("graph envelope sources omit graph provenance: %#v", result.Envelope().Sources())
	}
	if bytes.Contains(result.Envelope().CanonicalJSON(), []byte("resolved-secret-marker")) || containsWindowsDrivePath(result.Envelope().CanonicalJSON()) {
		t.Fatal("graph envelope contains unrestricted configuration or an absolute path")
	}
}

func TestGraphV1SupportsEveryGraphAndConfigurationType(t *testing.T) {
	t.Parallel()

	for _, graphType := range []GraphType{GraphTypeModules, GraphTypePlugins, GraphTypeCapabilities, GraphTypeGeneration, GraphTypeConfiguration} {
		t.Run(string(graphType), func(t *testing.T) {
			evidence := resolvedInspectEvidence(t)
			result, err := NewGraph(GraphInput{Evidence: evidence, Type: graphType})
			if err != nil || !result.Valid() || result.Type() != graphType || result.NodeCount() != 0 || result.EdgeCount() != 0 {
				t.Fatalf("NewGraph = %#v, %v", result, err)
			}
			if !bytes.Contains(result.Envelope().ResultJSON(), []byte(`"nodes":[]`)) || !bytes.Contains(result.Envelope().ResultJSON(), []byte(`"edges":[]`)) {
				t.Fatalf("empty graph collections are not explicit: %s", result.Envelope().ResultJSON())
			}
		})
	}

	for _, test := range []struct {
		name          string
		configuration string
		environment   string
		mode          generation.ConfigurationMode
	}{
		{name: "default", mode: generation.ConfigurationModeDefault},
		{name: "environment", environment: "production", mode: generation.ConfigurationModeEnvironment},
		{name: "explicit", configuration: "deploy/customer.yaml", mode: generation.ConfigurationModeExplicit},
	} {
		t.Run("mode-"+test.name, func(t *testing.T) {
			evidence := resolvedInspectEvidenceFor(t, test.configuration, test.environment)
			result, err := NewGraph(GraphInput{Evidence: evidence, Type: GraphTypeModules})
			if err != nil || result.Envelope().ConfigurationMode() != test.mode {
				t.Fatalf("configuration mode = %q, %v", result.Envelope().ConfigurationMode(), err)
			}
		})
	}
}

func TestGraphV1CanonicalizesPermutations(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	moduleSource := graphDiagnosticSource(evidence.Modules()[0].Source())
	capabilitySource := intrinsicExplainSource(t, evidence, "kernel.health/v1")
	nodes := []GraphNode{
		{ID: "module:example.com/inspect", Kind: "module", Label: "example.com/inspect", Sources: []diagnosticjson.Source{moduleSource, moduleSource}},
		{ID: "capability:kernel.health/v1", Kind: "capability", Label: "kernel.health/v1", Sources: []diagnosticjson.Source{capabilitySource}},
	}
	edges := []GraphEdge{
		{ID: "selected-provider:kernel.health/v1", Kind: "selected-provider", From: "module:example.com/inspect", To: "capability:kernel.health/v1", Reason: "intrinsic-kernel", Sources: []diagnosticjson.Source{capabilitySource}},
		{ID: "declared-requirement:kernel.health/v1", Kind: "declared-requirement", From: "module:example.com/inspect", To: "capability:kernel.health/v1", Reason: "project-declaration", Sources: []diagnosticjson.Source{moduleSource}},
	}
	diagnostics := []diagnosticjson.Diagnostic{
		{Code: "PLYSTRA_ZETA", Severity: diagnosticjson.SeverityWarning, Message: "Review the graph."},
		{Code: "PLYSTRA_ALPHA", Severity: diagnosticjson.SeverityInfo, Message: "The graph is deterministic."},
	}
	build := func() GraphResult {
		result, err := NewGraph(GraphInput{Evidence: evidence, Type: GraphTypeCapabilities, Nodes: nodes, Edges: edges, Diagnostics: diagnostics})
		if err != nil {
			t.Fatalf("NewGraph: %v", err)
		}
		return result
	}
	first := build()
	slices.Reverse(nodes)
	slices.Reverse(edges)
	slices.Reverse(diagnostics)
	second := build()
	if len(first.Nodes()[1].Sources) != 1 || !bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()) || first.Envelope().Digest() != second.Envelope().Digest() {
		t.Fatalf("permuted graph = nodes %#v equal %t digest %q/%q", first.Nodes(), bytes.Equal(first.Envelope().CanonicalJSON(), second.Envelope().CanonicalJSON()), first.Envelope().Digest(), second.Envelope().Digest())
	}
}

func TestGraphV1RejectsIncompleteAndUnsafeInput(t *testing.T) {
	t.Parallel()

	valid := resolvedInspectEvidence(t)
	moduleSource := graphDiagnosticSource(valid.Modules()[0].Source())
	base := GraphInput{
		Evidence: valid,
		Type:     GraphTypeCapabilities,
		Nodes: []GraphNode{
			{ID: "module:example.com/inspect", Kind: "module", Label: "example.com/inspect", Sources: []diagnosticjson.Source{moduleSource}},
			{ID: "capability:kernel.health/v1", Kind: "capability", Label: "kernel.health/v1"},
		},
		Edges: []GraphEdge{{
			ID:     "declared-requirement:kernel.health/v1",
			Kind:   "declared-requirement",
			From:   "module:example.com/inspect",
			To:     "capability:kernel.health/v1",
			Reason: "project-declaration",
		}},
	}
	tests := []struct {
		name   string
		mutate func(*GraphInput)
		want   string
	}{
		{name: "zero evidence", mutate: func(input *GraphInput) { input.Evidence = resolutionevidence.Evidence{} }, want: "resolution evidence"},
		{name: "missing configuration", mutate: func(input *GraphInput) { input.Evidence = syntheticInspectEvidence(t, false, true, true) }, want: "configuration"},
		{name: "missing assembly", mutate: func(input *GraphInput) { input.Evidence = syntheticInspectEvidence(t, true, false, true) }, want: "assembly"},
		{name: "missing transports", mutate: func(input *GraphInput) { input.Evidence = syntheticInspectEvidence(t, true, true, false) }, want: "transports"},
		{name: "graph type", mutate: func(input *GraphInput) { input.Type = "actions" }, want: "graph type"},
		{name: "node kind", mutate: func(input *GraphInput) { input.Nodes[0].Kind = "Bad Kind" }, want: "kind"},
		{name: "node namespace", mutate: func(input *GraphInput) { input.Nodes[0].ID = "plugin:example.com/inspect" }, want: "namespace"},
		{name: "node path", mutate: func(input *GraphInput) { input.Nodes[0].ID = `module:C:\Users\person\project` }, want: "absolute path"},
		{name: "node slash path", mutate: func(input *GraphInput) { input.Nodes[0].ID = "module:C:/Users/person/project" }, want: "absolute path"},
		{name: "node unix path", mutate: func(input *GraphInput) { input.Nodes[0].ID = "module:/home/person/project" }, want: "absolute path"},
		{name: "node label", mutate: func(input *GraphInput) { input.Nodes[0].Label = "Open /home/person/project." }, want: "absolute path"},
		{name: "duplicate node", mutate: func(input *GraphInput) { input.Nodes = append(input.Nodes, input.Nodes[0]) }, want: "duplicated"},
		{name: "node count", mutate: func(input *GraphInput) { input.Nodes = make([]GraphNode, maximumGraphNodes+1) }, want: "node count"},
		{name: "edge kind", mutate: func(input *GraphInput) { input.Edges[0].Kind = "Bad Kind" }, want: "kind"},
		{name: "edge namespace", mutate: func(input *GraphInput) { input.Edges[0].ID = "edge:kernel.health/v1" }, want: "namespace"},
		{name: "edge from", mutate: func(input *GraphInput) { input.Edges[0].From = "module:missing" }, want: "does not identify"},
		{name: "edge to", mutate: func(input *GraphInput) { input.Edges[0].To = "capability:missing/v1" }, want: "does not identify"},
		{name: "self edge", mutate: func(input *GraphInput) { input.Edges[0].To = input.Edges[0].From }, want: "self edge"},
		{name: "edge reason", mutate: func(input *GraphInput) { input.Edges[0].Reason = "Bad Reason" }, want: "reason"},
		{name: "duplicate edge id", mutate: func(input *GraphInput) { input.Edges = append(input.Edges, input.Edges[0]) }, want: "duplicated"},
		{name: "edge count", mutate: func(input *GraphInput) { input.Edges = make([]GraphEdge, maximumGraphEdges+1) }, want: "edge count"},
		{name: "duplicate relationship", mutate: func(input *GraphInput) {
			duplicate := input.Edges[0]
			duplicate.ID = "declared-requirement:duplicate"
			input.Edges = append(input.Edges, duplicate)
		}, want: "typed relationship"},
		{name: "node source", mutate: func(input *GraphInput) {
			input.Nodes[0].Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../secret", Kind: "project-marker"}}
		}, want: "sources"},
		{name: "edge source", mutate: func(input *GraphInput) {
			input.Edges[0].Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../secret", Kind: "requirement"}}
		}, want: "sources"},
		{name: "additional source", mutate: func(input *GraphInput) {
			input.Sources = []diagnosticjson.Source{{Module: "example.com/inspect", Path: "../secret", Kind: "graph"}}
		}, want: "sources"},
		{name: "invalid diagnostic", mutate: func(input *GraphInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "invalid", Severity: diagnosticjson.SeverityError, Message: "Resolve the error."}}
		}, want: "shared envelope"},
		{name: "unsafe diagnostic", mutate: func(input *GraphInput) {
			input.Diagnostics = []diagnosticjson.Diagnostic{{Code: "PLYSTRA_UNSAFE", Severity: diagnosticjson.SeverityError, Message: "Open /home/person/secret.yaml."}}
		}, want: "absolute path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := cloneGraphInput(base)
			test.mutate(&input)
			result, err := NewGraph(input)
			if !errors.Is(err, ErrGraph) || !strings.Contains(err.Error(), test.want) || result.Valid() {
				t.Fatalf("NewGraph = %#v, %v; want ErrGraph containing %q", result, err, test.want)
			}
		})
	}
}

func TestGraphV1StorageIsDefensiveAndSchemaIndependent(t *testing.T) {
	t.Parallel()

	evidence := resolvedInspectEvidence(t)
	source := graphDiagnosticSource(evidence.Modules()[0].Source())
	nodes := []GraphNode{
		{ID: "module:example.com/inspect", Kind: "module", Label: "example.com/inspect", Sources: []diagnosticjson.Source{source}},
		{ID: "capability:kernel.health/v1", Kind: "capability", Label: "kernel.health/v1"},
	}
	edges := []GraphEdge{{ID: "declared-requirement:kernel.health/v1", Kind: "declared-requirement", From: nodes[0].ID, To: nodes[1].ID}}
	result, err := NewGraph(GraphInput{Evidence: evidence, Type: GraphTypeCapabilities, Nodes: nodes, Edges: edges})
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	before := result.Envelope().CanonicalJSON()
	nodes[0].ID = "mutated"
	nodes[0].Sources[0].Path = "mutated"
	edges[0].From = "mutated"
	returnedNodes := result.Nodes()
	moduleIndex := slices.IndexFunc(returnedNodes, func(node GraphNode) bool { return node.ID == "module:example.com/inspect" })
	if moduleIndex < 0 {
		t.Fatalf("graph nodes omit current module: %#v", returnedNodes)
	}
	returnedNodes[moduleIndex].ID = "mutated"
	returnedNodes[moduleIndex].Sources[0].Path = "mutated"
	returnedEdges := result.Edges()
	returnedEdges[0].From = "mutated"
	evidenceJSON := result.ResolutionEvidenceJSON()
	evidenceJSON[0] = '['
	canonical := result.Envelope().CanonicalJSON()
	canonical[0] = '['
	storedModule := graphNodeByID(t, result.Nodes(), "module:example.com/inspect")
	if !result.Valid() || !bytes.Equal(before, result.Envelope().CanonicalJSON()) || storedModule.Sources[0].Path == "mutated" || result.Edges()[0].From == "mutated" || !bytes.Equal(result.ResolutionEvidenceJSON(), evidence.CanonicalJSON()) {
		t.Fatal("graph result storage aliases mutable input or returned data")
	}
	if (GraphResult{}).Valid() || GraphSchemaV1() == ExplainSchemaV1() || GraphSchemaV1() == InspectSchemaV1() || GraphSchemaV1().Name() != "plystra.graph" || GraphSchemaV1().Version() != 1 {
		t.Fatal("graph, explain, and inspect schema identities are not independent")
	}
}

func cloneGraphInput(input GraphInput) GraphInput {
	result := input
	result.Nodes = cloneGraphNodes(input.Nodes)
	result.Edges = cloneGraphEdges(input.Edges)
	result.Diagnostics = append([]diagnosticjson.Diagnostic(nil), input.Diagnostics...)
	result.Sources = append([]diagnosticjson.Source(nil), input.Sources...)
	return result
}

func graphDiagnosticSource(source resolutionevidence.Source) diagnosticjson.Source {
	return diagnosticjson.Source{Module: source.Module(), Path: source.Path(), Kind: source.Kind(), Line: source.Line(), Column: source.Column()}
}

func graphNodeByID(t testing.TB, nodes []GraphNode, id string) GraphNode {
	t.Helper()
	for _, node := range nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("graph nodes omit %q: %#v", id, nodes)
	return GraphNode{}
}
