package commandschema

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/modulepath"
)

// ChangePlanSchemaV1 identifies the canonical public change-plan payload.
const ChangePlanSchemaV1 = "plystra.change-plan/v1"

// ErrChangePlanPayload reports an invalid change-plan payload.
var ErrChangePlanPayload = errors.New("build plystra.change-plan result")

// ChangePlanSnapshotInput identifies the private inputs used by a plan without
// exposing their contents.
type ChangePlanSnapshotInput struct {
	ModulePath        string
	ConfigurationMode string
	ConfigurationPath string
	Environment       string
	RootDigest        string
	SelectedDigest    string
}

// ChangePlanPathInput is one public-safe read or write-set identity.
type ChangePlanPathInput struct {
	Path         string
	Kind         string
	BeforeDigest string
	AfterDigest  string
}

// ChangePlanOperationInput is one normalized requested or derived operation.
// It intentionally contains identities only; typed configuration values are
// never part of a public plan projection.
type ChangePlanOperationInput struct {
	Kind        string
	Action      string
	Interface   string
	Root        string
	Constructor string
	Instance    string
	Provider    string
	Owner       string
	Path        string
	Derived     bool
}

// ChangePlanConfigurationScopeInput identifies the selected current-project
// layer affected by the plan.
type ChangePlanConfigurationScopeInput struct {
	Mode        string
	Path        string
	Environment string
}

// ChangePlanInput is the construction-only form of one change plan.
type ChangePlanInput struct {
	RequestDigest       string
	InputSnapshot       ChangePlanSnapshotInput
	ReadSet             []ChangePlanPathInput
	WriteSet            []ChangePlanPathInput
	Operations          []ChangePlanOperationInput
	ConfigurationScope  ChangePlanConfigurationScopeInput
	ConfigurationImpact configurationimpact.Impact
	Effects             Effects
	FinalModelDigest    string
	Verification        []string
	Completeness        string
}

// ChangePlan is one immutable plystra.change-plan/v1 payload.
type ChangePlan struct {
	requestDigest      string
	planDigest         string
	inputSnapshot      ChangePlanSnapshotInput
	readSet            []ChangePlanPathInput
	writeSet           []ChangePlanPathInput
	operations         []ChangePlanOperationInput
	configurationScope ChangePlanConfigurationScopeInput
	impact             configurationimpact.Impact
	effects            Effects
	finalModelDigest   string
	verification       []string
	completeness       string
	canonicalJSON      []byte
	prepared           bool
}

type changePlanSnapshotDocument struct {
	ModulePath        string `json:"module_path"`
	ConfigurationMode string `json:"configuration_mode"`
	ConfigurationPath string `json:"configuration_path"`
	Environment       string `json:"environment"`
	RootDigest        string `json:"root_digest"`
	SelectedDigest    string `json:"selected_digest"`
}

type changePlanPathDocument struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	BeforeDigest string `json:"before_digest,omitempty"`
	AfterDigest  string `json:"after_digest,omitempty"`
}

