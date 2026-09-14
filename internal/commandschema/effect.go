package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/modulepath"
)

const EffectSchemaV1 = "plystra.effect/v1"

var (
	// ErrEffect reports an invalid typed effect.
	ErrEffect = errors.New("build plystra.effect")
	// ErrEffects reports invalid disposition accounting.
	ErrEffects = errors.New("build Plystra effect accounting")
)

// EffectClass is the closed public side-effect vocabulary.
type EffectClass string

const (
	EffectProjectWrite         EffectClass = "project_write"
	EffectTemporaryFile        EffectClass = "temporary_file"
	EffectCacheMaterialization EffectClass = "cache_materialization"
	EffectDownload             EffectClass = "download"
	EffectTrustedCodeExecution EffectClass = "trusted_code_execution"
	EffectProcessStartup       EffectClass = "process_startup"
	EffectBackendRead          EffectClass = "backend_read"
	EffectBackendWrite         EffectClass = "backend_write"
	EffectPublication          EffectClass = "publication"
)

// EffectInput is the construction-only form of one observed, planned,
// skipped, or unverified effect.
type EffectInput struct {
	ID            string
	Class         EffectClass
	Phase         string
	Target        string
	Owner         Owner
	Reason        string
	Reversibility string
	Verification  []string
}

// Effect is one immutable plystra.effect/v1 record.
type Effect struct {
	input         EffectInput
	canonicalJSON []byte
	prepared      bool
}

type effectDocument struct {
	Schema        string                `json:"schema"`
	ID            string                `json:"id"`
	Class         EffectClass           `json:"class"`
	Phase         string                `json:"phase"`
	Target        string                `json:"target"`
	Owner         recoveryOwnerDocument `json:"owner"`
	Reason        string                `json:"reason"`
	Reversibility string                `json:"reversibility"`
	Verification  []string              `json:"verification"`
}

// NewEffect validates and constructs one typed effect.
func NewEffect(input EffectInput) (Effect, error) {
	normalized, err := normalizeEffect(input)
	if err != nil {
		return Effect{}, fmt.Errorf("%w: %v", ErrEffect, err)
	}
	canonical, err := json.Marshal(effectDocumentFrom(normalized))
	if err != nil {
		return Effect{}, fmt.Errorf("%w: encode: %v", ErrEffect, err)
	}
	return Effect{input: normalized, canonicalJSON: canonical, prepared: true}, nil
}

// Valid reports whether NewEffect produced this effect.
func (e Effect) Valid() bool {
	if !e.prepared {
		return false
	}
	normalized, err := normalizeEffect(e.input)
	if err != nil {
		return false
	}
	canonical, err := json.Marshal(effectDocumentFrom(normalized))
	return err == nil && bytes.Equal(canonical, e.canonicalJSON)
}

// ID returns the stable effect identity.
func (e Effect) ID() string { return e.input.ID }

// Class returns the closed effect class.
func (e Effect) Class() EffectClass { return e.input.Class }

// CanonicalJSON returns a defensive copy of the effect document.
func (e Effect) CanonicalJSON() []byte { return append([]byte(nil), e.canonicalJSON...) }

// EffectsInput assigns each effect to exactly one disposition.
type EffectsInput struct {
	Observed   []Effect
	Planned    []Effect
	Skipped    []Effect
	Unverified []Effect
}

// Effects is immutable complete effect accounting.
type Effects struct {
	observed   []Effect
	planned    []Effect
	skipped    []Effect
	unverified []Effect
	prepared   bool
}

type effectsDocument struct {
	Observed   []effectDocument `json:"observed"`
	Planned    []effectDocument `json:"planned"`
	Skipped    []effectDocument `json:"skipped"`
	Unverified []effectDocument `json:"unverified"`
}

// NewEffects validates, orders, and assigns every effect exactly once.
func NewEffects(input EffectsInput) (Effects, error) {
	seen := make(map[string]string)
	observed, err := normalizeEffectDisposition("observed", input.Observed, seen)
	if err != nil {
		return Effects{}, fmt.Errorf("%w: %v", ErrEffects, err)
	}
	planned, err := normalizeEffectDisposition("planned", input.Planned, seen)
	if err != nil {
		return Effects{}, fmt.Errorf("%w: %v", ErrEffects, err)
	}
	skipped, err := normalizeEffectDisposition("skipped", input.Skipped, seen)
	if err != nil {
		return Effects{}, fmt.Errorf("%w: %v", ErrEffects, err)
	}
	unverified, err := normalizeEffectDisposition("unverified", input.Unverified, seen)
	if err != nil {
		return Effects{}, fmt.Errorf("%w: %v", ErrEffects, err)
	}
	return Effects{observed: observed, planned: planned, skipped: skipped, unverified: unverified, prepared: true}, nil
}

