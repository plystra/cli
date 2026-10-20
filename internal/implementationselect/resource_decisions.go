package implementationselect

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
)

var errResourceBindingPreviewLimit = errors.New("Resource binding preview exceeds candidate limit")

type resourceBindingOption struct {
	name    string
	sources []string
}

type resourceBindingChoice struct {
	option  string
	reason  string
	sources []string
}

type resourceBindingDecision struct {
	reason       string
	alternatives map[string]bool
	sources      map[string]bool
}

type resourceBindingProbe struct {
	inputs       applicationresolve.SelectionInputs
	target       string
	provider     constructorsymbol.Symbol
	visits       int
	set          func([]byte, string, string, string) ([]byte, bool, error)
	decisions    map[string]*resourceBindingDecision
	combinations map[string]bool
	missing      *applicationmeta.ResourceConfigurationError
	accepted     bool
	choiceCount  int
}

func viableResourceBindingDecisions(inputs applicationresolve.SelectionInputs, updated []byte, target string, provider constructorsymbol.Symbol) ([]configurationimpact.UnresolvedDecision, *applicationmeta.ResourceConfigurationError, error) {
	probe := resourceBindingProbe{
		inputs: inputs, target: target, provider: provider,
		set:       applicationmeta.SetResourceInstanceBinding,
		decisions: make(map[string]*resourceBindingDecision), combinations: make(map[string]bool),
	}
	if inputs.ConfigurationSelection().Mode() == "environment" {
		probe.set = applicationmeta.SetResourceInstanceBindingOverlay
	}
	if _, err := probe.explore(updated, nil); err != nil {
		return nil, nil, err
	}
	if !probe.accepted {
		return nil, nil, nil
	}
	parameters := make([]string, 0, len(probe.decisions))
	for parameter := range probe.decisions {
		parameters = append(parameters, parameter)
	}
	sort.Strings(parameters)
	if len(parameters) > 1 {
		// Joint alternatives preserve the validated parameter correlations.
		alternatives := make([]string, 0, len(probe.combinations))
		for assignment := range probe.combinations {
			alternatives = append(alternatives, assignment)
		}
		sourceSet := make(map[string]bool)
		for _, choice := range probe.decisions {
			for source := range choice.sources {
				sourceSet[source] = true
			}
		}
		sources := make([]string, 0, len(sourceSet))
		for source := range sourceSet {
			sources = append(sources, source)
		}
		sort.Strings(alternatives)
		sort.Strings(sources)
		return []configurationimpact.UnresolvedDecision{{
			Target: fmt.Sprintf("resources.bind.instances[%q]", target),
			Reason: "joint-resource-binding-assignment", Alternatives: alternatives, Sources: sources,
		}}, probe.missing, nil
	}
	decisions := make([]configurationimpact.UnresolvedDecision, 0, len(parameters))
	for _, parameter := range parameters {
		choice := probe.decisions[parameter]
		alternatives := make([]string, 0, len(choice.alternatives))
		for alternative := range choice.alternatives {
			alternatives = append(alternatives, alternative)
		}
		sources := make([]string, 0, len(choice.sources))
		for source := range choice.sources {
			sources = append(sources, source)
		}
		sort.Strings(alternatives)
		sort.Strings(sources)
		decisions = append(decisions, configurationimpact.UnresolvedDecision{
			Target: fmt.Sprintf("resources.bind.instances[%q][%q]", target, parameter),
			Reason: choice.reason, Alternatives: alternatives, Sources: sources,
		})
	}
	return decisions, probe.missing, nil
}

