package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/modulepath"
)

// DoctorSchemaV1 identifies the payload returned by plystra doctor.
const DoctorSchemaV1 = "plystra.doctor/v1"

// ErrDoctorPayload reports an invalid doctor payload.
var ErrDoctorPayload = errors.New("build plystra.doctor result")

// DoctorPayloadInput is the construction-only result of a read-only local
// prerequisite inspection.
type DoctorPayloadInput struct {
	ModulePath        string
	ConfigurationPath string
	Environment       string
	Offline           bool
	Checks            []DoctorCheckInput
	DataCompiler      *DoctorDataCompilerInput
}

// DoctorCheckInput is one bounded prerequisite observation.
type DoctorCheckInput struct {
	ID           string
	Status       SupportState
	Reason       string
	Verification []string
}

// DoctorDataCompilerInput is the exact selected compiler identity and its
// public-safe local availability facts. It never contains a source or cache
// path.
type DoctorDataCompilerInput struct {
	ModulePath          string
	ModuleVersion       string
	ModuleChecksum      string
	DistributionSchema  string
	ManifestDigest      string
	CommandImportPath   string
	AnalyzeProtocol     string
	EmitProtocol        string
	DeclarationLanguage string
	GoToolchain         string
	GOOS                string
	GOARCH              string
	BinaryDigest        string
	Source              SupportState
	Cache               SupportState
	Build               SupportState
	Offline             SupportState
}

// DoctorPayload is one immutable plystra.doctor/v1 payload.
type DoctorPayload struct {
	modulePath        string
	configurationPath string
	environment       string
	offline           bool
	checks            []DoctorCheck
	dataCompiler      *DoctorDataCompiler
	canonicalJSON     []byte
	prepared          bool
}

type doctorPayloadDocument struct {
	Schema            string                      `json:"schema"`
	ModulePath        string                      `json:"module_path"`
	ConfigurationPath string                      `json:"configuration_path"`
	Environment       string                      `json:"environment"`
	Offline           bool                        `json:"offline"`
	Checks            []doctorCheckDocument       `json:"checks"`
	DataCompiler      *doctorDataCompilerDocument `json:"data_compiler"`
}

type doctorCheckDocument struct {
	ID           string       `json:"id"`
	Status       SupportState `json:"status"`
	Reason       string       `json:"reason"`
	Verification []string     `json:"verification"`
}

type doctorDataCompilerDocument struct {
	ModulePath          string       `json:"module_path"`
	ModuleVersion       string       `json:"module_version"`
	ModuleChecksum      string       `json:"module_checksum"`
	DistributionSchema  string       `json:"distribution_schema"`
	ManifestDigest      string       `json:"manifest_digest"`
	CommandImportPath   string       `json:"command_import_path"`
	AnalyzeProtocol     string       `json:"analyze_protocol"`
	EmitProtocol        string       `json:"emit_protocol"`
	DeclarationLanguage string       `json:"declaration_language"`
	GoToolchain         string       `json:"go_toolchain"`
	GOOS                string       `json:"goos"`
	GOARCH              string       `json:"goarch"`
	BinaryDigest        string       `json:"binary_digest"`
	Source              SupportState `json:"source"`
	Cache               SupportState `json:"cache"`
	Build               SupportState `json:"build"`
	Offline             SupportState `json:"offline"`
}

// DoctorCheck is one immutable prerequisite observation.
type DoctorCheck struct{ input DoctorCheckInput }

func (c DoctorCheck) ID() string             { return c.input.ID }
func (c DoctorCheck) Status() SupportState   { return c.input.Status }
func (c DoctorCheck) Reason() string         { return c.input.Reason }
func (c DoctorCheck) Verification() []string { return append([]string(nil), c.input.Verification...) }

// DoctorDataCompiler is one immutable selected compiler fact set.
type DoctorDataCompiler struct{ input DoctorDataCompilerInput }

