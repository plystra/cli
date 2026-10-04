package applicationmeta

import (
	"fmt"
	"github.com/plystra/cli/internal/interfaceid"
	"strconv"
)

func interfacePolicyPath(id interfaceid.Identifier) string {
	return fmt.Sprintf("interfaces.policies[%q]", id.String())
}

func interfacePolicyDigest(policy InterfacePolicy) string {
	return digestStrings("interfaces.policies", policy.interfaceID.String(), "timeout", policy.timeout.String(), "retry", policy.retry.Eligibility, strconv.Itoa(policy.retry.MaxAttempts), policy.retry.Backoff.String())
}

func interfacePolicyRemovalDigest(id interfaceid.Identifier) string {
	return digestStrings("interfaces.policies", id.String(), "removed")
}

func interfaceDeclarationDigest(path string, id interfaceid.Identifier, removed bool) string {
	if removed {
		return digestStrings(path, id.String(), "removed")
	}
	return digestStrings(path, id.String())
}

func implementationChoiceDigest(choice ImplementationChoice) string {
	return digestStrings("interfaces.use", choice.interfaceID.String(), choice.constructor.String())
}
