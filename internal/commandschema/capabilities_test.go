package commandschema_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/transporttoolchain"
)

func TestCapabilitiesAreCanonicalClosedAndDefensive(t *testing.T) {
	t.Parallel()

	input := validCapabilitiesInput(t)
	input.Support = []commandschema.CapabilitySupportInput{
		{
			ID:        "inspect.capabilities",
			Specified: commandschema.SupportYes,
			Parsed:    commandschema.SupportYes,
			Generated: commandschema.SupportNotApplicable,
			Executed:  commandschema.SupportYes,
			Accepted:  commandschema.SupportYes,
		},
		{
			ID:        "data",
			Specified: commandschema.SupportYes,
			Parsed:    commandschema.SupportNo,
			Generated: commandschema.SupportNo,
			Executed:  commandschema.SupportNo,
			Accepted:  commandschema.SupportNo,
		},
	}
	payload, err := commandschema.NewCapabilities(input)
	if err != nil || !payload.Valid() {
		t.Fatalf("NewCapabilities = %#v, %v", payload, err)
	}
	want := fmt.Sprintf(
		`{"schema":"plystra.capabilities/v1","installed":{"cli_version":"1.2.3","kernel_version":"v1.4.0","specification_revision":"0123456789abcdef0123456789abcdef01234567","go_requirement":"1.26","platform":{"goos":"testos","goarch":"testarch"},"transport_toolchain":%s},"schemas":[{"role":"continuation","available":false,"name":null,"version":null},{"role":"diagnostic","available":false,"name":null,"version":null},{"role":"graph","available":true,"name":"plystra.graph","version":1},{"role":"inspection","available":true,"name":"plystra.inspect","version":1},{"role":"recovery","available":true,"name":"plystra.recovery","version":1},{"role":"result","available":true,"name":"plystra.result","version":1}],"commands":[{"id":"inspect","path":["inspect"],"arguments":[{"name":"--config","kind":"option","position":null,"value":"string","required":false,"repeatable":false,"choices":[],"requires":[],"conflicts":["--env"]},{"name":"--env","kind":"option","position":null,"value":"string","required":false,"repeatable":false,"choices":[],"requires":[],"conflicts":["--config"]},{"name":"--format","kind":"option","position":null,"value":"string","required":false,"repeatable":false,"choices":["human","json"],"requires":[],"conflicts":[]}],"selectors":["configuration"],"stable_defaults":[{"name":"verbosity","value":"concise"}],"interaction_modes":["non_interactive"],"output_formats":["human","json"]}],"selectors":[{"id":"configuration","arguments":["--config","--env"],"environment_variables":["PLYSTRA_CONFIG","PLYSTRA_ENV"],"modes":["default","environment","explicit-config"],"default_mode":"default"}],"effect_classes":["project_write","temporary_file","cache_materialization","download","trusted_code_execution","process_startup","backend_read","backend_write","publication"],"limits":{"project_document_bytes":1048576},"defaults":{"interaction_mode":"non_interactive","output_format":"human","startup_timeout":"2m","invocation_timeout":"30s"},"support":[{"id":"data","specified":"yes","parsed":"no","generated":"no","executed":"no","accepted":"no"},{"id":"inspect.capabilities","specified":"yes","parsed":"yes","generated":"not_applicable","executed":"yes","accepted":"yes"}]}`,
		input.TransportToolchain.RecordJSON(),
	)
	if string(payload.CanonicalJSON()) != want {
		t.Fatalf("CanonicalJSON = %s\nwant = %s", payload.CanonicalJSON(), want)
	}
	if payload.Schema() != commandschema.CapabilitiesSchemaV1 ||
		payload.CLIVersion() != input.CLIVersion ||
		payload.KernelVersion() != input.KernelVersion ||
		payload.SpecificationRevision() != input.SpecificationRevision ||
		payload.GoRequirement() != input.GoRequirement ||
		payload.GOOS() != input.GOOS || payload.GOARCH() != input.GOARCH ||
		len(payload.Schemas()) != 6 || payload.Schemas()[0].Role() != commandschema.CapabilitySchemaContinuation || payload.Schemas()[0].Available() || payload.Schemas()[5].Name() != commandschema.ResultSchemaName || payload.Schemas()[5].Version() != commandschema.ResultSchemaVersion ||
		len(payload.Commands()) != 1 || payload.Commands()[0].ID() != "inspect" || len(payload.Commands()[0].Arguments()) != 3 || payload.Commands()[0].Arguments()[0].Name() != "--config" ||
		len(payload.Selectors()) != 1 || payload.Selectors()[0].ID() != "configuration" || payload.DefaultInteraction() != commandschema.CapabilityInteractionNonInteractive || payload.DefaultOutput() != commandschema.CapabilityOutputHuman || len(payload.EffectClasses()) != 9 ||
		payload.ProjectDocumentBytes() != input.ProjectDocumentBytes ||
		payload.StartupTimeoutText() != "2m" || payload.InvocationTimeoutText() != "30s" {
		t.Fatalf("capability accessors do not match input: %#v", payload)
	}

	input.Schemas[0].Name = "changed"
	input.Commands[0].ID = "changed"
	input.Commands[0].Arguments[0].Name = "--changed"
	input.Selectors[0].ID = "changed"
	input.EffectClasses[0] = "changed"
	input.Support[0].ID = "changed"
	returnedSchemas := payload.Schemas()
	returnedSchemas[0] = commandschema.CapabilitySchema{}
	returnedCommands := payload.Commands()
	returnedCommands[0] = commandschema.CapabilityCommand{}
	returnedArguments := payload.Commands()[0].Arguments()
	returnedArguments[0] = commandschema.CapabilityArgument{}
	returnedSelectors := payload.Selectors()
	returnedSelectors[0] = commandschema.CapabilitySelector{}
	returnedEffects := payload.EffectClasses()
	returnedEffects[0] = "changed"
	returnedSupport := payload.Support()
	returnedSupport[0] = commandschema.CapabilitySupport{}
	returnedComponents := payload.TransportToolchain().Components()
	returnedComponents[0] = transporttoolchain.Component{}
	canonical := payload.CanonicalJSON()
	canonical[0] = '['
	if !payload.Valid() || payload.Schemas()[0].Role() != commandschema.CapabilitySchemaContinuation || payload.Commands()[0].ID() != "inspect" || payload.Commands()[0].Arguments()[0].Name() != "--config" || payload.Selectors()[0].ID() != "configuration" || payload.EffectClasses()[0] != commandschema.EffectProjectWrite || payload.Support()[0].ID() != "data" || bytes.HasPrefix(payload.CanonicalJSON(), []byte("[")) || len(payload.TransportToolchain().Components()) != 13 {
		t.Fatal("Capabilities exposed mutable construction or result state")
	}
}

