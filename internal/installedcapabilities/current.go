// Package installedcapabilities constructs Project-independent facts for the
// exact running Plystra CLI distribution.
package installedcapabilities

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticschema"
	"github.com/plystra/cli/internal/invocationpolicy"
	"github.com/plystra/cli/internal/transporttoolchain"
	"github.com/plystra/cli/internal/version"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

// ErrCurrent reports an internally inconsistent installed distribution.
var ErrCurrent = errors.New("build installed Plystra capabilities")

// Current returns the immutable facts compiled into this CLI process. It does
// not inspect a Project, environment selector, path, VCS state, or clock.
func Current() (commandschema.Capabilities, error) {
	toolchain, err := transporttoolchain.Current()
	if err != nil {
		return commandschema.Capabilities{}, fmt.Errorf("%w: transport toolchain: %v", ErrCurrent, err)
	}
	capabilities, err := commandschema.NewCapabilities(commandschema.CapabilitiesInput{
		InvocationPolicy: commandschema.CapabilityInvocationPolicy{
			SchemaVersion:        kernelinvocation.PolicySchemaVersion,
			CompilerVersion:      kernelinvocation.PolicyCompilerVersion,
			DefaultsVersion:      kernelinvocation.PolicyDefaultsVersion,
			DurationBytes:        64,
			MaximumTimeout:       kernelinvocation.MaximumPolicyDuration,
			DefaultAttempts:      invocationpolicy.Default().Retry.MaxAttempts,
			RetryEligibility:     kernelinvocation.RetryReplaySafe,
			RetryDefaultAttempts: 2,
			MaximumRetryAttempts: kernelinvocation.MaximumRetryAttempts,
			MaximumRetryBackoff:  kernelinvocation.MaximumPolicyDuration,
		},
		CLIVersion:            version.Current,
		KernelVersion:         version.KernelVersion,
		SpecificationRevision: version.SpecificationRevision,
		GoRequirement:         version.GoRequirement,
		GOOS:                  runtime.GOOS,
		GOARCH:                runtime.GOARCH,
		TransportToolchain:    toolchain,
		Schemas: []commandschema.CapabilitySchemaInput{
			{Role: commandschema.CapabilitySchemaContinuation},
			{Role: commandschema.CapabilitySchemaDiagnostic},
			{
				Role:      commandschema.CapabilitySchemaGraph,
				Available: true,
				Name:      diagnosticschema.GraphSchemaV1().Name(),
				Version:   diagnosticschema.GraphSchemaV1().Version(),
			},
			{
				Role:      commandschema.CapabilitySchemaInspection,
				Available: true,
				Name:      diagnosticschema.InspectSchemaV1().Name(),
				Version:   diagnosticschema.InspectSchemaV1().Version(),
			},
			{
				Role:      commandschema.CapabilitySchemaRecovery,
				Available: true,
				Name:      commandschema.RecoverySchemaName,
				Version:   commandschema.RecoverySchemaVersion,
			},
			{
				Role:      commandschema.CapabilitySchemaResult,
				Available: true,
				Name:      commandschema.ResultSchemaName,
				Version:   commandschema.ResultSchemaVersion,
			},
		},
		Commands:                   installedCommands(),
		Selectors:                  installedSelectors(),
		DefaultInteraction:         commandschema.CapabilityInteractionNonInteractive,
		DefaultOutput:              commandschema.CapabilityOutputHuman,
		EffectClasses:              installedEffectClasses(),
		ProjectDocumentBytes:       applicationmeta.MaximumSize,
		StartupTimeout:             applicationmeta.DefaultStartupTimeout,
		InvocationTimeout:          applicationmeta.DefaultInvocationTimeout,
		InvocationConcurrencyLimit: applicationmeta.DefaultInvocationConcurrencyLimit,
		MaximumConcurrencyLimit:    kernelinvocation.MaximumConcurrencyLimit,
		Support: append([]commandschema.CapabilitySupportInput{
			{
				ID:        "invocation.default-concurrency",
				Specified: commandschema.SupportYes,
				Parsed:    commandschema.SupportNotApplicable,
				Generated: commandschema.SupportYes,
				Executed:  commandschema.SupportYes,
				Accepted:  commandschema.SupportYes,
			},
			{
				ID:        "inspect.capabilities",
				Specified: commandschema.SupportYes,
				Parsed:    commandschema.SupportYes,
				Generated: commandschema.SupportNotApplicable,
				Executed:  commandschema.SupportYes,
				Accepted:  commandschema.SupportYes,
			},
			{
				ID:        "transport.connect",
				Specified: commandschema.SupportYes,
				Parsed:    commandschema.SupportYes,
				Generated: commandschema.SupportYes,
				Executed:  commandschema.SupportYes,
				Accepted:  commandschema.SupportYes,
			},
			invocationpolicy.TimeoutSupport(),
			invocationpolicy.LegacyTimeoutSupport(),
			{
				ID:        "resource",
				Specified: commandschema.SupportYes,
				Parsed:    commandschema.SupportYes,
				Generated: commandschema.SupportYes,
				Executed:  commandschema.SupportYes,
				Accepted:  commandschema.SupportNo,
			},
			{
				ID:        "resource.consumer.discovery",
				Specified: commandschema.SupportYes, Parsed: commandschema.SupportYes,
				Generated: commandschema.SupportNotApplicable, Executed: commandschema.SupportNotApplicable,
				Accepted: commandschema.SupportYes,
			},
			{
				ID:        "resource.contract",
				Specified: commandschema.SupportYes, Parsed: commandschema.SupportYes,
				Generated: commandschema.SupportNotApplicable, Executed: commandschema.SupportNotApplicable,
				Accepted: commandschema.SupportYes,
			},
			{
				ID:        "resource.provider.discovery",
				Specified: commandschema.SupportYes, Parsed: commandschema.SupportYes,
				Generated: commandschema.SupportNotApplicable, Executed: commandschema.SupportNotApplicable,
				Accepted: commandschema.SupportYes,
			},
			{
				ID:        "resource.provider.scaffold",
				Specified: commandschema.SupportYes, Parsed: commandschema.SupportYes,
				Generated: commandschema.SupportYes, Executed: commandschema.SupportYes,
				Accepted: commandschema.SupportNo,
			},
			{
				ID:        "resource.provider.selection",
				Specified: commandschema.SupportYes, Parsed: commandschema.SupportYes,
				Generated: commandschema.SupportYes, Executed: commandschema.SupportYes,
				Accepted: commandschema.SupportNo,
			},
			{
				ID:        "data",
				Specified: commandschema.SupportYes,
				Parsed:    commandschema.SupportNo,
				Generated: commandschema.SupportNo,
				Executed:  commandschema.SupportNo,
				Accepted:  commandschema.SupportNo,
			},
			{
				ID:        "data.compiler",
				Specified: commandschema.SupportYes,
				Parsed:    commandschema.SupportNo,
				Generated: commandschema.SupportNo,
				Executed:  commandschema.SupportNo,
				Accepted:  commandschema.SupportNo,
			},
		}, invocationpolicy.RetrySupport()...),
	})
	if err != nil {
		return commandschema.Capabilities{}, fmt.Errorf("%w: %v", ErrCurrent, err)
	}
	return capabilities, nil
}
