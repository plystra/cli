package installedcapabilities

import (
	"strings"

	"github.com/plystra/cli/internal/commandschema"
)

const (
	configurationSelectorID = "configuration"
	pluginTargetSelectorID  = "plugin-target"
)

func installedSelectors() []commandschema.CapabilitySelectorInput {
	return []commandschema.CapabilitySelectorInput{
		{
			ID:                   configurationSelectorID,
			Arguments:            []string{"--config", "--env"},
			EnvironmentVariables: []string{"PLYSTRA_CONFIG", "PLYSTRA_ENV"},
			Modes:                []string{"default", "environment", "explicit-config"},
			DefaultMode:          "default",
		},
		{
			ID:        pluginTargetSelectorID,
			Arguments: []string{"--interactive", "--plugin"},
			Modes:     []string{"explicit", "enclosing", "sole", "explicit-interactive"},
		},
	}
}

func installedCommands() []commandschema.CapabilityCommandInput {
	return []commandschema.CapabilityCommandInput{
		installedCommand("help", nil, nil, nil, false, false),
		installedCommand("version", nil, nil, nil, false, false),
		installedCommand("new", []commandschema.CapabilityArgumentInput{
			positional("project-name", 1),
			stringOption("--module"),
			stringOption("--template"),
			{
				Name:       "--adopt-export",
				Kind:       commandschema.CapabilityArgumentOption,
				Value:      commandschema.CapabilityArgumentString,
				Repeatable: true,
				Requires:   []string{"--template"},
			},
			stringOption("--plugin"),
			flagOption("--git"),
			flagOption("--github-ci"),
			flagOption("--interactive"),
			flagOption("--no-agent-guidance"),
			formatOption(),
		}, nil, []commandschema.CapabilityDefaultInput{
			{Name: "agent-guidance", Value: "enabled"},
			{Name: "git", Value: "disabled"},
			{Name: "github-ci", Value: "disabled"},
			{Name: "module-path", Value: "project-name"},
		}, true, true),
		installedCommand("add", []commandschema.CapabilityArgumentInput{positional("go-module-query", 1)}, nil, nil, false, false),
		installedCommand("remove", []commandschema.CapabilityArgumentInput{positional("go-module-path", 1)}, nil, nil, false, false),
		installedCommand("update", []commandschema.CapabilityArgumentInput{positional("go-module-query", 1)}, nil, nil, false, false),
		installedCommand("use", append([]commandschema.CapabilityArgumentInput{
			positional("interface-id", 1),
			positional("constructor-symbol", 2),
		}, configurationArguments()...), []string{configurationSelectorID}, nil, false, false),
		installedCommand("plugin.create", []commandschema.CapabilityArgumentInput{positional("plugin-name", 1)}, nil, nil, false, false),
		installedCommand("interface.create", []commandschema.CapabilityArgumentInput{positional("interface-name", 1)}, nil, nil, false, false),
		installedCommand("implement", []commandschema.CapabilityArgumentInput{
			positional("interface-id", 1),
			requiredStringOption("--package"),
		}, nil, nil, false, false),
		installedCommand("capability.create", []commandschema.CapabilityArgumentInput{
			positional("capability-name", 1),
			stringOption("--plugin"),
			flagOption("--interactive"),
			flagOption("--confirm"),
			flagOption("--query"),
			flagOption("--expose"),
		}, []string{pluginTargetSelectorID}, nil, true, false),
		installedCommand("capability.implement", []commandschema.CapabilityArgumentInput{
			positional("capability-id", 1),
			stringOption("--plugin"),
			flagOption("--interactive"),
		}, []string{pluginTargetSelectorID}, nil, true, false),
		installedCommand("capability.expose", append([]commandschema.CapabilityArgumentInput{
			positional("capability-id", 1),
		}, configurationArguments()...), []string{configurationSelectorID}, nil, false, false),
		installedCommand("guidance.sync", []commandschema.CapabilityArgumentInput{flagOption("--replace-generated")}, nil, nil, false, false),
		installedCommand("guidance.check", nil, nil, nil, false, false),
		projectInspectionCommand("inspect", nil),
		installedCommand("inspect.capabilities", []commandschema.CapabilityArgumentInput{formatOption()}, nil, nil, false, true),
		projectInspectionCommand("inspect.modules", nil),
		projectInspectionCommand("inspect.interfaces", nil),
		projectInspectionCommand("inspect.resources", nil),
		projectInspectionCommand("inspect.implementations", nil),
		projectInspectionCommand("inspect.configuration", nil),
		projectInspectionCommand("explain.capability", []commandschema.CapabilityArgumentInput{positional("capability-id", 1)}),
		projectInspectionCommand("explain.plugin", []commandschema.CapabilityArgumentInput{positional("plugin-id", 1)}),
		projectInspectionCommand("explain.config", []commandschema.CapabilityArgumentInput{positional("field-path", 1)}),
		projectInspectionCommand("explain.alias", []commandschema.CapabilityArgumentInput{positional("alias-id", 1)}),
		projectInspectionCommand("explain.exposure", []commandschema.CapabilityArgumentInput{positional("capability-or-alias-id", 1)}),
		installedCommand("check", configurationArguments(), []string{configurationSelectorID}, nil, false, false),
		installedCommand("generate", append([]commandschema.CapabilityArgumentInput{flagOption("--check")}, configurationArguments()...), []string{configurationSelectorID}, nil, false, false),
	}
}