func (c DoctorDataCompiler) ModulePath() string          { return c.input.ModulePath }
func (c DoctorDataCompiler) ModuleVersion() string       { return c.input.ModuleVersion }
func (c DoctorDataCompiler) ModuleChecksum() string      { return c.input.ModuleChecksum }
func (c DoctorDataCompiler) DistributionSchema() string  { return c.input.DistributionSchema }
func (c DoctorDataCompiler) ManifestDigest() string      { return c.input.ManifestDigest }
func (c DoctorDataCompiler) CommandImportPath() string   { return c.input.CommandImportPath }
func (c DoctorDataCompiler) AnalyzeProtocol() string     { return c.input.AnalyzeProtocol }
func (c DoctorDataCompiler) EmitProtocol() string        { return c.input.EmitProtocol }
func (c DoctorDataCompiler) DeclarationLanguage() string { return c.input.DeclarationLanguage }
func (c DoctorDataCompiler) GoToolchain() string         { return c.input.GoToolchain }
func (c DoctorDataCompiler) GOOS() string                { return c.input.GOOS }
func (c DoctorDataCompiler) GOARCH() string              { return c.input.GOARCH }
func (c DoctorDataCompiler) BinaryDigest() string        { return c.input.BinaryDigest }
func (c DoctorDataCompiler) Source() SupportState        { return c.input.Source }
func (c DoctorDataCompiler) Cache() SupportState         { return c.input.Cache }
func (c DoctorDataCompiler) Build() SupportState         { return c.input.Build }
func (c DoctorDataCompiler) Offline() SupportState       { return c.input.Offline }

