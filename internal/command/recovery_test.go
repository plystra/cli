package command

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/aliasresolution"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/capabilitycreate"
	"github.com/plystra/cli/internal/capabilityexpose"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/capabilityversion"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/dependencyadd"
	"github.com/plystra/cli/internal/dependencyremove"
	"github.com/plystra/cli/internal/dependencyupdate"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/generationactivation"
	"github.com/plystra/cli/internal/generationexec"
	"github.com/plystra/cli/internal/generationresolution"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationcreate"
	"github.com/plystra/cli/internal/implementationdecl"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/implementationselect"
	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacecreate"
	"github.com/plystra/cli/internal/interfacedecl"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/moduleargument"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/newproject"
	"github.com/plystra/cli/internal/plugincreate"
	"github.com/plystra/cli/internal/pluginindex"
	"github.com/plystra/cli/internal/pluginmeta"
	"github.com/plystra/cli/internal/plugintarget"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/providerresolution"
)

func TestWriteCommandFailureAddsOnePrimaryRecoveryForCommonTypedFailures(t *testing.T) {
	t.Parallel()

	missing, ambiguous, invalidChoice, mismatch, contractConflict := recoveryProviderFailures(t)
	tests := []struct {
		name    string
		err     error
		context recoveryContext
		want    string
		code    string
	}{
		{
			name: "invalid template with nested Provider ambiguity",
			err: fmt.Errorf(
				"%w: template cannot qualify: %w; correction: publish a corrected template version",
				newproject.ErrInvalidTemplate,
				ambiguous,
			),
			want: "publish a corrected template version",
			code: diagnosticTemplateInvalid,
		},
		{
			name: "missing Provider",
			err:  missing,
			want: "Add an intended dependency with `plystra add <go-module-query>` whose Plugin provides email.send/v1.",
			code: diagnosticProviderMissing,
		},
		{
			name:    "ambiguous Provider",
			err:     ambiguous,
			context: commandRecoveryContext("", "production", nil),
			want:    "Select one compatible Provider explicitly by running `plystra use email.send/v1 <plugin-id> --env \"production\"`.",
			code:    diagnosticProviderAmbiguous,
		},
		{
			name:    "invalid Provider choice",
			err:     invalidChoice,
			context: commandRecoveryContext("deploy/customer.yaml", "", nil),
			want:    "Replace the invalid Provider choice with one visible compatible Plugin by running `plystra use email.send/v1 <plugin-id> --config \"deploy/customer.yaml\"`.",
			code:    diagnosticProviderSelectionInvalid,
		},
		{
			name: "Provider contract mismatch",
			err:  mismatch,
			want: "Make every Provider of email.send/v1 carry one identical provider-independent capability.yaml.",
			code: diagnosticProviderContractMismatch,
		},
		{
			name: "Provider contract conflict",
			err:  contractConflict,
			want: "Make every Provider of email.send/v1 carry one identical provider-independent capability.yaml.",
			code: diagnosticProviderContractConflict,
		},
		{
			name:    "inherited configuration conflict",
			err:     fmt.Errorf("compose dependencies: %w", applicationmeta.ErrInheritedConflict),
			context: commandRecoveryContext("", "test", nil),
			want:    "Set or remove the conflicting field explicitly in plystra.test.yaml, then rerun the command.",
			code:    diagnosticConfigurationInheritedConflict,
		},
		{
			name:    "constructor configuration schema",
			err:     fmt.Errorf("compose configuration: %w", applicationmeta.ErrConfigurationSchema),
			context: commandRecoveryContext("", "test", nil),
			want:    "Correct the reported owning Project document by using the fully qualified symbol of a discovered constructor with a compiled Go Config schema, or remove that constructor configuration entry, then rerun the command.",
			code:    diagnosticConstructorConfigurationSchemaInvalid,
		},
		{
			name:    "constructor configuration value",
			err:     fmt.Errorf("parse configuration: %w", applicationmeta.ErrConfigurationValues),
			context: commandRecoveryContext("deploy/customer.yaml", "", nil),
			want:    "Correct the reported constructor configuration field in the owning Project document to match its compiled Go Config field type, then rerun the command.",
			code:    diagnosticConstructorConfigurationValuesInvalid,
		},
		{
			name:    "unselected constructor configuration",
			err:     fmt.Errorf("resolve configuration owner: %w", applicationresolve.ErrUnownedConstructorConfiguration),
			context: commandRecoveryContext("", "test", nil),
			want:    "Name the reported constructor in an effective interfaces.use entry, make it reachable through an Interface requirement, or remove its configuration from plystra.test.yaml, then rerun the command.",
			code:    diagnosticConstructorConfigurationUnselected,
		},
		{
			name: "configuration selection",
			err:  fmt.Errorf("resolve: %w", applicationresolve.ErrConfigurationSelection),
			want: "Select exactly one existing Project configuration with `--env <environment>` or `--config <yaml-path>`, then rerun the command.",
			code: diagnosticConfigurationSelectionInvalid,
		},
		{
			name:    "application runtime dependency",
			err:     fmt.Errorf("%w: go.mod is stale; run plystra generate to repair module metadata transactionally", applicationgenerate.ErrRuntimeDependency),
			context: commandRecoveryContext("", "production", nil),
			want:    "Run `plystra generate --env \"production\"` to repair the required direct application runtime dependencies.",
			code:    diagnosticApplicationDependencyDrift,
		},
		{
			name: "invalid selected configuration",
			err: errors.Join(
				applicationresolve.ErrConfigurationSelection,
				applicationresolve.ErrManifest,
				applicationmeta.ErrInvalidManifest,
			),
			context: commandRecoveryContext("", "production", nil),
			want:    "Edit plystra.production.yaml so every value matches a selected Plugin's closed typed schema, then rerun the command.",
			code:    diagnosticConfigurationInvalid,
		},
		{
			name: "invalid dependency Project manifest",
			err:  errors.Join(applicationresolve.ErrManifest, applicationmeta.ErrInvalidManifest),
			want: "Correct the reported root or dependency Project plystra.yaml, then rerun the command.",
			code: diagnosticProjectManifestInvalid,
		},
		{
			name: "Plugin targeting",
			err:  fmt.Errorf("author Capability: %w", plugintarget.ErrAmbiguous),
			want: "Rerun with `--plugin <plugin-directory-or-id>` to select one exact local Plugin.",
			code: diagnosticPluginTargetAmbiguous,
		},
		{
			name: "generation helper failure",
			err:  fmt.Errorf("run extension: %w", generationexec.ErrCompile),
			want: "Fix the selected generation package reported above, then rerun the command.",
			code: diagnosticGenerationCompileFailed,
		},
		{
			name:    "generated ownership",
			err:     fmt.Errorf("install: %w", generatedfiles.ErrUnexpected),
			context: commandRecoveryContext("", "staging", nil),
			want:    "Move the reported unowned path outside generated/, then run `plystra generate --env \"staging\"`.",
			code:    diagnosticGeneratedUnexpectedOutput,
		},
		{
			name:    "Protobuf history",
			err:     fmt.Errorf("allocate fields: %w", protobufwiremap.ErrHistory),
			context: commandRecoveryContext("", "production", nil),
			want:    "Restore generated/proto/wire-map.json from its last known-good generated state, then run `plystra generate --env \"production\"`.",
			code:    diagnosticProtobufWireHistoryInvalid,
		},
		{
			name:    "Protobuf identity collision",
			err:     fmt.Errorf("project names: %w", protobufidentity.ErrCollision),
			context: commandRecoveryContext("", "", []string{"PLYSTRA_CONFIG=deploy/customer.yaml"}),
			want:    "Rename one conflicting authored field or enum member in the owning Interface contract, then run `plystra generate --config \"deploy/customer.yaml\"`.",
			code:    diagnosticProtobufIdentityCollision,
		},
		{
			name:    "Protobuf operation kind",
			err:     fmt.Errorf("project operation: %w", protobufmodel.ErrOperationKind),
			context: commandRecoveryContext("", "", []string{"PLYSTRA_CONFIG=deploy/customer.yaml"}),
			want:    "Remove the unsupported Capability from http.expose in deploy/customer.yaml, then run `plystra generate --config \"deploy/customer.yaml\"`.",
			code:    diagnosticProtobufOperationKindUnsupported,
		},
		{
			name: "Capability create confirmation",
			err:  errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrConfirmationRequired),
			want: "Review the visible Capability versions, then rerun the same `plystra capability create` command with `--confirm`.",
			code: diagnosticCapabilityCreateConfirmationRequired,
		},
		{
			name: "Capability create version exhaustion",
			err:  errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrVersionExhausted, capabilityversion.ErrOverflow),
			want: "Rerun `plystra capability create <new-capability-name> --query [--plugin <plugin>] [--expose]` with a new canonical Capability identity; the existing identity has no higher major version.",
			code: diagnosticCapabilityCreateVersionExhausted,
		},
		{
			name: "Capability create exact version",
			err:  errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrActionMismatch, capabilitycreate.ErrCreateAlreadyVisible),
			want: "Rerun `plystra capability implement <capability-name>/vN [--plugin <plugin>]` for the existing exact contract.",
			code: diagnosticCapabilityCreateAlreadyVisible,
		},
		{
			name: "Capability implement missing version",
			err:  errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrActionMismatch, capabilitycreate.ErrImplementNotVisible),
			want: "Rerun `plystra capability create <capability-name>/vN [--query] [--plugin <plugin>] [--confirm] [--expose]` to author the missing exact contract.",
			code: diagnosticCapabilityImplementNotVisible,
		},
		{
			name:    "Capability expose missing target",
			err:     errors.Join(capabilityexpose.ErrExpose, capabilityexpose.ErrNotVisible, interfaceresolution.ErrUnknownInterface),
			context: commandRecoveryContext("", "production", nil),
			want:    "Rerun `plystra capability expose <capability-name>/vN --env \"production\"` with one exact Capability visible in the selected Go Module graph.",
			code:    diagnosticCapabilityExposeNotVisible,
		},
		{
			name: "Capability create missing intent profile",
			err:  errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrIntentProfile, capabilitycreate.ErrIntentProfileRequired),
			want: "Rerun `plystra capability create <capability-name> --query [--plugin <plugin>] [--confirm] [--expose]` with the explicit query intent profile required for a new Capability identity.",
			code: diagnosticCapabilityCreateIntentProfileRequired,
		},
		{
			name: "Capability create inapplicable intent profile",
			err:  errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrIntentProfile, capabilitycreate.ErrIntentProfileNotAllowed),
			want: "Rerun `plystra capability create <capability-name> [--plugin <plugin>] [--confirm] [--expose]` without `--query`; a later version copies the highest visible contract's semantics.",
			code: diagnosticCapabilityCreateIntentProfileNotAllowed,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output strings.Builder
			writeCommandFailure(&output, "command failed", test.err, test.context)
			got := output.String()
			if !strings.Contains(got, "command failed: ") || !strings.Contains(got, "\n\nRecovery:\n"+test.want+"\n\nDiagnostic: "+test.code+"\n") {
				t.Fatalf("writeCommandFailure() = %q, want recovery %q and code %q", got, test.want, test.code)
			}
			if count := strings.Count(got, "Recovery:"); count != 1 {
				t.Fatalf("Recovery count = %d in %q", count, got)
			}
			if count := strings.Count(got, "Diagnostic:"); count != 1 {
				t.Fatalf("Diagnostic count = %d in %q", count, got)
			}
			if strings.Contains(got, "correction:") {
				t.Fatalf("embedded recovery was not removed: %q", got)
			}
		})
	}
}