func installedEffectClasses() []commandschema.EffectClass {
	return []commandschema.EffectClass{
		commandschema.EffectProjectWrite,
		commandschema.EffectTemporaryFile,
		commandschema.EffectCacheMaterialization,
		commandschema.EffectDownload,
		commandschema.EffectTrustedCodeExecution,
		commandschema.EffectProcessStartup,
		commandschema.EffectBackendRead,
		commandschema.EffectBackendWrite,
		commandschema.EffectPublication,
	}
}

func installedCommand(id string, arguments []commandschema.CapabilityArgumentInput, selectors []string, defaults []commandschema.CapabilityDefaultInput, interactive, jsonOutput bool) commandschema.CapabilityCommandInput {
	interactionModes := []commandschema.CapabilityInteractionMode{commandschema.CapabilityInteractionNonInteractive}
	if interactive {
		interactionModes = append(interactionModes, commandschema.CapabilityInteractionExplicit)
	}
	outputFormats := []commandschema.CapabilityOutputFormat{commandschema.CapabilityOutputHuman}
	if jsonOutput {
		outputFormats = append(outputFormats, commandschema.CapabilityOutputJSON)
	}
	return commandschema.CapabilityCommandInput{
		ID:               id,
		Path:             strings.Split(id, "."),
		Arguments:        arguments,
		Selectors:        selectors,
		StableDefaults:   defaults,
		InteractionModes: interactionModes,
		OutputFormats:    outputFormats,
	}
}

func projectInspectionCommand(id string, leading []commandschema.CapabilityArgumentInput) commandschema.CapabilityCommandInput {
	arguments := append([]commandschema.CapabilityArgumentInput(nil), leading...)
	arguments = append(arguments, flagOption("--verbose"), formatOption())
	arguments = append(arguments, configurationArguments()...)
	return installedCommand(
		id,
		arguments,
		[]string{configurationSelectorID},
		[]commandschema.CapabilityDefaultInput{{Name: "verbosity", Value: "concise"}},
		false,
		true,
	)
}

func positional(name string, position uint32) commandschema.CapabilityArgumentInput {
	return commandschema.CapabilityArgumentInput{
		Name:     name,
		Kind:     commandschema.CapabilityArgumentPositional,
		Position: position,
		Value:    commandschema.CapabilityArgumentString,
		Required: true,
	}
}

func stringOption(name string) commandschema.CapabilityArgumentInput {
	return commandschema.CapabilityArgumentInput{
		Name:  name,
		Kind:  commandschema.CapabilityArgumentOption,
		Value: commandschema.CapabilityArgumentString,
	}
}

func requiredStringOption(name string) commandschema.CapabilityArgumentInput {
	result := stringOption(name)
	result.Required = true
	return result
}

func flagOption(name string) commandschema.CapabilityArgumentInput {
	return commandschema.CapabilityArgumentInput{
		Name:  name,
		Kind:  commandschema.CapabilityArgumentOption,
		Value: commandschema.CapabilityArgumentFlag,
	}
}

func formatOption() commandschema.CapabilityArgumentInput {
	result := stringOption("--format")
	result.Choices = []string{"human", "json"}
	return result
}

func configurationArguments() []commandschema.CapabilityArgumentInput {
	configuration := stringOption("--config")
	configuration.Conflicts = []string{"--env"}
	environment := stringOption("--env")
	environment.Conflicts = []string{"--config"}
	return []commandschema.CapabilityArgumentInput{configuration, environment}
}
