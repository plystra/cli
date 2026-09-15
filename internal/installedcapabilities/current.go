// Package installedcapabilities constructs Project-independent facts for the
// exact running Plystra CLI distribution.
package installedcapabilities

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/transporttoolchain"
	"github.com/plystra/cli/internal/version"
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
		CLIVersion:            version.Current,
		KernelVersion:         version.KernelVersion,
		SpecificationRevision: version.SpecificationRevision,
		GoRequirement:         version.GoRequirement,
		GOOS:                  runtime.GOOS,
		GOARCH:                runtime.GOARCH,
		TransportToolchain:    toolchain,
		ProjectDocumentBytes:  applicationmeta.MaximumSize,
		StartupTimeout:        applicationmeta.DefaultStartupTimeout,
		InvocationTimeout:     applicationmeta.DefaultInvocationTimeout,
		Support: []commandschema.CapabilitySupportInput{
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
			{
				ID:        "interfaces.policies.*.timeout",
				Specified: commandschema.SupportYes,
				Parsed:    commandschema.SupportYes,
				Generated: commandschema.SupportYes,
				Executed:  commandschema.SupportNo,
				Accepted:  commandschema.SupportNo,
			},
			{
				ID:        "resource",
				Specified: commandschema.SupportYes,
				Parsed:    commandschema.SupportNo,
				Generated: commandschema.SupportNo,
				Executed:  commandschema.SupportNo,
				Accepted:  commandschema.SupportNo,
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
		},
	})
	if err != nil {
		return commandschema.Capabilities{}, fmt.Errorf("%w: %v", ErrCurrent, err)
	}
	return capabilities, nil
}
