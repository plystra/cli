package command

import (
	"fmt"
	"io"

	"github.com/plystra/cli/internal/agentguidance"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/projectlocate"
)

const guidanceUsage = `Usage:
  plystra guidance sync [--replace-generated]
  plystra guidance check

Commands:
  sync                   Refresh the installed catalog's manifest-owned guidance.
  check                  Compare guidance with the installed catalog without mutation.

Options:
  --replace-generated    Discard edits only in existing bounded regular paths owned by the prior manifest.

Ordinary sync changes or removes only unchanged paths listed by the previous
manifest. Missing prior-owned paths and desired paths absent from previous
ownership block both sync modes, whether the desired path is missing or already
occupied. The CLI never creates, edits, deletes, or claims optional
.agents/skills/plystra/local.md, unlisted files, sibling skills, or
repository-wide Agent instructions.

PLYSTRA_AGENT_GUIDANCE_DRIFT reports every stale, missing, unlisted, or manually
modified guidance path as a path-only agent-guidance source. Check is always
non-mutating, and a blocked sync leaves every Project file unchanged.
PLYSTRA_AGENT_GUIDANCE_MANIFEST_INVALID reports the ownership manifest as a
path-only agent-guidance source. PLYSTRA_PROJECT_CONCURRENT_CHANGE reports each
known guidance transaction path that changed after inspection.
`

type guidanceArguments struct {
	action           string
	replaceGenerated bool
}

func runGuidance(arguments []string, stdout, stderr io.Writer, workingDirectory string) int {
	if help, ok := guidanceHelp(arguments); ok {
		_, _ = io.WriteString(stdout, help)
		return 0
	}
	parsed, ok := parseGuidanceArguments(arguments)
	if !ok {
		_, _ = io.WriteString(stderr, guidanceUsage)
		return 2
	}

	project, err := projectlocate.Find(workingDirectory)
	if err != nil {
		writeCommandFailure(stderr, "locate Project", err, recoveryContext{})
		return 1
	}
	projection, err := agentguidance.Render(project.ModulePath())
	if err != nil {
		writeCommandFailure(stderr, "render Agent guidance", err, recoveryContext{})
		return 1
	}

	switch parsed.action {
	case "check":
		report, err := agentguidance.Check(project.Path(), projection)
		if err != nil {
			writeCommandFailure(stderr, "", err, recoveryContext{})
			return 1
		}
		if !report.Clean() {
			writeGuidanceDriftReport(stderr, project.ModulePath(), report)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Agent guidance is current for %s in %s\n", project.ModulePath(), project.Path())
		return 0
	case "sync":
		result, err := agentguidance.Sync(project.Path(), projection, agentguidance.SyncOptions{ReplaceGenerated: parsed.replaceGenerated})
		if err != nil {
			writeCommandFailure(stderr, "", err, recoveryContext{})
			return 1
		}
		if len(result.Changed()) == 0 && len(result.Removed()) == 0 {
			_, _ = fmt.Fprintf(stdout, "Agent guidance is current for %s in %s\n", project.ModulePath(), project.Path())
			return 0
		}
		_, _ = fmt.Fprintf(stdout, "synchronized Agent guidance for %s in %s:\n", project.ModulePath(), project.Path())
		for _, filePath := range result.Changed() {
			_, _ = fmt.Fprintf(stdout, "  changed %s\n", filePath)
		}
		for _, filePath := range result.Removed() {
			_, _ = fmt.Fprintf(stdout, "  removed %s\n", filePath)
		}
		return 0
	default:
		return 2
	}
}

func parseGuidanceArguments(arguments []string) (guidanceArguments, bool) {
	if len(arguments) < 2 || arguments[0] != "guidance" {
		return guidanceArguments{}, false
	}
	result := guidanceArguments{action: arguments[1]}
	switch result.action {
	case "check":
		if len(arguments) == 2 {
			return result, true
		}
	case "sync":
		if len(arguments) == 2 {
			return result, true
		}
		if len(arguments) == 3 && arguments[2] == "--replace-generated" {
			result.replaceGenerated = true
			return result, true
		}
	}
	return guidanceArguments{}, false
}

func guidanceHelp(arguments []string) (string, bool) {
	switch {
	case len(arguments) == 2 && arguments[0] == "guidance" && isHelp(arguments[1]):
		return guidanceUsage, true
	case len(arguments) == 3 && arguments[0] == "guidance" && (arguments[1] == "check" || arguments[1] == "sync") && isHelp(arguments[2]):
		return guidanceUsage, true
	default:
		return "", false
	}
}

func writeGuidanceDriftReport(writer io.Writer, modulePath string, report agentguidance.Report) {
	_, _ = fmt.Fprintln(writer, "Agent guidance is not current:")
	sourceInputs := make([]diagnosticjson.Source, 0, len(report.Changes()))
	for _, change := range report.Changes() {
		_, _ = fmt.Fprintf(writer, "  %s %s\n", change.Kind(), change.Path())
		sourceInputs = append(sourceInputs, diagnosticjson.Source{
			Module: modulePath,
			Path:   change.Path(),
			Kind:   "agent-guidance",
		})
	}
	if sources, err := diagnosticjson.CanonicalizeSources(sourceInputs); err == nil {
		for index, source := range sources {
			if index == 0 {
				_, _ = fmt.Fprintln(writer)
			}
			_, _ = fmt.Fprintf(writer, "Source: %s\n", explainSourceSummary(source))
		}
	}
	_, _ = fmt.Fprintf(writer, "\nRecovery:\n%s\n\nDiagnostic: %s\n", agentGuidanceDriftRecovery(), diagnosticAgentGuidanceDrift)
}
