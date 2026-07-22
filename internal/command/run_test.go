package command_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
)

const (
	wantUsage                  = "Usage:\n  plystra help\n  plystra version\n  plystra new <project-name> [options]\n  plystra add <go-module-query>\n  plystra remove <go-module-path>\n  plystra update <go-module-query>\n  plystra use <capability-name>/vN <plugin-id> [--env <environment>|--config <yaml-path>]\n  plystra plugin create <name>\n  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n  plystra inspect [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain capability <capability-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain plugin <plugin-id> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain config <field-path> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain alias <alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain exposure <capability-or-alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra check [--env <environment>|--config <yaml-path>]\n  plystra generate [--check] [--env <environment>|--config <yaml-path>]\n\nCommon actionable failures end with one Recovery block containing the primary\ncommand or file edit to perform before retrying.\n"
	wantAddUsage               = "Usage:\n  plystra add <go-module-query>\n\nAdds one ordinary Go Module dependency, recomposes root plystra.yaml, regenerates,\ntidies, and validates the complete Project in one rollback boundary.\n"
	wantRemoveUsage            = "Usage:\n  plystra remove <go-module-path>\n\nRemoves one ordinary Go Module dependency, recomposes root plystra.yaml,\nregenerates, tidies, and validates the complete Project in one rollback boundary.\n"
	wantUpdateUsage            = "Usage:\n  plystra update <go-module-query>\n\nUpdates one selected ordinary Go Module dependency, recomposes root plystra.yaml,\nregenerates, tidies, and validates the complete Project in one rollback boundary.\n"
	wantUseUsage               = "Usage:\n  plystra use <capability-name>/vN <plugin-id> [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --env <environment>    Write the Provider choice to plystra.<environment>.yaml.\n  --config <yaml-path>   Write the Provider choice to one complete replacement configuration.\n\nPLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit\nselector is present; setting both is an error. Explicit --env or --config\noverrides both variables, and the two flags cannot be combined. Relative\nconfiguration paths are resolved from the detected Plystra Project root.\n"
	wantNewUsage               = "Usage:\n  plystra new <project-name> [--module <go-module-path>] [--template <go-module-query>] [--plugin <name>] [--git|--no-git] [--github-ci|--no-github-ci] [--skills|--no-skills]\n\nOptions:\n  --module <go-module-path> Set the Go Module path; defaults to the project name.\n  --template <module-query> Create from one public, portable Plystra Project dependency.\n  --plugin <name>           Create an initial root-level plugin.\n  --git, --no-git           Initialize or omit a Git repository.\n  --github-ci, --no-github-ci\n                            Include or omit GitHub Actions CI.\n  --skills, --no-skills     Include or omit Plystra agent skills.\n\nInteractive creation asks for each unspecified choice. Non-interactive creation\nmust specify one flag from every choice pair.\n\nTemplate dependencies must be public, portable, and generation-stable. Creation\nrejects the staged Project unless immediate generation checking, applicable\nJavaScript SDK dependency installation plus typecheck/build/package validation,\nProject checks, the read-only Go package build, and an isolated lifecycle health\nsmoke all succeed. Validation-only npm output is removed before installation.\n"
	wantGenerateUsage          = "Usage:\n  plystra generate [--check] [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --check                Report drift without modifying configuration or generated files.\n  --env <environment>    Overlay root plystra.yaml with plystra.<environment>.yaml.\n  --config <yaml-path>   Use one complete current-project configuration instead of root plystra.yaml.\n\nPLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit\nselector is present; setting both is an error. Explicit --env or --config\noverrides both variables, and the two flags cannot be combined. Relative\nconfiguration paths are resolved from the detected Plystra Project root. Root\nplystra.yaml remains mandatory and is not merged beneath --config.\n"
	wantCheckUsage             = "Usage:\n  plystra check [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --env <environment>    Check root plystra.yaml with plystra.<environment>.yaml.\n  --config <yaml-path>   Check one complete current-project configuration instead of root plystra.yaml.\n\nThe check is read-only: it verifies dependency composition and generated output,\nthen runs go test -mod=readonly ./... when both are current. PLYSTRA_ENV and\nPLYSTRA_CONFIG supply equivalent selectors when no explicit selector is present;\nsetting both is an error. Explicit --env or --config overrides both variables,\nand the two flags cannot be combined. Relative configuration paths are resolved\nfrom the detected Plystra Project root. Root plystra.yaml remains mandatory and\nis not merged beneath --config.\n"
	wantInspectUsage           = "Usage:\n  plystra inspect [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --verbose              Add the complete deterministic resolution evidence to human output.\n  --format human|json    Select concise human output or the plystra.inspect v1 JSON schema.\n  --env <environment>    Inspect root plystra.yaml with plystra.<environment>.yaml.\n  --config <yaml-path>   Inspect one complete current-project configuration instead of root plystra.yaml.\n\nThe command is read-only and resolves the same selected application model used\nby generation and validation. JSON stdout contains exactly one schema document;\nprogress and diagnostics use stderr. PLYSTRA_ENV and PLYSTRA_CONFIG supply\nequivalent selectors when no explicit selector is present; setting both is an\nerror. Explicit --env or --config overrides both variables, and the two flags\ncannot be combined. Relative configuration paths are resolved from the detected\nPlystra Project root. Root plystra.yaml remains mandatory and is not merged\nbeneath --config.\n"
	wantExplainCapabilityUsage = "Usage:\n  plystra explain capability <capability-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain plugin <plugin-id> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain config <field-path> [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain alias <alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n  plystra explain exposure <capability-or-alias-name>/vN [--verbose] [--format human|json] [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --verbose              Add the complete deterministic resolution evidence to human output.\n  --format human|json    Select concise human output or the plystra.explain v1 JSON schema.\n  --env <environment>    Explain the model selected by plystra.<environment>.yaml.\n  --config <yaml-path>   Explain one complete current-project configuration instead of root plystra.yaml.\n\nThe command is read-only and explains one canonical Capability, Plugin, typed\nconfiguration-field, application-local Alias, or public-exposure decision from\nthe same selected application model used by generation and validation. Plugin\nconfiguration fields accept the dotted form config.<plugin-id>.<field>. JSON\nstdout contains exactly one schema document; progress and diagnostics use\nstderr. PLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no\nexplicit selector is present; setting both is an error. Explicit --env or\n--config overrides both variables,\nand the two flags cannot be combined. Relative configuration paths are resolved\nfrom the detected Plystra Project root. Root plystra.yaml remains mandatory and\nis not merged beneath --config.\n"
	wantCapabilityExposeUsage  = "Usage:\n  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n\nOptions:\n  --env <environment>    Write exposure to plystra.<environment>.yaml.\n  --config <yaml-path>   Write exposure to one complete replacement configuration.\n\nThe current Connect unary boundary accepts a canonical Capability whose\nexplicit semantics.kind is query or command. Exposing an event or stream fails\nthe transaction; remove it from http.expose until that operation kind is\nsupported rather than relabeling the contract.\n\nPLYSTRA_ENV and PLYSTRA_CONFIG supply equivalent selectors when no explicit\nselector is present; setting both is an error. Explicit --env or --config\noverrides both variables, and the two flags cannot be combined. Relative\nconfiguration paths are resolved from the detected Plystra Project root.\n"
	wantCapabilityCreateUsage  = "Usage:\n  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]\n\nIntent profiles:\n  --query   Create a read-only, safely retryable query contract for a new Capability identity.\n\nA new Capability identity requires one explicit intent profile. A later version\ncopies the complete semantics of its highest visible source contract; omit the\nprofile flag in that case. Names never imply semantics.\n"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{nil, {"help"}, {"-h"}, {"--help"}} {
		arguments := arguments
		t.Run(commandName(arguments), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(arguments, &stdout, &stderr); exitCode != 0 {
				t.Fatalf("Run(%q) exit code = %d, want 0", arguments, exitCode)
			}
			if stdout.String() != wantUsage {
				t.Fatalf("Run(%q) stdout = %q, want %q", arguments, stdout.String(), wantUsage)
			}
			if stderr.Len() != 0 {
				t.Fatalf("Run(%q) stderr = %q, want empty", arguments, stderr.String())
			}
		})
	}
}

func TestRunGenerateHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"generate", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantGenerateUsage || stderr.Len() != 0 {
			t.Fatalf("Run(generate %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunCheckHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"check", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantCheckUsage || stderr.Len() != 0 {
			t.Fatalf("Run(check %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunInspectHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"inspect", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantInspectUsage || stderr.Len() != 0 {
			t.Fatalf("Run(inspect %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunExplainHelp(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"explain", "help"},
		{"explain", "-h"},
		{"explain", "--help"},
		{"explain", "capability", "help"},
		{"explain", "capability", "-h"},
		{"explain", "capability", "--help"},
		{"explain", "plugin", "help"},
		{"explain", "plugin", "-h"},
		{"explain", "plugin", "--help"},
		{"explain", "config", "help"},
		{"explain", "config", "-h"},
		{"explain", "config", "--help"},
		{"explain", "alias", "help"},
		{"explain", "alias", "-h"},
		{"explain", "alias", "--help"},
		{"explain", "exposure", "help"},
		{"explain", "exposure", "-h"},
		{"explain", "exposure", "--help"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run(arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != wantExplainCapabilityUsage || stderr.Len() != 0 {
			t.Fatalf("Run(%q) = exit %d, stdout %q, stderr %q", arguments, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunNewHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"new", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantNewUsage || stderr.Len() != 0 {
			t.Fatalf("Run(new %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunAddHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"add", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantAddUsage || stderr.Len() != 0 {
			t.Fatalf("Run(add %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunRemoveHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"remove", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantRemoveUsage || stderr.Len() != 0 {
			t.Fatalf("Run(remove %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunUpdateHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"update", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantUpdateUsage || stderr.Len() != 0 {
			t.Fatalf("Run(update %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunUseHelp(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"help", "-h", "--help"} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		if exitCode := command.Run([]string{"use", argument}, &stdout, &stderr); exitCode != 0 || stdout.String() != wantUseUsage || stderr.Len() != 0 {
			t.Fatalf("Run(use %s) = exit %d, stdout %q, stderr %q", argument, exitCode, stdout.String(), stderr.String())
		}
	}
}

func TestRunVersion(t *testing.T) {
	t.Parallel()

	for _, argument := range []string{"version", "-version", "--version"} {
		argument := argument
		t.Run(argument, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run([]string{argument}, &stdout, &stderr); exitCode != 0 {
				t.Fatalf("Run(%q) exit code = %d, want 0", argument, exitCode)
			}
			if stdout.String() != "plystra 0.1.0\n" {
				t.Fatalf("Run(%q) stdout = %q", argument, stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("Run(%q) stderr = %q, want empty", argument, stderr.String())
			}
		})
	}
}

func TestRunCapabilityHelp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"capability", "help"}, want: "Usage:\n  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"},
		{arguments: []string{"capability", "create", "--help"}, want: wantCapabilityCreateUsage},
		{arguments: []string{"capability", "implement", "-h"}, want: "Usage:\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n"},
		{arguments: []string{"capability", "expose", "help"}, want: wantCapabilityExposeUsage},
	}
	for _, test := range tests {
		test := test
		t.Run(strings.Join(test.arguments, "-"), func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(test.arguments, &stdout, &stderr); exitCode != 0 || stdout.String() != test.want || stderr.Len() != 0 {
				t.Fatalf("Run(%q) = exit %d, stdout %q, stderr %q", test.arguments, exitCode, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunRejectsUnknownCommandAndExtraArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		wantError string
	}{
		{name: "unknown", arguments: []string{"unknown"}, wantError: "unknown command \"unknown\"\n\n" + wantUsage},
		{name: "help arguments", arguments: []string{"help", "extra"}, wantError: "help does not accept arguments\n"},
		{name: "version arguments", arguments: []string{"version", "extra"}, wantError: "version does not accept arguments\n"},
		{name: "new missing project", arguments: []string{"new"}, wantError: wantNewUsage},
		{name: "new unknown option", arguments: []string{"new", "app", "--unknown"}, wantError: wantNewUsage},
		{name: "new missing module path", arguments: []string{"new", "app", "--module"}, wantError: wantNewUsage},
		{name: "new duplicate module path", arguments: []string{"new", "app", "--module", "example.com/a", "--module", "example.com/b"}, wantError: wantNewUsage},
		{name: "new missing template query", arguments: []string{"new", "app", "--template"}, wantError: wantNewUsage},
		{name: "new duplicate template query", arguments: []string{"new", "app", "--template", "example.com/a", "--template", "example.com/b"}, wantError: wantNewUsage},
		{name: "new missing plugin name", arguments: []string{"new", "app", "--plugin"}, wantError: wantNewUsage},
		{name: "new removed library option", arguments: []string{"new", "app", "--library"}, wantError: wantNewUsage},
		{name: "new extra argument", arguments: []string{"new", "app", "extra"}, wantError: wantNewUsage},
		{name: "new conflicting choice", arguments: []string{"new", "app", "--git", "--no-git"}, wantError: wantNewUsage},
		{name: "add missing query", arguments: []string{"add"}, wantError: wantAddUsage},
		{name: "add option", arguments: []string{"add", "--upgrade"}, wantError: wantAddUsage},
		{name: "add extra argument", arguments: []string{"add", "example.com/platform", "extra"}, wantError: wantAddUsage},
		{name: "remove missing path", arguments: []string{"remove"}, wantError: wantRemoveUsage},
		{name: "remove option", arguments: []string{"remove", "--all"}, wantError: wantRemoveUsage},
		{name: "remove extra argument", arguments: []string{"remove", "example.com/platform", "extra"}, wantError: wantRemoveUsage},
		{name: "update missing query", arguments: []string{"update"}, wantError: wantUpdateUsage},
		{name: "update option", arguments: []string{"update", "--all"}, wantError: wantUpdateUsage},
		{name: "update extra argument", arguments: []string{"update", "example.com/platform", "extra"}, wantError: wantUpdateUsage},
		{name: "use missing arguments", arguments: []string{"use"}, wantError: wantUseUsage},
		{name: "use missing Plugin", arguments: []string{"use", "email.send/v1"}, wantError: wantUseUsage},
		{name: "use selector conflict", arguments: []string{"use", "email.send/v1", "acme.email", "--env", "test", "--config", "deploy.yaml"}, wantError: wantUseUsage},
		{name: "plugin missing subcommand", arguments: []string{"plugin"}, wantError: "usage: plystra plugin create <name>\n"},
		{name: "plugin unknown subcommand", arguments: []string{"plugin", "remove", "account"}, wantError: "usage: plystra plugin create <name>\n"},
		{name: "plugin missing name", arguments: []string{"plugin", "create"}, wantError: "usage: plystra plugin create <name>\n"},
		{name: "plugin extra argument", arguments: []string{"plugin", "create", "account", "extra"}, wantError: "usage: plystra plugin create <name>\n"},
		{name: "capability missing subcommand", arguments: []string{"capability"}, wantError: "usage:\n  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"},
		{name: "capability unknown subcommand", arguments: []string{"capability", "remove", "records.create/v1"}, wantError: "usage:\n  plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]\n  plystra capability implement <capability-name>/vN [--plugin <plugin>]\n  plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"},
		{name: "capability create missing reference", arguments: []string{"capability", "create"}, wantError: "usage: plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]\n"},
		{name: "capability implement missing reference", arguments: []string{"capability", "implement"}, wantError: "usage: plystra capability implement <capability-name>/vN [--plugin <plugin>]\n"},
		{name: "capability expose missing reference", arguments: []string{"capability", "expose"}, wantError: "usage: plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"},
		{name: "capability implement confirm", arguments: []string{"capability", "implement", "records.create/v1", "--confirm"}, wantError: "usage: plystra capability implement <capability-name>/vN [--plugin <plugin>]\n"},
		{name: "capability implement expose", arguments: []string{"capability", "implement", "records.create/v1", "--expose"}, wantError: "usage: plystra capability implement <capability-name>/vN [--plugin <plugin>]\n"},
		{name: "capability expose extra option", arguments: []string{"capability", "expose", "records.create/v1", "--confirm"}, wantError: "usage: plystra capability expose <capability-name>/vN [--env <environment>|--config <yaml-path>]\n"},
		{name: "capability create missing plugin", arguments: []string{"capability", "create", "records.create", "--plugin"}, wantError: "usage: plystra capability create <capability-name> [--query] [--plugin <plugin>] [--confirm] [--expose]\n"},
		{name: "inspect unknown option", arguments: []string{"inspect", "--graph"}, wantError: wantInspectUsage},
		{name: "inspect duplicate verbose", arguments: []string{"inspect", "--verbose", "--verbose"}, wantError: wantInspectUsage},
		{name: "inspect missing format", arguments: []string{"inspect", "--format"}, wantError: wantInspectUsage},
		{name: "inspect unknown format", arguments: []string{"inspect", "--format", "yaml"}, wantError: wantInspectUsage},
		{name: "inspect duplicate format", arguments: []string{"inspect", "--format", "human", "--format", "json"}, wantError: wantInspectUsage},
		{name: "inspect missing configuration path", arguments: []string{"inspect", "--config"}, wantError: wantInspectUsage},
		{name: "inspect duplicate configuration", arguments: []string{"inspect", "--config", "a.yaml", "--config", "b.yaml"}, wantError: wantInspectUsage},
		{name: "inspect missing environment", arguments: []string{"inspect", "--env"}, wantError: wantInspectUsage},
		{name: "inspect duplicate environment", arguments: []string{"inspect", "--env", "test", "--env", "production"}, wantError: wantInspectUsage},
		{name: "inspect selector conflict", arguments: []string{"inspect", "--env", "test", "--config", "deploy.yaml"}, wantError: wantInspectUsage},
		{name: "explain missing subject", arguments: []string{"explain", "capability"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing Plugin subject", arguments: []string{"explain", "plugin"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing Alias subject", arguments: []string{"explain", "alias"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing exposure subject", arguments: []string{"explain", "exposure"}, wantError: wantExplainCapabilityUsage},
		{name: "explain unsupported subject kind", arguments: []string{"explain", "configuration", "config.acme.email.host"}, wantError: wantExplainCapabilityUsage},
		{name: "explain unknown option", arguments: []string{"explain", "capability", "email.send/v1", "--graph"}, wantError: wantExplainCapabilityUsage},
		{name: "explain duplicate verbose", arguments: []string{"explain", "capability", "email.send/v1", "--verbose", "--verbose"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing format", arguments: []string{"explain", "capability", "email.send/v1", "--format"}, wantError: wantExplainCapabilityUsage},
		{name: "explain unknown format", arguments: []string{"explain", "capability", "email.send/v1", "--format", "yaml"}, wantError: wantExplainCapabilityUsage},
		{name: "explain duplicate format", arguments: []string{"explain", "capability", "email.send/v1", "--format", "human", "--format", "json"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing configuration", arguments: []string{"explain", "capability", "email.send/v1", "--config"}, wantError: wantExplainCapabilityUsage},
		{name: "explain duplicate configuration", arguments: []string{"explain", "capability", "email.send/v1", "--config", "a.yaml", "--config", "b.yaml"}, wantError: wantExplainCapabilityUsage},
		{name: "explain missing environment", arguments: []string{"explain", "capability", "email.send/v1", "--env"}, wantError: wantExplainCapabilityUsage},
		{name: "explain duplicate environment", arguments: []string{"explain", "capability", "email.send/v1", "--env", "test", "--env", "production"}, wantError: wantExplainCapabilityUsage},
		{name: "explain selector conflict", arguments: []string{"explain", "capability", "email.send/v1", "--env", "test", "--config", "deploy.yaml"}, wantError: wantExplainCapabilityUsage},
		{name: "generate unknown option", arguments: []string{"generate", "--write"}, wantError: wantGenerateUsage},
		{name: "generate duplicate check", arguments: []string{"generate", "--check", "--check"}, wantError: wantGenerateUsage},
		{name: "generate missing configuration path", arguments: []string{"generate", "--config"}, wantError: wantGenerateUsage},
		{name: "generate duplicate configuration", arguments: []string{"generate", "--config", "a.yaml", "--config", "b.yaml"}, wantError: wantGenerateUsage},
		{name: "generate missing environment", arguments: []string{"generate", "--env"}, wantError: wantGenerateUsage},
		{name: "generate duplicate environment", arguments: []string{"generate", "--env", "test", "--env", "production"}, wantError: wantGenerateUsage},
		{name: "generate selector conflict", arguments: []string{"generate", "--env", "test", "--config", "deploy.yaml"}, wantError: wantGenerateUsage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if exitCode := command.Run(test.arguments, &stdout, &stderr); exitCode != 2 {
				t.Fatalf("Run(%q) exit code = %d, want 2", test.arguments, exitCode)
			}
			if stdout.Len() != 0 {
				t.Fatalf("Run(%q) stdout = %q, want empty", test.arguments, stdout.String())
			}
			if stderr.String() != test.wantError {
				t.Fatalf("Run(%q) stderr = %q, want %q", test.arguments, stderr.String(), test.wantError)
			}
		})
	}
}

func TestRunRejectsMissingWriters(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	if exitCode := command.Run(nil, nil, &output); exitCode != 2 {
		t.Fatalf("Run with nil stdout exit code = %d, want 2", exitCode)
	}
	if exitCode := command.Run(nil, &output, nil); exitCode != 2 {
		t.Fatalf("Run with nil stderr exit code = %d, want 2", exitCode)
	}
}

func commandName(arguments []string) string {
	if len(arguments) == 0 {
		return "empty"
	}
	return arguments[0]
}
