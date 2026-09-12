package command

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/resolutionevidence"
)

func inspectModulesGraph(evidence resolutionevidence.Evidence) (diagnosticschema.GraphResult, error) {
	modules := evidence.Modules()
	current := ""
	nodes := make([]diagnosticschema.GraphNode, 0, len(modules))
	for _, module := range modules {
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			current = module.Path()
		}
		nodes = append(nodes, diagnosticschema.GraphNode{
			ID:      "module:" + module.Path(),
			Kind:    "module",
			Label:   module.Path(),
			Sources: []diagnosticjson.Source{explainSource(module.Source())},
		})
	}
	if current == "" {
		return diagnosticschema.GraphResult{}, fmt.Errorf("resolution evidence omits the current Project module")
	}

	edges := make([]diagnosticschema.GraphEdge, 0, len(modules)-1)
	for _, module := range modules {
		if module.Role() == resolutionevidence.ModuleRoleCurrent {
			continue
		}
		edges = append(edges, diagnosticschema.GraphEdge{
			ID:      fmt.Sprintf("requires:%s->%s", current, module.Path()),
			Kind:    "requires",
			From:    "module:" + current,
			To:      "module:" + module.Path(),
			Reason:  moduleGraphReason(module),
			Sources: []diagnosticjson.Source{explainSource(module.Source())},
		})
	}
	return diagnosticschema.NewGraph(diagnosticschema.GraphInput{
		Evidence: evidence,
		Type:     diagnosticschema.GraphTypeModules,
		Nodes:    nodes,
		Edges:    edges,
	})
}

func moduleGraphReason(module resolutionevidence.Module) string {
	if _, replaced := module.Replacement(); replaced {
		return "replacement"
	}
	if module.Workspace() {
		return "workspace"
	}
	if module.Direct() {
		return "direct"
	}
	if module.Indirect() {
		return "indirect"
	}
	return "transitive"
}

func writeHumanModuleGraph(writer io.Writer, result diagnosticschema.GraphResult, evidence resolutionevidence.Evidence, verbose bool) error {
	var content strings.Builder
	fmt.Fprintf(&content, "Module graph: %d modules, %d dependencies\n", result.NodeCount(), result.EdgeCount())
	for _, module := range evidence.Modules() {
		details := []string{string(module.Role())}
		if module.RequiredVersion() != "" {
			details = append(details, "required "+module.RequiredVersion())
		}
		if module.SelectedVersion() != "" {
			details = append(details, "selected "+module.SelectedVersion())
		}
		if module.Direct() {
			details = append(details, "direct")
		}
		if module.Indirect() {
			details = append(details, "indirect")
		}
		if module.Workspace() {
			details = append(details, "workspace")
		}
		if replacement, exists := module.Replacement(); exists {
			details = append(details, "replaced by "+replacement.ModulePath())
		}
		fmt.Fprintf(&content, "Module: %s (%s)\n", module.Path(), strings.Join(details, ", "))
		fmt.Fprintf(&content, "  Source: %s\n", explainSourceSummary(explainSource(module.Source())))
	}
	if result.EdgeCount() > 0 {
		content.WriteString("Dependencies:\n")
		for _, edge := range result.Edges() {
			from := strings.TrimPrefix(edge.From, "module:")
			to := strings.TrimPrefix(edge.To, "module:")
			fmt.Fprintf(&content, "  %s -> %s (%s)\n", from, to, edge.Reason)
		}
	}
	if verbose {
		var evidenceJSON bytes.Buffer
		if err := json.Indent(&evidenceJSON, result.ResolutionEvidenceJSON(), "", "  "); err != nil {
			return fmt.Errorf("format resolution evidence: %w", err)
		}
		content.WriteString("Resolution evidence:\n")
		for _, line := range strings.Split(evidenceJSON.String(), "\n") {
			content.WriteString("  ")
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
	_, err := io.WriteString(writer, content.String())
	return err
}