func TestWriteCommandFailureReportsProviderContractMismatchSources(t *testing.T) {
	t.Parallel()

	_, _, _, mismatch, _ := recoveryProviderFailures(t)
	var output strings.Builder
	writeCommandFailure(&output, "generate", fmt.Errorf("resolve application: %w", mismatch), recoveryContext{})
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/project:plystra.yaml:1:1 (declaration)\n" +
		"Source: example.com/provider:local/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)\n\n" +
		"Recovery:\nMake every Provider of email.send/v1 carry one identical provider-independent capability.yaml.\n\n" +
		"Diagnostic: " + diagnosticProviderContractMismatch + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 2 {
		t.Fatalf("Provider contract mismatch output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureReportsInheritedConfigurationConflictSources(t *testing.T) {
	t.Parallel()

	parseManifest := func(source string) applicationmeta.Manifest {
		t.Helper()
		manifest, err := applicationmeta.Parse([]byte(source))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		return manifest
	}
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/c", ModuleVersion: "v1.2.0", Manifest: parseManifest("interfaces: {use: {email.send/v1: example.com/secondary.New}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.1.0", Manifest: parseManifest("interfaces: {use: {email.send/v1: example.com/primary.New}}\n")},
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: parseManifest("interfaces: {use: {email.send/v1: example.com/primary.New}}\n")},
	}
	_, conflict := applicationmeta.Compose(dependencies, parseManifest("{}\n"), func(constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	})
	if !errors.Is(conflict, applicationmeta.ErrInheritedConflict) {
		t.Fatalf("Compose error = %v, want ErrInheritedConflict", conflict)
	}

	var output strings.Builder
	writeCommandFailure(&output, "check Plystra Project", fmt.Errorf("resolve application: %w", conflict), recoveryContext{})
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/a:plystra.yaml:1:1 (configuration-declaration)\n" +
		"Source: example.com/b:plystra.yaml:1:1 (configuration-declaration)\n" +
		"Source: example.com/c:plystra.yaml:1:1 (configuration-declaration)\n\n" +
		"Recovery:\nSet or remove the conflicting field explicitly in plystra.yaml, then rerun the command.\n\n" +
		"Diagnostic: " + diagnosticConfigurationInheritedConflict + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 3 {
		t.Fatalf("inherited configuration conflict output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureDoesNotInventUnavailableSources(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
		code string
	}{
		{name: "explicit target not found", err: fmt.Errorf("author Capability: %w", plugintarget.ErrNotFound), code: diagnosticPluginTargetNotFound},
		{name: "interactive selection failed", err: fmt.Errorf("author Capability: %w", plugintarget.ErrSelection), code: diagnosticPluginTargetInvalid},
		{name: "activation conflict without typed candidates", err: fmt.Errorf("resolve generation: %w", generationactivation.ErrAssociationConflict), code: diagnosticGenerationActivationConflict},
		{name: "missing activation without requirement provenance", err: fmt.Errorf("resolve generation: %w", generationactivation.ErrMissingAssociation), code: diagnosticGenerationActivationMissing},
		{name: "selected Provider extension without typed selection", err: fmt.Errorf("resolve generation: %w", generationactivation.ErrSelectedProviderExtension), code: diagnosticGenerationProviderExtensionMissing},
		{name: "activation cycle without typed edges", err: fmt.Errorf("resolve generation: %w", generationresolution.ErrActivationCycle), code: diagnosticGenerationActivationCycle},
		{name: "dependency cycle without typed edges", err: fmt.Errorf("resolve generation: %w", generationresolution.ErrDependencyCycle), code: diagnosticGenerationDependencyCycle},
		{name: "contribution cycle without typed edges", err: fmt.Errorf("resolve generation: %w", generationresolution.ErrContributionCycle), code: diagnosticGenerationContributionCycle},
		{name: "unordered contributions without typed entries", err: fmt.Errorf("resolve generation: %w", generationresolution.ErrUnorderedContributions), code: diagnosticGenerationContributionsUnordered},
		{name: "repeated state without typed extensions", err: fmt.Errorf("resolve generation: %w", generationresolution.ErrRepeatedState), code: diagnosticGenerationStateRepeated},
		{name: "nonconvergent generation without typed rules", err: fmt.Errorf("resolve generation: %w", generationresolution.ErrExtensionConvergence), code: diagnosticGenerationNonconvergent},
		{name: "unsupported helper API without typed declaration", err: fmt.Errorf("resolve generation: %w", generationexec.ErrUnsupportedAPI), code: diagnosticGenerationAPIUnsupported},
		{name: "unsupported manifest API without typed declaration", err: fmt.Errorf("resolve generation: %w", pluginmeta.ErrUnsupportedGenerationAPI), code: diagnosticGenerationAPIUnsupported},
		{name: "invalid generation package without typed declaration", err: fmt.Errorf("resolve generation: %w", pluginindex.ErrInvalidGenerationPackage), code: diagnosticGenerationPackageInvalid},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output strings.Builder
			writeCommandFailure(&output, "", test.err, recoveryContext{})
			got := output.String()
			if strings.Contains(got, "Source: ") || !strings.Contains(got, "Diagnostic: "+test.code+"\n") {
				t.Fatalf("source-less failure output = %q", got)
			}
		})
	}
}

