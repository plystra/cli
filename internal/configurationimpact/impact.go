// Package configurationimpact describes deterministic ownership consequences
// of one already validated configuration change.
package configurationimpact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const SchemaV1 = "plystra.configuration-impact/v1"

var ErrInvalid = errors.New("invalid configuration impact")

type NewlyRequiredField struct {
	Owner              string `json:"owner"`
	Path               string `json:"path"`
	SchemaSource       string `json:"schema_source"`
	RequirednessReason string `json:"requiredness_reason"`
	DefaultSatisfies   bool   `json:"default_satisfies"`
}

type RetainedOwner struct {
	Owner  string `json:"owner"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type NewlyUnownedPath struct {
	Owner  string `json:"owner"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

type DerivedOperation struct {
	Kind   string `json:"kind"`
	Action string `json:"action"`
	Owner  string `json:"owner"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

type UnresolvedDecision struct {
	Target       string   `json:"target"`
	Reason       string   `json:"reason"`
	Alternatives []string `json:"alternatives"`
	Sources      []string `json:"sources"`
}

type Input struct {
	NewlyRequiredFields []NewlyRequiredField
	RetainedOwners      []RetainedOwner
	NewlyUnownedPaths   []NewlyUnownedPath
	DerivedOperations   []DerivedOperation
	UnresolvedDecisions []UnresolvedDecision
}

type Impact struct {
	newlyRequiredFields []NewlyRequiredField
	retainedOwners      []RetainedOwner
	newlyUnownedPaths   []NewlyUnownedPath
	derivedOperations   []DerivedOperation
	unresolvedDecisions []UnresolvedDecision
	canonicalJSON       []byte
	prepared            bool
}

type document struct {
	Schema              string               `json:"schema"`
	NewlyRequiredFields []NewlyRequiredField `json:"newly_required_fields"`
	RetainedOwners      []RetainedOwner      `json:"retained_owners"`
	NewlyUnownedPaths   []NewlyUnownedPath   `json:"newly_unowned_paths"`
	DerivedOperations   []DerivedOperation   `json:"derived_operations"`
	UnresolvedDecisions []UnresolvedDecision `json:"unresolved_decisions"`
}

func New(input Input) (Impact, error) {
	result := Impact{
		newlyRequiredFields: append([]NewlyRequiredField{}, input.NewlyRequiredFields...),
		retainedOwners:      append([]RetainedOwner{}, input.RetainedOwners...),
		newlyUnownedPaths:   append([]NewlyUnownedPath{}, input.NewlyUnownedPaths...),
		derivedOperations:   append([]DerivedOperation{}, input.DerivedOperations...),
		unresolvedDecisions: cloneDecisions(input.UnresolvedDecisions),
	}
	if err := validateAndSort(&result); err != nil {
		return Impact{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	encoded, err := json.Marshal(result.document())
	if err != nil {
		return Impact{}, fmt.Errorf("%w: encode: %v", ErrInvalid, err)
	}
	result.canonicalJSON = encoded
	result.prepared = true
	return result, nil
}

func (i Impact) Valid() bool {
	if !i.prepared {
		return false
	}
	rebuilt, err := New(Input{
		NewlyRequiredFields: i.NewlyRequiredFields(),
		RetainedOwners:      i.RetainedOwners(),
		NewlyUnownedPaths:   i.NewlyUnownedPaths(),
		DerivedOperations:   i.DerivedOperations(),
		UnresolvedDecisions: i.UnresolvedDecisions(),
	})
	return err == nil && bytes.Equal(rebuilt.canonicalJSON, i.canonicalJSON)
}

func (i Impact) CanonicalJSON() []byte { return append([]byte(nil), i.canonicalJSON...) }
func (i Impact) NewlyRequiredFields() []NewlyRequiredField {
	return append([]NewlyRequiredField{}, i.newlyRequiredFields...)
}
func (i Impact) RetainedOwners() []RetainedOwner {
	return append([]RetainedOwner{}, i.retainedOwners...)
}
func (i Impact) NewlyUnownedPaths() []NewlyUnownedPath {
	return append([]NewlyUnownedPath{}, i.newlyUnownedPaths...)
}
func (i Impact) DerivedOperations() []DerivedOperation {
	return append([]DerivedOperation{}, i.derivedOperations...)
}
func (i Impact) UnresolvedDecisions() []UnresolvedDecision {
	return cloneDecisions(i.unresolvedDecisions)
}

func (i Impact) document() document {
	return document{
		Schema:              SchemaV1,
		NewlyRequiredFields: i.newlyRequiredFields,
		RetainedOwners:      i.retainedOwners,
		NewlyUnownedPaths:   i.newlyUnownedPaths,
		DerivedOperations:   i.derivedOperations,
		UnresolvedDecisions: i.unresolvedDecisions,
	}
}

func validateAndSort(i *Impact) error {
	for index, value := range i.newlyRequiredFields {
		if err := validateIdentity(value.Owner, value.Path, value.SchemaSource, value.RequirednessReason); err != nil {
			return fmt.Errorf("newly_required_fields[%d]: %v", index, err)
		}
	}
	for index, value := range i.retainedOwners {
		if err := validateIdentity(value.Owner, value.Path, value.Reason); err != nil {
			return fmt.Errorf("retained_owners[%d]: %v", index, err)
		}
	}
	for index, value := range i.newlyUnownedPaths {
		if err := validateIdentity(value.Owner, value.Path, value.Source); err != nil {
			return fmt.Errorf("newly_unowned_paths[%d]: %v", index, err)
		}
	}
	for index, value := range i.derivedOperations {
		if err := validateIdentity(value.Kind, value.Action, value.Owner, value.Path, value.Source); err != nil {
			return fmt.Errorf("derived_operations[%d]: %v", index, err)
		}
	}
	for index, value := range i.unresolvedDecisions {
		if err := validateIdentity(value.Target, value.Reason); err != nil || len(value.Alternatives) == 0 || len(value.Sources) == 0 {
			return fmt.Errorf("unresolved_decisions[%d]: target, reason, alternatives, and sources are required", index)
		}
		if !sortedUnique(value.Alternatives) || !sortedUnique(value.Sources) {
			return fmt.Errorf("unresolved_decisions[%d]: alternatives and sources must be sorted and unique", index)
		}
	}
	sort.Slice(i.newlyRequiredFields, func(left, right int) bool {
		return requiredKey(i.newlyRequiredFields[left]) < requiredKey(i.newlyRequiredFields[right])
	})
	sort.Slice(i.retainedOwners, func(left, right int) bool {
		return retainedKey(i.retainedOwners[left]) < retainedKey(i.retainedOwners[right])
	})
	sort.Slice(i.newlyUnownedPaths, func(left, right int) bool {
		return unownedKey(i.newlyUnownedPaths[left]) < unownedKey(i.newlyUnownedPaths[right])
	})
	sort.Slice(i.derivedOperations, func(left, right int) bool {
		return derivedKey(i.derivedOperations[left]) < derivedKey(i.derivedOperations[right])
	})
	sort.Slice(i.unresolvedDecisions, func(left, right int) bool {
		return decisionKey(i.unresolvedDecisions[left]) < decisionKey(i.unresolvedDecisions[right])
	})
	for name, values := range map[string][]string{
		"newly_required_fields": requiredKeys(i.newlyRequiredFields),
		"retained_owners":       retainedKeys(i.retainedOwners),
		"newly_unowned_paths":   unownedKeys(i.newlyUnownedPaths),
		"derived_operations":    derivedKeys(i.derivedOperations),
		"unresolved_decisions":  decisionKeys(i.unresolvedDecisions),
	} {
		for index := 1; index < len(values); index++ {
			if values[index-1] == values[index] {
				return fmt.Errorf("%s contains duplicate entries", name)
			}
		}
	}
	return nil
}

func validateIdentity(values ...string) error {
	for _, value := range values {
		if value == "" || len(value) > 4096 || strings.ContainsRune(value, '\x00') {
			return errors.New("bounded non-empty identity fields are required")
		}
	}
	return nil
}

func sortedUnique(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] >= values[index] {
			return false
		}
	}
	return true
}

func requiredKey(v NewlyRequiredField) string {
	return v.Owner + "\x00" + v.Path + "\x00" + v.SchemaSource
}
func retainedKey(v RetainedOwner) string   { return v.Owner + "\x00" + v.Path + "\x00" + v.Reason }
func unownedKey(v NewlyUnownedPath) string { return v.Owner + "\x00" + v.Path + "\x00" + v.Source }
func derivedKey(v DerivedOperation) string {
	return v.Kind + "\x00" + v.Action + "\x00" + v.Owner + "\x00" + v.Path + "\x00" + v.Source
}
func decisionKey(v UnresolvedDecision) string { return v.Target + "\x00" + v.Reason }

func requiredKeys(values []NewlyRequiredField) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = requiredKey(value)
	}
	return result
}
func retainedKeys(values []RetainedOwner) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = retainedKey(value)
	}
	return result
}
func unownedKeys(values []NewlyUnownedPath) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = unownedKey(value)
	}
	return result
}
func derivedKeys(values []DerivedOperation) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = derivedKey(value)
	}
	return result
}
func decisionKeys(values []UnresolvedDecision) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = decisionKey(value)
	}
	return result
}

func cloneDecisions(values []UnresolvedDecision) []UnresolvedDecision {
	result := make([]UnresolvedDecision, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Alternatives = append([]string(nil), value.Alternatives...)
		result[index].Sources = append([]string(nil), value.Sources...)
	}
	return result
}
