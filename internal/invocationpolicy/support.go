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

// RetrySupport reports each field of the generated replay-safe retry form.
func RetrySupport() []commandschema.CapabilitySupportInput {
	var support []commandschema.CapabilitySupportInput
	for _, field := range []string{"eligibility", "max_attempts", "backoff"} {
		fact := TimeoutSupport()
		fact.ID = "interfaces.policies.*.retry." + field
		support = append(support, fact)
	}
	return support
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