func TestWriteCommandFailureCanonicalizesNonconvergentGenerationSources(t *testing.T) {
	t.Parallel()

	authz := providerresolution.RequirementSource{
		Kind:             providerresolution.RequirementGenerationRule,
		Reference:        "generation authz rule",
		ModulePath:       "example.com/z-security",
		Path:             "shared/plugin.yaml",
		Line:             1,
		Column:           1,
		PluginID:         "example.authz",
		Namespace:        "authz",
		SourceCapability: "order.create/v1",
		RuleID:           "authz.require-policy",
	}
	authn := providerresolution.RequirementSource{
		Kind:             providerresolution.RequirementGenerationRule,
		Reference:        "generation authn rule",
		ModulePath:       "example.com/a-security",
		Path:             "authn/plugin.yaml",
		Line:             1,
		Column:           1,
		PluginID:         "example.authn",
		Namespace:        "authn",
		SourceCapability: "order.create/v1",
		RuleID:           "authn.require-session",
	}
	failure := &recoveryRequirementSourceError{
		cause:   generationresolution.ErrExtensionConvergence,
		sources: []providerresolution.RequirementSource{authz, authn, authz},
	}

	var output strings.Builder
	writeCommandFailure(&output, "generate", fmt.Errorf("resolve application: %w", failure), recoveryContext{})
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/a-security:authn/plugin.yaml:1:1 (generation-rule)\n" +
		"Source: example.com/z-security:shared/plugin.yaml:1:1 (generation-rule)\n\n" +
		"Recovery:\nMake the selected generation extensions deterministic and convergent for identical normalized input.\n\n" +
		"Diagnostic: " + diagnosticGenerationNonconvergent + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 2 {
		t.Fatalf("nonconvergent generation output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureCanonicalizesConflictingActivationSources(t *testing.T) {
	t.Parallel()

	declaration := func(pluginID, capability string) generationactivation.Declaration {
		t.Helper()
		manifest, err := pluginmeta.Parse([]byte("id: " + pluginID + "\nprovides: [" + capability + "]\ngeneration:\n  api: v1\n  package: ./generation\n  activations:\n    - namespace: authn\n      capability: " + capability + "\n"))
		if err != nil {
			t.Fatalf("Parse(%s): %v", pluginID, err)
		}
		generation, exists := manifest.Generation()
		if !exists {
			t.Fatalf("Parse(%s) returned no generation", pluginID)
		}
		return generationactivation.Declaration{
			PluginID:   pluginID,
			Source:     pluginID + " at shared/plugin.yaml",
			ModulePath: "example.com/project",
			SourcePath: "shared/plugin.yaml",
			Generation: generation,
		}
	}
	_, conflict := generationactivation.New([]generationactivation.Declaration{
		declaration("example.authn-password", "authn.session.verify/v1"),
		declaration("example.authn-legacy", "authn.token.verify/v1"),
	})
	if !errors.Is(conflict, generationactivation.ErrAssociationConflict) {
		t.Fatalf("New error = %v, want activation conflict", conflict)
	}

	var output strings.Builder
	writeCommandFailure(&output, "generate", fmt.Errorf("resolve application: %w", conflict), recoveryContext{})
	got := output.String()
	wantSource := "Source: example.com/project:shared/plugin.yaml:7:7 (plugin-declaration)\n"
	if strings.Count(got, "Source: ") != 1 || !strings.Contains(got, wantSource) || !strings.Contains(got, "Diagnostic: "+diagnosticGenerationActivationConflict+"\n") {
		t.Fatalf("activation conflict output = %q", got)
	}
}

func TestWriteCommandFailureReportsAmbiguousConfigurationOwnershipSources(t *testing.T) {
	t.Parallel()

	parseManifest := func(source string) applicationmeta.Manifest {
		t.Helper()
		manifest, err := applicationmeta.Parse([]byte(source))
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		return manifest
	}
	dependencies := []applicationmeta.Dependency{
		{ModulePath: "example.com/c", ModuleVersion: "v1.2.0", Manifest: parseManifest("interfaces: {use: {email.send/v1: example.com/primary.New}}\n")},
		{ModulePath: "example.com/b", ModuleVersion: "v1.1.0", Manifest: parseManifest("interfaces: {use: {email.send/v1: example.com/primary.New}}\n")},
		{ModulePath: "example.com/a", ModuleVersion: "v1.0.0", Manifest: parseManifest("interfaces: {use: {email.send/v1: example.com/primary.New}}\n")},
	}
	lookup := func(constructorsymbol.Symbol) (implementationinventory.Configuration, bool) {
		return implementationinventory.Configuration{}, false
	}
	initial, err := applicationmeta.MaintainDependencyConfiguration([]byte("{}\n"), applicationmeta.DependencyBaseline{}, nil, dependencies, lookup)
	if err != nil {
		t.Fatalf("MaintainDependencyConfiguration initial: %v", err)
	}
	composition, err := applicationmeta.Compose(dependencies, parseManifest("{}\n"), lookup)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	withoutChoice := strings.Replace(string(initial.Data()), "email.send/v1: example.com/primary.New", "", 1)
	if withoutChoice == string(initial.Data()) {
		t.Fatalf("test did not remove inherited choice: %s", initial.Data())
	}
	_, conflict := applicationmeta.MaintainDependencyConfiguration([]byte(withoutChoice), composition.DependencyBaseline(), initial.LocalPaths(), dependencies, lookup)
	if !errors.Is(conflict, applicationmeta.ErrAmbiguousConfigurationOwnership) {
		t.Fatalf("MaintainDependencyConfiguration error = %v, want ErrAmbiguousConfigurationOwnership", conflict)
	}

	var output strings.Builder
	writeCommandFailure(&output, "check Plystra Project", fmt.Errorf("resolve application: %w", conflict), recoveryContext{})
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/a:plystra.yaml:1:1 (configuration-declaration)\n" +
		"Source: example.com/b:plystra.yaml:1:1 (configuration-declaration)\n" +
		"Source: example.com/c:plystra.yaml:1:1 (configuration-declaration)\n\n" +
		"Recovery:\nMake the inherited field intent explicit in plystra.yaml by restoring it or writing its typed removal.\n\n" +
		"Diagnostic: " + diagnosticConfigurationOwnershipAmbiguous + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 3 {
		t.Fatalf("ambiguous configuration ownership output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureReportsCapabilityRequirementConflictSources(t *testing.T) {
	t.Parallel()

	declarationSource := providerresolution.RequirementSource{
		Kind:       providerresolution.RequirementDeclaration,
		Reference:  "plystra.yaml capabilities.require[email.send/v1]",
		ModulePath: "example.com/project",
		Path:       "plystra.yaml",
		Line:       1,
		Column:     1,
	}
	generationSource := providerresolution.RequirementSource{
		Kind:             providerresolution.RequirementGenerationRule,
		Reference:        "generation rule require-email",
		ModulePath:       "example.com/security",
		Path:             "authn/plugin.yaml",
		Line:             2,
		Column:           3,
		PluginID:         "example.security",
		Namespace:        "authn",
		SourceCapability: "session.verify/v1",
		RuleID:           "require-email",
	}
	_, conflict := providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{
		{Contract: recoveryContract("boolean"), Source: generationSource},
		{Contract: recoveryContract("string"), Source: declarationSource},
	}})
	if !errors.Is(conflict, providerresolution.ErrRequirementConflict) {
		t.Fatalf("Resolve error = %v, want ErrRequirementConflict", conflict)
	}
	var output strings.Builder
	writeCommandFailure(&output, "generate", fmt.Errorf("resolve application: %w", conflict), recoveryContext{})
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/project:plystra.yaml:1:1 (declaration)\n" +
		"Source: example.com/security:authn/plugin.yaml:2:3 (generation-rule)\n\n" +
		"Recovery:\nMake every Provider of email.send/v1 carry one identical provider-independent capability.yaml.\n\n" +
		"Diagnostic: " + diagnosticCapabilityRequirementConflict + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 2 {
		t.Fatalf("Capability requirement conflict output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureReportsProviderContractConflictSources(t *testing.T) {
	t.Parallel()

	_, _, _, _, conflict := recoveryProviderFailures(t)
	var output strings.Builder
	writeCommandFailure(&output, "generate", fmt.Errorf("resolve application: %w", conflict), recoveryContext{})
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/project:plystra.yaml:1:1 (declaration)\n" +
		"Source: example.com/provider-local:local/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)\n" +
		"Source: example.com/provider-smtp:smtp/capabilities/email.send/v1/capability.yaml:1:1 (provider-declaration)\n\n" +
		"Recovery:\nMake every Provider of email.send/v1 carry one identical provider-independent capability.yaml.\n\n" +
		"Diagnostic: " + diagnosticProviderContractConflict + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 3 {
		t.Fatalf("Provider contract conflict output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureReportsIntrinsicImplementationSelectionSources(t *testing.T) {
	t.Parallel()

	id, err := interfaceid.Parse("kernel.health/v1")
	if err != nil {
		t.Fatal(err)
	}
	constructor, err := constructorsymbol.Parse("example.com/application/health.New")
	if err != nil {
		t.Fatal(err)
	}
	_, resolutionErr := interfaceresolution.Resolve(interfaceresolution.Input{Choices: []interfaceresolution.Choice{{
		InterfaceID: id,
		Constructor: constructor,
		Sources: []interfaceresolution.ChoiceSource{
			{Reference: `example.com/z@v1.0.0/plystra.yaml interfaces.use["kernel.health/v1"]`, ModulePath: "example.com/z", Path: "plystra.yaml", Line: 1, Column: 1},
			{Reference: `example.com/a@v1.0.0/plystra.yaml interfaces.use["kernel.health/v1"]`, ModulePath: "example.com/a", Path: "plystra.yaml", Line: 1, Column: 1},
		},
	}}})
	if !errors.Is(resolutionErr, interfaceresolution.ErrIntrinsicChoice) {
		t.Fatalf("Resolve error = %v", resolutionErr)
	}

	var output strings.Builder
	writeCommandFailure(&output, "generate", resolutionErr, commandRecoveryContext("deploy/customer.yaml", "", nil))
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/a:plystra.yaml:1:1 (implementation-selection)\n" +
		"Source: example.com/z:plystra.yaml:1:1 (implementation-selection)\n\n" +
		"Recovery:\nSet the reported interfaces.use entry to null in deploy/customer.yaml to remove the effective selection; Kernel supplies that Interface intrinsically.\n\n" +
		"Diagnostic: " + diagnosticResolveIntrinsicInterfaceSelection + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 2 {
		t.Fatalf("intrinsic Implementation selection output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureReportsUnknownInterfaceSources(t *testing.T) {
	t.Parallel()

	id, err := interfaceid.Parse("records.missing/v1")
	if err != nil {
		t.Fatal(err)
	}
	_, resolutionErr := interfaceresolution.Resolve(interfaceresolution.Input{Requirements: []interfaceresolution.Requirement{
		{
			InterfaceID: id,
			Source: interfaceresolution.RequirementSource{
				Kind:       interfaceresolution.RequirementExposure,
				Reference:  `example.com/z@v1.0.0/plystra.yaml http.expose["records.missing/v1"]`,
				ModulePath: "example.com/z",
				Path:       "plystra.yaml",
				Line:       1,
				Column:     1,
			},
		},
		{
			InterfaceID: id,
			Source: interfaceresolution.RequirementSource{
				Kind:       interfaceresolution.RequirementDeclaration,
				Reference:  `example.com/a@v1.0.0/plystra.yaml interfaces.require["records.missing/v1"]`,
				ModulePath: "example.com/a",
				Path:       "plystra.yaml",
				Line:       1,
				Column:     1,
			},
		},
	}})
	if !errors.Is(resolutionErr, interfaceresolution.ErrUnknownInterface) {
		t.Fatalf("Resolve error = %v", resolutionErr)
	}

	var output strings.Builder
	writeCommandFailure(&output, "generate", resolutionErr, commandRecoveryContext("", "production", nil))
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/a:plystra.yaml:1:1 (declaration)\n" +
		"Source: example.com/z:plystra.yaml:1:1 (exposure)\n\n" +
		"Recovery:\nCorrect the reported Interface ID in plystra.production.yaml to one canonical Interface visible in the selected Go Module graph, then rerun the command.\n\n" +
		"Diagnostic: " + diagnosticResolveUnknownInterface + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 2 {
		t.Fatalf("unknown Interface output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureReportsReservedInterfaceSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const (
		modulePath = "example.com/recovery-reserved"
		sourcePath = "interfaces/kernel/health/v1/interface.go"
	)
	for path, data := range map[string]string{
		"go.mod":       "module " + modulePath + "\n\ngo 1.26\n",
		"plystra.yaml": "{}\n",
		sourcePath: `package healthv1

import "context"

//plystra:interface kernel.health/v1
type Interface interface {
	Health(context.Context, Request) (Response, error)
}

type Request struct{}
type Response struct{}
`,
	} {
		absolute := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	environment := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		upper := strings.ToUpper(entry)
		if strings.HasPrefix(upper, "GOWORK=") || strings.HasPrefix(upper, "GOPROXY=") || strings.HasPrefix(upper, "GOSUMDB=") {
			continue
		}
		environment = append(environment, entry)
	}
	environment = append(environment, "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	_, resolutionErr := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: environment})
	if !errors.Is(resolutionErr, interfaceresolution.ErrReservedInterface) {
		t.Fatalf("Resolve error = %v", resolutionErr)
	}

	var output strings.Builder
	writeCommandFailure(&output, "generate", resolutionErr, recoveryContext{})
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: " + modulePath + ":" + sourcePath + ":5:1 (interface-declaration)\n\n" +
		"Recovery:\nRemove the reported local kernel.* Interface declaration and import the canonical Kernel Interface package instead.\n\n" +
		"Diagnostic: " + diagnosticResolveReservedInterface + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 1 || strings.Contains(got, root) || strings.Contains(got, filepath.ToSlash(root)) {
		t.Fatalf("reserved Interface output = %q, want suffix %q", got, wantSuffix)
	}
}

func TestWriteCommandFailureReportsJoinedConcurrentChangeSources(t *testing.T) {
	t.Parallel()

	moduleChange := &concurrentSourceTestError{
		modulePath: "example.com/a",
		sourcePath: "go.mod",
		sourceKind: "module-dependency",
		cause:      fmt.Errorf("application go.mod changed: %w", moduledependency.ErrConcurrentChange),
	}
	configurationChange := &concurrentSourceTestError{
		modulePath: "example.com/z",
		sourcePath: "plystra.yaml",
		sourceKind: "configuration-declaration",
		cause:      fmt.Errorf("root configuration changed: %w", applicationresolve.ErrConcurrentChange),
	}
	joined := fmt.Errorf(
		"generate Project: %w",
		errors.Join(generatedfiles.ErrManifest, configurationChange, moduleChange, moduleChange),
	)

	var output strings.Builder
	writeCommandFailure(&output, "generate", joined, recoveryContext{})
	got := output.String()
	wantSuffix := "\n\n" +
		"Source: example.com/a:go.mod (module-dependency)\n" +
		"Source: example.com/z:plystra.yaml (configuration-declaration)\n\n" +
		"Recovery:\nStop concurrent Project edits, then rerun the command against the unchanged authored inputs.\n\n" +
		"Diagnostic: " + diagnosticProjectConcurrentChange + "\n"
	if !strings.HasSuffix(got, wantSuffix) || strings.Count(got, "Source: ") != 2 {
		t.Fatalf("concurrent change output = %q, want suffix %q", got, wantSuffix)
	}
	if strings.Contains(got, "Diagnostic: "+diagnosticGeneratedManifestInvalid) || strings.Count(got, "Diagnostic: ") != 1 {
		t.Fatalf("concurrent change output did not retain primary classification: %q", got)
	}
}

func TestPrimaryActionableDiagnosticAssignsStableCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "Project manifest", err: applicationresolve.ErrManifest, code: diagnosticcode.ProjectManifestInvalid},
		{name: "inherited configuration conflict", err: applicationmeta.ErrInheritedConflict, code: diagnosticcode.ConfigurationInheritedConflict},
		{name: "configuration ownership", err: applicationmeta.ErrAmbiguousConfigurationOwnership, code: diagnosticcode.ConfigurationOwnershipAmbiguous},
		{name: "HTTP transport", err: applicationmeta.ErrHTTPTransportSelection, code: diagnosticcode.HTTPTransportSelectionInvalid},
		{name: "constructor configuration schema", err: applicationmeta.ErrConfigurationSchema, code: diagnosticcode.ConstructorConfigurationSchemaInvalid},
		{name: "constructor configuration values", err: applicationmeta.ErrConfigurationValues, code: diagnosticcode.ConstructorConfigurationValuesInvalid},
		{name: "unselected constructor configuration", err: applicationresolve.ErrUnownedConstructorConfiguration, code: diagnosticcode.ConstructorConfigurationUnselected},
		{name: "environment overlay", err: applicationmeta.ErrApplyOverlay, code: diagnosticcode.EnvironmentOverlayInvalid},
		{name: "current configuration", err: applicationmeta.ErrInvalidManifest, code: diagnosticcode.ConfigurationInvalid},
		{name: "resolved configuration", err: configurationresolve.ErrInvalidConfiguration, code: diagnosticcode.ConfigurationInvalid},
		{name: "unselected Plugin configuration", err: configurationresolve.ErrUnselectedConfiguration, code: diagnosticcode.PluginConfigurationUnselected},
		{name: "missing configured Plugin", err: configurationresolve.ErrMissingPlugin, code: diagnosticcode.PluginConfigurationPluginMissing},
		{name: "configuration selection", err: applicationresolve.ErrConfigurationSelection, code: diagnosticcode.ConfigurationSelectionInvalid},
		{name: "Project marker", err: projectlocate.ErrInvalidManifest, code: diagnosticcode.ProjectManifestInvalid},
		{name: "Kernel dependency drift", err: applicationgenerate.ErrKernelDependency, code: diagnosticcode.ApplicationDependencyDrift},
		{name: "runtime dependency drift", err: applicationgenerate.ErrRuntimeDependency, code: diagnosticcode.ApplicationDependencyDrift},
		{name: "Project not found", err: projectlocate.ErrNotFound, code: diagnosticcode.ProjectNotFound},
		{name: "Go Module not found", err: modulelocate.ErrNotFound, code: diagnosticcode.GoModuleNotFound},
		{name: "located go.mod invalid", err: modulelocate.ErrInvalidGoMod, code: diagnosticcode.GoModuleInvalid},
		{name: "dependency go.mod invalid", err: moduledependency.ErrInvalidGoMod, code: diagnosticcode.GoModuleInvalid},
		{name: "module unavailable", err: moduledependency.ErrModuleUnavailable, code: diagnosticcode.GoModuleUnavailable},
		{name: "Go command", err: gocommand.ErrRun, code: diagnosticcode.GoCommandFailed},
		{name: "Plugin target ambiguous", err: plugintarget.ErrAmbiguous, code: diagnosticcode.PluginTargetAmbiguous},
		{name: "Plugin target missing", err: plugintarget.ErrNotFound, code: diagnosticcode.PluginTargetNotFound},
		{name: "Plugin target invalid", err: plugintarget.ErrSelection, code: diagnosticcode.PluginTargetInvalid},
		{name: "generation activation conflict", err: generationactivation.ErrAssociationConflict, code: diagnosticcode.GenerationActivationConflict},
		{name: "generation activation missing", err: generationactivation.ErrMissingAssociation, code: diagnosticcode.GenerationActivationMissing},
		{name: "generation Provider extension", err: generationactivation.ErrSelectedProviderExtension, code: diagnosticcode.GenerationProviderExtensionMissing},
		{name: "generation activation cycle", err: generationresolution.ErrActivationCycle, code: diagnosticcode.GenerationActivationCycle},
		{name: "generation dependency cycle", err: generationresolution.ErrDependencyCycle, code: diagnosticcode.GenerationDependencyCycle},
		{name: "generation contribution cycle", err: generationresolution.ErrContributionCycle, code: diagnosticcode.GenerationContributionCycle},
		{name: "generation contribution order", err: generationresolution.ErrUnorderedContributions, code: diagnosticcode.GenerationContributionsUnordered},
		{name: "generation repeated state", err: generationresolution.ErrRepeatedState, code: diagnosticcode.GenerationStateRepeated},
		{name: "generation convergence", err: generationresolution.ErrExtensionConvergence, code: diagnosticcode.GenerationNonconvergent},
		{name: "generation helper API", err: generationexec.ErrUnsupportedAPI, code: diagnosticcode.GenerationAPIUnsupported},
		{name: "Plugin generation API", err: pluginmeta.ErrUnsupportedGenerationAPI, code: diagnosticcode.GenerationAPIUnsupported},
		{name: "generation package", err: pluginindex.ErrInvalidGenerationPackage, code: diagnosticcode.GenerationPackageInvalid},
		{name: "generation compile", err: generationexec.ErrCompile, code: diagnosticcode.GenerationCompileFailed},
		{name: "generation execute", err: generationexec.ErrExecute, code: diagnosticcode.GenerationExecutionFailed},
		{name: "generation resolution execute", err: generationresolution.ErrExtensionExecution, code: diagnosticcode.GenerationExecutionFailed},
		{name: "generation extension", err: generationexec.ErrExtension, code: diagnosticcode.GenerationExtensionFailed},
		{name: "generation crash", err: generationexec.ErrCrash, code: diagnosticcode.GenerationCrashed},
		{name: "generation timeout", err: generationexec.ErrTimeout, code: diagnosticcode.GenerationTimeout},
		{name: "generation request size", err: generationexec.ErrRequestTooLarge, code: diagnosticcode.GenerationRequestTooLarge},
		{name: "generation output size", err: generationexec.ErrOutputTooLarge, code: diagnosticcode.GenerationOutputTooLarge},
		{name: "generation malformed output", err: generationexec.ErrMalformedOutput, code: diagnosticcode.GenerationOutputMalformed},
		{name: "generation invalid output", err: generationexec.ErrInvalidOutput, code: diagnosticcode.GenerationOutputInvalid},
		{name: "wrapped generation crash", err: errors.Join(generationexec.ErrExecute, generationexec.ErrCrash), code: diagnosticcode.GenerationCrashed},
		{name: "wrapped generation timeout", err: errors.Join(generationresolution.ErrExtensionExecution, generationexec.ErrTimeout), code: diagnosticcode.GenerationTimeout},
		{name: "compile timeout", err: errors.Join(generationexec.ErrCompile, generationexec.ErrTimeout), code: diagnosticcode.GenerationTimeout},
		{name: "generation extension diagnostic", err: generationresolution.ErrExtensionDiagnostic, code: diagnosticcode.GenerationExtensionDiagnostic},
		{name: "Alias conflict", err: aliasresolution.ErrConflict, code: diagnosticcode.AliasConflict},
		{name: "application Alias", err: aliasresolution.ErrInvalidApplicationAlias, code: diagnosticcode.AliasApplicationInvalid},
		{name: "extension Alias", err: aliasresolution.ErrInvalidExtensionOutput, code: diagnosticcode.AliasExtensionOutputInvalid},
		{name: "Alias resolution", err: generationresolution.ErrAliasResolution, code: diagnosticcode.AliasResolutionFailed},
		{name: "Protobuf wire history", err: protobufwiremap.ErrHistory, code: diagnosticcode.ProtobufWireHistoryInvalid},
		{name: "Protobuf identity", err: protobufidentity.ErrCollision, code: diagnosticcode.ProtobufIdentityCollision},
		{name: "Protobuf operation kind", err: protobufmodel.ErrOperationKind, code: diagnosticcode.ProtobufOperationKindUnsupported},
		{name: "generated ownership", err: generatedfiles.ErrConflict, code: diagnosticcode.GeneratedOwnershipConflict},
		{name: "unexpected generated output", err: generatedfiles.ErrUnexpected, code: diagnosticcode.GeneratedUnexpectedOutput},
		{name: "generated manifest", err: generatedfiles.ErrManifest, code: diagnosticcode.GeneratedManifestInvalid},
		{name: "Capability manifest", err: capabilitymeta.ErrInvalidManifest, code: diagnosticcode.CapabilityManifestInvalid},
		{name: "atomic concurrent change", err: atomicfs.ErrConcurrentChange, code: diagnosticcode.ProjectConcurrentChange},
		{name: "resolution concurrent change", err: applicationresolve.ErrConcurrentChange, code: diagnosticcode.ProjectConcurrentChange},
		{name: "generation concurrent change", err: applicationgenerate.ErrConcurrentChange, code: diagnosticcode.ProjectConcurrentChange},
		{name: "unknown Interface", err: interfaceresolution.ErrUnknownInterface, code: diagnosticcode.ResolveUnknownInterface},
		{name: "unknown Implementation", err: interfaceresolution.ErrUnknownConstructor, code: diagnosticcode.ResolveUnknownImplementation},
		{name: "incompatible Implementation", err: interfaceresolution.ErrIncompatibleChoice, code: diagnosticcode.ResolveIncompatibleImplementation},
		{name: "multiple Implementations", err: interfaceresolution.ErrAmbiguousImplementation, code: diagnosticcode.ResolveMultipleImplementations},
		{name: "missing Implementation", err: constructorgraph.ErrMissingBinding, code: diagnosticcode.ResolveMissingImplementation},
		{name: "constructor cycle", err: constructorgraph.ErrCycle, code: diagnosticcode.ResolveConstructorCycle},
		{name: "reserved Interface", err: interfaceresolution.ErrReservedInterface, code: diagnosticcode.ResolveReservedInterface},
		{name: "intrinsic Interface selection", err: interfaceresolution.ErrIntrinsicChoice, code: diagnosticcode.ResolveIntrinsicInterfaceSelection},
		{name: "invalid Implementation declaration", err: implementationdecl.ErrInvalid, code: diagnosticcode.ImplementationDeclarationInvalid},
		{name: "invalid Implementation Config", err: implementationinventory.ErrInvalidConfiguration, code: diagnosticcode.ImplementationConfigInvalid},
		{name: "invalid required Interface parameter", err: implementationinventory.ErrInvalidRequiredInterface, code: diagnosticcode.ImplementationRequiredInvalid},
		{name: "invalid optional Interface parameter", err: implementationinventory.ErrInvalidOptionalInterface, code: diagnosticcode.ImplementationOptionalInvalid},
		{name: "invalid Implementation result", err: implementationinventory.ErrInvalidResult, code: diagnosticcode.ImplementationResultInvalid},
		{name: "invalid Implementation conformance", err: implementationinventory.ErrInvalidConformance, code: diagnosticcode.ImplementationConformanceInvalid},
		{name: "invalid Interface declaration", err: interfacedecl.ErrInvalid, code: diagnosticcode.InterfaceDeclarationInvalid},
		{name: "invalid Interface contract", err: interfacecontract.ErrInvalid, code: diagnosticcode.InterfaceContractInvalid},
		{name: "invalid Interface metadata", err: interfacemeta.ErrInvalid, code: diagnosticcode.InterfaceMetadataInvalid},
		{name: "duplicate Interface ID", err: interfaceinventory.ErrDuplicateID, code: diagnosticcode.InterfaceIDDuplicate},
		{name: "invalid authored package", err: interfaceinventory.ErrPackage, code: diagnosticcode.AuthoredPackageInvalid},
		{name: "invalid Project create name", err: newproject.ErrInvalidProjectName, code: diagnosticcode.ProjectCreateNameInvalid},
		{name: "invalid Project create module", err: newproject.ErrInvalidModulePath, code: diagnosticcode.ProjectCreateModuleInvalid},
		{name: "invalid Project create template query", err: newproject.ErrInvalidTemplateQuery, code: diagnosticcode.ProjectCreateTemplateInvalid},
		{name: "invalid Project create Plugin name", err: newproject.ErrInvalidPluginName, code: diagnosticcode.ProjectCreatePluginNameInvalid},
		{name: "invalid Project create Plugin ID", err: newproject.ErrInvalidPluginID, code: diagnosticcode.ProjectCreatePluginIDInvalid},
		{name: "existing Project create target", err: newproject.ErrTargetExists, code: diagnosticcode.ProjectCreateTargetExists},
		{name: "failed Project Git initialization", err: newproject.ErrGitInitialization, code: diagnosticcode.ProjectCreateGitInitializationFailed},
		{name: "missing Project create choice", err: errNewChoiceRequired, code: diagnosticcode.ProjectCreateChoiceRequired},
		{name: "invalid Plugin create name", err: plugincreate.ErrInvalidName, code: diagnosticcode.PluginCreateNameInvalid},
		{name: "invalid derived Plugin ID", err: plugincreate.ErrDeriveID, code: diagnosticcode.PluginCreateIDInvalid},
		{name: "existing Plugin create target", err: plugincreate.ErrTargetExists, code: diagnosticcode.PluginCreateTargetExists},
		{name: "invalid Interface create name", err: interfacecreate.ErrInvalidName, code: diagnosticcode.InterfaceCreateNameInvalid},
		{name: "existing Interface create target", err: interfacecreate.ErrTargetExists, code: diagnosticcode.InterfaceCreateTargetExists},
		{name: "invalid Implementation create Interface", err: implementationcreate.ErrInvalidInterface, code: diagnosticcode.ImplementationCreateInterfaceInvalid},
		{name: "invalid Implementation create package", err: implementationcreate.ErrInvalidPackage, code: diagnosticcode.ImplementationCreatePackageInvalid},
		{name: "missing Implementation create Interface", err: implementationcreate.ErrInterfaceNotFound, code: diagnosticcode.ImplementationCreateInterfaceNotFound},
		{name: "existing Implementation create target", err: implementationcreate.ErrTargetExists, code: diagnosticcode.ImplementationCreateTargetExists},
		{name: "invalid dependency add query", err: fmt.Errorf("%w: %w", dependencyadd.ErrAdd, moduleargument.ErrInvalidQuery), code: diagnosticcode.DependencyAddQueryInvalid},
		{name: "invalid dependency remove path", err: fmt.Errorf("%w: %w", dependencyremove.ErrRemove, moduleargument.ErrInvalidPath), code: diagnosticcode.DependencyRemovePathInvalid},
		{name: "unselected dependency removal", err: fmt.Errorf("%w: %w", dependencyremove.ErrRemove, dependencyremove.ErrNotSelected), code: diagnosticcode.DependencyRemoveNotSelected},
		{name: "invalid dependency update query", err: fmt.Errorf("%w: %w", dependencyupdate.ErrUpdate, moduleargument.ErrInvalidQuery), code: diagnosticcode.DependencyUpdateQueryInvalid},
		{name: "unselected dependency update", err: fmt.Errorf("%w: %w", dependencyupdate.ErrUpdate, dependencyupdate.ErrNotSelected), code: diagnosticcode.DependencyUpdateNotSelected},
		{name: "invalid Capability create reference", err: fmt.Errorf("%w: %w", capabilitycreate.ErrCreate, capabilitycreate.ErrInvalidReference), code: diagnosticcode.CapabilityCreateReferenceInvalid},
		{name: "existing Capability create target", err: errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrActionMismatch, capabilitycreate.ErrCreateAlreadyVisible), code: diagnosticcode.CapabilityCreateAlreadyVisible},
		{name: "Capability create confirmation", err: errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrConfirmationRequired), code: diagnosticcode.CapabilityCreateConfirmationRequired},
		{name: "Capability create version exhaustion", err: errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrVersionExhausted, capabilityversion.ErrOverflow), code: diagnosticcode.CapabilityCreateVersionExhausted},
		{name: "missing Capability create intent profile", err: errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrIntentProfile, capabilitycreate.ErrIntentProfileRequired), code: diagnosticcode.CapabilityCreateIntentProfileRequired},
		{name: "inapplicable Capability create intent profile", err: errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrIntentProfile, capabilitycreate.ErrIntentProfileNotAllowed), code: diagnosticcode.CapabilityCreateIntentProfileNotAllowed},
		{name: "invalid Capability implement reference", err: fmt.Errorf("%w: %w", capabilitycreate.ErrImplement, capabilitycreate.ErrInvalidReference), code: diagnosticcode.CapabilityImplementReferenceInvalid},
		{name: "missing Capability implement target", err: errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrActionMismatch, capabilitycreate.ErrImplementNotVisible), code: diagnosticcode.CapabilityImplementNotVisible},
		{name: "invalid Capability expose reference", err: fmt.Errorf("%w: %w", capabilityexpose.ErrExpose, capabilityexpose.ErrInvalidReference), code: diagnosticcode.CapabilityExposeReferenceInvalid},
		{name: "missing Capability expose target", err: errors.Join(capabilityexpose.ErrExpose, capabilityexpose.ErrNotVisible, interfaceresolution.ErrUnknownInterface), code: diagnosticcode.CapabilityExposeNotVisible},
		{name: "invalid use Interface", err: implementationselect.ErrInvalidInterfaceID, code: diagnosticcode.UseInterfaceInvalid},
		{name: "invalid use constructor", err: implementationselect.ErrInvalidConstructor, code: diagnosticcode.UseConstructorInvalid},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			diagnostic, ok := primaryActionableDiagnostic(test.err, recoveryContext{})
			if !ok || diagnostic.code != test.code || diagnostic.recovery == "" || !diagnosticcode.Valid(diagnostic.code) {
				t.Fatalf("primaryActionableDiagnostic(%v) = %#v, %t; want canonical code %q", test.err, diagnostic, ok, test.code)
			}
		})
	}
}

