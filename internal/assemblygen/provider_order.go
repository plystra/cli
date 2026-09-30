package assemblygen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
)

func providerConstructionOrder(providers []plannedProvider, invocations []InvocationInput) ([]int, error) {
	byProvider := make(map[string]int, len(providers))
	for index, provider := range providers {
		byProvider[provider.PluginID] = index
	}
	selected := make(map[capabilityid.Identifier]int, len(invocations))
	for _, input := range invocations {
		canonical, err := capabilitymeta.NormalizeSchema(input.ContractJSON)
		if err != nil {
			return nil, fmt.Errorf("%w: lifecycle dependency contract: %v", ErrInvalidProvider, err)
		}
		var contract runtimeContract
		if err := json.Unmarshal(canonical, &contract); err != nil {
			return nil, fmt.Errorf("%w: lifecycle dependency contract: %v", ErrInvalidProvider, err)
		}
		identifier, err := capabilityid.Parse(contract.ID)
		if err != nil {
			return nil, fmt.Errorf("%w: lifecycle dependency identity: %v", ErrInvalidProvider, err)
		}
		if _, duplicate := selected[identifier]; duplicate {
			return nil, fmt.Errorf("%w: lifecycle dependency %s", ErrDuplicateInvocation, identifier)
		}
		if input.Intrinsic {
			if !strings.HasPrefix(identifier.Name(), "kernel.") || input.ProviderID != "" {
				return nil, fmt.Errorf("%w: intrinsic lifecycle dependency %s", ErrInvalidProvider, identifier)
			}
			selected[identifier] = -1
			continue
		}
		provider, exists := byProvider[input.ProviderID]
		if !exists || strings.HasPrefix(identifier.Name(), "kernel.") {
			return nil, fmt.Errorf("%w: lifecycle dependency %s selects absent or invalid provider", ErrInvalidProvider, identifier)
		}
		selected[identifier] = provider
	}
	edges := make([][]int, len(providers))
	for index, provider := range providers {
		for _, dependency := range provider.dependencies {
			owner, exists := selected[dependency.id]
			if !exists {
				return nil, fmt.Errorf("%w: plugin %q dependency %s has no selected provider", ErrInvocationDependency, provider.PluginID, dependency.id)
			}
			if owner >= 0 {
				edges[index] = append(edges[index], owner)
			}
		}
		sort.Ints(edges[index])
	}
	state := make([]uint8, len(providers))
	order := make([]int, 0, len(providers))
	var visit func(int) error
	visit = func(index int) error {
		switch state[index] {
		case 1:
			return fmt.Errorf("%w: constructor cycle through plugin %q", ErrInvocationDependency, providers[index].PluginID)
		case 2:
			return nil
		}
		state[index] = 1
		for _, dependency := range edges[index] {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[index] = 2
		order = append(order, index)
		return nil
	}
	for index := range providers {
		if err := visit(index); err != nil {
			return nil, err
		}
	}
	return order, nil
}
