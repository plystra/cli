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
		`{"schema":"plystra.capabilities/v1","installed":{"cli_version":"1.2.3","kernel_version":"v1.4.0","specification_revision":"0123456789abcdef0123456789abcdef01234567","go_requirement":"1.26","platform":{"goos":"testos","goarch":"testarch"},"transport_toolchain":%s},"schemas":[{"role":"continuation","available":false,"name":null,"version":null},{"role":"diagnostic","available":false,"name":null,"version":null},{"role":"graph","available":true,"name":"plystra.graph","version":1},{"role":"inspection","available":true,"name":"plystra.inspect","version":1},{"role":"recovery","available":true,"name":"plystra.recovery","version":1},{"role":"result","available":true,"name":"plystra.result","version":1}],"limits":{"project_document_bytes":1048576},"defaults":{"startup_timeout":"2m","invocation_timeout":"30s"},"support":[{"id":"data","specified":"yes","parsed":"no","generated":"no","executed":"no","accepted":"no"},{"id":"inspect.capabilities","specified":"yes","parsed":"yes","generated":"not_applicable","executed":"yes","accepted":"yes"}]}`,
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
		payload.ProjectDocumentBytes() != input.ProjectDocumentBytes ||
		payload.StartupTimeoutText() != "2m" || payload.InvocationTimeoutText() != "30s" {
		t.Fatalf("capability accessors do not match input: %#v", payload)
	}

	input.Schemas[0].Name = "changed"
	input.Support[0].ID = "changed"
	returnedSchemas := payload.Schemas()
	returnedSchemas[0] = commandschema.CapabilitySchema{}
	returnedSupport := payload.Support()
	returnedSupport[0] = commandschema.CapabilitySupport{}
	returnedComponents := payload.TransportToolchain().Components()
	returnedComponents[0] = transporttoolchain.Component{}
	canonical := payload.CanonicalJSON()
	canonical[0] = '['
	if !payload.Valid() || payload.Schemas()[0].Role() != commandschema.CapabilitySchemaContinuation || payload.Support()[0].ID() != "data" || bytes.HasPrefix(payload.CanonicalJSON(), []byte("[")) || len(payload.TransportToolchain().Components()) != 13 {
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