func TestPrimaryActionableDiagnosticRequiresMatchingDependencyOperationAndCondition(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		dependencyremove.ErrRemove,
		dependencyremove.ErrNotSelected,
		dependencyupdate.ErrUpdate,
		dependencyupdate.ErrNotSelected,
		fmt.Errorf("%w: %w", dependencyremove.ErrRemove, dependencyupdate.ErrNotSelected),
		fmt.Errorf("%w: %w", dependencyupdate.ErrUpdate, dependencyremove.ErrNotSelected),
	} {
		if diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{}); ok {
			t.Fatalf("primaryActionableDiagnostic(%v) = %#v, true; want no cross-family classification", err, diagnostic)
		}
	}
}

func TestPrimaryActionableDiagnosticRequiresMatchingCapabilityOperationAndReferenceCondition(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		capabilitycreate.ErrCreate,
		capabilitycreate.ErrImplement,
		capabilitycreate.ErrInvalidReference,
		capabilityexpose.ErrExpose,
		capabilityexpose.ErrInvalidReference,
		capabilityid.ErrInvalid,
		fmt.Errorf("%w: %w", capabilitycreate.ErrCreate, capabilityexpose.ErrInvalidReference),
		fmt.Errorf("%w: %w", capabilitycreate.ErrImplement, capabilityexpose.ErrInvalidReference),
		fmt.Errorf("%w: %w", capabilityexpose.ErrExpose, capabilitycreate.ErrInvalidReference),
	} {
		if diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{}); ok {
			t.Fatalf("primaryActionableDiagnostic(%v) = %#v, true; want no cross-family classification", err, diagnostic)
		}
	}
}

