// Package applicationmeta parses the bounded CLI-owned configuration envelope
// of a runnable application's plystra.yaml.
package applicationmeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"go.yaml.in/yaml/v3"
)

// MaximumSize is the largest application declaration inspected by the CLI.
const MaximumSize = 1 << 20

// ErrInvalidManifest reports unsafe or invalid plystra.yaml metadata.
var ErrInvalidManifest = errors.New("invalid application manifest metadata")

// Alias is one immutable explicit application-local Capability Alias
// declaration. Canonical target existence and exposure subset validation occur
// after provider resolution supplies the target catalog.
type Alias struct {
	id          capabilityid.Identifier
	target      capabilityid.Identifier
	exposure    generation.Exposure
	hasExposure bool
	deprecated  string
	source      string
}

// ID returns the canonical application-local Alias ID.
func (a Alias) ID() capabilityid.Identifier { return a.id }

// Target returns the direct canonical target ID declared by the application.
func (a Alias) Target() capabilityid.Identifier { return a.target }

// Exposure returns explicit requested surfaces. A false result means inherit
// all application surfaces available to the canonical target.
func (a Alias) Exposure() (generation.Exposure, bool) {
	if !a.hasExposure {
		return generation.Exposure{}, false
	}
	return a.exposure, true
}

// Deprecated returns the application-local deprecation message, if any.
func (a Alias) Deprecated() string { return a.deprecated }

// Source returns stable configuration-path provenance for diagnostics.
func (a Alias) Source() string { return a.source }

// HTTPExposure is one explicit canonical Capability selected for generated
// HTTP and browser-facing application surfaces.
type HTTPExposure struct {
	id     capabilityid.Identifier
	source string
}

// ID returns the exact canonical Capability ID declared under http.expose.
func (e HTTPExposure) ID() capabilityid.Identifier { return e.id }

// Source returns stable configuration-path provenance for diagnostics.
func (e HTTPExposure) Source() string { return e.source }

// Manifest is the immutable normalized application metadata currently used by
// canonical HTTP exposure and Capability Alias resolution.
type Manifest struct {
	httpAddress    string
	hasHTTPAddress bool
	httpExposures  []HTTPExposure
	aliases        []Alias
}

// HTTPAddress returns the explicitly configured listener address. A false
// result means the http section or address field was omitted.
func (m Manifest) HTTPAddress() (string, bool) { return m.httpAddress, m.hasHTTPAddress }

// HTTPExposures returns defensive declarations sorted by canonical ID.
func (m Manifest) HTTPExposures() []HTTPExposure {
	return append([]HTTPExposure(nil), m.httpExposures...)
}

// Aliases returns defensive declarations sorted by Alias ID.
func (m Manifest) Aliases() []Alias { return append([]Alias(nil), m.aliases...) }

// Parse reads one strict bounded plystra.yaml and normalizes canonical HTTP
// exposure together with concise and expanded capabilities.aliases declarations.
func Parse(data []byte) (Manifest, error) {
	root, err := decodeDocument(data)
	if err != nil {
		return Manifest{}, err
	}
	values, err := mapping(root, "document")
	if err != nil {
		return Manifest{}, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "http", "timeouts", "capabilities", "config":
		default:
			return Manifest{}, invalid("unknown key %q", key)
		}
	}
	for _, section := range []string{"timeouts", "config"} {
		if node, exists := values[section]; exists && node.Kind != yaml.MappingNode {
			return Manifest{}, invalid("%s must be a mapping", section)
		}
	}

	address, hasAddress, exposures, err := parseHTTP(values["http"])
	if err != nil {
		return Manifest{}, err
	}
	aliases, err := parseCapabilities(values["capabilities"])
	if err != nil {
		return Manifest{}, err
	}
	return Manifest{
		httpAddress:    address,
		hasHTTPAddress: hasAddress,
		httpExposures:  exposures,
		aliases:        aliases,
	}, nil
}

