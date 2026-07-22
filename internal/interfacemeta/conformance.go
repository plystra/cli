package interfacemeta

import (
	"errors"

	"go.yaml.in/yaml/v3"
)

const (
	// CanonicalConformancePackage is the only Behavioral Conformance Suite
	// entry package for an Interface package.
	CanonicalConformancePackage = "./conformance"
)

// ErrInvalidConformance reports invalid Behavioral Conformance configuration.
var ErrInvalidConformance = errors.New("invalid Interface Behavioral Conformance configuration")

// Conformance is immutable configuration for the canonical owner-supplied
// Behavioral Conformance Suite. It does not execute the suite.
type Conformance struct {
	packagePath string
}

// Package returns the canonical package path relative to the Interface package.
func (c Conformance) Package() string { return c.packagePath }

func normalizeConformance(sourcePath string, root *yaml.Node) (Conformance, bool, error) {
	var value *yaml.Node
	for index := 0; index < len(root.Content); index += 2 {
		if root.Content[index].Value == "conformance" {
			value = root.Content[index+1]
			break
		}
	}
	if value == nil {
		return Conformance{}, false, nil
	}
	if value.Kind != yaml.MappingNode || value.Tag != "!!map" {
		return Conformance{}, false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidConformance, "conformance must be a mapping with exactly the required package field")
	}

	var packageNode *yaml.Node
	for index := 0; index < len(value.Content); index += 2 {
		field := value.Content[index]
		switch field.Value {
		case "package":
			packageNode = value.Content[index+1]
		default:
			return Conformance{}, false, invalidWith(sourcePath, field.Line, field.Column, ErrInvalidConformance, "unknown field %q; the only allowed field is package", "conformance."+field.Value)
		}
	}
	if packageNode == nil {
		return Conformance{}, false, invalidWith(sourcePath, value.Line, value.Column, ErrInvalidConformance, "required field conformance.package is missing")
	}
	if packageNode.Kind != yaml.ScalarNode || packageNode.Tag != "!!str" {
		return Conformance{}, false, invalidWith(sourcePath, packageNode.Line, packageNode.Column, ErrInvalidConformance, "conformance.package must be the exact string %q", CanonicalConformancePackage)
	}
	if packageNode.Value != CanonicalConformancePackage {
		return Conformance{}, false, invalidWith(sourcePath, packageNode.Line, packageNode.Column, ErrInvalidConformance, "conformance.package %q must be exactly %q", packageNode.Value, CanonicalConformancePackage)
	}
	return Conformance{packagePath: CanonicalConformancePackage}, true, nil
}