func TestPrimaryActionableDiagnosticRequiresMatchingCapabilityActionAndCondition(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		capabilitycreate.ErrActionMismatch,
		capabilitycreate.ErrCreateAlreadyVisible,
		capabilitycreate.ErrImplementNotVisible,
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrCreateAlreadyVisible),
		errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrImplementNotVisible),
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrActionMismatch, capabilitycreate.ErrImplementNotVisible),
		errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrActionMismatch, capabilitycreate.ErrCreateAlreadyVisible),
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrActionMismatch, capabilitycreate.ErrCreateAlreadyVisible, capabilitycreate.ErrImplementNotVisible),
		errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrActionMismatch, capabilitycreate.ErrCreateAlreadyVisible, capabilitycreate.ErrImplementNotVisible),
	} {
		if diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{}); ok {
			t.Fatalf("primaryActionableDiagnostic(%v) = %#v, true; want no cross-condition classification", err, diagnostic)
		}
	}
}

func TestPrimaryActionableDiagnosticRequiresCapabilityExposeNotVisibleBoundary(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		capabilityexpose.ErrExpose,
		capabilityexpose.ErrNotVisible,
		interfaceresolution.ErrUnknownInterface,
		errors.Join(capabilityexpose.ErrExpose, capabilityexpose.ErrNotVisible),
		errors.Join(capabilityexpose.ErrExpose, interfaceresolution.ErrUnknownInterface),
		errors.Join(capabilitycreate.ErrCreate, capabilityexpose.ErrExpose, capabilityexpose.ErrNotVisible, interfaceresolution.ErrUnknownInterface),
		errors.Join(capabilitycreate.ErrImplement, capabilityexpose.ErrExpose, capabilityexpose.ErrNotVisible, interfaceresolution.ErrUnknownInterface),
		errors.Join(capabilityexpose.ErrExpose, capabilityexpose.ErrInvalidReference, capabilityexpose.ErrNotVisible, interfaceresolution.ErrUnknownInterface),
	} {
		diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{})
		if ok && diagnostic.code == diagnosticCapabilityExposeNotVisible {
			t.Fatalf("primaryActionableDiagnostic(%v) = %#v, true; want no cross-condition exposure classification", err, diagnostic)
		}
	}
}

