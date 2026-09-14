package command

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/diagnosticschema"
)

func TestParseNewArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		want      newArguments
		ok        bool
	}{
		{name: "project", arguments: []string{"new", "app"}, want: newArguments{projectName: "app"}, ok: true},
		{name: "module", arguments: []string{"new", "app", "--module", "example.com/acme/app"}, want: newArguments{projectName: "app", modulePath: "example.com/acme/app"}, ok: true},
		{name: "template", arguments: []string{"new", "app", "--template", "example.com/acme/platform@v1.2.3"}, want: newArguments{projectName: "app", template: "example.com/acme/platform@v1.2.3"}, ok: true},
		{name: "template exports", arguments: []string{"new", "app", "--template", "example.com/acme/platform@v1.2.3", "--adopt-export", "runtime", "--adopt-export", "defaults"}, want: newArguments{projectName: "app", template: "example.com/acme/platform@v1.2.3", adoptExports: []string{"runtime", "defaults"}}, ok: true},
		{name: "plugin", arguments: []string{"new", "app", "--plugin", "account"}, want: newArguments{projectName: "app", plugin: "account"}, ok: true},
		{name: "tool opt ins", arguments: []string{"new", "app", "--git", "--github-ci"}, want: newArguments{projectName: "app", git: choiceYes, githubCI: choiceYes}, ok: true},
		{name: "interactive", arguments: []string{"new", "app", "--interactive"}, want: newArguments{projectName: "app", interactive: true}, ok: true},
		{name: "guidance opt out", arguments: []string{"new", "app", "--no-agent-guidance"}, want: newArguments{projectName: "app", noAgentGuidance: true}, ok: true},
		{name: "all options", arguments: []string{"new", "app", "--template", "example.com/acme/platform@v1.2.3", "--adopt-export", "defaults", "--no-agent-guidance", "--interactive", "--github-ci", "--git"}, want: newArguments{projectName: "app", template: "example.com/acme/platform@v1.2.3", adoptExports: []string{"defaults"}, git: choiceYes, githubCI: choiceYes, interactive: true, noAgentGuidance: true}, ok: true},
		{name: "missing project", arguments: []string{"new"}},
		{name: "removed library option", arguments: []string{"new", "app", "--library"}},
		{name: "option as project", arguments: []string{"new", "--library"}},
		{name: "missing module", arguments: []string{"new", "app", "--module"}},
		{name: "option as module", arguments: []string{"new", "app", "--module", "--library"}},
		{name: "duplicate module", arguments: []string{"new", "app", "--module", "example.com/a", "--module", "example.com/b"}},
		{name: "missing template", arguments: []string{"new", "app", "--template"}},
		{name: "option as template", arguments: []string{"new", "app", "--template", "--library"}},
		{name: "duplicate template", arguments: []string{"new", "app", "--template", "example.com/a", "--template", "example.com/b"}},
		{name: "missing adopted export", arguments: []string{"new", "app", "--template", "example.com/a", "--adopt-export"}},
		{name: "option as adopted export", arguments: []string{"new", "app", "--template", "example.com/a", "--adopt-export", "--plugin"}},
		{name: "adopted export without template", arguments: []string{"new", "app", "--adopt-export", "defaults"}},
		{name: "missing plugin", arguments: []string{"new", "app", "--plugin"}},
		{name: "option as plugin", arguments: []string{"new", "app", "--plugin", "--library"}},
		{name: "duplicate plugin", arguments: []string{"new", "app", "--plugin", "account", "--plugin", "profile"}},
		{name: "duplicate git", arguments: []string{"new", "app", "--git", "--git"}},
		{name: "removed no git", arguments: []string{"new", "app", "--no-git"}},
		{name: "duplicate github ci", arguments: []string{"new", "app", "--github-ci", "--github-ci"}},
		{name: "removed no github ci", arguments: []string{"new", "app", "--no-github-ci"}},
		{name: "removed skills", arguments: []string{"new", "app", "--skills"}},
		{name: "removed no skills", arguments: []string{"new", "app", "--no-skills"}},
		{name: "duplicate interactive", arguments: []string{"new", "app", "--interactive", "--interactive"}},
		{name: "duplicate guidance opt out", arguments: []string{"new", "app", "--no-agent-guidance", "--no-agent-guidance"}},
		{name: "unknown", arguments: []string{"new", "app", "--unknown"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseNewArguments(test.arguments)
			if ok != test.ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseNewArguments(%q) = %#v, %t; want %#v, %t", test.arguments, got, ok, test.want, test.ok)
			}
		})
	}
}

