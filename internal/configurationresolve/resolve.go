// Package configurationresolve binds selected plugins to validated private
// runtime configuration without exposing values to generation extensions.
package configurationresolve

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/kernel/configuration"
	"github.com/plystra/kernel/plugin/manifest"
)

var (
	// ErrResolve reports an invalid selected-plugin configuration closure.
	ErrResolve = errors.New("resolve plugin configuration")
	// ErrInvalidContext reports an absent generation-resolution context.
	ErrInvalidContext = errors.New("invalid configuration resolution context")
	// ErrUnselectedConfiguration reports a plystra.yaml configuration object
	// for a plugin outside the final selected set.
	ErrUnselectedConfiguration = errors.New("configuration targets unselected plugin")
	// ErrMissingPlugin reports selected plugin provenance absent from inventory.
	ErrMissingPlugin = errors.New("selected configuration plugin is absent")
	// ErrInvalidConfiguration reports values that do not conform to the
	// selected plugin's declaration.
	ErrInvalidConfiguration = errors.New("invalid selected plugin configuration")
)

// Binding joins one selected Plugin ID and import path to its validated schema
// and private runtime values. Formatting never exposes the values.
type Binding struct {
	pluginID      string
	modulePath    string
	moduleVersion string
	importPath    string
	source        string
	schema        manifest.Config
	yaml          []byte
	explicit      bool
}

// PluginID returns the selected concrete Plugin ID.
func (b Binding) PluginID() string { return b.pluginID }

// ModulePath returns the selected plugin's owning Go Module path.
func (b Binding) ModulePath() string { return b.modulePath }

// ModuleVersion returns the selected canonical Go Module version, or empty for
// local and active workspace source.
func (b Binding) ModuleVersion() string { return b.moduleVersion }

// ImportPath returns the selected plugin package import path.
func (b Binding) ImportPath() string { return b.importPath }

// Source returns stable configuration provenance without a local path.
func (b Binding) Source() string { return b.source }

// Schema returns the immutable validated plugin declaration.
func (b Binding) Schema() manifest.Config { return b.schema }

// YAML returns defensive private per-plugin runtime values for CLI-owned
// validation and transaction fingerprinting. They must not enter generated
// source or extension input.
func (b Binding) YAML() []byte { return append([]byte(nil), b.yaml...) }

// Explicit reports whether plystra.yaml contained this Plugin ID. False means
// an omitted object normalized to an empty mapping before defaults were
// applied.
func (b Binding) Explicit() bool { return b.explicit }

// String returns only a redaction marker.
func (Binding) String() string { return "<redacted-plugin-configuration-binding>" }

// GoString prevents Go-syntax formatting from exposing configuration values.
func (Binding) GoString() string { return "<redacted-plugin-configuration-binding>" }

// Format redacts configuration values for every fmt verb.
func (Binding) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-plugin-configuration-binding>"))
}

// LogValue redacts configuration values for structured standard-library
// logging.
func (Binding) LogValue() slog.Value {
	return slog.StringValue("<redacted-plugin-configuration-binding>")
}

// Result is one immutable Plugin-ID-sorted selected configuration closure and
// a private digest suitable for concurrent-input detection.
type Result struct {
	bindings []Binding
	digest   string
	prepared bool
}

// Valid reports whether the result was produced by Resolve.
func (r Result) Valid() bool { return r.prepared && validDigest(r.digest) }

// Bindings returns defensive selected bindings in Plugin ID order.
func (r Result) Bindings() []Binding { return append([]Binding(nil), r.bindings...) }

// Binding returns one selected Plugin ID's configuration binding.
func (r Result) Binding(pluginID string) (Binding, bool) {
	index := sort.Search(len(r.bindings), func(index int) bool {
		return r.bindings[index].pluginID >= pluginID
	})
	if index >= len(r.bindings) || r.bindings[index].pluginID != pluginID {
		return Binding{}, false
	}
	return r.bindings[index], true
}

// Digest returns a private stable SHA-256 digest of selected plugin manifests
// and runtime values. It is not generated output or extension input.
func (r Result) Digest() string {
	if !r.Valid() {
		return ""
	}
	return r.digest
}

// String returns only a redaction marker.
func (Result) String() string { return "<redacted-plugin-configurations>" }

// GoString prevents Go-syntax formatting from exposing configuration values.
func (Result) GoString() string { return "<redacted-plugin-configurations>" }

// Format redacts configuration values for every fmt verb.
func (Result) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("<redacted-plugin-configurations>"))
}

// LogValue redacts configuration values for structured standard-library
// logging.
func (Result) LogValue() slog.Value {
	return slog.StringValue("<redacted-plugin-configurations>")
}

// Resolve retains the legacy selected-Plugin assembly boundary while the
// Interface architecture is removed gate by gate. Constructor-keyed Config
// belongs exclusively to generated Implementation assembly and is never
// interpreted as legacy Plugin configuration here.
func Resolve(_ applicationmeta.Manifest, inventory plugininventory.Index, context generation.Context) (Result, error) {
	if len(context.CanonicalJSON()) == 0 || context.Digest() == "" {
		return Result{}, fmt.Errorf("%w: %w", ErrResolve, ErrInvalidContext)
	}
	selected := context.Plugins()
	bindings := make([]Binding, 0, len(selected))
	hash := sha256.New()
	writeDigestRecord(hash, []byte("plystra-configuration-resolution-v1"))
	for _, selectedPlugin := range selected {
		pluginID := selectedPlugin.ID().String()
		plugin, exists := inventory.ByID(pluginID)
		if !exists {
			return Result{}, fmt.Errorf("%w: %w %q", ErrResolve, ErrMissingPlugin, pluginID)
		}
		data := []byte("{}\n")
		source := "implicit empty configuration for selected plugin " + pluginID
		if err := configuration.Validate(plugin.Config(), data); err != nil {
			return Result{}, fmt.Errorf("%w: %w for plugin %q at %s: %w", ErrResolve, ErrInvalidConfiguration, pluginID, source, err)
		}
		binding := Binding{
			pluginID:      pluginID,
			modulePath:    plugin.ModulePath(),
			moduleVersion: plugin.ModuleVersion(),
			importPath:    plugin.ImportPath(),
			source:        source,
			schema:        plugin.Config(),
			yaml:          append([]byte(nil), data...),
			explicit:      false,
		}
		bindings = append(bindings, binding)
		writeDigestRecord(hash, []byte(pluginID))
		writeDigestRecord(hash, plugin.ManifestData())
		writeDigestRecord(hash, data)
	}
	digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
	return Result{bindings: bindings, digest: digest, prepared: true}, nil
}

type digestWriter interface {
	Write([]byte) (int, error)
}

func writeDigestRecord(hash digestWriter, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(value)
}

func validDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