func TestPrimaryActionableDiagnosticRequiresMatchingCapabilityIntentAndCondition(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		capabilitycreate.ErrIntentProfile,
		capabilitycreate.ErrIntentProfileRequired,
		capabilitycreate.ErrIntentProfileNotAllowed,
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrIntentProfile),
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrIntentProfileRequired),
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrIntentProfileNotAllowed),
		errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrIntentProfile, capabilitycreate.ErrIntentProfileRequired),
		errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrIntentProfile, capabilitycreate.ErrIntentProfileNotAllowed),
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrIntentProfile, capabilitycreate.ErrIntentProfileRequired, capabilitycreate.ErrIntentProfileNotAllowed),
	} {
		if diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{}); ok {
			t.Fatalf("primaryActionableDiagnostic(%v) = %#v, true; want no cross-condition classification", err, diagnostic)
		}
	}
}

func TestPrimaryActionableDiagnosticRequiresCapabilityCreateConfirmationBoundary(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		capabilitycreate.ErrCreate,
		capabilitycreate.ErrConfirmationRequired,
		errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrConfirmationRequired),
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrImplement, capabilitycreate.ErrConfirmationRequired),
	} {
		if diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{}); ok {
			t.Fatalf("primaryActionableDiagnostic(%v) = %#v, true; want no cross-operation classification", err, diagnostic)
		}
	}
}

