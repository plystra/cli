package plugincreate

import (
	"fmt"
	"go/format"
	"go/token"
	"strings"
	"unicode"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/configurationgen"
	"github.com/plystra/kernel/plugin/manifest"
)

const pluginTemplate = `package %s

import configuration %q

// Config is the generated configuration type for %s.
type Config = configuration.%s

// Plugin implements %s.
type Plugin struct{}

// New constructs the %s plugin.
func New(_ Config) *Plugin {
	return &Plugin{}
}
`

const pluginTestTemplate = `package %s

import "testing"

func TestNew(t *testing.T) {
	t.Parallel()

	if plugin := New(Config{}); plugin == nil {
		t.Fatal("New returned nil")
	}
}
`

const pluginReadmeTemplate = `# %s plugin

Plugin ID: ` + "`%s`" + `

This is a root-level Plugin in the ` + "`%s`" + ` Plystra Project. Its declarative source is ` + "`plugin.yaml`" + `, and its generated configuration adapter is committed under the Project's ` + "`generated/go/configuration/`" + ` directory. Every Project generates the final selected-Provider assembly centrally.

## Capabilities

Canonical capabilities implemented by this plugin are listed under ` + "`provides`" + ` in ` + "`plugin.yaml`" + `. Their declarations live at ` + "`capabilities/<capability-name>/vN/capability.yaml`" + `, and provider methods remain in plugin-owned Go files outside ` + "`generated/`" + `.

Create a custom capability from the module root with:

` + "```text" + `
plystra capability create <capability-name> --query --plugin %s
` + "```" + `

Implement an existing canonical capability with:

` + "```text" + `
plystra capability implement <capability-name>/vN --plugin %s
` + "```" + `
`

func renderScaffold(modulePath, name, id string) ([]atomicfs.Write, error) {
	packageName := goPackageName(name)
	schema, err := manifest.ParseConfig([]byte("{}\n"))
	if err != nil {
		return nil, fmt.Errorf("prepare empty configuration declaration: %w", err)
	}
	configurationSource, err := configurationgen.Render(configurationgen.Input{
		PluginName: name,
		PluginID:   id,
		Schema:     schema,
	})
	if err != nil {
		return nil, fmt.Errorf("render configuration source: %w", err)
	}
	pluginSource, err := format.Source([]byte(fmt.Sprintf(
		pluginTemplate,
		packageName,
		modulePath+"/generated/go/configuration",
		id,
		configurationSource.TypeName(),
		id,
		id,
	)))
	if err != nil {
		return nil, fmt.Errorf("format plugin source: %w", err)
	}
	testSource, err := format.Source([]byte(fmt.Sprintf(pluginTestTemplate, packageName)))
	if err != nil {
		return nil, fmt.Errorf("format plugin test: %w", err)
	}
	return []atomicfs.Write{
		{Path: name + "/plugin.yaml", Data: []byte("id: " + id + "\n\nconfig: {}\n"), Mode: 0o644, MustNotExist: true, ParentMustNotExist: true},
		{Path: name + "/plugin.go", Data: pluginSource, Mode: 0o644, MustNotExist: true},
		{Path: name + "/plugin_test.go", Data: testSource, Mode: 0o644, MustNotExist: true},
		{Path: name + "/README.md", Data: []byte(fmt.Sprintf(pluginReadmeTemplate, title(name), id, modulePath, name, name)), Mode: 0o644, MustNotExist: true},
		{Path: configurationSource.Path(), Data: configurationSource.Data(), Mode: 0o644, MustNotExist: true},
	}, nil
}

func goPackageName(name string) string {
	value := strings.ReplaceAll(name, "-", "")
	if token.Lookup(value).IsKeyword() {
		return value + "plugin"
	}
	return value
}

func title(name string) string {
	words := strings.Split(name, "-")
	for index, word := range words {
		runes := []rune(word)
		runes[0] = unicode.ToUpper(runes[0])
		words[index] = string(runes)
	}
	return strings.Join(words, " ")
}
