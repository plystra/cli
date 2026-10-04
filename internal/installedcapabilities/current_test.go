package installedcapabilities_test

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/installedcapabilities"
	"github.com/plystra/cli/internal/transporttoolchain"
	"github.com/plystra/cli/internal/version"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestCurrentReportsExactInstalledDistribution(t *testing.T) {
	t.Parallel()

	capabilities, err := installedcapabilities.Current()
	if err != nil || !capabilities.Valid() {
		t.Fatalf("Current = %#v, %v", capabilities, err)
	}
	if capabilities.CLIVersion() != version.Current ||
		capabilities.KernelVersion() != version.KernelVersion ||
		capabilities.SpecificationRevision() != version.SpecificationRevision ||
		capabilities.GoRequirement() != version.GoRequirement ||
		capabilities.GOOS() != runtime.GOOS || capabilities.GOARCH() != runtime.GOARCH ||
		capabilities.ProjectDocumentBytes() != applicationmeta.MaximumSize ||
		capabilities.InvocationConcurrencyLimit() != applicationmeta.DefaultInvocationConcurrencyLimit ||
		capabilities.MaximumConcurrencyLimit() != kernelinvocation.MaximumConcurrencyLimit ||
		capabilities.StartupTimeout() != applicationmeta.DefaultStartupTimeout ||
		capabilities.InvocationTimeout() != applicationmeta.DefaultInvocationTimeout {
		t.Fatalf("installed facts = %#v", capabilities)
	}
	wantSchemas := []string{
		"continuation|false||0",
		"diagnostic|false||0",
		"graph|true|plystra.graph|1",
		"inspection|true|plystra.inspect|1",
		"recovery|true|plystra.recovery|1",
		"result|true|plystra.result|1",
	}
	gotSchemas := make([]string, 0, len(capabilities.Schemas()))
	for _, schema := range capabilities.Schemas() {
		gotSchemas = append(gotSchemas, fmt.Sprintf("%s|%t|%s|%d", schema.Role(), schema.Available(), schema.Name(), schema.Version()))
	}
	if !reflect.DeepEqual(gotSchemas, wantSchemas) {
		t.Fatalf("schemas = %#v, want %#v", gotSchemas, wantSchemas)
	}
	wantCommandIDs := []string{
		"add",
		"capability.create",
		"capability.expose",
		"capability.implement",
		"check",
		"explain.alias",
		"explain.capability",
		"explain.config",
		"explain.exposure",
		"explain.plugin",
		"generate",
		"guidance.check",
		"guidance.sync",
		"help",
		"implement",
		"inspect",
		"inspect.capabilities",
		"inspect.configuration",
		"inspect.implementations",
		"inspect.interfaces",
		"inspect.modules",
		"inspect.resources",
		"interface.create",
		"new",
		"plugin.create",
		"remove",
		"update",
		"use",
		"version",
	}
	commandsByID := make(map[string]commandschema.CapabilityCommand, len(capabilities.Commands()))
	gotCommandIDs := make([]string, 0, len(capabilities.Commands()))
	for _, command := range capabilities.Commands() {
		gotCommandIDs = append(gotCommandIDs, command.ID())
		commandsByID[command.ID()] = command
	}
	if !reflect.DeepEqual(gotCommandIDs, wantCommandIDs) {
		t.Fatalf("commands = %#v, want %#v", gotCommandIDs, wantCommandIDs)
	}
	assertInstalledCommandFacts(t, commandsByID)

	selectors := capabilities.Selectors()
	if len(selectors) != 2 || selectors[0].ID() != "configuration" || selectors[1].ID() != "plugin-target" {
		t.Fatalf("selectors = %#v", selectors)
	}
	configurationDefault, configurationHasDefault := selectors[0].DefaultMode()
	pluginDefault, pluginHasDefault := selectors[1].DefaultMode()
	if !reflect.DeepEqual(selectors[0].Arguments(), []string{"--config", "--env"}) ||
		!reflect.DeepEqual(selectors[0].EnvironmentVariables(), []string{"PLYSTRA_CONFIG", "PLYSTRA_ENV"}) ||
		!reflect.DeepEqual(selectors[0].Modes(), []string{"default", "environment", "explicit-config"}) ||
		!configurationHasDefault || configurationDefault != "default" ||
		!reflect.DeepEqual(selectors[1].Arguments(), []string{"--interactive", "--plugin"}) ||
		len(selectors[1].EnvironmentVariables()) != 0 ||
		!reflect.DeepEqual(selectors[1].Modes(), []string{"enclosing", "explicit", "explicit-interactive", "sole"}) ||
		pluginHasDefault || pluginDefault != "" {
		t.Fatalf("selector facts = %#v / %#v", selectors[0], selectors[1])
	}
	if capabilities.DefaultInteraction() != commandschema.CapabilityInteractionNonInteractive || capabilities.DefaultOutput() != commandschema.CapabilityOutputHuman {
		t.Fatalf("global defaults = interaction %q, output %q", capabilities.DefaultInteraction(), capabilities.DefaultOutput())
	}
	wantEffects := []commandschema.EffectClass{
		commandschema.EffectProjectWrite,
		commandschema.EffectTemporaryFile,
		commandschema.EffectCacheMaterialization,
		commandschema.EffectDownload,
		commandschema.EffectTrustedCodeExecution,
		commandschema.EffectProcessStartup,
		commandschema.EffectBackendRead,
		commandschema.EffectBackendWrite,
		commandschema.EffectPublication,
	}
	if !reflect.DeepEqual(capabilities.EffectClasses(), wantEffects) {
		t.Fatalf("effect classes = %#v, want %#v", capabilities.EffectClasses(), wantEffects)
	}
	for _, planned := range []string{"build", "capability.require", "data.migration.apply", "data.migration.plan", "data.migration.status", "dev", "doctor", "fix", "release", "sdk.link", "sdk.pack", "sdk.publish", "test"} {
		if _, exists := commandsByID[planned]; exists {
			t.Fatalf("planned command %q reported as installed", planned)
		}
	}
	wantSupport := []string{
		"data|yes|no|no|no|no",
		"data.compiler|yes|no|no|no|no",
		"inspect.capabilities|yes|yes|not_applicable|yes|yes",
		"interfaces.policies.*.retry.backoff|yes|yes|yes|yes|yes",
		"interfaces.policies.*.retry.eligibility|yes|yes|yes|yes|yes",
		"interfaces.policies.*.retry.max_attempts|yes|yes|yes|yes|yes",
		"interfaces.policies.*.timeout|yes|yes|yes|yes|yes",
		"invocation.default-concurrency|yes|not_applicable|yes|yes|yes",
		"legacy.capability-timeout|yes|yes|yes|no|no",
		"resource|yes|no|no|no|no",
		"resource.consumer.discovery|yes|yes|not_applicable|not_applicable|yes",
		"resource.contract|yes|yes|not_applicable|not_applicable|yes",
		"resource.provider.discovery|yes|yes|not_applicable|not_applicable|yes",
		"template.interface-inheritance|yes|yes|yes|yes|yes",
		"transport.connect|yes|yes|yes|yes|yes",
	}
	gotSupport := make([]string, 0, len(capabilities.Support()))
	for _, support := range capabilities.Support() {
		gotSupport = append(gotSupport, strings.Join([]string{
			support.ID(),
			string(support.Specified()),
			string(support.Parsed()),
			string(support.Generated()),
			string(support.Executed()),
			string(support.Accepted()),
		}, "|"))
	}
	if !reflect.DeepEqual(gotSupport, wantSupport) {
		t.Fatalf("support = %#v, want %#v", gotSupport, wantSupport)
	}

	wantToolchain, err := transporttoolchain.Current()
	if err != nil {
		t.Fatalf("transporttoolchain.Current: %v", err)
	}
	gotToolchain := capabilities.TransportToolchain()
	if !gotToolchain.Valid() || len(gotToolchain.Components()) != 13 || gotToolchain.Digest() != wantToolchain.Digest() || !bytes.Equal(gotToolchain.RecordJSON(), wantToolchain.RecordJSON()) {
		t.Fatalf("transport toolchain = %#v", gotToolchain)
	}
}

