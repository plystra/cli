package command

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/installedcapabilities"
)

func TestInstalledCapabilityCatalogAgreesWithCommandParsers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		id          string
		invocations [][]string
	}{
		{id: "add", invocations: [][]string{{"add", "example.com/acme/platform@v1.0.0"}}},
		{id: "capability.create", invocations: [][]string{{"capability", "create", "records.read", "--plugin", "records", "--interactive", "--confirm", "--query", "--expose"}}},
		{id: "capability.expose", invocations: [][]string{{"capability", "expose", "records.read/v1", "--env", "production"}, {"capability", "expose", "records.read/v1", "--config", "deploy/customer.yaml"}}},
		{id: "capability.implement", invocations: [][]string{{"capability", "implement", "records.read/v1", "--plugin", "records", "--interactive"}}},
		{id: "check", invocations: [][]string{{"check", "--env", "production"}, {"check", "--config", "deploy/customer.yaml"}}},
		{id: "explain.alias", invocations: [][]string{{"explain", "alias", "mail.send/v1", "--verbose", "--format", "json", "--env", "production"}}},
		{id: "explain.capability", invocations: [][]string{{"explain", "capability", "email.send/v1", "--verbose", "--format", "json", "--config", "deploy/customer.yaml"}}},
		{id: "explain.config", invocations: [][]string{{"explain", "config", "config.acme.email.host", "--verbose", "--format", "json", "--env", "production"}}},
		{id: "explain.exposure", invocations: [][]string{{"explain", "exposure", "mail.send/v1", "--verbose", "--format", "json", "--config", "deploy/customer.yaml"}}},
		{id: "explain.plugin", invocations: [][]string{{"explain", "plugin", "acme.email", "--verbose", "--format", "json", "--env", "production"}}},
		{id: "generate", invocations: [][]string{{"generate", "--check", "--env", "production"}, {"generate", "--check", "--config", "deploy/customer.yaml"}}},
		{id: "guidance.check", invocations: [][]string{{"guidance", "check"}}},
		{id: "guidance.sync", invocations: [][]string{{"guidance", "sync", "--replace-generated"}}},
		{id: "help", invocations: [][]string{{"help"}}},
		{id: "implement", invocations: [][]string{{"implement", "records.list/v1", "--package", "./recordlist"}}},
		{id: "inspect", invocations: [][]string{{"inspect", "--verbose", "--format", "json", "--env", "production"}, {"inspect", "--config", "deploy/customer.yaml"}}},
		{id: "inspect.capabilities", invocations: [][]string{{"inspect", "capabilities", "--format", "json"}}},
		{id: "inspect.configuration", invocations: [][]string{{"inspect", "configuration", "--verbose", "--format", "json", "--config", "deploy/customer.yaml"}}},
		{id: "inspect.implementations", invocations: [][]string{{"inspect", "implementations", "--verbose", "--format", "json", "--env", "production"}}},
		{id: "inspect.interfaces", invocations: [][]string{{"inspect", "interfaces", "--verbose", "--format", "json", "--config", "deploy/customer.yaml"}}},
		{id: "inspect.modules", invocations: [][]string{{"inspect", "modules", "--verbose", "--format", "json", "--env", "production"}}},
		{id: "inspect.resources", invocations: [][]string{{"inspect", "resources", "--verbose", "--format", "json", "--config", "deploy/customer.yaml"}}},
		{id: "interface.create", invocations: [][]string{{"interface", "create", "records.list"}}},
		{id: "new", invocations: [][]string{{"new", "app", "--module", "example.com/acme/app", "--template", "example.com/acme/platform@v1.0.0", "--plugin", "records", "--git", "--github-ci", "--interactive", "--no-agent-guidance", "--format", "json"}}},
		{id: "plugin.create", invocations: [][]string{{"plugin", "create", "records"}}},
		{id: "remove", invocations: [][]string{{"remove", "example.com/acme/platform"}}},
		{id: "update", invocations: [][]string{{"update", "example.com/acme/platform@v1.1.0"}}},
		{id: "use", invocations: [][]string{{"use", "records.list/v1", "example.com/acme/recordlist.New", "--env", "production"}, {"use", "records.list/v1", "example.com/acme/recordlist.New", "--config", "deploy/customer.yaml"}}},
		{id: "version", invocations: [][]string{{"version"}}},
	}

	capabilities, err := installedcapabilities.Current()
	if err != nil {
		t.Fatalf("installedcapabilities.Current: %v", err)
	}
	gotIDs := make([]string, 0, len(capabilities.Commands()))
	for _, command := range capabilities.Commands() {
		gotIDs = append(gotIDs, command.ID())
	}
	wantIDs := make([]string, 0, len(tests))
	for _, test := range tests {
		wantIDs = append(wantIDs, test.id)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("installed command IDs = %#v, parser probes = %#v", gotIDs, wantIDs)
	}

	for _, test := range tests {
		test := test
		t.Run(test.id, func(t *testing.T) {
			t.Parallel()
			for _, invocation := range test.invocations {
				if !installedParserAccepts(t, test.id, invocation) {
					t.Errorf("installed parser rejected catalog invocation %q", invocation)
				}
			}
			invalid := append(append([]string(nil), test.invocations[0]...), "--unsupported-installed-argument")
			if installedParserAccepts(t, test.id, invalid) {
				t.Errorf("installed parser accepted undeclared argument in %q", invalid)
			}
		})
	}
}

func installedParserAccepts(t testing.TB, commandID string, arguments []string) bool {
	t.Helper()
	switch commandID {
	case "new":
		_, ok := parseNewArguments(arguments)
		return ok
	case "use":
		_, ok := parseUseArguments(arguments)
		return ok
	case "capability.create", "capability.expose", "capability.implement":
		parsed, ok := parseCapabilityArguments(arguments)
		return ok && commandID == "capability."+parsed.action
	case "guidance.check", "guidance.sync":
		parsed, ok := parseGuidanceArguments(arguments)
		return ok && commandID == "guidance."+parsed.action
	case "inspect.capabilities":
		_, ok := parseInspectCapabilitiesArguments(arguments)
		return ok
	case "inspect", "inspect.configuration", "inspect.implementations", "inspect.interfaces", "inspect.modules", "inspect.resources":
		parsed, ok := parseInspectArguments(arguments)
		if !ok {
			return false
		}
		if commandID == "inspect" {
			return parsed.graphType == ""
		}
		return parsed.graphType == diagnosticschema.GraphType(strings.TrimPrefix(commandID, "inspect."))
	case "explain.alias", "explain.capability", "explain.config", "explain.exposure", "explain.plugin":
		parsed, ok := parseExplainArguments(arguments)
		if !ok {
			return false
		}
		kind := strings.TrimPrefix(commandID, "explain.")
		if kind == "config" {
			kind = string(diagnosticschema.ExplainSubjectConfiguration)
		}
		return string(parsed.subjectKind) == kind
	case "check":
		_, ok := parseCheckArguments(arguments)
		return ok
	case "generate":
		_, ok := parseGenerateArguments(arguments)
		return ok
	default:
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		return runIn(arguments, &stdout, &stderr, t.TempDir(), nil, nil, nil) != 2
	}
}
