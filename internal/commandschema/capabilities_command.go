package commandschema

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// CapabilityArgumentKind is the closed installed command-argument vocabulary.
type CapabilityArgumentKind string

const (
	CapabilityArgumentPositional CapabilityArgumentKind = "positional"
	CapabilityArgumentOption     CapabilityArgumentKind = "option"
)

// CapabilityArgumentValue is the closed installed argument-value vocabulary.
type CapabilityArgumentValue string

const (
	CapabilityArgumentFlag   CapabilityArgumentValue = "flag"
	CapabilityArgumentString CapabilityArgumentValue = "string"
)

// CapabilityInteractionMode is the closed installed interaction vocabulary.
type CapabilityInteractionMode string

const (
	CapabilityInteractionNonInteractive CapabilityInteractionMode = "non_interactive"
	CapabilityInteractionExplicit       CapabilityInteractionMode = "explicit_interactive"
)

// CapabilityOutputFormat is the closed installed command-output vocabulary.
type CapabilityOutputFormat string

const (
	CapabilityOutputHuman CapabilityOutputFormat = "human"
	CapabilityOutputJSON  CapabilityOutputFormat = "json"
)

// CapabilityArgumentInput is the construction-only form of one installed
// command argument. Position is positive only for positional arguments.
type CapabilityArgumentInput struct {
	Name       string
	Kind       CapabilityArgumentKind
	Position   uint32
	Value      CapabilityArgumentValue
	Required   bool
	Repeatable bool
	Choices    []string
	Requires   []string
	Conflicts  []string
}

// CapabilityArgument is one immutable installed command argument.
type CapabilityArgument struct {
	input CapabilityArgumentInput
}

func (a CapabilityArgument) Name() string                   { return a.input.Name }
func (a CapabilityArgument) Kind() CapabilityArgumentKind   { return a.input.Kind }
func (a CapabilityArgument) Position() uint32               { return a.input.Position }
func (a CapabilityArgument) Value() CapabilityArgumentValue { return a.input.Value }
func (a CapabilityArgument) Required() bool                 { return a.input.Required }
func (a CapabilityArgument) Repeatable() bool               { return a.input.Repeatable }
func (a CapabilityArgument) Choices() []string              { return cloneStrings(a.input.Choices) }
func (a CapabilityArgument) Requires() []string             { return cloneStrings(a.input.Requires) }
func (a CapabilityArgument) Conflicts() []string            { return cloneStrings(a.input.Conflicts) }
func (a CapabilityArgument) Valid() bool                    { return validateCapabilityArgument(a.input) == nil }

// CapabilityDefaultInput is the construction-only form of one stable command
// default not already represented by the global defaults or selector.
type CapabilityDefaultInput struct {
	Name  string
	Value string
}

// CapabilityDefault is one immutable stable command default.
type CapabilityDefault struct {
	input CapabilityDefaultInput
}

func (d CapabilityDefault) Name() string  { return d.input.Name }
func (d CapabilityDefault) Value() string { return d.input.Value }
func (d CapabilityDefault) Valid() bool   { return validateCapabilityDefault(d.input) == nil }

// CapabilityCommandInput is the construction-only installed parser record for
// one invokable leaf command.
type CapabilityCommandInput struct {
	ID               string
	Path             []string
	Arguments        []CapabilityArgumentInput
	Selectors        []string
	StableDefaults   []CapabilityDefaultInput
	InteractionModes []CapabilityInteractionMode
	OutputFormats    []CapabilityOutputFormat
}

// CapabilityCommand is one immutable installed leaf-command record.
type CapabilityCommand struct {
	input CapabilityCommandInput
}

func (c CapabilityCommand) ID() string     { return c.input.ID }
func (c CapabilityCommand) Path() []string { return cloneStrings(c.input.Path) }

func (c CapabilityCommand) Arguments() []CapabilityArgument {
	result := make([]CapabilityArgument, len(c.input.Arguments))
	for index, input := range c.input.Arguments {
		result[index] = CapabilityArgument{input: cloneCapabilityArgumentInput(input)}
	}
	return result
}