func parseHTTP(node *yaml.Node) (string, bool, []HTTPExposure, error) {
	if node == nil {
		return "", false, nil, nil
	}
	values, err := mapping(node, "http")
	if err != nil {
		return "", false, nil, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "address", "expose":
		default:
			return "", false, nil, invalid("http contains unknown key %q", key)
		}
	}
	address := ""
	hasAddress := false
	if addressNode, exists := values["address"]; exists {
		address, err = strictString(addressNode)
		if err != nil || address == "" || len(address) > 4096 || strings.TrimSpace(address) != address || strings.ContainsRune(address, '\x00') {
			return "", false, nil, invalid("http.address must be a non-empty trimmed string of at most 4096 bytes with no NUL")
		}
		hasAddress = true
	}
	exposeNode, exists := values["expose"]
	if !exists {
		return address, hasAddress, nil, nil
	}
	if exposeNode.Kind != yaml.SequenceNode {
		return "", false, nil, invalid("http.expose must be a sequence of canonical Capability IDs")
	}
	exposures := make([]HTTPExposure, 0, len(exposeNode.Content))
	seen := make(map[capabilityid.Identifier]int, len(exposeNode.Content))
	for index, item := range exposeNode.Content {
		value, valueErr := strictString(item)
		if valueErr != nil || value == "" {
			return "", false, nil, invalid("http.expose[%d] must be a canonical Capability ID string", index)
		}
		id, parseErr := capabilityid.Parse(value)
		if parseErr != nil {
			return "", false, nil, invalid("http.expose[%d] %q is not a canonical Capability ID", index, value)
		}
		if previous, duplicate := seen[id]; duplicate {
			return "", false, nil, invalid("http.expose[%d] duplicates Capability %q from http.expose[%d]", index, id.String(), previous)
		}
		seen[id] = index
		exposures = append(exposures, HTTPExposure{
			id:     id,
			source: fmt.Sprintf("plystra.yaml http.expose[%q]", id.String()),
		})
	}
	sort.Slice(exposures, func(left, right int) bool {
		return exposures[left].id.String() < exposures[right].id.String()
	})
	return address, hasAddress, exposures, nil
}

func parseCapabilities(node *yaml.Node) ([]Alias, error) {
	if node == nil {
		return nil, nil
	}
	values, err := mapping(node, "capabilities")
	if err != nil {
		return nil, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "require", "use", "aliases":
		default:
			return nil, invalid("capabilities contains unknown key %q", key)
		}
	}
	if require, exists := values["require"]; exists && require.Kind != yaml.SequenceNode {
		return nil, invalid("capabilities.require must be a sequence")
	}
	if use, exists := values["use"]; exists && use.Kind != yaml.MappingNode {
		return nil, invalid("capabilities.use must be a mapping")
	}
	aliasesNode, exists := values["aliases"]
	if !exists {
		return nil, nil
	}
	aliasValues, err := mapping(aliasesNode, "capabilities.aliases")
	if err != nil {
		return nil, err
	}
	aliases := make([]Alias, 0, len(aliasValues))
	for _, aliasValue := range sortedNodeKeys(aliasValues) {
		id, err := capabilityid.Parse(aliasValue)
		if err != nil {
			return nil, invalid("capabilities.aliases key %q is not a canonical Capability ID", aliasValue)
		}
		if strings.HasPrefix(id.Name(), "kernel.") {
			return nil, invalid("capabilities.aliases key %q uses the reserved kernel.* canonical namespace", aliasValue)
		}
		path := fmt.Sprintf("capabilities.aliases[%q]", aliasValue)
		alias, err := parseAlias(path, id, aliasValues[aliasValue])
		if err != nil {
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	if err := rejectAliasChains(aliases); err != nil {
		return nil, err
	}
	return aliases, nil
}

func parseAlias(path string, id capabilityid.Identifier, node *yaml.Node) (Alias, error) {
	alias := Alias{id: id, source: "plystra.yaml " + path}
	var targetValue string
	var err error
	switch {
	case node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str":
		targetValue = node.Value
	case node != nil && node.Kind == yaml.MappingNode:
		values, mappingErr := mapping(node, path)
		if mappingErr != nil {
			return Alias{}, mappingErr
		}
		for _, key := range sortedNodeKeys(values) {
			switch key {
			case "target", "expose", "deprecated":
			default:
				return Alias{}, invalid("%s contains unknown key %q", path, key)
			}
		}
		targetNode, exists := values["target"]
		if !exists {
			return Alias{}, invalid("%s.target is required", path)
		}
		targetValue, err = strictString(targetNode)
		if err != nil || targetValue == "" {
			return Alias{}, invalid("%s.target must be a non-empty string", path)
		}
		if exposeNode, exists := values["expose"]; exists {
			alias.exposure, err = parseExposure(path+".expose", exposeNode)
			if err != nil {
				return Alias{}, err
			}
			alias.hasExposure = true
		}
		if deprecatedNode, exists := values["deprecated"]; exists {
			alias.deprecated, err = parseDeprecation(path+".deprecated", deprecatedNode)
			if err != nil {
				return Alias{}, err
			}
		}
	default:
		return Alias{}, invalid("%s must be a canonical target string or expanded mapping", path)
	}

	alias.target, err = capabilityid.Parse(targetValue)
	if err != nil {
		return Alias{}, invalid("%s target %q is not a canonical Capability ID", path, targetValue)
	}
	if alias.id.Major() != alias.target.Major() {
		return Alias{}, invalid("%s Alias %q and target %q must use the same major version", path, alias.id.String(), alias.target.String())
	}
	if alias.id == alias.target {
		return Alias{}, invalid("%s Alias %q cannot target itself", path, alias.id.String())
	}
	return alias, nil
}

func parseExposure(path string, node *yaml.Node) (generation.Exposure, error) {
	values, err := mapping(node, path)
	if err != nil {
		return generation.Exposure{}, err
	}
	for _, key := range sortedNodeKeys(values) {
		switch key {
		case "go", "http", "javascript":
		default:
			return generation.Exposure{}, invalid("%s contains unknown key %q", path, key)
		}
	}
	result := generation.Exposure{}
	fields := []struct {
		name  string
		value *bool
	}{
		{name: "go", value: &result.Go},
		{name: "http", value: &result.HTTP},
		{name: "javascript", value: &result.JavaScript},
	}
	for _, field := range fields {
		node, exists := values[field.name]
		if !exists {
			return generation.Exposure{}, invalid("%s.%s is required when expose is present", path, field.name)
		}
		*field.value, err = strictBool(node)
		if err != nil {
			return generation.Exposure{}, invalid("%s.%s must be true or false", path, field.name)
		}
	}
	return result, nil
}

func parseDeprecation(path string, node *yaml.Node) (string, error) {
	values, err := mapping(node, path)
	if err != nil {
		return "", err
	}
	for _, key := range sortedNodeKeys(values) {
		if key != "message" {
			return "", invalid("%s contains unknown key %q", path, key)
		}
	}
	messageNode, exists := values["message"]
	if !exists {
		return "", invalid("%s.message is required", path)
	}
	message, err := strictString(messageNode)
	if err != nil || message == "" || len(message) > 1024 || strings.ContainsRune(message, '\x00') {
		return "", invalid("%s.message must be a non-empty string of at most 1024 bytes with no NUL", path)
	}
	return message, nil
}

func rejectAliasChains(aliases []Alias) error {
	byID := make(map[capabilityid.Identifier]Alias, len(aliases))
	for _, alias := range aliases {
		byID[alias.id] = alias
	}
	for _, start := range aliases {
		path := []capabilityid.Identifier{start.id}
		seen := map[capabilityid.Identifier]struct{}{start.id: {}}
		current := start
		for {
			path = append(path, current.target)
			next, isAlias := byID[current.target]
			if !isAlias {
				break
			}
			if _, cycle := seen[next.id]; cycle {
				return invalid("capabilities.aliases contains forbidden Alias cycle %s; every Alias must directly target a canonical Capability", renderAliasPath(path))
			}
			seen[next.id] = struct{}{}
			current = next
		}
		if len(path) > 2 {
			return invalid("capabilities.aliases contains forbidden Alias chain %s; every Alias must directly target a canonical Capability", renderAliasPath(path))
		}
	}
	return nil
}

func renderAliasPath(values []capabilityid.Identifier) string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return strings.Join(result, " -> ")
}