func TestPrimaryActionableDiagnosticRequiresCapabilityCreateVersionExhaustionBoundary(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		capabilitycreate.ErrCreate,
		capabilitycreate.ErrVersionExhausted,
		capabilityversion.ErrOverflow,
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrVersionExhausted),
		errors.Join(capabilitycreate.ErrCreate, capabilityversion.ErrOverflow),
		errors.Join(capabilitycreate.ErrImplement, capabilitycreate.ErrVersionExhausted, capabilityversion.ErrOverflow),
		errors.Join(capabilitycreate.ErrCreate, capabilitycreate.ErrImplement, capabilitycreate.ErrVersionExhausted, capabilityversion.ErrOverflow),
	} {
		if diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{}); ok {
			t.Fatalf("primaryActionableDiagnostic(%v) = %#v, true; want no cross-operation classification", err, diagnostic)
		}
	}
}

func TestPrimaryActionableDiagnosticLeavesBroadUseFailuresUnclassified(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		implementationselect.ErrSelect,
		implementationselect.ErrConfigurationWrite,
	} {
		if diagnostic, ok := primaryActionableDiagnostic(err, recoveryContext{}); ok {
			t.Fatalf("primaryActionableDiagnostic(%v) = %#v, true; want no guessed classification", err, diagnostic)
		}
	}
}