func (p *resourceBindingProbe) explore(document []byte, selected map[string]resourceBindingChoice) (bool, error) {
	composed, err := p.inputs.ComposeCandidate(document)
	if err != nil {
		return false, nil
	}
	validationErr := p.inputs.ValidateCandidate(composed)
	if validationErr == nil {
		return p.accept(selected, nil)
	}
	var missing *applicationmeta.ResourceConfigurationError
	if errors.Is(validationErr, applicationmeta.ErrConfigurationRequired) && errors.As(validationErr, &missing) && missing.InstanceName() == p.target && missing.Provider() == p.provider {
		if p.inputs.ValidateOtherResourceRequiredConfiguration(composed, p.target) != nil {
			return false, nil
		}
		paths, err := p.inputs.MissingRequiredResourceFields(composed, p.target)
		if err != nil || len(paths) == 0 {
			return false, nil
		}
		return p.accept(selected, missing)
	}
	var failure *constructorgraph.ResourceBindingError
	if !errors.Is(validationErr, constructorgraph.ErrAmbiguousResourceBinding) || !errors.As(validationErr, &failure) || failure.Namespace() != constructorgraph.ResourceConsumerInstance || failure.Consumer() != p.target {
		return false, nil
	}
	parameter := failure.ParameterName()
	if _, repeated := selected[parameter]; repeated {
		return false, nil
	}
	options := p.options(failure)
	if len(options) == 0 {
		return false, nil
	}
	if len(options) > maximumResourceBindingPreviewCandidates {
		return false, errResourceBindingPreviewLimit
	}
	reason := "ambiguous-resource-binding"
	sources := resourceBindingFailureSources(p.inputs.ConfigurationSelection().Path(), failure)
	var found bool
	for _, option := range options {
		p.visits++
		if p.visits > maximumResourceBindingPreviewCandidates {
			return false, errResourceBindingPreviewLimit
		}
		updated, _, err := p.set(document, p.target, parameter, option.name)
		if err != nil {
			return false, err
		}
		next := make(map[string]resourceBindingChoice, len(selected)+1)
		for name, choice := range selected {
			next[name] = choice
		}
		choiceSources := append(slices.Clone(sources), option.sources...)
		next[parameter] = resourceBindingChoice{option: option.name, reason: reason, sources: choiceSources}
		viable, err := p.explore(updated, next)
		if err != nil {
			return false, err
		}
		found = found || viable
	}
	return found, nil
}

func (p *resourceBindingProbe) options(failure *constructorgraph.ResourceBindingError) []resourceBindingOption {
	var options []resourceBindingOption
	for _, instance := range failure.Candidates() {
		var sources []string
		for _, candidateSource := range instance.Sources() {
			if candidateSource.Reference != "" {
				sources = append(sources, candidateSource.Reference)
			}
		}
		options = append(options, resourceBindingOption{name: instance.Name(), sources: sources})
	}
	sort.Slice(options, func(left, right int) bool { return options[left].name < options[right].name })
	return options
}

func (p *resourceBindingProbe) accept(selected map[string]resourceBindingChoice, missing *applicationmeta.ResourceConfigurationError) (bool, error) {
	if p.accepted && (p.missing == nil) != (missing == nil) {
		return false, ErrSelect
	}
	if p.accepted && p.choiceCount != len(selected) {
		return false, ErrSelect
	}
	p.accepted = true
	p.choiceCount = len(selected)
	if missing != nil {
		p.missing = missing
	}
	parameters := make([]string, 0, len(selected))
	for parameter := range selected {
		parameters = append(parameters, parameter)
	}
	sort.Strings(parameters)
	assignment := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		choice := selected[parameter]
		assignment = append(assignment, parameter+"="+choice.option)
		decision := p.decisions[parameter]
		if decision == nil {
			decision = &resourceBindingDecision{reason: choice.reason, alternatives: make(map[string]bool), sources: make(map[string]bool)}
			p.decisions[parameter] = decision
		}
		if decision.reason != choice.reason {
			return false, ErrSelect
		}
		decision.alternatives[choice.option] = true
		for _, source := range choice.sources {
			if source != "" {
				decision.sources[source] = true
			}
		}
	}
	p.combinations[strings.Join(assignment, ",")] = true
	return true, nil
}

func resourceBindingFailureSources(selectedPath string, failure *constructorgraph.ResourceBindingError) []string {
	sources := []string{selectedPath}
	dependency := failure.Dependency()
	if declaration := dependency.DeclarationSource().Reference; declaration != "" {
		sources = append(sources, declaration)
	}
	for _, source := range append(failure.Sources(), dependency.ConsumerSelectionSources()...) {
		if source.Reference != "" {
			sources = append(sources, source.Reference)
		}
	}
	return sources
}