func TestResolveNewChoicesUsesStableDefaultsAndExplicitInteraction(t *testing.T) {
	t.Parallel()

	defaults, err := resolveNewChoices(newArguments{}, func(string, bool) (bool, error) {
		t.Fatal("non-interactive defaults prompted")
		return false, nil
	})
	if err != nil || defaults != (resolvedNewChoices{agentGuidance: true}) {
		t.Fatalf("default resolveNewChoices = %#v, %v", defaults, err)
	}

	optedIn, err := resolveNewChoices(newArguments{git: choiceYes, githubCI: choiceYes}, func(string, bool) (bool, error) {
		t.Fatal("explicit non-interactive choices prompted")
		return false, nil
	})
	if err != nil || optedIn != (resolvedNewChoices{git: true, githubCI: true, agentGuidance: true}) {
		t.Fatalf("opt-in resolveNewChoices = %#v, %v", optedIn, err)
	}

	withoutGuidance, err := resolveNewChoices(newArguments{noAgentGuidance: true}, func(string, bool) (bool, error) {
		t.Fatal("guidance opt-out prompted")
		return false, nil
	})
	if err != nil || withoutGuidance != (resolvedNewChoices{}) {
		t.Fatalf("guidance opt-out resolveNewChoices = %#v, %v", withoutGuidance, err)
	}

	prompts := make([]string, 0, 1)
	interactive, err := resolveNewChoices(newArguments{git: choiceYes, interactive: true, noAgentGuidance: true}, func(question string, defaultValue bool) (bool, error) {
		prompts = append(prompts, question)
		if !defaultValue {
			t.Fatal("new project prompts must default to yes")
		}
		return false, nil
	})
	if err != nil || interactive != (resolvedNewChoices{git: true}) {
		t.Fatalf("interactive resolveNewChoices = %#v, %v", interactive, err)
	}
	if !reflect.DeepEqual(prompts, []string{"Include GitHub Actions CI?"}) {
		t.Fatalf("prompts = %q", prompts)
	}

	if choices, err := resolveNewChoices(newArguments{interactive: true}, nil); choices != (resolvedNewChoices{}) || !errors.Is(err, errNewChoicePrompt) || !strings.Contains(err.Error(), "interactive input is unavailable") {
		t.Fatalf("unavailable interactive input = %#v, %v", choices, err)
	}
}

func TestPromptNewProjectAcceptsDefaultsAndRetriesInvalidInput(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	prompt := promptNewProject(strings.NewReader("later\n\nno\n"), &output)
	git, err := prompt("Initialize a Git repository?", true)
	if err != nil || !git {
		t.Fatalf("Git prompt = %t, %v", git, err)
	}
	ci, err := prompt("Include GitHub Actions CI?", true)
	if err != nil || ci {
		t.Fatalf("CI prompt = %t, %v", ci, err)
	}
	want := "Initialize a Git repository? [Y/n]: Please enter yes or no.\n" +
		"Initialize a Git repository? [Y/n]: " +
		"Include GitHub Actions CI? [Y/n]: "
	if output.String() != want {
		t.Fatalf("prompt output = %q, want %q", output.String(), want)
	}
}

func TestParseGenerateArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments         []string
		check             bool
		configurationPath string
		environmentName   string
		ok                bool
	}{
		{arguments: []string{"generate"}, ok: true},
		{arguments: []string{"generate", "--check"}, check: true, ok: true},
		{arguments: []string{"generate", "--config", "deploy/customer.yaml"}, configurationPath: "deploy/customer.yaml", ok: true},
		{arguments: []string{"generate", "--check", "--config", "deploy/customer.yaml"}, check: true, configurationPath: "deploy/customer.yaml", ok: true},
		{arguments: []string{"generate", "--config", "deploy/customer.yaml", "--check"}, check: true, configurationPath: "deploy/customer.yaml", ok: true},
		{arguments: []string{"generate", "--env", "test"}, environmentName: "test", ok: true},
		{arguments: []string{"generate", "--check", "--env", "production"}, check: true, environmentName: "production", ok: true},
		{arguments: nil},
		{arguments: []string{"generate", "--write"}},
		{arguments: []string{"generate", "--check", "--check"}},
		{arguments: []string{"generate", "--config"}},
		{arguments: []string{"generate", "--config", ""}},
		{arguments: []string{"generate", "--config", "a.yaml", "--config", "b.yaml"}},
		{arguments: []string{"generate", "--env"}},
		{arguments: []string{"generate", "--env", ""}},
		{arguments: []string{"generate", "--env", "test", "--env", "production"}},
		{arguments: []string{"generate", "--env", "test", "--config", "deploy.yaml"}, configurationPath: "deploy.yaml", environmentName: "test", ok: true},
	}
	for _, test := range tests {
		result, ok := parseGenerateArguments(test.arguments)
		if result.check != test.check || result.configurationPath != test.configurationPath || result.environmentName != test.environmentName || ok != test.ok {
			t.Errorf("parseGenerateArguments(%q) = %#v, %t; want check %t, path %q, environment %q, ok %t", test.arguments, result, ok, test.check, test.configurationPath, test.environmentName, test.ok)
		}
	}
}

func TestParseCheckArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments         []string
		configurationPath string
		environmentName   string
		ok                bool
	}{
		{arguments: []string{"check"}, ok: true},
		{arguments: []string{"check", "--config", "deploy/customer.yaml"}, configurationPath: "deploy/customer.yaml", ok: true},
		{arguments: []string{"check", "--env", "test"}, environmentName: "test", ok: true},
		{arguments: nil},
		{arguments: []string{"generate"}},
		{arguments: []string{"check", "--check"}},
		{arguments: []string{"check", "--config"}},
		{arguments: []string{"check", "--config", ""}},
		{arguments: []string{"check", "--config", "a.yaml", "--config", "b.yaml"}},
		{arguments: []string{"check", "--env"}},
		{arguments: []string{"check", "--env", ""}},
		{arguments: []string{"check", "--env", "test", "--env", "production"}},
		{arguments: []string{"check", "--env", "test", "--config", "deploy.yaml"}, configurationPath: "deploy.yaml", environmentName: "test", ok: true},
	}
	for _, test := range tests {
		result, ok := parseCheckArguments(test.arguments)
		if result.configurationPath != test.configurationPath || result.environmentName != test.environmentName || ok != test.ok {
			t.Errorf("parseCheckArguments(%q) = %#v, %t; want path %q, environment %q, ok %t", test.arguments, result, ok, test.configurationPath, test.environmentName, test.ok)
		}
	}
}

func TestParseInspectArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments         []string
		graphType         diagnosticschema.GraphType
		format            commandFormat
		verbose           bool
		configurationPath string
		environmentName   string
		ok                bool
	}{
		{arguments: []string{"inspect"}, format: commandFormatHuman, ok: true},
		{arguments: []string{"inspect", "--verbose"}, format: commandFormatHuman, verbose: true, ok: true},
		{arguments: []string{"inspect", "--format", "human"}, format: commandFormatHuman, ok: true},
		{arguments: []string{"inspect", "--format", "json", "--verbose"}, format: commandFormatJSON, verbose: true, ok: true},
		{arguments: []string{"inspect", "modules"}, graphType: diagnosticschema.GraphTypeModules, format: commandFormatHuman, ok: true},
		{arguments: []string{"inspect", "interfaces"}, graphType: diagnosticschema.GraphTypeInterfaces, format: commandFormatHuman, ok: true},
		{arguments: []string{"inspect", "implementations"}, graphType: diagnosticschema.GraphTypeImplementations, format: commandFormatHuman, ok: true},
		{arguments: []string{"inspect", "configuration"}, graphType: diagnosticschema.GraphTypeConfiguration, format: commandFormatHuman, ok: true},
		{arguments: []string{"inspect", "--config", "deploy/customer.yaml", "--format", "json"}, format: commandFormatJSON, configurationPath: "deploy/customer.yaml", ok: true},
		{arguments: []string{"inspect", "--env", "production", "--verbose"}, format: commandFormatHuman, verbose: true, environmentName: "production", ok: true},
		{arguments: nil},
		{arguments: []string{"check"}},
		{arguments: []string{"inspect", "--verbose", "--verbose"}},
		{arguments: []string{"inspect", "--format"}},
		{arguments: []string{"inspect", "--format", ""}},
		{arguments: []string{"inspect", "--format", "yaml"}},
		{arguments: []string{"inspect", "--format", "json", "--format", "human"}},
		{arguments: []string{"inspect", "--config"}},
		{arguments: []string{"inspect", "--config", ""}},
		{arguments: []string{"inspect", "--config", "a.yaml", "--config", "b.yaml"}},
		{arguments: []string{"inspect", "--env"}},
		{arguments: []string{"inspect", "--env", ""}},
		{arguments: []string{"inspect", "--env", "test", "--env", "production"}},
		{arguments: []string{"inspect", "--env", "test", "--config", "deploy.yaml"}, format: commandFormatHuman, configurationPath: "deploy.yaml", environmentName: "test", ok: true},
	}
	for _, test := range tests {
		result, ok := parseInspectArguments(test.arguments)
		if result.graphType != test.graphType || result.format != test.format || result.verbose != test.verbose || result.configurationPath != test.configurationPath || result.environmentName != test.environmentName || ok != test.ok {
			t.Errorf("parseInspectArguments(%q) = %#v, %t; want graph %q, format %q, verbose %t, path %q, environment %q, ok %t", test.arguments, result, ok, test.graphType, test.format, test.verbose, test.configurationPath, test.environmentName, test.ok)
		}
	}
}

func TestParseExplainArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments         []string
		subjectKind       diagnosticschema.ExplainSubjectKind
		subject           string
		format            commandFormat
		verbose           bool
		configurationPath string
		environmentName   string
		ok                bool
	}{
		{arguments: []string{"explain", "capability", "email.send/v1"}, subjectKind: diagnosticschema.ExplainSubjectCapability, subject: "email.send/v1", format: commandFormatHuman, ok: true},
		{arguments: []string{"explain", "capability", "email.send/v1", "--verbose"}, subjectKind: diagnosticschema.ExplainSubjectCapability, subject: "email.send/v1", format: commandFormatHuman, verbose: true, ok: true},
		{arguments: []string{"explain", "capability", "email.send/v1", "--format", "json", "--verbose"}, subjectKind: diagnosticschema.ExplainSubjectCapability, subject: "email.send/v1", format: commandFormatJSON, verbose: true, ok: true},
		{arguments: []string{"explain", "capability", "email.send/v1", "--config", "deploy/customer.yaml", "--format", "human"}, subjectKind: diagnosticschema.ExplainSubjectCapability, subject: "email.send/v1", format: commandFormatHuman, configurationPath: "deploy/customer.yaml", ok: true},
		{arguments: []string{"explain", "capability", "email.send/v1", "--env", "production", "--format", "json"}, subjectKind: diagnosticschema.ExplainSubjectCapability, subject: "email.send/v1", format: commandFormatJSON, environmentName: "production", ok: true},
		{arguments: []string{"explain", "plugin", "acme.email"}, subjectKind: diagnosticschema.ExplainSubjectPlugin, subject: "acme.email", format: commandFormatHuman, ok: true},
		{arguments: []string{"explain", "config", "config.acme.email.host", "--env", "production"}, subjectKind: diagnosticschema.ExplainSubjectConfiguration, subject: "config.acme.email.host", format: commandFormatHuman, environmentName: "production", ok: true},
		{arguments: []string{"explain", "alias", "mail.send/v1", "--config", "deploy/customer.yaml"}, subjectKind: diagnosticschema.ExplainSubjectAlias, subject: "mail.send/v1", format: commandFormatHuman, configurationPath: "deploy/customer.yaml", ok: true},
		{arguments: []string{"explain", "exposure", "mail.send/v1", "--env", "production"}, subjectKind: diagnosticschema.ExplainSubjectExposure, subject: "mail.send/v1", format: commandFormatHuman, environmentName: "production", ok: true},
		{arguments: nil},
		{arguments: []string{"explain"}},
		{arguments: []string{"explain", "capability"}},
		{arguments: []string{"explain", "configuration", "config.acme.email.host"}},
		{arguments: []string{"explain", "capability", "--verbose"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--verbose", "--verbose"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--format"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--format", "yaml"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--format", "json", "--format", "human"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--config"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--config", "a.yaml", "--config", "b.yaml"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--env"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--env", "test", "--env", "production"}},
		{arguments: []string{"explain", "capability", "email.send/v1", "--env", "test", "--config", "deploy.yaml"}, subjectKind: diagnosticschema.ExplainSubjectCapability, subject: "email.send/v1", format: commandFormatHuman, configurationPath: "deploy.yaml", environmentName: "test", ok: true},
	}
	for _, test := range tests {
		result, ok := parseExplainArguments(test.arguments)
		if result.subjectKind != test.subjectKind || result.subject != test.subject || result.format != test.format || result.verbose != test.verbose || result.configurationPath != test.configurationPath || result.environmentName != test.environmentName || ok != test.ok {
			t.Errorf("parseExplainArguments(%q) = %#v, %t; want kind %q, subject %q, format %q, verbose %t, path %q, environment %q, ok %t", test.arguments, result, ok, test.subjectKind, test.subject, test.format, test.verbose, test.configurationPath, test.environmentName, test.ok)
		}
	}
}
