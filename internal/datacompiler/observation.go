package datacompiler

import "strings"

// ObservationClass identifies a bounded side effect observed while acquiring
// or invoking the trusted Data compiler.
type ObservationClass string

const (
	ObservationCacheMaterialization ObservationClass = "cache_materialization"
	ObservationTemporaryFile        ObservationClass = "temporary_file"
	ObservationTrustedExecution     ObservationClass = "trusted_code_execution"
)

// Observation is a public-safe compiler effect observation. Targets and
// verification arguments are logical identities; they never contain host
// paths, private cache locations, or raw tool arguments.
type Observation struct {
	ID            string
	Class         ObservationClass
	Phase         string
	Target        string
	Reason        string
	Reversibility string
	Verification  []string
}

// ObservationSink receives observations in occurrence order.
type ObservationSink func(Observation)

func recordObservation(sink ObservationSink, observation Observation) {
	if sink == nil {
		return
	}
	observation.Verification = append([]string(nil), observation.Verification...)
	sink(observation)
}

func compilerObservationTarget(manifestDigest, goos, goarch string) string {
	return "data-compiler/" + strings.TrimPrefix(manifestDigest, "sha256:") + "/" + goos + "-" + goarch
}