func TestCapabilitiesCanBeNestedInCanonicalResult(t *testing.T) {
	t.Parallel()

	payload, err := commandschema.NewCapabilities(validCapabilitiesInput(t))
	if err != nil {
		t.Fatalf("NewCapabilities: %v", err)
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{})
	if err != nil {
		t.Fatalf("NewEffects: %v", err)
	}
	result, err := commandschema.NewResult(commandschema.ResultInput{
		Operation:    "inspect.capabilities",
		InvocationID: testInvocationID,
		Status:       commandschema.StatusSuccess,
		Effects:      effects,
		Payload:      payload,
	})
	if err != nil || !result.Valid() || !bytes.Contains(result.CanonicalJSON(), payload.CanonicalJSON()) {
		t.Fatalf("NewResult = %#v, %v", result, err)
	}
}

func TestCapabilitiesRejectInvalidInstalledFactsAndSupport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*commandschema.CapabilitiesInput)
	}{
		{name: "CLI version", mutate: func(input *commandschema.CapabilitiesInput) { input.CLIVersion = "v1.2.3" }},
		{name: "Kernel version", mutate: func(input *commandschema.CapabilitiesInput) { input.KernelVersion = "1.4.0" }},
		{name: "specification revision", mutate: func(input *commandschema.CapabilitiesInput) { input.SpecificationRevision = "ABC" }},
		{name: "Go requirement", mutate: func(input *commandschema.CapabilitiesInput) { input.GoRequirement = "1.26.0" }},
		{name: "GOOS", mutate: func(input *commandschema.CapabilitiesInput) { input.GOOS = "Test OS" }},
		{name: "GOARCH", mutate: func(input *commandschema.CapabilitiesInput) { input.GOARCH = "" }},
		{name: "toolchain", mutate: func(input *commandschema.CapabilitiesInput) { input.TransportToolchain = transporttoolchain.Identity{} }},
		{name: "missing schemas", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas = nil }},
		{name: "schema role", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[0].Role = "future" }},
		{name: "duplicate schema", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[0].Role = input.Schemas[1].Role }},
		{name: "unavailable schema identity", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[2].Name = "plystra.continuation" }},
		{name: "unavailable schema version", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[2].Version = 1 }},
		{name: "available schema name", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[0].Name = "Graph" }},
		{name: "embedded schema version", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[0].Name = "plystra.result/v1" }},
		{name: "wildcard schema name", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[0].Name = "plystra.*" }},
		{name: "available schema version", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[0].Version = 0 }},
		{name: "excessive schema version", mutate: func(input *commandschema.CapabilitiesInput) { input.Schemas[0].Version = 1 << 31 }},
		{name: "missing commands", mutate: func(input *commandschema.CapabilitiesInput) { input.Commands = nil }},
		{name: "command ID", mutate: func(input *commandschema.CapabilitiesInput) { input.Commands[0].ID = "Inspect" }},
		{name: "command path", mutate: func(input *commandschema.CapabilitiesInput) { input.Commands[0].Path = []string{"explain"} }},
		{name: "duplicate command", mutate: func(input *commandschema.CapabilitiesInput) {
			input.Commands = append(input.Commands, input.Commands[0])
		}},
		{name: "command argument", mutate: func(input *commandschema.CapabilitiesInput) { input.Commands[0].Arguments[0].Name = "config" }},
		{name: "asymmetric conflict", mutate: func(input *commandschema.CapabilitiesInput) { input.Commands[0].Arguments[1].Conflicts = nil }},
		{name: "required conflict", mutate: func(input *commandschema.CapabilitiesInput) {
			input.Commands[0].Arguments[1].Requires = []string{"--config"}
		}},
		{name: "format choices", mutate: func(input *commandschema.CapabilitiesInput) {
			input.Commands[0].Arguments[0].Choices = []string{"human"}
		}},
		{name: "unknown selector", mutate: func(input *commandschema.CapabilitiesInput) { input.Commands[0].Selectors[0] = "future" }},
		{name: "selector argument absent", mutate: func(input *commandschema.CapabilitiesInput) {
			input.Commands[0].Arguments = input.Commands[0].Arguments[2:]
		}},
		{name: "missing selectors", mutate: func(input *commandschema.CapabilitiesInput) { input.Selectors = nil }},
		{name: "selector ID", mutate: func(input *commandschema.CapabilitiesInput) { input.Selectors[0].ID = "Configuration" }},
		{name: "selector environment", mutate: func(input *commandschema.CapabilitiesInput) {
			input.Selectors[0].EnvironmentVariables[0] = "plystra_config"
		}},
		{name: "selector default", mutate: func(input *commandschema.CapabilitiesInput) { input.Selectors[0].DefaultMode = "future" }},
		{name: "unused selector", mutate: func(input *commandschema.CapabilitiesInput) { input.Commands[0].Selectors = nil }},
		{name: "default interaction", mutate: func(input *commandschema.CapabilitiesInput) { input.DefaultInteraction = "terminal" }},
		{name: "unsupported command default interaction", mutate: func(input *commandschema.CapabilitiesInput) {
			input.Commands[0].InteractionModes = []commandschema.CapabilityInteractionMode{commandschema.CapabilityInteractionExplicit}
			input.Commands[0].Arguments = append(input.Commands[0].Arguments, commandschema.CapabilityArgumentInput{Name: "--interactive", Kind: commandschema.CapabilityArgumentOption, Value: commandschema.CapabilityArgumentFlag})
		}},
		{name: "interactive string", mutate: func(input *commandschema.CapabilitiesInput) {
			input.Commands[0].InteractionModes = append(input.Commands[0].InteractionModes, commandschema.CapabilityInteractionExplicit)
			input.Commands[0].Arguments = append(input.Commands[0].Arguments, commandschema.CapabilityArgumentInput{Name: "--interactive", Kind: commandschema.CapabilityArgumentOption, Value: commandschema.CapabilityArgumentString})
		}},
		{name: "default output", mutate: func(input *commandschema.CapabilitiesInput) { input.DefaultOutput = "yaml" }},
		{name: "unsupported command default output", mutate: func(input *commandschema.CapabilitiesInput) {
			input.Commands[0].OutputFormats = []commandschema.CapabilityOutputFormat{commandschema.CapabilityOutputJSON}
		}},
		{name: "missing effect classes", mutate: func(input *commandschema.CapabilitiesInput) { input.EffectClasses = nil }},
		{name: "unknown effect class", mutate: func(input *commandschema.CapabilitiesInput) { input.EffectClasses[0] = "filesystem" }},
		{name: "duplicate effect class", mutate: func(input *commandschema.CapabilitiesInput) { input.EffectClasses[0] = input.EffectClasses[1] }},
		{name: "document limit", mutate: func(input *commandschema.CapabilitiesInput) { input.ProjectDocumentBytes = 0 }},
		{name: "startup timeout", mutate: func(input *commandschema.CapabilitiesInput) { input.StartupTimeout = 0 }},
		{name: "invocation timeout", mutate: func(input *commandschema.CapabilitiesInput) { input.InvocationTimeout = -time.Second }},
		{name: "missing support", mutate: func(input *commandschema.CapabilitiesInput) { input.Support = nil }},
		{name: "support ID", mutate: func(input *commandschema.CapabilitiesInput) { input.Support[0].ID = "Inspect.Capabilities" }},
		{name: "support stage", mutate: func(input *commandschema.CapabilitiesInput) { input.Support[0].Accepted = "future" }},
		{name: "duplicate support", mutate: func(input *commandschema.CapabilitiesInput) { input.Support = append(input.Support, input.Support[0]) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := validCapabilitiesInput(t)
			test.mutate(&input)
			payload, err := commandschema.NewCapabilities(input)
			if !errors.Is(err, commandschema.ErrCapabilities) || payload.Valid() {
				t.Fatalf("NewCapabilities = %#v, %v; want ErrCapabilities", payload, err)
			}
		})
	}
}

