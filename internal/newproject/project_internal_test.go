package newproject

import (
	"fmt"
	"strings"
	"testing"
)

func TestGeneratedSkillUsesProgressiveDisclosure(t *testing.T) {
	t.Parallel()

	const modulePath = "example.com/acme/application"
	text := fmt.Sprintf(skillTemplate, modulePath)
	if err := validateGeneratedSkill([]byte(text), modulePath); err != nil {
		t.Fatalf("validateGeneratedSkill() rejected template: %v", err)
	}

	workflowStart := strings.Index(text, "## Choose the smallest workflow")
	detailStart := strings.Index(text, "## Detailed task reference")
	moduleReference := strings.Index(text, "## Start from the module boundary")
	if workflowStart < 0 || detailStart <= workflowStart || moduleReference <= detailStart {
		t.Fatalf("generated skill section order = workflow %d, detail %d, module reference %d", workflowStart, detailStart, moduleReference)
	}
}

func TestGeneratedSkillDescribesStagedContractLifecycle(t *testing.T) {
	t.Parallel()

	text := fmt.Sprintf(skillTemplate, "example.com/acme/application")
	for _, phrase := range []string{
		"Before a contract appears in any published tag",
		"A published v0.0.1-rc.N tag and its artifacts are immutable",
		"A newer RC may revise the same",
		"revalidating every affected downstream Project",
		"Never move, delete, overwrite, or reuse a published tag",
		"After stable v0.0.1, an incompatible exact contract change requires",
	} {
		if !strings.Contains(text, phrase) {
			t.Fatalf("generated skill omits staged lifecycle guidance %q", phrase)
		}
	}
	for _, obsolete := range []string{
		"Before v0.0.1, rewrite unreleased contracts",
		"After a public release, never change a released contract in place",
	} {
		if strings.Contains(text, obsolete) {
			t.Fatalf("generated skill retains obsolete lifecycle guidance %q", obsolete)
		}
	}
}

func TestValidateSkillProgressiveDisclosureRejectsAdvancedConceptsInOrdinaryPath(t *testing.T) {
	t.Parallel()

	base := fmt.Sprintf(skillTemplate, "example.com/acme/application")
	const boundary = "## Detailed task reference"
	for _, concept := range []string{
		"Provider candidates",
		"Capability Aliases",
		"Generation Extensions",
		"fixed-point resolution",
		"template provenance",
		"wire-map allocation",
		"Protobuf projection",
		"ConnectRPC transport",
		"release evidence",
		"Kernel assembly",
	} {
		concept := concept
		t.Run(concept, func(t *testing.T) {
			t.Parallel()
			text := strings.Replace(base, boundary, concept+"\n\n"+boundary, 1)
			if err := validateSkillProgressiveDisclosure(text); err == nil {
				t.Fatalf("validateSkillProgressiveDisclosure() accepted %q before boundary", concept)
			}
		})
	}
}

func TestValidateSkillProgressiveDisclosureKeepsTemplateWorkflowConceptFree(t *testing.T) {
	t.Parallel()

	base := fmt.Sprintf(skillTemplate, "example.com/acme/application")
	const boundary = "### Change ordinary business behavior"
	text := strings.Replace(base, boundary, "Inspect each Capability Provider.\n\n"+boundary, 1)
	if err := validateSkillProgressiveDisclosure(text); err == nil {
		t.Fatal("validateSkillProgressiveDisclosure() accepted architecture concepts in the template workflow")
	}
}

func TestValidateSkillProgressiveDisclosureRequiresOrderedBoundary(t *testing.T) {
	t.Parallel()

	text := fmt.Sprintf(skillTemplate, "example.com/acme/application")
	text = strings.Replace(text, "## Detailed task reference", "## Complete reference", 1)
	if err := validateSkillProgressiveDisclosure(text); err == nil {
		t.Fatal("validateSkillProgressiveDisclosure() accepted a missing detail boundary")
	}
}

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

func TestRelativeReplacementPath(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value string
		want  bool
	}{
		{value: ".", want: true},
		{value: "..", want: true},
		{value: "./support", want: true},
		{value: "../support", want: true},
		{value: `.\support`, want: true},
		{value: `..\support`, want: true},
		{value: "/opt/support", want: false},
		{value: `C:\support`, want: false},
		{value: "example.com/acme/support", want: false},
	} {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			if got := relativeReplacementPath(test.value); got != test.want {
				t.Fatalf("relativeReplacementPath(%q) = %t, want %t", test.value, got, test.want)
			}
		})
	}
}