func assertInstalledCommandFacts(t testing.TB, commands map[string]commandschema.CapabilityCommand) {
	t.Helper()

	newCommand := commands["new"]
	newArguments := installedArgumentsByName(newCommand)
	if _, exists := newArguments["--adopt-export"]; exists {
		t.Fatal("new command advertises removed export adoption option")
	}
	if len(newArguments) != 9 || newArguments["project-name"].Kind() != commandschema.CapabilityArgumentPositional || newArguments["project-name"].Position() != 1 || !newArguments["project-name"].Required() ||
		!reflect.DeepEqual(newArguments["--format"].Choices(), []string{"human", "json"}) ||
		!reflect.DeepEqual(newCommand.InteractionModes(), []commandschema.CapabilityInteractionMode{commandschema.CapabilityInteractionNonInteractive, commandschema.CapabilityInteractionExplicit}) ||
		!reflect.DeepEqual(newCommand.OutputFormats(), []commandschema.CapabilityOutputFormat{commandschema.CapabilityOutputHuman, commandschema.CapabilityOutputJSON}) ||
		!reflect.DeepEqual(installedDefaultFacts(newCommand), []string{"agent-guidance=enabled", "git=disabled", "github-ci=disabled", "module-path=project-name"}) {
		t.Fatalf("new command facts = %#v", newCommand)
	}

	capabilityCreate := commands["capability.create"]
	if !reflect.DeepEqual(capabilityCreate.Selectors(), []string{"plugin-target"}) || !reflect.DeepEqual(capabilityCreate.InteractionModes(), []commandschema.CapabilityInteractionMode{commandschema.CapabilityInteractionNonInteractive, commandschema.CapabilityInteractionExplicit}) {
		t.Fatalf("capability.create facts = %#v", capabilityCreate)
	}
	implementArguments := installedArgumentsByName(commands["implement"])
	if !implementArguments["--package"].Required() {
		t.Fatalf("implement --package is not required: %#v", commands["implement"])
	}
	for _, id := range []string{"inspect", "inspect.configuration", "inspect.implementations", "inspect.interfaces", "inspect.modules", "inspect.resources", "explain.alias", "explain.capability", "explain.config", "explain.exposure", "explain.plugin"} {
		command := commands[id]
		arguments := installedArgumentsByName(command)
		if !reflect.DeepEqual(command.Selectors(), []string{"configuration"}) || !reflect.DeepEqual(installedDefaultFacts(command), []string{"verbosity=concise"}) ||
			arguments["--verbose"].Value() != commandschema.CapabilityArgumentFlag || !reflect.DeepEqual(arguments["--format"].Choices(), []string{"human", "json"}) {
			t.Fatalf("project inspection command %q facts = %#v", id, command)
		}
	}
	capabilitiesCommand := commands["inspect.capabilities"]
	if len(capabilitiesCommand.Arguments()) != 1 || capabilitiesCommand.Arguments()[0].Name() != "--format" || len(capabilitiesCommand.Selectors()) != 0 || !reflect.DeepEqual(capabilitiesCommand.OutputFormats(), []commandschema.CapabilityOutputFormat{commandschema.CapabilityOutputHuman, commandschema.CapabilityOutputJSON}) {
		t.Fatalf("inspect.capabilities facts = %#v", capabilitiesCommand)
	}
	generateArguments := installedArgumentsByName(commands["generate"])
	if generateArguments["--check"].Value() != commandschema.CapabilityArgumentFlag || !reflect.DeepEqual(commands["generate"].Selectors(), []string{"configuration"}) {
		t.Fatalf("generate facts = %#v", commands["generate"])
	}
}