func decodeDocument(data []byte) (*yaml.Node, error) {
	if len(data) == 0 {
		return nil, invalid("document is empty")
	}
	if len(data) > MaximumSize {
		return nil, invalid("document exceeds %d bytes", MaximumSize)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return nil, invalid("decode YAML: %v", err)
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, invalid("multiple YAML documents are not allowed")
		}
		return nil, invalid("decode trailing YAML: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 {
		return nil, invalid("expected one YAML document")
	}
	if err := rejectReferences(&document); err != nil {
		return nil, err
	}
	return document.Content[0], nil
}

func rejectReferences(root *yaml.Node) error {
	stack := []*yaml.Node{root}
	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]
		if node == nil {
			continue
		}
		if node.Kind == yaml.AliasNode || node.Alias != nil || node.Anchor != "" {
			return invalid("YAML anchors and aliases are not allowed")
		}
		stack = append(stack, node.Content...)
	}
	return nil
}

func mapping(node *yaml.Node, path string) (map[string]*yaml.Node, error) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, invalid("%s must be a mapping", path)
	}
	result := make(map[string]*yaml.Node, len(node.Content)/2)
	for index := 0; index < len(node.Content); index += 2 {
		key, err := strictString(node.Content[index])
		if err != nil {
			return nil, invalid("%s contains a non-string key", path)
		}
		if _, duplicate := result[key]; duplicate {
			return nil, invalid("%s contains duplicate key %q", path, key)
		}
		result[key] = node.Content[index+1]
	}
	return result, nil
}

func strictString(node *yaml.Node) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", errors.New("must be a string")
	}
	return node.Value, nil
}

func strictBool(node *yaml.Node) (bool, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag != "!!bool" {
		return false, errors.New("must be a boolean")
	}
	switch node.Value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, errors.New("must be true or false")
	}
}

func sortedNodeKeys(values map[string]*yaml.Node) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func invalid(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidManifest, fmt.Sprintf(format, arguments...))
}