func (c CapabilityCommand) Selectors() []string { return cloneStrings(c.input.Selectors) }

func (c CapabilityCommand) StableDefaults() []CapabilityDefault {
	result := make([]CapabilityDefault, len(c.input.StableDefaults))
	for index, input := range c.input.StableDefaults {
		result[index] = CapabilityDefault{input: input}
	}
	return result
}

func (c CapabilityCommand) InteractionModes() []CapabilityInteractionMode {
	return append([]CapabilityInteractionMode(nil), c.input.InteractionModes...)
}

func (c CapabilityCommand) OutputFormats() []CapabilityOutputFormat {
	return append([]CapabilityOutputFormat(nil), c.input.OutputFormats...)
}

// Valid checks intrinsic normalization. Capabilities additionally validates
// selector references and distribution-wide defaults.
func (c CapabilityCommand) Valid() bool { return validateCapabilityCommand(c.input) == nil }

// CapabilitySelectorInput is the construction-only installed definition for a
// selector shared by one or more commands.
type CapabilitySelectorInput struct {
	ID                   string
	Arguments            []string
	EnvironmentVariables []string
	Modes                []string
	DefaultMode          string
}

// CapabilitySelector is one immutable installed selector definition.
type CapabilitySelector struct {
	input CapabilitySelectorInput
}

func (s CapabilitySelector) ID() string          { return s.input.ID }
func (s CapabilitySelector) Arguments() []string { return cloneStrings(s.input.Arguments) }
func (s CapabilitySelector) EnvironmentVariables() []string {
	return cloneStrings(s.input.EnvironmentVariables)
}
func (s CapabilitySelector) Modes() []string { return cloneStrings(s.input.Modes) }
func (s CapabilitySelector) DefaultMode() (string, bool) {
	return s.input.DefaultMode, s.input.DefaultMode != ""
}
func (s CapabilitySelector) Valid() bool { return validateCapabilitySelector(s.input) == nil }

type capabilityArgumentDocument struct {
	Name       string                  `json:"name"`
	Kind       CapabilityArgumentKind  `json:"kind"`
	Position   *uint32                 `json:"position"`
	Value      CapabilityArgumentValue `json:"value"`
	Required   bool                    `json:"required"`
	Repeatable bool                    `json:"repeatable"`
	Choices    []string                `json:"choices"`
	Requires   []string                `json:"requires"`
	Conflicts  []string                `json:"conflicts"`
}

type capabilityDefaultDocument struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type capabilityCommandDocument struct {
	ID               string                       `json:"id"`
	Path             []string                     `json:"path"`
	Arguments        []capabilityArgumentDocument `json:"arguments"`
	Selectors        []string                     `json:"selectors"`
	StableDefaults   []capabilityDefaultDocument  `json:"stable_defaults"`
	InteractionModes []CapabilityInteractionMode  `json:"interaction_modes"`
	OutputFormats    []CapabilityOutputFormat     `json:"output_formats"`
}

type capabilitySelectorDocument struct {
	ID                   string   `json:"id"`
	Arguments            []string `json:"arguments"`
	EnvironmentVariables []string `json:"environment_variables"`
	Modes                []string `json:"modes"`
	DefaultMode          *string  `json:"default_mode"`
}

var capabilityEffectClasses = []EffectClass{
	EffectProjectWrite,
	EffectTemporaryFile,
	EffectCacheMaterialization,
	EffectDownload,
	EffectTrustedCodeExecution,
	EffectProcessStartup,
	EffectBackendRead,
	EffectBackendWrite,
	EffectPublication,
}