func installedArgumentsByName(command commandschema.CapabilityCommand) map[string]commandschema.CapabilityArgument {
	result := make(map[string]commandschema.CapabilityArgument, len(command.Arguments()))
	for _, argument := range command.Arguments() {
		result[argument.Name()] = argument
	}
	return result
}

func installedDefaultFacts(command commandschema.CapabilityCommand) []string {
	result := make([]string, 0, len(command.StableDefaults()))
	for _, value := range command.StableDefaults() {
		result = append(result, value.Name()+"="+value.Value())
	}
	return result
}

func TestCurrentIsDeterministicAndProjectIndependent(t *testing.T) {
	t.Parallel()

	first, err := installedcapabilities.Current()
	if err != nil {
		t.Fatalf("first Current: %v", err)
	}
	second, err := installedcapabilities.Current()
	if err != nil {
		t.Fatalf("second Current: %v", err)
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) {
		t.Fatalf("Current is not deterministic:\nfirst  %s\nsecond %s", first.CanonicalJSON(), second.CanonicalJSON())
	}
	policy := first.InvocationPolicy()
	if policy.RetryEligibility != "replay_safe" || policy.RetryDefaultAttempts != 2 || policy.MaximumRetryAttempts != 16 || policy.RetryDefaultBackoff != 0 || policy.MaximumRetryBackoff != 1<<63-1 {
		t.Fatalf("retry support facts = %#v", policy)
	}
	if first.InvocationTimeout() != 0 || first.InvocationTimeoutText() != "0s" || policy.SchemaVersion != 1 || policy.CompilerVersion != 1 || policy.DefaultsVersion != 1 || policy.MaximumTimeout != 1<<63-1 || policy.DurationBytes != 64 || policy.DefaultAttempts != 1 || policy.CircuitEnabled {
		t.Fatalf("compiled policy support facts = %#v", policy)
	}
	lower := strings.ToLower(string(first.CanonicalJSON()))
	for _, forbidden := range []string{"working_directory", "environment_name", "configuration_path", "timestamp", `c:\\`, `d:\\`} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("installed capabilities contain Project or machine state %q: %s", forbidden, first.CanonicalJSON())
		}
	}
}
