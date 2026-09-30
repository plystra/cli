// Package invocationpolicy owns the resolved policy input shared by generated
// assembly and frozen application identity.
package invocationpolicy

import (
	"errors"
	"fmt"
	"time"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/kernel/invocation"
)

var ErrInvalid = errors.New("invalid compiled invocation policy")

// Policy contains every normalized field, including disabled stages and versions.
type Policy struct {
	SchemaVersion    int           `json:"schema_version"`
	CompilerVersion  int           `json:"compiler_version"`
	DefaultsVersion  int           `json:"defaults_version"`
	Timeout          time.Duration `json:"timeout_ns"`
	ConcurrencyLimit int           `json:"concurrency_limit"`
	QueueLimit       int           `json:"queue_limit"`
	Retry            Retry         `json:"retry"`
	Circuit          Circuit       `json:"circuit"`
}

type Retry struct {
	Eligibility string        `json:"eligibility"`
	MaxAttempts int           `json:"max_attempts"`
	Backoff     time.Duration `json:"backoff_ns"`
}

type Circuit struct {
	FailureThreshold int           `json:"failure_threshold"`
	OpenFor          time.Duration `json:"open_for_ns"`
	ProbeLimit       int           `json:"probe_limit"`
}

// Default resolves the complete absence contract; no runtime defaulting remains.
func Default() Policy {
	return Policy{
		SchemaVersion:    invocation.PolicySchemaVersion,
		CompilerVersion:  invocation.PolicyCompilerVersion,
		DefaultsVersion:  invocation.PolicyDefaultsVersion,
		ConcurrencyLimit: invocation.DefaultConcurrencyLimit,
		Retry:            Retry{MaxAttempts: 1},
	}
}

// Compile resolves the authored policy into complete immutable assembly input.
func Compile(authored applicationmeta.InterfacePolicy) Policy {
	policy := Default()
	policy.Timeout = authored.Timeout()
	policy.Retry = Retry{Eligibility: authored.RetryEligibility(), MaxAttempts: authored.RetryMaxAttempts(), Backoff: authored.RetryBackoff()}
	return policy
}

// Validate rejects incompatible input before rendering. Enabled stages remain
// subject to the installed authored-policy support gate.
func (p Policy) Validate() error {
	if p.SchemaVersion != invocation.PolicySchemaVersion || p.CompilerVersion != invocation.PolicyCompilerVersion || p.DefaultsVersion != invocation.PolicyDefaultsVersion ||
		p.Timeout < 0 || p.ConcurrencyLimit < 1 || p.ConcurrencyLimit > invocation.MaximumConcurrencyLimit || p.QueueLimit != 0 || p.Circuit != (Circuit{}) {
		return ErrInvalid
	}
	r := p.Retry
	if r.MaxAttempts == 1 && r.Eligibility == "" && r.Backoff == 0 {
		return nil
	}
	if r.Eligibility == invocation.RetryReplaySafe && r.MaxAttempts >= 2 && r.MaxAttempts <= invocation.MaximumRetryAttempts && r.Backoff >= 0 && p.Timeout > 0 {
		return nil
	}
	return ErrInvalid
}

// Source returns an exact Go assembly literal. Version numbers are frozen here,
// not read from the runtime module's constants by the generated application.
func (p Policy) Source() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	return fmt.Sprintf("kernelinvocation.Policy{SchemaVersion: %d, CompilerVersion: %d, DefaultsVersion: %d, Timeout: %d, ConcurrencyLimit: %d, QueueLimit: %d, Retry: kernelinvocation.RetryPolicy{Eligibility: %q, MaxAttempts: %d, Backoff: %d}, Circuit: kernelinvocation.CircuitPolicy{FailureThreshold: %d, OpenFor: %d, ProbeLimit: %d}}",
		p.SchemaVersion, p.CompilerVersion, p.DefaultsVersion, p.Timeout, p.ConcurrencyLimit, p.QueueLimit,
		p.Retry.Eligibility, p.Retry.MaxAttempts, p.Retry.Backoff, p.Circuit.FailureThreshold, p.Circuit.OpenFor, p.Circuit.ProbeLimit), nil
}