func validCapabilitiesInput(t testing.TB) commandschema.CapabilitiesInput {
	t.Helper()
	toolchain, err := transporttoolchain.Current()
	if err != nil {
		t.Fatalf("transporttoolchain.Current: %v", err)
	}
	return commandschema.CapabilitiesInput{
		CLIVersion:            "1.2.3",
		KernelVersion:         "v1.4.0",
		SpecificationRevision: "0123456789abcdef0123456789abcdef01234567",
		GoRequirement:         "1.26",
		GOOS:                  "testos",
		GOARCH:                "testarch",
		TransportToolchain:    toolchain,
		Schemas: []commandschema.CapabilitySchemaInput{
			{Role: commandschema.CapabilitySchemaResult, Available: true, Name: commandschema.ResultSchemaName, Version: commandschema.ResultSchemaVersion},
			{Role: commandschema.CapabilitySchemaGraph, Available: true, Name: "plystra.graph", Version: 1},
			{Role: commandschema.CapabilitySchemaContinuation},
			{Role: commandschema.CapabilitySchemaInspection, Available: true, Name: "plystra.inspect", Version: 1},
			{Role: commandschema.CapabilitySchemaDiagnostic},
			{Role: commandschema.CapabilitySchemaRecovery, Available: true, Name: commandschema.RecoverySchemaName, Version: commandschema.RecoverySchemaVersion},
		},
		Commands: []commandschema.CapabilityCommandInput{{
			ID:   "inspect",
			Path: []string{"inspect"},
			Arguments: []commandschema.CapabilityArgumentInput{
				{Name: "--format", Kind: commandschema.CapabilityArgumentOption, Value: commandschema.CapabilityArgumentString, Choices: []string{"json", "human"}},
				{Name: "--env", Kind: commandschema.CapabilityArgumentOption, Value: commandschema.CapabilityArgumentString, Conflicts: []string{"--config"}},
				{Name: "--config", Kind: commandschema.CapabilityArgumentOption, Value: commandschema.CapabilityArgumentString, Conflicts: []string{"--env"}},
			},
			Selectors:        []string{"configuration"},
			StableDefaults:   []commandschema.CapabilityDefaultInput{{Name: "verbosity", Value: "concise"}},
			InteractionModes: []commandschema.CapabilityInteractionMode{commandschema.CapabilityInteractionNonInteractive},
			OutputFormats:    []commandschema.CapabilityOutputFormat{commandschema.CapabilityOutputJSON, commandschema.CapabilityOutputHuman},
		}},
		Selectors: []commandschema.CapabilitySelectorInput{{
			ID:                   "configuration",
			Arguments:            []string{"--env", "--config"},
			EnvironmentVariables: []string{"PLYSTRA_ENV", "PLYSTRA_CONFIG"},
			Modes:                []string{"explicit-config", "default", "environment"},
			DefaultMode:          "default",
		}},
		DefaultInteraction: commandschema.CapabilityInteractionNonInteractive,
		DefaultOutput:      commandschema.CapabilityOutputHuman,
		EffectClasses: []commandschema.EffectClass{
			commandschema.EffectPublication,
			commandschema.EffectBackendWrite,
			commandschema.EffectBackendRead,
			commandschema.EffectProcessStartup,
			commandschema.EffectTrustedCodeExecution,
			commandschema.EffectDownload,
			commandschema.EffectCacheMaterialization,
			commandschema.EffectTemporaryFile,
			commandschema.EffectProjectWrite,
		},
		ProjectDocumentBytes: 1 << 20,
		StartupTimeout:       2 * time.Minute,
		InvocationTimeout:    30 * time.Second,
		Support: []commandschema.CapabilitySupportInput{{
			ID:        "interfaces.policies.*.timeout",
			Specified: commandschema.SupportYes,
			Parsed:    commandschema.SupportYes,
			Generated: commandschema.SupportYes,
			Executed:  commandschema.SupportNo,
			Accepted:  commandschema.SupportNo,
		}},
	}
}