type changePlanOperationDocument struct {
	Kind        string `json:"kind"`
	Action      string `json:"action"`
	Interface   string `json:"interface,omitempty"`
	Root        string `json:"root,omitempty"`
	Constructor string `json:"constructor,omitempty"`
	Instance    string `json:"instance,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Owner       string `json:"owner,omitempty"`
	Path        string `json:"path,omitempty"`
	Derived     bool   `json:"derived"`
}

type changePlanScopeDocument struct {
	Mode        string `json:"mode"`
	Path        string `json:"path"`
	Environment string `json:"environment,omitempty"`
}

type changePlanImpactDocument struct {
	NewlyRequiredFields []configurationimpact.NewlyRequiredField `json:"newly_required_fields"`
	RetainedOwners      []configurationimpact.RetainedOwner      `json:"retained_owners"`
	NewlyUnownedPaths   []configurationimpact.NewlyUnownedPath   `json:"newly_unowned_paths"`
	DerivedOperations   []configurationimpact.DerivedOperation   `json:"derived_operations"`
	UnresolvedDecisions []configurationimpact.UnresolvedDecision `json:"unresolved_decisions"`
}

type changePlanDocument struct {
	Schema              string                        `json:"schema"`
	RequestDigest       string                        `json:"request_digest"`
	PlanDigest          string                        `json:"plan_digest"`
	InputSnapshot       changePlanSnapshotDocument    `json:"input_snapshot"`
	ReadSet             []changePlanPathDocument      `json:"read_set"`
	WriteSet            []changePlanPathDocument      `json:"write_set"`
	Operations          []changePlanOperationDocument `json:"operations"`
	ConfigurationScope  changePlanScopeDocument       `json:"configuration_scope"`
	ConfigurationImpact changePlanImpactDocument      `json:"configuration_impact"`
	Effects             effectsDocument               `json:"effects"`
	FinalModelDigest    string                        `json:"final_model_digest"`
	Verification        []string                      `json:"verification"`
	Completeness        string                        `json:"completeness"`
}

type changePlanIdentityDocument struct {
	Schema              string                        `json:"schema"`
	RequestDigest       string                        `json:"request_digest"`
	InputSnapshot       changePlanSnapshotDocument    `json:"input_snapshot"`
	ReadSet             []changePlanPathDocument      `json:"read_set"`
	WriteSet            []changePlanPathDocument      `json:"write_set"`
	Operations          []changePlanOperationDocument `json:"operations"`
	ConfigurationScope  changePlanScopeDocument       `json:"configuration_scope"`
	ConfigurationImpact changePlanImpactDocument      `json:"configuration_impact"`
	Effects             effectsDocument               `json:"effects"`
	FinalModelDigest    string                        `json:"final_model_digest"`
	Verification        []string                      `json:"verification"`
	Completeness        string                        `json:"completeness"`
}

// NewChangePlan validates, orders, and constructs one public plan.
func NewChangePlan(input ChangePlanInput) (ChangePlan, error) {
	if err := validateChangePlanInput(input); err != nil {
		return ChangePlan{}, fmt.Errorf("%w: %v", ErrChangePlanPayload, err)
	}
	if !input.ConfigurationImpact.Valid() {
		return ChangePlan{}, fmt.Errorf("%w: configuration impact is invalid", ErrChangePlanPayload)
	}
	if !input.Effects.Valid() {
		return ChangePlan{}, fmt.Errorf("%w: effects are invalid", ErrChangePlanPayload)
	}
	result := ChangePlan{
		requestDigest:      input.RequestDigest,
		inputSnapshot:      input.InputSnapshot,
		readSet:            append([]ChangePlanPathInput(nil), input.ReadSet...),
		writeSet:           append([]ChangePlanPathInput(nil), input.WriteSet...),
		operations:         append([]ChangePlanOperationInput(nil), input.Operations...),
		configurationScope: input.ConfigurationScope,
		impact:             input.ConfigurationImpact,
		effects:            input.Effects,
		finalModelDigest:   input.FinalModelDigest,
		verification:       append([]string(nil), input.Verification...),
		completeness:       input.Completeness,
		prepared:           true,
	}
	sortChangePlan(&result)
	identity := result.identityDocument()
	identityJSON, err := json.Marshal(identity)
	if err != nil {
		return ChangePlan{}, fmt.Errorf("%w: encode identity: %v", ErrChangePlanPayload, err)
	}
	sum := sha256.Sum256(identityJSON)
	result.planDigest = "sha256:" + hex.EncodeToString(sum[:])
	canonical, err := json.Marshal(result.document())
	if err != nil {
		return ChangePlan{}, fmt.Errorf("%w: encode: %v", ErrChangePlanPayload, err)
	}
	result.canonicalJSON = canonical
	return result, nil
}

// Valid reports whether NewChangePlan produced this plan.
func (p ChangePlan) Valid() bool {
	if !p.prepared {
		return false
	}
	rebuilt, err := NewChangePlan(ChangePlanInput{
		RequestDigest: p.requestDigest, InputSnapshot: p.inputSnapshot,
		ReadSet: p.readSet, WriteSet: p.writeSet, Operations: p.operations,
		ConfigurationScope: p.configurationScope, ConfigurationImpact: p.impact,
		Effects: p.effects, FinalModelDigest: p.finalModelDigest,
		Verification: p.verification, Completeness: p.completeness,
	})
	return err == nil && rebuilt.planDigest == p.planDigest && bytes.Equal(rebuilt.canonicalJSON, p.canonicalJSON)
}

func (ChangePlan) Schema() string                                    { return ChangePlanSchemaV1 }
func (p ChangePlan) RequestDigest() string                           { return p.requestDigest }
func (p ChangePlan) PlanDigest() string                              { return p.planDigest }
func (p ChangePlan) Completeness() string                            { return p.completeness }
func (p ChangePlan) ConfigurationImpact() configurationimpact.Impact { return p.impact }
func (p ChangePlan) CanonicalJSON() []byte                           { return append([]byte(nil), p.canonicalJSON...) }
func (ChangePlan) commandPayload()                                   {}

func (p ChangePlan) document() changePlanDocument {
	return changePlanDocument{
		Schema: ChangePlanSchemaV1, RequestDigest: p.requestDigest, PlanDigest: p.planDigest,
		InputSnapshot: snapshotDocument(p.inputSnapshot), ReadSet: pathDocuments(p.readSet),
		WriteSet: pathDocuments(p.writeSet), Operations: operationDocuments(p.operations),
		ConfigurationScope: scopeDocument(p.configurationScope), ConfigurationImpact: impactDocument(p.impact),
		Effects: effectsDocumentFrom(p.effects), FinalModelDigest: p.finalModelDigest,
		Verification: append([]string(nil), p.verification...), Completeness: p.completeness,
	}
}

func (p ChangePlan) identityDocument() changePlanIdentityDocument {
	document := p.document()
	return changePlanIdentityDocument{
		Schema: document.Schema, RequestDigest: document.RequestDigest,
		InputSnapshot: document.InputSnapshot, ReadSet: document.ReadSet,
		WriteSet: document.WriteSet, Operations: document.Operations,
		ConfigurationScope: document.ConfigurationScope, ConfigurationImpact: document.ConfigurationImpact,
		Effects: document.Effects, FinalModelDigest: document.FinalModelDigest,
		Verification: document.Verification, Completeness: document.Completeness,
	}
}

func validateChangePlanInput(input ChangePlanInput) error {
	if modulepath.CheckProject(input.InputSnapshot.ModulePath) != nil {
		return errors.New("input snapshot module path is invalid")
	}
	if !validDigest(input.RequestDigest) || !validDigest(input.FinalModelDigest) {
		return errors.New("request and final-model digests must be sha256 identities")
	}
	if input.InputSnapshot.ConfigurationMode != "default" && input.InputSnapshot.ConfigurationMode != "environment" && input.InputSnapshot.ConfigurationMode != "explicit-config" {
		return errors.New("input snapshot configuration mode is invalid")
	}
	if input.InputSnapshot.ConfigurationMode == "environment" {
		if input.InputSnapshot.Environment == "" || input.InputSnapshot.ConfigurationPath == "" {
			return errors.New("environment snapshot requires environment and path")
		}
	} else if input.InputSnapshot.Environment != "" {
		return errors.New("non-environment snapshot cannot contain an environment")
	}
	if input.InputSnapshot.ConfigurationPath == "" || !validPlanRelativePath(input.InputSnapshot.ConfigurationPath) || !validDigest(input.InputSnapshot.RootDigest) || !validDigest(input.InputSnapshot.SelectedDigest) {
		return errors.New("input snapshot path or digest is invalid")
	}
	if input.ConfigurationScope.Mode != input.InputSnapshot.ConfigurationMode || input.ConfigurationScope.Path != input.InputSnapshot.ConfigurationPath || input.ConfigurationScope.Environment != input.InputSnapshot.Environment {
		return errors.New("configuration scope does not match input snapshot")
	}
	if len(input.Operations) == 0 || len(input.Operations) > 1024 {
		return errors.New("operations must contain between 1 and 1024 entries")
	}
	for index, operation := range input.Operations {
		if !validOperation(operation) {
			return fmt.Errorf("operations[%d] is invalid", index)
		}
	}
	if err := validatePathSet("read_set", input.ReadSet, false); err != nil {
		return err
	}
	if err := validatePathSet("write_set", input.WriteSet, true); err != nil {
		return err
	}
	if input.Completeness != "exact" && input.Completeness != "non_exact" {
		return errors.New("completeness must be exact or non_exact")
	}
	if len(input.Verification) == 0 {
		return errors.New("verification must contain one command")
	}
	for _, value := range input.Verification {
		if value == "" || len(value) > 256 || strings.ContainsRune(value, '\x00') {
			return errors.New("verification contains an invalid token")
		}
	}
	return nil
}

func validOperation(value ChangePlanOperationInput) bool {
	if value.Kind == "" || value.Action == "" || len(value.Kind) > 128 || len(value.Action) > 128 || strings.ContainsRune(value.Kind, '\x00') || strings.ContainsRune(value.Action, '\x00') {
		return false
	}
	if value.Kind == "interface_root" {
		if value.Root != "require" && value.Root != "expose" {
			return false
		}
	} else if value.Root != "" {
		return false
	}
	for _, field := range []string{value.Interface, value.Root, value.Constructor, value.Instance, value.Provider, value.Owner, value.Path} {
		if len(field) > 4096 || strings.ContainsRune(field, '\x00') {
			return false
		}
	}
	return true
}

func validatePathSet(name string, values []ChangePlanPathInput, writes bool) error {
	if !writes && len(values) == 0 {
		return fmt.Errorf("%s must contain between 1 and 4096 entries", name)
	}
	if len(values) > 4096 {
		return fmt.Errorf("%s must contain at most 4096 entries", name)
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if !validPlanRelativePath(value.Path) || value.Kind == "" || len(value.Kind) > 128 || strings.ContainsRune(value.Kind, '\x00') {
			return fmt.Errorf("%s[%d] is invalid", name, index)
		}
		if writes {
			if value.BeforeDigest != "" && !validDigest(value.BeforeDigest) {
				return fmt.Errorf("%s[%d].before_digest is invalid", name, index)
			}
			if !validDigest(value.AfterDigest) {
				return fmt.Errorf("%s[%d].after_digest is invalid", name, index)
			}
		} else {
			if !validDigest(value.BeforeDigest) {
				return fmt.Errorf("%s[%d].before_digest is invalid", name, index)
			}
			if value.AfterDigest != "" {
				return fmt.Errorf("%s[%d] cannot contain after_digest", name, index)
			}
		}
		if _, exists := seen[value.Path]; exists {
			return fmt.Errorf("%s contains duplicate path %q", name, value.Path)
		}
		seen[value.Path] = struct{}{}
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validPlanRelativePath(value string) bool {
	return value != "" && len(value) <= 4096 && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "\\") && !strings.Contains(value, "..") && !strings.ContainsAny(value, "\\:\x00")
}

func sortChangePlan(p *ChangePlan) {
	sort.Slice(p.readSet, func(left, right int) bool { return p.readSet[left].Path < p.readSet[right].Path })
	sort.Slice(p.writeSet, func(left, right int) bool { return p.writeSet[left].Path < p.writeSet[right].Path })
	sort.Slice(p.operations, func(left, right int) bool {
		return operationKey(p.operations[left]) < operationKey(p.operations[right])
	})
	sort.Strings(p.verification)
}

func operationKey(value ChangePlanOperationInput) string {
	return strings.Join([]string{value.Kind, value.Action, value.Interface, value.Root, value.Constructor, value.Instance, value.Provider, value.Owner, value.Path, fmt.Sprint(value.Derived)}, "\x00")
}

func snapshotDocument(value ChangePlanSnapshotInput) changePlanSnapshotDocument {
	return changePlanSnapshotDocument{ModulePath: value.ModulePath, ConfigurationMode: value.ConfigurationMode, ConfigurationPath: value.ConfigurationPath, Environment: value.Environment, RootDigest: value.RootDigest, SelectedDigest: value.SelectedDigest}
}
func pathDocuments(values []ChangePlanPathInput) []changePlanPathDocument {
	result := make([]changePlanPathDocument, len(values))
	for index, value := range values {
		result[index] = changePlanPathDocument{Path: value.Path, Kind: value.Kind, BeforeDigest: value.BeforeDigest, AfterDigest: value.AfterDigest}
	}
	return result
}
func operationDocuments(values []ChangePlanOperationInput) []changePlanOperationDocument {
	result := make([]changePlanOperationDocument, len(values))
	for index, value := range values {
		result[index] = changePlanOperationDocument{Kind: value.Kind, Action: value.Action, Interface: value.Interface, Root: value.Root, Constructor: value.Constructor, Instance: value.Instance, Provider: value.Provider, Owner: value.Owner, Path: value.Path, Derived: value.Derived}
	}
	return result
}
func scopeDocument(value ChangePlanConfigurationScopeInput) changePlanScopeDocument {
	return changePlanScopeDocument{Mode: value.Mode, Path: value.Path, Environment: value.Environment}
}
func impactDocument(value configurationimpact.Impact) changePlanImpactDocument {
	return changePlanImpactDocument{
		NewlyRequiredFields: value.NewlyRequiredFields(), RetainedOwners: value.RetainedOwners(), NewlyUnownedPaths: value.NewlyUnownedPaths(), DerivedOperations: value.DerivedOperations(), UnresolvedDecisions: value.UnresolvedDecisions(),
	}
}