func TestRecoverySelectorPreservesModeWithoutEchoingUnsafePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		context    recoveryContext
		wantSuffix string
		reject     string
	}{
		{name: "default", context: commandRecoveryContext("", "", nil), wantSuffix: "`plystra generate`"},
		{name: "explicit environment", context: commandRecoveryContext("", "production", []string{"PLYSTRA_CONFIG=ignored.yaml"}), wantSuffix: "`plystra generate --env \"production\"`"},
		{name: "ambient environment", context: commandRecoveryContext("", "", []string{"PLYSTRA_ENV=test"}), wantSuffix: "`plystra generate --env \"test\"`"},
		{name: "explicit configuration", context: commandRecoveryContext("deploy/customer.yaml", "", []string{"PLYSTRA_ENV=ignored"}), wantSuffix: "`plystra generate --config \"deploy/customer.yaml\"`"},
		{name: "ambient configuration", context: commandRecoveryContext("", "", []string{"PLYSTRA_CONFIG=deploy/ambient.yaml"}), wantSuffix: "`plystra generate --config \"deploy/ambient.yaml\"`"},
		{name: "absolute configuration", context: commandRecoveryContext(filepath.Join(t.TempDir(), "private-token.yaml"), "", nil), wantSuffix: "`plystra generate --config <yaml-path>`", reject: "private-token"},
		{name: "shell-sensitive configuration", context: commandRecoveryContext("deploy/`private-token`.yaml", "", nil), wantSuffix: "`plystra generate --config <yaml-path>`", reject: "private-token"},
		{name: "unsafe environment", context: commandRecoveryContext("", "production\nprivate-token", nil), wantSuffix: "`plystra generate --env <environment>`", reject: "private-token"},
		{name: "shell-sensitive environment", context: commandRecoveryContext("", "$env:private-token", nil), wantSuffix: "`plystra generate --env <environment>`", reject: "private-token"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			diagnostic, ok := primaryActionableDiagnostic(generatedfiles.ErrConflict, test.context)
			if !ok || diagnostic.code != diagnosticGeneratedOwnershipConflict || !strings.Contains(diagnostic.recovery, test.wantSuffix) {
				t.Fatalf("primaryActionableDiagnostic() = %#v, %t; want code %q and recovery %q", diagnostic, ok, diagnosticGeneratedOwnershipConflict, test.wantSuffix)
			}
			if test.reject != "" && strings.Contains(diagnostic.recovery, test.reject) {
				t.Fatalf("primaryActionableDiagnostic() leaked %q in %q", test.reject, diagnostic.recovery)
			}
		})
	}
}

func FuzzRecoverySelectorDoesNotInjectDiagnosticLines(f *testing.F) {
	for _, seed := range []string{"production", "deploy/customer.yaml", "../outside.yaml", "value\nRecovery:\nunsafe", "$env:SECRET", "`command`", strings.Repeat("x", 600)} {
		f.Add(true, seed)
		f.Add(false, seed)
	}
	f.Fuzz(func(t *testing.T, environmentMode bool, value string) {
		context := commandRecoveryContext(value, "", nil)
		if environmentMode {
			context = commandRecoveryContext("", value, nil)
		}
		diagnostic, ok := primaryActionableDiagnostic(generatedfiles.ErrConflict, context)
		if !ok {
			t.Fatal("generated ownership conflict was not actionable")
		}
		if strings.ContainsAny(diagnostic.recovery, "\r\n\x00") {
			t.Fatalf("recovery action contains an injected line or NUL: %q", diagnostic.recovery)
		}
		if len(diagnostic.recovery) > 1000 {
			t.Fatalf("recovery action is unbounded: %d bytes", len(diagnostic.recovery))
		}
	})
}

func TestWriteCommandFailureChoosesOneJoinedProblemAndLeavesUnknownErrorsUnchanged(t *testing.T) {
	t.Parallel()

	_, ambiguous, invalidChoice, _, _ := recoveryProviderFailures(t)
	joined := errors.Join(ambiguous, invalidChoice)
	var output strings.Builder
	writeCommandFailure(&output, "", joined, recoveryContext{})
	got := output.String()
	for _, want := range []string{"selected Plugin ID is not visible", "\n\nRecovery:\n", "\nDiagnostic: " + diagnosticProviderSelectionInvalid + "\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("joined output = %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "ambiguous canonical Capability provider") {
		t.Fatalf("joined output included a non-primary problem: %q", got)
	}
	if strings.Contains(got, "correction:") || strings.Count(got, "Recovery:") != 1 {
		t.Fatalf("joined output contains duplicate recovery advice: %q", got)
	}

	output.Reset()
	unknown := errors.New("internal renderer failed")
	writeCommandFailure(&output, "inspect", unknown, recoveryContext{})
	if got := output.String(); got != "inspect: internal renderer failed\n" {
		t.Fatalf("unknown output = %q", got)
	}
}

func recoveryProviderFailures(t *testing.T) (missing, ambiguous, invalidChoice, mismatch, contractConflict error) {
	t.Helper()
	contract := recoveryContract("string")
	requirement := providerresolution.Requirement{
		Contract: contract,
		Source: providerresolution.RequirementSource{
			Kind:       providerresolution.RequirementDeclaration,
			Reference:  "plystra.yaml capabilities.require[email.send/v1]",
			ModulePath: "example.com/project",
			Path:       "plystra.yaml",
			Line:       1,
			Column:     1,
		},
	}
	_, missing = providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{requirement}})
	candidates := []providerresolution.Candidate{
		{PluginID: "acme.email.local", Contract: contract, Source: "local/capability.yaml"},
		{PluginID: "acme.email.smtp", Contract: contract, Source: "smtp/capability.yaml"},
	}
	_, ambiguous = providerresolution.Resolve(providerresolution.Input{Requirements: []providerresolution.Requirement{requirement}, Candidates: candidates})
	_, invalidChoice = providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{requirement},
		Candidates:   candidates,
		Choices: []providerresolution.Choice{{
			Capability: "email.send/v1",
			PluginID:   "missing.email",
			Sources: []providerresolution.ChoiceSource{{
				Kind:       providerresolution.ChoiceSourceCurrentProject,
				Reference:  "plystra.yaml capabilities.use[email.send/v1]",
				ModulePath: "example.com/project",
				Path:       "plystra.yaml",
				Line:       2,
				Column:     3,
			}},
		}},
	})
	_, mismatch = providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{requirement},
		Candidates: []providerresolution.Candidate{{
			PluginID: "acme.email.local",
			Contract: recoveryContract("boolean"),
			Source:   "local/capability.yaml",
			DeclarationSource: providerresolution.ProviderSource{
				ModulePath: "example.com/provider",
				Path:       "local/capabilities/email.send/v1/capability.yaml",
				Line:       1,
				Column:     1,
			},
		}},
	})
	_, contractConflict = providerresolution.Resolve(providerresolution.Input{
		Requirements: []providerresolution.Requirement{{
			Capability: "email.send/v1",
			Source:     requirement.Source,
		}},
		Candidates: []providerresolution.Candidate{
			{
				PluginID: "acme.email.local",
				Contract: contract,
				Source:   "local/capability.yaml",
				DeclarationSource: providerresolution.ProviderSource{
					ModulePath: "example.com/provider-local",
					Path:       "local/capabilities/email.send/v1/capability.yaml",
					Line:       1,
					Column:     1,
				},
			},
			{
				PluginID: "acme.email.smtp",
				Contract: recoveryContract("boolean"),
				Source:   "smtp/capability.yaml",
				DeclarationSource: providerresolution.ProviderSource{
					ModulePath: "example.com/provider-smtp",
					Path:       "smtp/capabilities/email.send/v1/capability.yaml",
					Line:       1,
					Column:     1,
				},
			},
		},
	})
	for name, err := range map[string]error{
		"missing": missing, "ambiguous": ambiguous, "invalid choice": invalidChoice, "contract mismatch": mismatch, "contract conflict": contractConflict,
	} {
		if err == nil {
			t.Fatalf("%s provider input unexpectedly resolved", name)
		}
	}
	return missing, ambiguous, invalidChoice, mismatch, contractConflict
}

type concurrentSourceTestError struct {
	modulePath string
	sourcePath string
	sourceKind string
	cause      error
}

func (e *concurrentSourceTestError) Error() string      { return e.cause.Error() }
func (e *concurrentSourceTestError) Unwrap() error      { return e.cause }
func (e *concurrentSourceTestError) ModulePath() string { return e.modulePath }
func (e *concurrentSourceTestError) SourcePath() string { return e.sourcePath }
func (e *concurrentSourceTestError) SourceKind() string { return e.sourceKind }
func (*concurrentSourceTestError) Line() int            { return 0 }
func (*concurrentSourceTestError) Column() int          { return 0 }

type recoveryRequirementSourceError struct {
	cause   error
	sources []providerresolution.RequirementSource
}

func (e *recoveryRequirementSourceError) Error() string { return e.cause.Error() }
func (e *recoveryRequirementSourceError) Unwrap() error { return e.cause }
func (e *recoveryRequirementSourceError) RequirementSources() []providerresolution.RequirementSource {
	return append([]providerresolution.RequirementSource(nil), e.sources...)
}

func recoveryContract(fieldType string) []byte {
	return []byte("id: email.send/v1\nrequest: {value: {type: " + fieldType + "}}\n" + `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`)
}
