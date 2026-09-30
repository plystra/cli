package applicationresolve

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/invocationpolicy"
	"github.com/plystra/cli/internal/version"
)

// ErrPolicyNotEnforced reports an authored active policy whose installed
// generation and runtime execution support are not both available.
var ErrPolicyNotEnforced = errors.New("active Interface policy is not enforced")

// PolicyNotEnforcedError retains only public policy identity, installed support
// facts, and module-relative declarations, never configuration values.
type PolicyNotEnforcedError struct {
	interfaceID   interfaceid.Identifier
	field         string
	cliVersion    string
	kernelVersion string
	support       commandschema.CapabilitySupport
	sources       []applicationinput.ConfigurationSource
}

// InterfaceID returns the exact reachable Interface with an unsupported policy.
func (e *PolicyNotEnforcedError) InterfaceID() interfaceid.Identifier {
	if e == nil {
		return interfaceid.Identifier{}
	}
	return e.interfaceID
}

// Field returns the authored policy field, relative to the Interface entry.
func (e *PolicyNotEnforcedError) Field() string {
	if e == nil {
		return ""
	}
	return e.field
}

// Support returns the immutable installed support-stage record.
func (e *PolicyNotEnforcedError) Support() commandschema.CapabilitySupport {
	if e == nil {
		return commandschema.CapabilitySupport{}
	}
	return e.support
}

// Sources returns a defensive copy of the effective declaration locations.
func (e *PolicyNotEnforcedError) Sources() []applicationinput.ConfigurationSource {
	if e == nil {
		return nil
	}
	return append([]applicationinput.ConfigurationSource(nil), e.sources...)
}

func (e *PolicyNotEnforcedError) Error() string {
	if e == nil {
		return ErrPolicyNotEnforced.Error()
	}
	return fmt.Sprintf("%s: interfaces.policies[%q].%s; installed CLI %s, Kernel %s; specified=%s parsed=%s generated=%s executed=%s accepted=%s",
		ErrPolicyNotEnforced, e.interfaceID, e.field, e.cliVersion, e.kernelVersion,
		e.support.Specified(), e.support.Parsed(), e.support.Generated(), e.support.Executed(), e.support.Accepted())
}

// Unwrap identifies policy enforcement failure through ordinary error wrapping.
func (*PolicyNotEnforcedError) Unwrap() error { return ErrPolicyNotEnforced }

func validateExecutablePolicies(manifest applicationmeta.Manifest, interfaces interfaceresolution.Result, legacy generation.Context, sources applicationinput.SourceContext) error {
	policies := manifest.InterfacePolicies()
	if len(policies) == 0 {
		return nil
	}
	active := make(map[string]bool)
	for _, binding := range interfaces.Graph().Bindings() {
		active[binding.InterfaceID().String()] = true
	}
	legacyActive := make(map[string]bool)
	for _, requirement := range legacy.Requirements() {
		legacyActive[requirement.String()] = true
	}
	timeoutSupport, err := commandschema.NewCapabilitySupport(invocationpolicy.TimeoutSupport())
	if err != nil {
		return err
	}
	legacySupport, err := commandschema.NewCapabilitySupport(invocationpolicy.LegacyTimeoutSupport())
	if err != nil {
		return err
	}
	for _, policy := range policies {
		support := timeoutSupport
		if !active[policy.InterfaceID().String()] && !legacyActive[policy.InterfaceID().String()] {
			continue
		}
		if legacyActive[policy.InterfaceID().String()] {
			support = legacySupport
		}
		if support.Generated() == commandschema.SupportYes && support.Executed() == commandschema.SupportYes {
			continue
		}
		field := fmt.Sprintf("interfaces.policies[%q].timeout", policy.InterfaceID())
		locations, err := applicationinput.ConfigurationSources(sources, policy.Source(), field)
		if err != nil {
			return fmt.Errorf("policy %s provenance: %w", policy.InterfaceID(), err)
		}
		slices.SortFunc(locations, func(a, b applicationinput.ConfigurationSource) int {
			if cmp := strings.Compare(a.ModulePath, b.ModulePath); cmp != 0 {
				return cmp
			}
			if cmp := strings.Compare(a.Path, b.Path); cmp != 0 {
				return cmp
			}
			if a.Line != b.Line {
				return a.Line - b.Line
			}
			return a.Column - b.Column
		})
		return &PolicyNotEnforcedError{interfaceID: policy.InterfaceID(), field: "timeout", cliVersion: version.Current, kernelVersion: version.KernelVersion, support: support, sources: locations}
	}
	return nil
}
