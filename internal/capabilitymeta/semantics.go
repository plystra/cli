package capabilitymeta

import kernelmanifest "github.com/plystra/kernel/plugin/manifest"

type CapabilityKind = kernelmanifest.CapabilityKind

const (
	CapabilityKindQuery   = kernelmanifest.CapabilityKindQuery
	CapabilityKindCommand = kernelmanifest.CapabilityKindCommand
	CapabilityKindEvent   = kernelmanifest.CapabilityKindEvent
	CapabilityKindStream  = kernelmanifest.CapabilityKindStream
)

type CapabilityEffects = kernelmanifest.CapabilityEffects

const (
	CapabilityEffectsNone          = kernelmanifest.CapabilityEffectsNone
	CapabilityEffectsLocal         = kernelmanifest.CapabilityEffectsLocal
	CapabilityEffectsExternal      = kernelmanifest.CapabilityEffectsExternal
	CapabilityEffectsExternalWrite = kernelmanifest.CapabilityEffectsExternalWrite
)

type IdempotencyMode = kernelmanifest.IdempotencyMode

const (
	IdempotencyModeNone     = kernelmanifest.IdempotencyModeNone
	IdempotencyModeInherent = kernelmanifest.IdempotencyModeInherent
	IdempotencyModeKeyed    = kernelmanifest.IdempotencyModeKeyed
)

type RetrySafety = kernelmanifest.RetrySafety

const (
	RetrySafetyNever                  = kernelmanifest.RetrySafetyNever
	RetrySafetySafe                   = kernelmanifest.RetrySafetySafe
	RetrySafetyRequiresIdempotencyKey = kernelmanifest.RetrySafetyRequiresIdempotencyKey
)

type CancellationMode = kernelmanifest.CancellationMode

const (
	CancellationModeUnsupported = kernelmanifest.CancellationModeUnsupported
	CancellationModeBestEffort  = kernelmanifest.CancellationModeBestEffort
)

type CompletionMode = kernelmanifest.CompletionMode

const (
	CompletionModeCompletedBeforeReturn = kernelmanifest.CompletionModeCompletedBeforeReturn
	CompletionModeAcceptedForProcessing = kernelmanifest.CompletionModeAcceptedForProcessing
)

type OrderingMode = kernelmanifest.OrderingMode

const (
	OrderingModeNone   = kernelmanifest.OrderingModeNone
	OrderingModePerKey = kernelmanifest.OrderingModePerKey
	OrderingModeGlobal = kernelmanifest.OrderingModeGlobal
)

type DataClassification = kernelmanifest.DataClassification

const (
	DataClassificationPublic       = kernelmanifest.DataClassificationPublic
	DataClassificationInternal     = kernelmanifest.DataClassificationInternal
	DataClassificationConfidential = kernelmanifest.DataClassificationConfidential
	DataClassificationRestricted   = kernelmanifest.DataClassificationRestricted
)

type IdempotencySemantics = kernelmanifest.IdempotencySemantics
type RetrySemantics = kernelmanifest.RetrySemantics
type CancellationSemantics = kernelmanifest.CancellationSemantics
type CompletionSemantics = kernelmanifest.CompletionSemantics
type OrderingSemantics = kernelmanifest.OrderingSemantics
type DataSemantics = kernelmanifest.DataSemantics
type CapabilitySemantics = kernelmanifest.CapabilitySemantics
