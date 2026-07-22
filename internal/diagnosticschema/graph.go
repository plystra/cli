package diagnosticschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/resolutionevidence"
)

const (
	maximumGraphNodes          = 4_096
	maximumGraphEdges          = 16_384
	maximumGraphIdentityLength = 1_024
)

var (
	// ErrGraph reports incomplete, inconsistent, or unsafe input for the
	// plystra.graph v1 result schema.
	ErrGraph = errors.New("build plystra.graph result")

	graphSchemaV1 = mustSchema("plystra.graph", 1)
)

// GraphType identifies one closed public graph projection.
type GraphType string

const (
	GraphTypeModules       GraphType = "modules"
	GraphTypePlugins       GraphType = "plugins"
	GraphTypeCapabilities  GraphType = "capabilities"
	GraphTypeGeneration    GraphType = "generation"
	GraphTypeConfiguration GraphType = "configuration"
)

// GraphNodeKind is a stable lower-kebab node vocabulary owned by a typed
// graph builder.
type GraphNodeKind string

// GraphEdgeKind is a stable lower-kebab edge vocabulary owned by a typed
// graph builder.
type GraphEdgeKind string

// GraphNode is one typed graph vertex. ID is namespaced by Kind, Label is the
// bounded human identity, and Sources contain stable declaration provenance.
type GraphNode struct {
	ID      string
	Kind    GraphNodeKind
	Label   string
	Sources []diagnosticjson.Source
}

// GraphEdge is one directed typed relationship between existing node IDs.
// Reason is an optional stable lower-kebab decision code.
type GraphEdge struct {
	ID      string
	Kind    GraphEdgeKind
	From    string
	To      string
	Reason  string
	Sources []diagnosticjson.Source
}

// GraphInput is the construction-only input for one plystra.graph v1 result.
// Nodes and edges come from a graph-type-specific resolver over Evidence.
type GraphInput struct {
	Evidence    resolutionevidence.Evidence
	Type        GraphType
	Nodes       []GraphNode
	Edges       []GraphEdge
	Diagnostics []diagnosticjson.Diagnostic
	Sources     []diagnosticjson.Source
}

// GraphResult is one immutable plystra.graph v1 diagnostic result.
type GraphResult struct {
	envelope               diagnosticjson.Envelope
	evidence               resolutionevidence.Evidence
	graphType              GraphType
	nodes                  []GraphNode
	edges                  []GraphEdge
	resolutionEvidenceJSON []byte
	prepared               bool
}

type graphDocument struct {
	Type               GraphType       `json:"type"`
	Nodes              []graphNode     `json:"nodes"`
	Edges              []graphEdge     `json:"edges"`
	ResolutionEvidence json.RawMessage `json:"resolution_evidence"`
}

type graphNode struct {
	ID      string        `json:"id"`
	Kind    GraphNodeKind `json:"kind"`
	Label   string        `json:"label"`
	Sources []graphSource `json:"sources"`
}

type graphEdge struct {
	ID      string        `json:"id"`
	Kind    GraphEdgeKind `json:"kind"`
	From    string        `json:"from"`
	To      string        `json:"to"`
	Reason  string        `json:"reason"`
	Sources []graphSource `json:"sources"`
}

