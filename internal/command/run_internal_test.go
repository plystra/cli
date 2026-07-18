package command

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseNewArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		want      newArguments
		ok        bool
	}{
		{name: "project", arguments: []string{"new", "example.com/acme/app"}, want: newArguments{modulePath: "example.com/acme/app"}, ok: true},
		{name: "plugin", arguments: []string{"new", "example.com/acme/app", "--plugin", "account"}, want: newArguments{modulePath: "example.com/acme/app", plugin: "account"}, ok: true},
		{name: "all choices enabled", arguments: []string{"new", "example.com/acme/app", "--git", "--github-ci", "--skills"}, want: newArguments{modulePath: "example.com/acme/app", git: choiceYes, githubCI: choiceYes, skills: choiceYes}, ok: true},
		{name: "all choices disabled", arguments: []string{"new", "example.com/acme/app", "--no-skills", "--no-git", "--no-github-ci"}, want: newArguments{modulePath: "example.com/acme/app", git: choiceNo, githubCI: choiceNo, skills: choiceNo}, ok: true},
		{name: "mixed choices", arguments: []string{"new", "example.com/acme/app", "--no-git", "--github-ci", "--no-skills"}, want: newArguments{modulePath: "example.com/acme/app", git: choiceNo, githubCI: choiceYes, skills: choiceNo}, ok: true},
		{name: "missing module", arguments: []string{"new"}},
		{name: "removed library option", arguments: []string{"new", "example.com/acme/app", "--library"}},
		{name: "option as module", arguments: []string{"new", "--library"}},
		{name: "missing plugin", arguments: []string{"new", "example.com/acme/app", "--plugin"}},
		{name: "option as plugin", arguments: []string{"new", "example.com/acme/app", "--plugin", "--library"}},
		{name: "duplicate plugin", arguments: []string{"new", "example.com/acme/app", "--plugin", "account", "--plugin", "profile"}},
		{name: "duplicate git", arguments: []string{"new", "example.com/acme/app", "--git", "--git"}},
		{name: "conflicting git", arguments: []string{"new", "example.com/acme/app", "--git", "--no-git"}},
		{name: "duplicate github ci", arguments: []string{"new", "example.com/acme/app", "--github-ci", "--github-ci"}},
		{name: "conflicting github ci", arguments: []string{"new", "example.com/acme/app", "--no-github-ci", "--github-ci"}},
		{name: "duplicate skills", arguments: []string{"new", "example.com/acme/app", "--skills", "--skills"}},
		{name: "conflicting skills", arguments: []string{"new", "example.com/acme/app", "--no-skills", "--skills"}},
		{name: "unknown", arguments: []string{"new", "example.com/acme/app", "--unknown"}},
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

func TestResolveNewChoicesUsesFlagsAndPromptsOnlyMissingValues(t *testing.T) {
	t.Parallel()

	prompts := make([]string, 0, 2)
	choices, err := resolveNewChoices(newArguments{
		git:      choiceNo,
		githubCI: choiceYes,
	}, func(question string, defaultValue bool) (bool, error) {
		prompts = append(prompts, question)
		if !defaultValue {
			t.Fatal("new project prompts must default to yes")
		}
		return false, nil
	})
	if err != nil || choices != (resolvedNewChoices{git: false, githubCI: true, skills: false}) {
		t.Fatalf("resolveNewChoices = %#v, %v", choices, err)
	}
	if !reflect.DeepEqual(prompts, []string{"Include Plystra development skills?"}) {
		t.Fatalf("prompts = %q", prompts)
	}

	allExplicit, err := resolveNewChoices(newArguments{git: choiceYes, githubCI: choiceNo, skills: choiceYes}, func(string, bool) (bool, error) {
		t.Fatal("explicit choices prompted")
		return false, nil
	})
	if err != nil || allExplicit != (resolvedNewChoices{git: true, githubCI: false, skills: true}) {
		t.Fatalf("explicit resolveNewChoices = %#v, %v", allExplicit, err)
	}
}

func TestResolveNewChoicesRejectsOmissionsWithoutTerminal(t *testing.T) {
	t.Parallel()

	choices, err := resolveNewChoices(newArguments{git: choiceYes}, nil)
	if choices != (resolvedNewChoices{}) || !errors.Is(err, errNewChoiceRequired) || !strings.Contains(err.Error(), "--github-ci or --no-github-ci") || !strings.Contains(err.Error(), "--skills or --no-skills") {
		t.Fatalf("resolveNewChoices = %#v, %v", choices, err)
	}
}

func TestPromptNewProjectAcceptsDefaultsAndRetriesInvalidInput(t *testing.T) {
	t.Parallel()

	var output strings.Builder
	prompt := promptNewProject(strings.NewReader("later\n\nno\nyes\n"), &output)
	git, err := prompt("Initialize a Git repository?", true)
	if err != nil || !git {
		t.Fatalf("Git prompt = %t, %v", git, err)
	}
	ci, err := prompt("Include GitHub Actions CI?", true)
	if err != nil || ci {
		t.Fatalf("CI prompt = %t, %v", ci, err)
	}
	skills, err := prompt("Include Plystra development skills?", true)
	if err != nil || !skills {
		t.Fatalf("skills prompt = %t, %v", skills, err)
	}
	want := "Initialize a Git repository? [Y/n]: Please enter yes or no.\n" +
		"Initialize a Git repository? [Y/n]: " +
		"Include GitHub Actions CI? [Y/n]: " +
		"Include Plystra development skills? [Y/n]: "
	if output.String() != want {
		t.Fatalf("prompt output = %q, want %q", output.String(), want)
	}
}

func TestParseGenerateArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments []string
		check     bool
		ok        bool
	}{
		{arguments: []string{"generate"}, ok: true},
		{arguments: []string{"generate", "--check"}, check: true, ok: true},
		{arguments: nil},
		{arguments: []string{"generate", "--write"}},
		{arguments: []string{"generate", "--check", "--check"}},
	}
	for _, test := range tests {
		check, ok := parseGenerateArguments(test.arguments)
		if check != test.check || ok != test.ok {
			t.Errorf("parseGenerateArguments(%q) = %t, %t; want %t, %t", test.arguments, check, ok, test.check, test.ok)
		}
	}
}
