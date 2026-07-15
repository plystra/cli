package generation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

const (
	maximumJSONSize  = 1 << 20
	maximumJSONDepth = 64
)

func normalizeJSONObject(data []byte, emptyDefault bool) ([]byte, map[string]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		if !emptyDefault {
			return nil, nil, errors.New("JSON object is required")
		}
		return []byte("{}"), map[string]any{}, nil
	}
	if len(data) > maximumJSONSize {
		return nil, nil, fmt.Errorf("JSON exceeds %d bytes", maximumJSONSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := decodeJSONValue(decoder, 1)
	if err != nil {
		return nil, nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, nil, errors.New("multiple JSON values are not allowed")
		}
		return nil, nil, fmt.Errorf("decode trailing JSON: %w", err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, nil, errors.New("must be a JSON object")
	}
	canonical, err := json.Marshal(object)
	if err != nil {
		return nil, nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return canonical, object, nil
}

func decodeJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > maximumJSONDepth {
		return nil, fmt.Errorf("JSON exceeds maximum depth %d", maximumJSONDepth)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	switch token := token.(type) {
	case json.Delim:
		switch token {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("decode object key: %w", err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key must be a string")
				}
				if _, duplicate := object[key]; duplicate {
					return nil, fmt.Errorf("object contains duplicate key %q", key)
				}
				value, err := decodeJSONValue(decoder, depth+1)
				if err != nil {
					return nil, fmt.Errorf("object key %q: %w", key, err)
				}
				object[key] = value
			}
			if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') {
				return nil, errors.New("object is not closed")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				value, err := decodeJSONValue(decoder, depth+1)
				if err != nil {
					return nil, fmt.Errorf("array item %d: %w", len(array), err)
				}
				array = append(array, value)
			}
			if closing, err := decoder.Token(); err != nil || closing != json.Delim(']') {
				return nil, errors.New("array is not closed")
			}
			return array, nil
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", token)
		}
	case json.Number:
		return normalizeJSONNumber(token)
	case string, bool, nil:
		return token, nil
	default:
		return nil, fmt.Errorf("unsupported JSON token %T", token)
	}
}

func normalizeJSONNumber(number json.Number) (any, error) {
	text := number.String()
	if !strings.ContainsAny(text, ".eE") {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return nil, errors.New("integer is outside the signed 64-bit range")
		}
		return value, nil
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
		return nil, errors.New("number must be finite and representable as float64")
	}
	if value == 0 {
		return float64(0), nil
	}
	return value, nil
}

type canonicalContext struct {
	API               string                `json:"api"`
	Plugins           []canonicalPlugin     `json:"plugins"`
	Capabilities      []canonicalCapability `json:"capabilities"`
	Requirements      []string              `json:"requirements"`
	Providers         []canonicalProvider   `json:"providers"`
	CapabilityAliases []canonicalAlias      `json:"capability_aliases"`
}

type canonicalPlugin struct {
	ID            string          `json:"id"`
	Module        canonicalModule `json:"module"`
	Provides      []string        `json:"provides"`
	Requires      []string        `json:"requires"`
	BuildMetadata json.RawMessage `json:"build_metadata"`
}

type canonicalModule struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

type canonicalCapability struct {
	ID             string          `json:"id"`
	Intrinsic      bool            `json:"intrinsic"`
	Exposure       Exposure        `json:"exposure"`
	ContractDigest string          `json:"contract_digest"`
	Contract       json.RawMessage `json:"contract"`
}

type canonicalProvider struct {
	Capability string `json:"capability"`
	Plugin     string `json:"plugin"`
}

type canonicalAlias struct {
	ID         string                 `json:"id"`
	Target     string                 `json:"target"`
	Exposure   Exposure               `json:"exposure"`
	Deprecated string                 `json:"deprecated,omitempty"`
	Sources    []canonicalAliasSource `json:"sources"`
}

type canonicalAliasSource struct {
	Kind AliasSourceKind `json:"kind"`
	ID   string          `json:"id"`
}

func encodeContext(plugins []PluginView, capabilities []CapabilityView, requirements []CapabilityID, providers []ProviderView, aliases []CapabilityAliasView) ([]byte, error) {
	canonical := canonicalContext{
		API:               Version,
		Plugins:           make([]canonicalPlugin, len(plugins)),
		Capabilities:      make([]canonicalCapability, len(capabilities)),
		Requirements:      make([]string, len(requirements)),
		Providers:         make([]canonicalProvider, len(providers)),
		CapabilityAliases: make([]canonicalAlias, len(aliases)),
	}
	for index, plugin := range plugins {
		canonical.Plugins[index] = canonicalPlugin{
			ID:            plugin.id.String(),
			Module:        canonicalModule{Path: plugin.module.path, Version: plugin.module.version},
			Provides:      capabilityIDStrings(plugin.provides),
			Requires:      capabilityIDStrings(plugin.requires),
			BuildMetadata: json.RawMessage(plugin.buildMetadataJSON),
		}
	}
	for index, capability := range capabilities {
		canonical.Capabilities[index] = canonicalCapability{
			ID:             capability.id.String(),
			Intrinsic:      capability.intrinsic,
			Exposure:       capability.exposure,
			ContractDigest: capability.contractDigest,
			Contract:       json.RawMessage(capability.contractJSON),
		}
	}
	for index, requirement := range requirements {
		canonical.Requirements[index] = requirement.String()
	}
	for index, provider := range providers {
		canonical.Providers[index] = canonicalProvider{
			Capability: provider.capability.String(),
			Plugin:     provider.plugin.String(),
		}
	}
	for index, alias := range aliases {
		sources := make([]canonicalAliasSource, len(alias.sources))
		for sourceIndex, source := range alias.sources {
			sources[sourceIndex] = canonicalAliasSource{Kind: source.kind, ID: source.id}
		}
		canonical.CapabilityAliases[index] = canonicalAlias{
			ID:         alias.id.String(),
			Target:     alias.target.String(),
			Exposure:   alias.exposure,
			Deprecated: alias.deprecated,
			Sources:    sources,
		}
	}
	return json.Marshal(canonical)
}

func capabilityIDStrings(values []CapabilityID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}