type graphSource struct {
	Module string `json:"module"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// GraphSchemaV1 returns the immutable command-owned schema descriptor.
func GraphSchemaV1() diagnosticjson.Schema { return graphSchemaV1 }

// NewGraph validates and constructs one complete plystra.graph v1 result.
func NewGraph(input GraphInput) (GraphResult, error) {
	if !input.Evidence.Valid() {
		return GraphResult{}, fmt.Errorf("%w: resolution evidence is not valid", ErrGraph)
	}
	selection, exists := input.Evidence.ConfigurationSelection()
	if !exists {
		return GraphResult{}, fmt.Errorf("%w: resolution evidence omits selected configuration provenance", ErrGraph)
	}
	if _, exists := input.Evidence.StaticAssembly(); !exists {
		return GraphResult{}, fmt.Errorf("%w: resolution evidence omits static assembly membership", ErrGraph)
	}
	if _, exists := input.Evidence.HTTPTransports(); !exists {
		return GraphResult{}, fmt.Errorf("%w: resolution evidence omits selected HTTP transports", ErrGraph)
	}
	if !validGraphType(input.Type) {
		return GraphResult{}, fmt.Errorf("%w: graph type %q is not supported", ErrGraph, input.Type)
	}
	for index, diagnostic := range input.Diagnostics {
		if err := validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message); err != nil {
			return GraphResult{}, fmt.Errorf("%w: %v", ErrGraph, err)
		}
	}

	nodes, edges, err := normalizeGraphElements(input.Type, selection.Mode(), input.Evidence.BuildModelDigest(), input.Nodes, input.Edges)
	if err != nil {
		return GraphResult{}, fmt.Errorf("%w: %v", ErrGraph, err)
	}
	allSources := collectSources(input.Evidence, input.Sources)
	for _, node := range nodes {
		allSources = append(allSources, node.Sources...)
	}
	for _, edge := range edges {
		allSources = append(allSources, edge.Sources...)
	}
	allSources, err = normalizeGraphSources(selection.Mode(), input.Evidence.BuildModelDigest(), allSources)
	if err != nil {
		return GraphResult{}, fmt.Errorf("%w: sources: %v", ErrGraph, err)
	}

	evidenceJSON := input.Evidence.CanonicalJSON()
	resultJSON, err := encodeGraphDocument(graphDocument{
		Type:               input.Type,
		Nodes:              graphNodes(nodes),
		Edges:              graphEdges(edges),
		ResolutionEvidence: evidenceJSON,
	})
	if err != nil {
		return GraphResult{}, fmt.Errorf("%w: encode result: %v", ErrGraph, err)
	}
	envelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 graphSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: input.Evidence.BuildModelDigest(),
		Diagnostics:            input.Diagnostics,
		Sources:                allSources,
		Result:                 resultJSON,
	})
	if err != nil {
		return GraphResult{}, fmt.Errorf("%w: shared envelope: %v", ErrGraph, err)
	}
	return GraphResult{
		envelope:               envelope,
		evidence:               input.Evidence,
		graphType:              input.Type,
		nodes:                  nodes,
		edges:                  edges,
		resolutionEvidenceJSON: evidenceJSON,
		prepared:               true,
	}, nil
}

// Valid reports whether NewGraph produced this internally consistent result.
func (r GraphResult) Valid() bool {
	if !r.prepared || !r.evidence.Valid() || !r.envelope.Valid() || r.envelope.Schema() != graphSchemaV1 || r.envelope.ApplicationModelDigest() != r.evidence.BuildModelDigest() {
		return false
	}
	selection, exists := r.evidence.ConfigurationSelection()
	if !exists || selection.Mode() != r.envelope.ConfigurationMode() {
		return false
	}
	if _, exists := r.evidence.StaticAssembly(); !exists {
		return false
	}
	if _, exists := r.evidence.HTTPTransports(); !exists {
		return false
	}
	for index, diagnostic := range r.envelope.Diagnostics() {
		if validateDisplayText(fmt.Sprintf("diagnostics[%d].message", index), diagnostic.Message) != nil {
			return false
		}
	}
	nodes, edges, err := normalizeGraphElements(r.graphType, selection.Mode(), r.evidence.BuildModelDigest(), r.nodes, r.edges)
	if err != nil || !equalGraphNodes(nodes, r.nodes) || !equalGraphEdges(edges, r.edges) {
		return false
	}
	if !bytes.Equal(r.resolutionEvidenceJSON, r.evidence.CanonicalJSON()) {
		return false
	}
	resultJSON, err := encodeGraphDocument(graphDocument{
		Type:               r.graphType,
		Nodes:              graphNodes(r.nodes),
		Edges:              graphEdges(r.edges),
		ResolutionEvidence: append([]byte(nil), r.resolutionEvidenceJSON...),
	})
	if err != nil {
		return false
	}
	canonicalEnvelope, err := diagnosticjson.New(diagnosticjson.Input{
		Schema:                 graphSchemaV1,
		ConfigurationMode:      selection.Mode(),
		ApplicationModelDigest: r.evidence.BuildModelDigest(),
		Diagnostics:            r.envelope.Diagnostics(),
		Sources:                r.envelope.Sources(),
		Result:                 resultJSON,
	})
	return err == nil && bytes.Equal(canonicalEnvelope.CanonicalJSON(), r.envelope.CanonicalJSON())
}

// Envelope returns the immutable shared diagnostic envelope.
func (r GraphResult) Envelope() diagnosticjson.Envelope { return r.envelope }

// Type returns modules, plugins, capabilities, generation, or configuration.
func (r GraphResult) Type() GraphType { return r.graphType }

// Nodes returns a defensive copy in canonical ID order.
func (r GraphResult) Nodes() []GraphNode { return cloneGraphNodes(r.nodes) }

// Edges returns a defensive copy in canonical ID order.
func (r GraphResult) Edges() []GraphEdge { return cloneGraphEdges(r.edges) }

// NodeCount returns the number of typed vertices.
func (r GraphResult) NodeCount() int { return len(r.nodes) }

// EdgeCount returns the number of directed typed relationships.
func (r GraphResult) EdgeCount() int { return len(r.edges) }

// ResolutionEvidenceJSON returns a defensive copy of the complete canonical
// resolution-evidence document embedded in this command result.
func (r GraphResult) ResolutionEvidenceJSON() []byte {
	return append([]byte(nil), r.resolutionEvidenceJSON...)
}

func validGraphType(value GraphType) bool {
	switch value {
	case GraphTypeModules, GraphTypePlugins, GraphTypeCapabilities, GraphTypeGeneration, GraphTypeConfiguration:
		return true
	default:
		return false
	}
}

func normalizeGraphElements(graphType GraphType, mode generation.ConfigurationMode, digest string, inputNodes []GraphNode, inputEdges []GraphEdge) ([]GraphNode, []GraphEdge, error) {
	if !validGraphType(graphType) {
		return nil, nil, fmt.Errorf("graph type %q is not supported", graphType)
	}
	if len(inputNodes) > maximumGraphNodes {
		return nil, nil, fmt.Errorf("node count exceeds %d", maximumGraphNodes)
	}
	if len(inputEdges) > maximumGraphEdges {
		return nil, nil, fmt.Errorf("edge count exceeds %d", maximumGraphEdges)
	}

	nodes := make([]GraphNode, len(inputNodes))
	nodeIDs := make(map[string]struct{}, len(inputNodes))
	for index, input := range inputNodes {
		if !validExplanationCode(string(input.Kind)) {
			return nil, nil, fmt.Errorf("nodes[%d].kind %q is not canonical lower kebab case", index, input.Kind)
		}
		if err := validateGraphElementID(fmt.Sprintf("nodes[%d].id", index), input.ID, string(input.Kind)); err != nil {
			return nil, nil, err
		}
		if _, duplicate := nodeIDs[input.ID]; duplicate {
			return nil, nil, fmt.Errorf("nodes[%d].id %q is duplicated", index, input.ID)
		}
		if err := validateDisplayText(fmt.Sprintf("nodes[%d].label", index), input.Label); err != nil {
			return nil, nil, err
		}
		sources, err := normalizeGraphSources(mode, digest, input.Sources)
		if err != nil {
			return nil, nil, fmt.Errorf("nodes[%d].sources: %v", index, err)
		}
		nodeIDs[input.ID] = struct{}{}
		nodes[index] = GraphNode{ID: input.ID, Kind: input.Kind, Label: input.Label, Sources: sources}
	}
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].ID < nodes[right].ID })

	edges := make([]GraphEdge, len(inputEdges))
	edgeIDs := make(map[string]struct{}, len(inputEdges))
	semanticEdges := make(map[string]struct{}, len(inputEdges))
	for index, input := range inputEdges {
		if !validExplanationCode(string(input.Kind)) {
			return nil, nil, fmt.Errorf("edges[%d].kind %q is not canonical lower kebab case", index, input.Kind)
		}
		if err := validateGraphElementID(fmt.Sprintf("edges[%d].id", index), input.ID, string(input.Kind)); err != nil {
			return nil, nil, err
		}
		if _, duplicate := edgeIDs[input.ID]; duplicate {
			return nil, nil, fmt.Errorf("edges[%d].id %q is duplicated", index, input.ID)
		}
		if _, exists := nodeIDs[input.From]; !exists {
			return nil, nil, fmt.Errorf("edges[%d].from %q does not identify a graph node", index, input.From)
		}
		if _, exists := nodeIDs[input.To]; !exists {
			return nil, nil, fmt.Errorf("edges[%d].to %q does not identify a graph node", index, input.To)
		}
		if input.From == input.To {
			return nil, nil, fmt.Errorf("edges[%d] is a self edge", index)
		}
		if input.Reason != "" && !validExplanationCode(input.Reason) {
			return nil, nil, fmt.Errorf("edges[%d].reason %q is not canonical lower kebab case", index, input.Reason)
		}
		sources, err := normalizeGraphSources(mode, digest, input.Sources)
		if err != nil {
			return nil, nil, fmt.Errorf("edges[%d].sources: %v", index, err)
		}
		normalized := GraphEdge{ID: input.ID, Kind: input.Kind, From: input.From, To: input.To, Reason: input.Reason, Sources: sources}
		semanticKey := graphEdgeSemanticKey(normalized)
		if _, duplicate := semanticEdges[semanticKey]; duplicate {
			return nil, nil, fmt.Errorf("edges[%d] duplicates an existing typed relationship", index)
		}
		edgeIDs[input.ID] = struct{}{}
		semanticEdges[semanticKey] = struct{}{}
		edges[index] = normalized
	}
	sort.Slice(edges, func(left, right int) bool { return edges[left].ID < edges[right].ID })
	return nodes, edges, nil
}

func validateGraphElementID(name, value, namespace string) error {
	if value == "" || len(value) > maximumGraphIdentityLength || !utf8.ValidString(value) {
		return fmt.Errorf("%s must be non-empty valid UTF-8 and at most %d bytes", name, maximumGraphIdentityLength)
	}
	if strings.IndexFunc(value, func(character rune) bool { return unicode.IsControl(character) || unicode.IsSpace(character) }) >= 0 {
		return fmt.Errorf("%s must not contain whitespace or control characters", name)
	}
	if !strings.HasPrefix(value, namespace+":") || len(value) == len(namespace)+1 {
		return fmt.Errorf("%s %q must use the %q namespace", name, value, namespace+":")
	}
	suffix := value[len(namespace)+1:]
	if strings.HasPrefix(suffix, "/") || hasWindowsDrivePrefix(suffix) || strings.Contains(value, "\\") || containsAbsolutePath(value) {
		return fmt.Errorf("%s must not contain a machine-specific absolute path", name)
	}
	return nil
}

func hasWindowsDrivePrefix(value string) bool {
	return len(value) >= 3 && ((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) && value[1] == ':' && (value[2] == '/' || value[2] == '\\')
}

func normalizeGraphSources(mode generation.ConfigurationMode, digest string, values []diagnosticjson.Source) ([]diagnosticjson.Source, error) {
	return normalizeSchemaSources(graphSchemaV1, mode, digest, values)
}

func graphEdgeSemanticKey(edge GraphEdge) string {
	var builder strings.Builder
	builder.WriteString(string(edge.Kind))
	builder.WriteByte(0)
	builder.WriteString(edge.From)
	builder.WriteByte(0)
	builder.WriteString(edge.To)
	builder.WriteByte(0)
	builder.WriteString(edge.Reason)
	return builder.String()
}

func graphNodes(values []GraphNode) []graphNode {
	result := make([]graphNode, len(values))
	for index, node := range values {
		result[index] = graphNode{ID: node.ID, Kind: node.Kind, Label: node.Label, Sources: graphSources(node.Sources)}
	}
	return result
}

func graphEdges(values []GraphEdge) []graphEdge {
	result := make([]graphEdge, len(values))
	for index, edge := range values {
		result[index] = graphEdge{ID: edge.ID, Kind: edge.Kind, From: edge.From, To: edge.To, Reason: edge.Reason, Sources: graphSources(edge.Sources)}
	}
	return result
}

func graphSources(values []diagnosticjson.Source) []graphSource {
	result := make([]graphSource, len(values))
	for index, source := range values {
		result[index] = graphSource{Module: source.Module, Path: source.Path, Kind: source.Kind, Line: source.Line, Column: source.Column}
	}
	return result
}

func cloneGraphNodes(values []GraphNode) []GraphNode {
	result := make([]GraphNode, len(values))
	for index, node := range values {
		result[index] = node
		result[index].Sources = append([]diagnosticjson.Source(nil), node.Sources...)
	}
	return result
}

func cloneGraphEdges(values []GraphEdge) []GraphEdge {
	result := make([]GraphEdge, len(values))
	for index, edge := range values {
		result[index] = edge
		result[index].Sources = append([]diagnosticjson.Source(nil), edge.Sources...)
	}
	return result
}

func equalGraphNodes(left, right []GraphNode) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Kind != right[index].Kind || left[index].Label != right[index].Label || !equalDiagnosticSources(left[index].Sources, right[index].Sources) {
			return false
		}
	}
	return true
}

func equalGraphEdges(left, right []GraphEdge) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].ID != right[index].ID || left[index].Kind != right[index].Kind || left[index].From != right[index].From || left[index].To != right[index].To || left[index].Reason != right[index].Reason || !equalDiagnosticSources(left[index].Sources, right[index].Sources) {
			return false
		}
	}
	return true
}

func encodeGraphDocument(document graphDocument) ([]byte, error) {
	if len(document.ResolutionEvidence) == 0 || !json.Valid(document.ResolutionEvidence) {
		return nil, errors.New("resolution evidence is not valid JSON")
	}
	return json.Marshal(document)
}
