package applicationmeta

import (
	"fmt"
	"sort"
	"strings"
)

// HTTPTransportSelectionError reports effective public Interface exposures
// whose selected current-Project configuration enables no HTTP transport.
type HTTPTransportSelectionError struct {
	exposures []HTTPExposure
}

// Exposures returns a defensive copy sorted by canonical Interface ID.
func (e *HTTPTransportSelectionError) Exposures() []HTTPExposure {
	if e == nil {
		return nil
	}
	return append([]HTTPExposure(nil), e.exposures...)
}

func (e *HTTPTransportSelectionError) Error() string {
	if e == nil {
		return ErrHTTPTransportSelection.Error()
	}
	declared := make([]string, len(e.exposures))
	for index, exposure := range e.exposures {
		declared[index] = fmt.Sprintf("%s at %s", exposure.ID(), exposure.Source())
	}
	return fmt.Sprintf(
		"%s: http.expose is nonempty while http.transports.connect and http.transports.rest are both false; enable at least one transport in the selected current-project configuration; exposed Interfaces: %s",
		ErrHTTPTransportSelection,
		strings.Join(declared, ", "),
	)
}

// Unwrap supports errors.Is with ErrHTTPTransportSelection.
func (*HTTPTransportSelectionError) Unwrap() error { return ErrHTTPTransportSelection }

func newHTTPTransportSelectionError(exposures []HTTPExposure) error {
	ordered := append([]HTTPExposure(nil), exposures...)
	sort.Slice(ordered, func(left, right int) bool {
		return ordered[left].ID().String() < ordered[right].ID().String()
	})
	return &HTTPTransportSelectionError{exposures: ordered}
}