// NewDoctorPayload validates and constructs one doctor payload.
func NewDoctorPayload(input DoctorPayloadInput) (DoctorPayload, error) {
	if err := validateDoctorPayload(input); err != nil {
		return DoctorPayload{}, fmt.Errorf("%w: %v", ErrDoctorPayload, err)
	}
	checks := make([]DoctorCheck, len(input.Checks))
	ordered := append([]DoctorCheckInput(nil), input.Checks...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	for index, check := range ordered {
		checks[index] = DoctorCheck{input: DoctorCheckInput{ID: check.ID, Status: check.Status, Reason: check.Reason, Verification: append([]string(nil), check.Verification...)}}
	}
	var compiler *DoctorDataCompiler
	if input.DataCompiler != nil {
		value := *input.DataCompiler
		compiler = &DoctorDataCompiler{input: value}
	}
	payload := DoctorPayload{modulePath: input.ModulePath, configurationPath: input.ConfigurationPath, environment: input.Environment, offline: input.Offline, checks: checks, dataCompiler: compiler, prepared: true}
	canonical, err := json.Marshal(payload.document())
	if err != nil {
		return DoctorPayload{}, fmt.Errorf("%w: encode: %v", ErrDoctorPayload, err)
	}
	payload.canonicalJSON = canonical
	return payload, nil
}

func (p DoctorPayload) Valid() bool {
	if !p.prepared {
		return false
	}
	input := DoctorPayloadInput{ModulePath: p.modulePath, ConfigurationPath: p.configurationPath, Environment: p.environment, Offline: p.offline}
	input.Checks = make([]DoctorCheckInput, len(p.checks))
	for index, check := range p.checks {
		input.Checks[index] = check.input
	}
	if p.dataCompiler != nil {
		value := p.dataCompiler.input
		input.DataCompiler = &value
	}
	rebuilt, err := NewDoctorPayload(input)
	return err == nil && bytes.Equal(rebuilt.canonicalJSON, p.canonicalJSON)
}

func (DoctorPayload) Schema() string              { return DoctorSchemaV1 }
func (p DoctorPayload) ModulePath() string        { return p.modulePath }
func (p DoctorPayload) ConfigurationPath() string { return p.configurationPath }
func (p DoctorPayload) Environment() string       { return p.environment }
func (p DoctorPayload) Offline() bool             { return p.offline }
func (p DoctorPayload) Checks() []DoctorCheck     { return append([]DoctorCheck(nil), p.checks...) }
func (p DoctorPayload) DataCompiler() (DoctorDataCompiler, bool) {
	if p.dataCompiler == nil {
		return DoctorDataCompiler{}, false
	}
	return *p.dataCompiler, true
}
func (p DoctorPayload) CanonicalJSON() []byte { return append([]byte(nil), p.canonicalJSON...) }
func (DoctorPayload) commandPayload()         {}

func validateDoctorPayload(input DoctorPayloadInput) error {
	if err := modulepath.CheckProject(input.ModulePath); err != nil {
		return fmt.Errorf("module path: %v", err)
	}
	if !validSafeText(input.ConfigurationPath, 512) || input.ConfigurationPath[0] == '/' || input.ConfigurationPath[0] == '\\' {
		return errors.New("configuration path is invalid")
	}
	if input.Environment != "" && (len(input.Environment) > 200 || !validSafeText(input.Environment, 200)) {
		return errors.New("environment is invalid")
	}
	if len(input.Checks) == 0 || len(input.Checks) > 128 {
		return errors.New("checks must contain between 1 and 128 records")
	}
	seen := make(map[string]struct{}, len(input.Checks))
	for index, check := range input.Checks {
		if !validLowerKebab(check.ID, 128) {
			return fmt.Errorf("checks[%d].id is invalid", index)
		}
		if _, exists := seen[check.ID]; exists {
			return fmt.Errorf("checks[%d].id is duplicated", index)
		}
		seen[check.ID] = struct{}{}
		if !validSupportState(check.Status) || !validSafeText(check.Reason, 1024) || len(check.Verification) > 16 {
			return fmt.Errorf("checks[%d] is invalid", index)
		}
		for _, value := range check.Verification {
			if !validSafeText(value, 256) {
				return fmt.Errorf("checks[%d].verification is invalid", index)
			}
		}
	}
	if input.DataCompiler != nil {
		if err := validateDoctorDataCompiler(*input.DataCompiler); err != nil {
			return fmt.Errorf("Data compiler: %v", err)
		}
	}
	return nil
}

func validateDoctorDataCompiler(input DoctorDataCompilerInput) error {
	for name, value := range map[string]string{
		"module path": input.ModulePath, "module version": input.ModuleVersion,
		"module checksum": input.ModuleChecksum, "distribution schema": input.DistributionSchema,
		"manifest digest": input.ManifestDigest, "command import path": input.CommandImportPath,
		"analyze protocol": input.AnalyzeProtocol, "emit protocol": input.EmitProtocol,
		"declaration language": input.DeclarationLanguage, "Go toolchain": input.GoToolchain,
		"GOOS": input.GOOS, "GOARCH": input.GOARCH,
	} {
		if !validSafeText(value, 512) {
			return fmt.Errorf("%s is invalid", name)
		}
	}
	if input.BinaryDigest != "" && !validSafeText(input.BinaryDigest, 256) {
		return errors.New("binary digest is invalid")
	}
	for name, state := range map[string]SupportState{"source": input.Source, "cache": input.Cache, "build": input.Build, "offline": input.Offline} {
		if !validSupportState(state) {
			return fmt.Errorf("%s availability %q is invalid", name, state)
		}
	}
	return nil
}

func doctorCheckDocuments(values []DoctorCheck) []doctorCheckDocument {
	result := make([]doctorCheckDocument, len(values))
	for index, value := range values {
		result[index] = doctorCheckDocument{ID: value.ID(), Status: value.Status(), Reason: value.Reason(), Verification: value.Verification()}
	}
	return result
}

func (p DoctorPayload) document() doctorPayloadDocument {
	var compiler *doctorDataCompilerDocument
	if p.dataCompiler != nil {
		value := p.dataCompiler.input
		compiler = &doctorDataCompilerDocument{ModulePath: value.ModulePath, ModuleVersion: value.ModuleVersion, ModuleChecksum: value.ModuleChecksum, DistributionSchema: value.DistributionSchema, ManifestDigest: value.ManifestDigest, CommandImportPath: value.CommandImportPath, AnalyzeProtocol: value.AnalyzeProtocol, EmitProtocol: value.EmitProtocol, DeclarationLanguage: value.DeclarationLanguage, GoToolchain: value.GoToolchain, GOOS: value.GOOS, GOARCH: value.GOARCH, BinaryDigest: value.BinaryDigest, Source: value.Source, Cache: value.Cache, Build: value.Build, Offline: value.Offline}
	}
	return doctorPayloadDocument{Schema: DoctorSchemaV1, ModulePath: p.modulePath, ConfigurationPath: p.configurationPath, Environment: p.environment, Offline: p.offline, Checks: doctorCheckDocuments(p.checks), DataCompiler: compiler}
}
