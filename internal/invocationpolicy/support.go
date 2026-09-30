package invocationpolicy

import "github.com/plystra/cli/internal/commandschema"

// TimeoutSupport is shared by executable-policy validation and discovery.
func TimeoutSupport() commandschema.CapabilitySupportInput {
	return commandschema.CapabilitySupportInput{
		ID:        "interfaces.policies.*.timeout",
		Specified: commandschema.SupportYes,
		Parsed:    commandschema.SupportYes,
		Generated: commandschema.SupportYes,
		Executed:  commandschema.SupportYes,
		Accepted:  commandschema.SupportYes,
	}
}

// LegacyTimeoutSupport keeps transitional contribution wrappers fail-closed:
// their preparation and completion still execute outside Kernel governance.
func LegacyTimeoutSupport() commandschema.CapabilitySupportInput {
	return commandschema.CapabilitySupportInput{
		ID:        "legacy.capability-timeout",
		Specified: commandschema.SupportYes,
		Parsed:    commandschema.SupportYes,
		Generated: commandschema.SupportYes,
		Executed:  commandschema.SupportNo,
		Accepted:  commandschema.SupportNo,
	}
}