func normalizeCapabilityEffectClasses(input []EffectClass) ([]EffectClass, error) {
	if len(input) != len(capabilityEffectClasses) {
		return nil, fmt.Errorf("effect classes must contain exactly %d records", len(capabilityEffectClasses))
	}
	seen := make(map[EffectClass]bool, len(input))
	for index, class := range input {
		if !validEffectClass(class) {
			return nil, fmt.Errorf("effect_classes[%d] is invalid", index)
		}
		if seen[class] {
			return nil, fmt.Errorf("effect_classes[%d] duplicates %q", index, class)
		}
		seen[class] = true
	}
	for _, class := range capabilityEffectClasses {
		if !seen[class] {
			return nil, fmt.Errorf("effect classes omit %q", class)
		}
	}
	return append([]EffectClass(nil), capabilityEffectClasses...), nil
}

func normalizeCapabilitySelectors(input []CapabilitySelectorInput) ([]CapabilitySelector, error) {
	if len(input) == 0 || len(input) > 32 {
		return nil, errors.New("selectors must contain between 1 and 32 records")
	}
	ordered := make([]CapabilitySelectorInput, len(input))
	for index, selector := range input {
		ordered[index] = cloneCapabilitySelectorInput(selector)
		sort.Strings(ordered[index].Arguments)
		sort.Strings(ordered[index].EnvironmentVariables)
		sort.Strings(ordered[index].Modes)
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	result := make([]CapabilitySelector, len(ordered))
	for index, selector := range ordered {
		if err := validateCapabilitySelector(selector); err != nil {
			return nil, fmt.Errorf("selectors[%d]: %v", index, err)
		}
		if index > 0 && ordered[index-1].ID == selector.ID {
			return nil, fmt.Errorf("selectors[%d] duplicates %q", index, selector.ID)
		}
		result[index] = CapabilitySelector{input: selector}
	}
	return result, nil
}

func normalizeCapabilityCommands(input []CapabilityCommandInput, selectors []CapabilitySelector, defaultInteraction CapabilityInteractionMode, defaultOutput CapabilityOutputFormat) ([]CapabilityCommand, error) {
	if len(input) == 0 || len(input) > 256 {
		return nil, errors.New("commands must contain between 1 and 256 records")
	}
	selectorByID := make(map[string]CapabilitySelector, len(selectors))
	selectorUse := make(map[string]bool, len(selectors))
	for _, selector := range selectors {
		selectorByID[selector.ID()] = selector
	}
	ordered := make([]CapabilityCommandInput, len(input))
	for index, command := range input {
		ordered[index] = normalizeCapabilityCommandInput(command)
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	result := make([]CapabilityCommand, len(ordered))
	for index, command := range ordered {
		if err := validateCapabilityCommand(command); err != nil {
			return nil, fmt.Errorf("commands[%d]: %v", index, err)
		}
		if index > 0 && ordered[index-1].ID == command.ID {
			return nil, fmt.Errorf("commands[%d] duplicates %q", index, command.ID)
		}
		if !containsInteractionMode(command.InteractionModes, defaultInteraction) {
			return nil, fmt.Errorf("commands[%d] does not support default interaction mode %q", index, defaultInteraction)
		}
		if !containsOutputFormat(command.OutputFormats, defaultOutput) {
			return nil, fmt.Errorf("commands[%d] does not support default output format %q", index, defaultOutput)
		}
		arguments := make(map[string]struct{}, len(command.Arguments))
		for _, argument := range command.Arguments {
			arguments[argument.Name] = struct{}{}
		}
		for _, selectorID := range command.Selectors {
			selector, exists := selectorByID[selectorID]
			if !exists {
				return nil, fmt.Errorf("commands[%d] references unknown selector %q", index, selectorID)
			}
			for _, argument := range selector.Arguments() {
				if _, exists := arguments[argument]; !exists {
					return nil, fmt.Errorf("commands[%d] selector %q requires argument %q", index, selectorID, argument)
				}
			}
			selectorUse[selectorID] = true
		}
		result[index] = CapabilityCommand{input: command}
	}
	for _, selector := range selectors {
		if !selectorUse[selector.ID()] {
			return nil, fmt.Errorf("selector %q is not used by an installed command", selector.ID())
		}
	}
	return result, nil
}

func normalizeCapabilityCommandInput(input CapabilityCommandInput) CapabilityCommandInput {
	result := cloneCapabilityCommandInput(input)
	for index, argument := range result.Arguments {
		result.Arguments[index] = cloneCapabilityArgumentInput(argument)
		sort.Strings(result.Arguments[index].Choices)
		sort.Strings(result.Arguments[index].Requires)
		sort.Strings(result.Arguments[index].Conflicts)
	}
	sort.Slice(result.Arguments, func(left, right int) bool {
		return capabilityArgumentKey(result.Arguments[left]) < capabilityArgumentKey(result.Arguments[right])
	})
	sort.Strings(result.Selectors)
	sort.Slice(result.StableDefaults, func(left, right int) bool {
		return result.StableDefaults[left].Name < result.StableDefaults[right].Name
	})
	sort.Slice(result.InteractionModes, func(left, right int) bool {
		return capabilityInteractionRank(result.InteractionModes[left]) < capabilityInteractionRank(result.InteractionModes[right])
	})
	sort.Slice(result.OutputFormats, func(left, right int) bool {
		return capabilityOutputRank(result.OutputFormats[left]) < capabilityOutputRank(result.OutputFormats[right])
	})
	return result
}

func validateCapabilityCommand(input CapabilityCommandInput) error {
	if !validCommandID(input.ID) || len(input.Path) == 0 || len(input.Path) > 8 {
		return errors.New("id or command path is invalid")
	}
	for _, token := range input.Path {
		if !validLowerKebab(token, 64) {
			return errors.New("command path contains an invalid token")
		}
	}
	if strings.Join(input.Path, ".") != input.ID {
		return errors.New("id does not match command path")
	}
	if len(input.Arguments) > 64 {
		return errors.New("argument count exceeds 64")
	}
	arguments := make(map[string]CapabilityArgumentInput, len(input.Arguments))
	positions := make(map[uint32]bool)
	for index, argument := range input.Arguments {
		if err := validateCapabilityArgument(argument); err != nil {
			return fmt.Errorf("arguments[%d]: %v", index, err)
		}
		if _, exists := arguments[argument.Name]; exists {
			return fmt.Errorf("arguments[%d] duplicates %q", index, argument.Name)
		}
		arguments[argument.Name] = argument
		if argument.Kind == CapabilityArgumentPositional {
			if positions[argument.Position] {
				return fmt.Errorf("arguments[%d] duplicates position %d", index, argument.Position)
			}
			positions[argument.Position] = true
		}
	}
	for position := uint32(1); position <= uint32(len(positions)); position++ {
		if !positions[position] {
			return errors.New("positional argument positions must be contiguous")
		}
	}
	for index, argument := range input.Arguments {
		for _, reference := range append(cloneStrings(argument.Requires), argument.Conflicts...) {
			if reference == argument.Name {
				return fmt.Errorf("arguments[%d] references itself", index)
			}
			if _, exists := arguments[reference]; !exists {
				return fmt.Errorf("arguments[%d] references unknown argument %q", index, reference)
			}
		}
		for _, conflict := range argument.Conflicts {
			if !containsString(arguments[conflict].Conflicts, argument.Name) {
				return fmt.Errorf("arguments[%d] conflict with %q is not symmetric", index, conflict)
			}
		}
		for _, required := range argument.Requires {
			if containsString(argument.Conflicts, required) {
				return fmt.Errorf("arguments[%d] both requires and conflicts with %q", index, required)
			}
		}
	}
	for index, selector := range input.Selectors {
		if !validLowerKebab(selector, 64) || index > 0 && input.Selectors[index-1] == selector {
			return fmt.Errorf("selectors[%d] is invalid or duplicated", index)
		}
	}
	for index, value := range input.StableDefaults {
		if err := validateCapabilityDefault(value); err != nil {
			return fmt.Errorf("stable_defaults[%d]: %v", index, err)
		}
		if index > 0 && input.StableDefaults[index-1].Name == value.Name {
			return fmt.Errorf("stable_defaults[%d] duplicates %q", index, value.Name)
		}
	}
	if len(input.InteractionModes) == 0 || len(input.InteractionModes) > 2 {
		return errors.New("interaction modes must contain one or two records")
	}
	for index, mode := range input.InteractionModes {
		if !validCapabilityInteractionMode(mode) || index > 0 && input.InteractionModes[index-1] == mode {
			return fmt.Errorf("interaction_modes[%d] is invalid or duplicated", index)
		}
	}
	interactive, interactiveArgument := arguments["--interactive"]
	if interactiveArgument && interactive.Value != CapabilityArgumentFlag {
		return errors.New("--interactive must be a flag")
	}
	if interactiveArgument != containsInteractionMode(input.InteractionModes, CapabilityInteractionExplicit) {
		return errors.New("explicit interaction mode and --interactive argument must agree")
	}
	if len(input.OutputFormats) == 0 || len(input.OutputFormats) > 2 {
		return errors.New("output formats must contain one or two records")
	}
	for index, format := range input.OutputFormats {
		if !validCapabilityOutputFormat(format) || index > 0 && input.OutputFormats[index-1] == format {
			return fmt.Errorf("output_formats[%d] is invalid or duplicated", index)
		}
	}
	formatArgument, hasFormatArgument := arguments["--format"]
	if hasFormatArgument {
		if formatArgument.Value != CapabilityArgumentString || !equalOutputChoices(formatArgument.Choices, input.OutputFormats) {
			return errors.New("--format choices and output formats must agree")
		}
	} else if len(input.OutputFormats) != 1 || input.OutputFormats[0] != CapabilityOutputHuman {
		return errors.New("commands without --format must expose only human output")
	}
	for _, stableDefault := range input.StableDefaults {
		switch stableDefault.Name {
		case "interaction-mode":
			if !containsInteractionMode(input.InteractionModes, CapabilityInteractionMode(stableDefault.Value)) {
				return errors.New("interaction-mode default is not supported")
			}
		case "output-format":
			if !containsOutputFormat(input.OutputFormats, CapabilityOutputFormat(stableDefault.Value)) {
				return errors.New("output-format default is not supported")
			}
		}
	}
	return nil
}

func validateCapabilityArgument(input CapabilityArgumentInput) error {
	switch input.Kind {
	case CapabilityArgumentPositional:
		if !validLowerKebab(input.Name, 64) || input.Position == 0 || !input.Required || input.Repeatable || input.Value != CapabilityArgumentString {
			return errors.New("positional argument shape is invalid")
		}
	case CapabilityArgumentOption:
		if !validOptionName(input.Name) || input.Position != 0 {
			return errors.New("option argument shape is invalid")
		}
	default:
		return fmt.Errorf("kind %q is invalid", input.Kind)
	}
	switch input.Value {
	case CapabilityArgumentFlag:
		if input.Kind != CapabilityArgumentOption || input.Required || input.Repeatable || len(input.Choices) != 0 {
			return errors.New("flag argument shape is invalid")
		}
	case CapabilityArgumentString:
	default:
		return fmt.Errorf("value %q is invalid", input.Value)
	}
	for index, choice := range input.Choices {
		if !validToken(choice, 128) || index > 0 && input.Choices[index-1] == choice {
			return fmt.Errorf("choices[%d] is invalid or duplicated", index)
		}
	}
	for name, values := range map[string][]string{"requires": input.Requires, "conflicts": input.Conflicts} {
		for index, value := range values {
			if !validArgumentReference(value) || index > 0 && values[index-1] == value {
				return fmt.Errorf("%s[%d] is invalid or duplicated", name, index)
			}
		}
	}
	return nil
}

func validateCapabilityDefault(input CapabilityDefaultInput) error {
	if !validLowerKebab(input.Name, 64) || !validSafeText(input.Value, 256) {
		return errors.New("name or value is invalid")
	}
	return nil
}

func validateCapabilitySelector(input CapabilitySelectorInput) error {
	if !validLowerKebab(input.ID, 64) || len(input.Arguments) > 16 || len(input.EnvironmentVariables) > 16 || len(input.Modes) == 0 || len(input.Modes) > 16 {
		return errors.New("id or selector bounds are invalid")
	}
	if len(input.Arguments) == 0 && len(input.EnvironmentVariables) == 0 {
		return errors.New("selector must expose an argument or environment variable")
	}
	for index, argument := range input.Arguments {
		if !validOptionName(argument) || index > 0 && input.Arguments[index-1] == argument {
			return fmt.Errorf("arguments[%d] is invalid or duplicated", index)
		}
	}
	for index, variable := range input.EnvironmentVariables {
		if !validEnvironmentVariable(variable) || index > 0 && input.EnvironmentVariables[index-1] == variable {
			return fmt.Errorf("environment_variables[%d] is invalid or duplicated", index)
		}
	}
	for index, mode := range input.Modes {
		if !validToken(mode, 64) || index > 0 && input.Modes[index-1] == mode {
			return fmt.Errorf("modes[%d] is invalid or duplicated", index)
		}
	}
	if input.DefaultMode != "" && !containsString(input.Modes, input.DefaultMode) {
		return errors.New("default mode is not one of the installed modes")
	}
	return nil
}

func capabilityCommandDocuments(values []CapabilityCommand) []capabilityCommandDocument {
	result := make([]capabilityCommandDocument, len(values))
	for index, command := range values {
		arguments := make([]capabilityArgumentDocument, len(command.input.Arguments))
		for argumentIndex, argument := range command.input.Arguments {
			var position *uint32
			if argument.Kind == CapabilityArgumentPositional {
				value := argument.Position
				position = &value
			}
			arguments[argumentIndex] = capabilityArgumentDocument{
				Name:       argument.Name,
				Kind:       argument.Kind,
				Position:   position,
				Value:      argument.Value,
				Required:   argument.Required,
				Repeatable: argument.Repeatable,
				Choices:    nonNilStrings(argument.Choices),
				Requires:   nonNilStrings(argument.Requires),
				Conflicts:  nonNilStrings(argument.Conflicts),
			}
		}
		defaults := make([]capabilityDefaultDocument, len(command.input.StableDefaults))
		for defaultIndex, value := range command.input.StableDefaults {
			defaults[defaultIndex] = capabilityDefaultDocument(value)
		}
		result[index] = capabilityCommandDocument{
			ID:               command.ID(),
			Path:             nonNilStrings(command.input.Path),
			Arguments:        arguments,
			Selectors:        nonNilStrings(command.input.Selectors),
			StableDefaults:   defaults,
			InteractionModes: append([]CapabilityInteractionMode{}, command.input.InteractionModes...),
			OutputFormats:    append([]CapabilityOutputFormat{}, command.input.OutputFormats...),
		}
	}
	return result
}

func capabilitySelectorDocuments(values []CapabilitySelector) []capabilitySelectorDocument {
	result := make([]capabilitySelectorDocument, len(values))
	for index, selector := range values {
		var defaultMode *string
		if selector.input.DefaultMode != "" {
			value := selector.input.DefaultMode
			defaultMode = &value
		}
		result[index] = capabilitySelectorDocument{
			ID:                   selector.ID(),
			Arguments:            nonNilStrings(selector.input.Arguments),
			EnvironmentVariables: nonNilStrings(selector.input.EnvironmentVariables),
			Modes:                nonNilStrings(selector.input.Modes),
			DefaultMode:          defaultMode,
		}
	}
	return result
}

func cloneCapabilityCommandInput(input CapabilityCommandInput) CapabilityCommandInput {
	arguments := make([]CapabilityArgumentInput, len(input.Arguments))
	for index, argument := range input.Arguments {
		arguments[index] = cloneCapabilityArgumentInput(argument)
	}
	return CapabilityCommandInput{
		ID:               input.ID,
		Path:             cloneStrings(input.Path),
		Arguments:        arguments,
		Selectors:        cloneStrings(input.Selectors),
		StableDefaults:   append([]CapabilityDefaultInput(nil), input.StableDefaults...),
		InteractionModes: append([]CapabilityInteractionMode(nil), input.InteractionModes...),
		OutputFormats:    append([]CapabilityOutputFormat(nil), input.OutputFormats...),
	}
}

func cloneCapabilityArgumentInput(input CapabilityArgumentInput) CapabilityArgumentInput {
	return CapabilityArgumentInput{
		Name:       input.Name,
		Kind:       input.Kind,
		Position:   input.Position,
		Value:      input.Value,
		Required:   input.Required,
		Repeatable: input.Repeatable,
		Choices:    cloneStrings(input.Choices),
		Requires:   cloneStrings(input.Requires),
		Conflicts:  cloneStrings(input.Conflicts),
	}
}

func cloneCapabilitySelectorInput(input CapabilitySelectorInput) CapabilitySelectorInput {
	return CapabilitySelectorInput{
		ID:                   input.ID,
		Arguments:            cloneStrings(input.Arguments),
		EnvironmentVariables: cloneStrings(input.EnvironmentVariables),
		Modes:                cloneStrings(input.Modes),
		DefaultMode:          input.DefaultMode,
	}
}

func capabilityArgumentKey(input CapabilityArgumentInput) string {
	switch input.Kind {
	case CapabilityArgumentPositional:
		return fmt.Sprintf("0\x00%010d\x00%s", input.Position, input.Name)
	case CapabilityArgumentOption:
		return "1\x00" + input.Name
	default:
		return "2\x00" + input.Name
	}
}

func validCommandID(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	segments := strings.Split(value, ".")
	if len(segments) > 8 {
		return false
	}
	for _, segment := range segments {
		if !validLowerKebab(segment, 64) {
			return false
		}
	}
	return true
}

func validOptionName(value string) bool {
	return len(value) > 2 && strings.HasPrefix(value, "--") && validLowerKebab(value[2:], 64)
}

func validEnvironmentVariable(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	previousUnderscore := false
	for _, character := range []byte(value[1:]) {
		switch {
		case character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
			previousUnderscore = false
		case character == '_' && !previousUnderscore:
			previousUnderscore = true
		default:
			return false
		}
	}
	return !previousUnderscore
}

func validArgumentReference(value string) bool {
	return validOptionName(value) || validLowerKebab(value, 64)
}

func validCapabilityInteractionMode(value CapabilityInteractionMode) bool {
	switch value {
	case CapabilityInteractionNonInteractive, CapabilityInteractionExplicit:
		return true
	default:
		return false
	}
}

func validCapabilityOutputFormat(value CapabilityOutputFormat) bool {
	switch value {
	case CapabilityOutputHuman, CapabilityOutputJSON:
		return true
	default:
		return false
	}
}

func capabilityInteractionRank(value CapabilityInteractionMode) int {
	switch value {
	case CapabilityInteractionNonInteractive:
		return 0
	case CapabilityInteractionExplicit:
		return 1
	default:
		return 2
	}
}

func capabilityOutputRank(value CapabilityOutputFormat) int {
	switch value {
	case CapabilityOutputHuman:
		return 0
	case CapabilityOutputJSON:
		return 1
	default:
		return 2
	}
}

func containsInteractionMode(values []CapabilityInteractionMode, expected CapabilityInteractionMode) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func containsOutputFormat(values []CapabilityOutputFormat, expected CapabilityOutputFormat) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func equalOutputChoices(choices []string, formats []CapabilityOutputFormat) bool {
	if len(choices) != len(formats) {
		return false
	}
	for index, choice := range choices {
		if choice != string(formats[index]) {
			return false
		}
	}
	return true
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func nonNilStrings(values []string) []string {
	return append([]string{}, values...)
}