// Valid reports whether NewEffects produced this accounting object.
func (e Effects) Valid() bool {
	if !e.prepared {
		return false
	}
	rebuilt, err := NewEffects(EffectsInput{
		Observed:   e.Observed(),
		Planned:    e.Planned(),
		Skipped:    e.Skipped(),
		Unverified: e.Unverified(),
	})
	return err == nil && equalEffects(e, rebuilt)
}

// Observed returns a defensive copy of completed effects.
func (e Effects) Observed() []Effect { return cloneEffects(e.observed) }

// Planned returns a defensive copy of determined but unexecuted effects.
func (e Effects) Planned() []Effect { return cloneEffects(e.planned) }

// Skipped returns a defensive copy of deliberately unattempted effects.
func (e Effects) Skipped() []Effect { return cloneEffects(e.skipped) }

// Unverified returns a defensive copy of effects whose outcome is unknown.
func (e Effects) Unverified() []Effect { return cloneEffects(e.unverified) }

func normalizeEffect(input EffectInput) (EffectInput, error) {
	if !validLowerKebab(input.ID, 128) {
		return EffectInput{}, errors.New("id must be canonical lower kebab case")
	}
	if !validEffectClass(input.Class) {
		return EffectInput{}, fmt.Errorf("class %q is not supported", input.Class)
	}
	if !validToken(input.Phase, 128) || !validRelativePath(input.Target, true) {
		return EffectInput{}, errors.New("phase or target is invalid")
	}
	if modulepath.CheckProject(input.Owner.Module) != nil || !validRelativePath(input.Owner.Path, true) {
		return EffectInput{}, errors.New("owner must identify a valid module and project-relative path")
	}
	if !validToken(input.Reason, 128) || !validToken(input.Reversibility, 128) {
		return EffectInput{}, errors.New("reason or reversibility is invalid")
	}
	if !validArgumentVector(input.Verification) {
		return EffectInput{}, errors.New("verification must be a fully bound argv")
	}
	return EffectInput{
		ID:            input.ID,
		Class:         input.Class,
		Phase:         input.Phase,
		Target:        input.Target,
		Owner:         input.Owner,
		Reason:        input.Reason,
		Reversibility: input.Reversibility,
		Verification:  cloneStrings(input.Verification),
	}, nil
}

func validEffectClass(class EffectClass) bool {
	switch class {
	case EffectProjectWrite, EffectTemporaryFile, EffectCacheMaterialization, EffectDownload, EffectTrustedCodeExecution, EffectProcessStartup, EffectBackendRead, EffectBackendWrite, EffectPublication:
		return true
	default:
		return false
	}
}

func normalizeEffectDisposition(disposition string, input []Effect, seen map[string]string) ([]Effect, error) {
	if len(input) > 4_096 {
		return nil, fmt.Errorf("%s effect count exceeds 4096", disposition)
	}
	result := cloneEffects(input)
	for index, effect := range result {
		if !effect.Valid() {
			return nil, fmt.Errorf("%s[%d] is invalid", disposition, index)
		}
		if prior, exists := seen[effect.ID()]; exists {
			return nil, fmt.Errorf("effect %q appears in both %s and %s", effect.ID(), prior, disposition)
		}
		seen[effect.ID()] = disposition
	}
	sort.Slice(result, func(left, right int) bool { return effectKey(result[left]) < effectKey(result[right]) })
	return result, nil
}

func effectDocumentFrom(input EffectInput) effectDocument {
	return effectDocument{
		Schema:        EffectSchemaV1,
		ID:            input.ID,
		Class:         input.Class,
		Phase:         input.Phase,
		Target:        input.Target,
		Owner:         recoveryOwnerDocument{Module: input.Owner.Module, Path: input.Owner.Path},
		Reason:        input.Reason,
		Reversibility: input.Reversibility,
		Verification:  cloneStrings(input.Verification),
	}
}

func effectsDocumentFrom(input Effects) effectsDocument {
	return effectsDocument{
		Observed:   effectDocuments(input.observed),
		Planned:    effectDocuments(input.planned),
		Skipped:    effectDocuments(input.skipped),
		Unverified: effectDocuments(input.unverified),
	}
}

func effectDocuments(values []Effect) []effectDocument {
	result := make([]effectDocument, len(values))
	for index, value := range values {
		result[index] = effectDocumentFrom(value.input)
	}
	return result
}

func cloneEffects(values []Effect) []Effect {
	return append([]Effect(nil), values...)
}

func effectKey(value Effect) string {
	return fmt.Sprintf("%s\x00%s\x00%s\x00%s", value.input.Phase, value.input.Class, value.input.Target, value.input.ID)
}

func equalEffects(left, right Effects) bool {
	return equalEffectSlices(left.observed, right.observed) && equalEffectSlices(left.planned, right.planned) && equalEffectSlices(left.skipped, right.skipped) && equalEffectSlices(left.unverified, right.unverified)
}

func equalEffectSlices(left, right []Effect) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !bytes.Equal(left[index].canonicalJSON, right[index].canonicalJSON) {
			return false
		}
	}
	return true
}
