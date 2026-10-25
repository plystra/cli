package applicationresolve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfaceresolution"
	"github.com/plystra/cli/internal/invocationpolicy"
	"github.com/plystra/cli/internal/moduledependency"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/version"
)

// ErrPolicyNotEnforced reports an authored active policy whose installed
// generation and runtime execution support are not both available.
var ErrPolicyNotEnforced = errors.New("active Interface policy is not enforced")

// ErrPolicySupportUnverified reports an operational failure while inspecting
// the application's selected runtime, rather than an incompatible contract.
var ErrPolicySupportUnverified = errors.New("selected Kernel policy support is unverified")

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

// KernelVersion identifies the application-selected version and replacement
// kind without revealing a local replacement's filesystem path.
func (e *PolicyNotEnforcedError) KernelVersion() string {
	if e == nil {
		return ""
	}
	return e.kernelVersion
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
	return fmt.Sprintf("%s: interfaces.policies[%q].%s; installed CLI %s, selected Kernel %s; specified=%s parsed=%s generated=%s executed=%s accepted=%s",
		ErrPolicyNotEnforced, e.interfaceID, e.field, e.cliVersion, e.kernelVersion,
		e.support.Specified(), e.support.Parsed(), e.support.Generated(), e.support.Executed(), e.support.Accepted())
}

// Unwrap identifies policy enforcement failure through ordinary error wrapping.
func (*PolicyNotEnforcedError) Unwrap() error { return ErrPolicyNotEnforced }

func validateExecutablePolicies(ctx context.Context, module modulelocate.Module, dependencies moduledependency.Index, options Options, manifest applicationmeta.Manifest, interfaces interfaceresolution.Result, legacy generation.Context, sources applicationinput.SourceContext) error {
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
	legacySupport, err := commandschema.NewCapabilitySupport(invocationpolicy.LegacyTimeoutSupport())
	if err != nil {
		return err
	}
	kernelVersion := selectedPolicyKernelVersion(dependencies)
	var kernelSupport invocationpolicy.KernelSupport
	kernelLoaded := false
	for _, policy := range policies {
		if !active[policy.InterfaceID().String()] && !legacyActive[policy.InterfaceID().String()] {
			continue
		}
		inputs := []commandschema.CapabilitySupportInput{invocationpolicy.TimeoutSupport()}
		if policy.RetryEligibility() != "" {
			inputs = append(inputs, invocationpolicy.RetrySupport()...)
		}
		unsupported := ""
		if !legacyActive[policy.InterfaceID().String()] {
			if !kernelLoaded {
				kernelSupport, err = loadPolicyKernelSupport(ctx, module, dependencies, options)
				if err != nil {
					return fmt.Errorf("%w: %w", ErrPolicySupportUnverified, err)
				}
				kernelLoaded = true
			}
			unsupported = kernelSupport.UnsupportedField(invocationpolicy.Compile(policy))
		}
		for _, input := range inputs {
			fieldName := strings.TrimPrefix(input.ID, "interfaces.policies.*.")
			if fieldName == unsupported {
				input.Executed, input.Accepted = commandschema.SupportNo, commandschema.SupportNo
			}
			support, err := commandschema.NewCapabilitySupport(input)
			if err != nil {
				return err
			}
			if legacyActive[policy.InterfaceID().String()] {
				support = legacySupport
			}
			if support.Generated() == commandschema.SupportYes && support.Executed() == commandschema.SupportYes {
				continue
			}
			field := fmt.Sprintf("interfaces.policies[%q]", policy.InterfaceID())
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
			return &PolicyNotEnforcedError{interfaceID: policy.InterfaceID(), field: fieldName, cliVersion: version.Current, kernelVersion: kernelVersion, support: support, sources: locations}
		}
	}
	return nil
}

func selectedPolicyKernelVersion(dependencies moduledependency.Index) string {
	for _, dependency := range dependencies.Modules() {
		if dependency.Path() != "github.com/plystra/kernel" {
			continue
		}
		identity := dependency.SelectedVersion()
		if dependency.Workspace() {
			identity = "workspace"
		}
		if replacement, exists := dependency.Replacement(); exists {
			if replacement.Local() {
				return identity + " (local replacement)"
			}
			return identity + " => " + replacement.Path() + "@" + replacement.Version()
		}
		return identity
	}
	return "unselected"
}

func loadPolicyKernelSupport(ctx context.Context, application modulelocate.Module, dependencies moduledependency.Index, options Options) (invocationpolicy.KernelSupport, error) {
	if err := ctx.Err(); err != nil {
		return invocationpolicy.KernelSupport{}, err
	}
	for _, dependency := range dependencies.Modules() {
		if dependency.Path() != "github.com/plystra/kernel" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dependency.Root(), "invocation")); errors.Is(err, os.ErrNotExist) {
			return invocationpolicy.KernelSupport{}, nil
		} else if err != nil {
			return invocationpolicy.KernelSupport{}, errors.New("selected invocation package is unreadable")
		}
		entries, err := os.ReadDir(filepath.Join(dependency.Root(), "invocation"))
		if err != nil {
			return invocationpolicy.KernelSupport{}, errors.New("selected invocation sources are unreadable")
		}
		hasSource := false
		for _, entry := range entries {
			hasSource = hasSource || !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go")
		}
		if !hasSource {
			return invocationpolicy.KernelSupport{}, nil
		}
		pkg, err := interfaceinventory.LoadSelectedPackage(ctx, application, dependencies, dependency, "github.com/plystra/kernel/invocation", interfaceinventory.Options{
			GoCommand: options.GoCommand, Environment: options.Environment, OutputLimit: options.DependencyOutputLimit, Offline: options.Offline, ObserveLoad: options.ObservePolicySupport,
		})
		if err != nil {
			if errors.Is(err, interfaceinventory.ErrSelectedPackageUnavailable) {
				return invocationpolicy.KernelSupport{}, nil
			}
			return invocationpolicy.KernelSupport{}, err
		}
		return invocationpolicy.ReadKernelSupport(pkg), nil
	}
	return invocationpolicy.KernelSupport{}, nil
}
