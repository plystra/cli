package newproject

import (
	"strings"
	"testing"
)

func TestValidateSkillProcessGuidanceAllowsGoModuleReferences(t *testing.T) {
	t.Parallel()

	text := strings.Join([]string{
		"The current Go Module path is example.com/acme/application.",
		"Run plystra add github.com/acme/email@v1.4.2.",
		"Import github.com/plystra/kernel/invocation from generated application code.",
	}, "\n")
	if err := validateSkillProcessGuidance(text, "example.com/acme/application"); err != nil {
		t.Fatalf("validateSkillProcessGuidance() rejected Go Module references: %v", err)
	}
}

func TestValidateSkillProcessGuidanceRejectsGitWorkflow(t *testing.T) {
	t.Parallel()

	for _, guidance := range []string{
		"Initialize Git.",
		"Configure GitHub Actions.",
		"Commit the generated output.",
		"Create a feature branch.",
		"Push the change.",
		"Open a pull request.",
		"Update the repository.",
		"Track this work in GitHub.",
		"Store the project in version control.",
	} {
		guidance := guidance
		t.Run(guidance, func(t *testing.T) {
			t.Parallel()
			if err := validateSkillProcessGuidance(guidance, "example.com/acme/application"); err == nil {
				t.Fatalf("validateSkillProcessGuidance(%q) succeeded", guidance)
			}
		})
	}
}
