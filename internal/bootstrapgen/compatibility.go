package bootstrapgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/applicationmeta"
)

const applicationModelCompatibilityVersion = 1

// ErrInvalidApplicationModelCompatibility reports a compatibility projection
// that cannot be tied to one complete generated application model.
var ErrInvalidApplicationModelCompatibility = errors.New("invalid application-model compatibility projection")

// ApplicationModelCompatibility is the immutable, non-secret projection of
// declarative YAML values that can require different generated application
// output. Its canonical identity is cryptographically associated with the
// complete application-model digest.
type ApplicationModelCompatibility struct {
	document      applicationModelCompatibilityDocument
	canonicalJSON []byte
	digest        string
	prepared      bool
}

type applicationModelCompatibilityDocument struct {
	Version                int                                     `json:"version"`
	ApplicationModelDigest string                                  `json:"application_model_digest"`
	Projection             applicationModelCompatibilityProjection `json:"projection"`
}

type applicationModelCompatibilityProjection struct {
	HTTPTransports        applicationModelCompatibilityHTTPTransports   `json:"http_transports"`
	HTTPCORS              *applicationModelCompatibilityHTTPCORS        `json:"http_cors"`
	HTTPExposures         []string                                      `json:"http_exposures"`
	InterfaceRequirements []string                                      `json:"interface_requirements"`
	ImplementationChoices []applicationModelCompatibilityImplementation `json:"implementation_choices"`
	InterfacePolicies     []applicationModelCompatibilityPolicy         `json:"interface_policies"`
}

type applicationModelCompatibilityHTTPTransports struct {
	Connect bool `json:"connect"`
	REST    bool `json:"rest"`
}

type applicationModelCompatibilityHTTPCORS struct {
	AllowedOrigins   []string `json:"allowed_origins"`
	AllowCredentials bool     `json:"allow_credentials"`
}

type applicationModelCompatibilityImplementation struct {
	Interface   string `json:"interface"`
	Constructor string `json:"constructor"`
}

type applicationModelCompatibilityPolicy struct {
	Interface string `json:"interface"`
	Timeout   string `json:"timeout"`
}

// NewApplicationModelCompatibility projects one final typed application
// manifest without process settings, ordinary Plugin configuration, Secret
// references, source paths, or resolved Secret values.
func NewApplicationModelCompatibility(applicationModelDigest string, manifest applicationmeta.Manifest) (ApplicationModelCompatibility, error) {
	if !validApplicationModelCompatibilityDigest(applicationModelDigest) {
		return ApplicationModelCompatibility{}, fmt.Errorf("%w: application-model digest is not a canonical SHA-256 digest", ErrInvalidApplicationModelCompatibility)
	}
	transports := manifest.HTTPTransports()
	projection := applicationModelCompatibilityProjection{
		HTTPTransports: applicationModelCompatibilityHTTPTransports{
			Connect: transports.Connect,
			REST:    transports.REST,
		},
		HTTPExposures:         make([]string, 0),
		InterfaceRequirements: make([]string, 0),
		ImplementationChoices: make([]applicationModelCompatibilityImplementation, 0),
		InterfacePolicies:     make([]applicationModelCompatibilityPolicy, 0),
	}
	if cors, exists := manifest.HTTPCORS(); exists {
		normalized, err := applicationmeta.NormalizeHTTPCORS(cors)
		if err != nil {
			return ApplicationModelCompatibility{}, fmt.Errorf("%w: normalize HTTP CORS: %v", ErrInvalidApplicationModelCompatibility, err)
		}
		projection.HTTPCORS = &applicationModelCompatibilityHTTPCORS{
			AllowedOrigins:   append([]string(nil), normalized.AllowedOrigins...),
			AllowCredentials: normalized.AllowCredentials,
		}
	}
	for _, exposure := range manifest.HTTPExposures() {
		projection.HTTPExposures = append(projection.HTTPExposures, exposure.ID().String())
	}
	for _, requirement := range manifest.InterfaceRequirements() {
		projection.InterfaceRequirements = append(projection.InterfaceRequirements, requirement.ID().String())
	}
	for _, choice := range manifest.ImplementationChoices() {
		projection.ImplementationChoices = append(projection.ImplementationChoices, applicationModelCompatibilityImplementation{
			Interface:   choice.InterfaceID().String(),
			Constructor: choice.Constructor().String(),
		})
	}
	for _, policy := range manifest.InterfacePolicies() {
		projection.InterfacePolicies = append(projection.InterfacePolicies, applicationModelCompatibilityPolicy{
			Interface: policy.InterfaceID().String(),
			Timeout:   policy.Timeout().String(),
		})
	}
	sort.Strings(projection.HTTPExposures)
	sort.Strings(projection.InterfaceRequirements)
	sort.Slice(projection.ImplementationChoices, func(left, right int) bool {
		return projection.ImplementationChoices[left].Interface < projection.ImplementationChoices[right].Interface
	})
	sort.Slice(projection.InterfacePolicies, func(left, right int) bool {
		return projection.InterfacePolicies[left].Interface < projection.InterfacePolicies[right].Interface
	})
	document := applicationModelCompatibilityDocument{
		Version:                applicationModelCompatibilityVersion,
		ApplicationModelDigest: applicationModelDigest,
		Projection:             projection,
	}
	canonical, err := encodeApplicationModelCompatibility(document)
	if err != nil {
		return ApplicationModelCompatibility{}, fmt.Errorf("%w: encode canonical projection: %v", ErrInvalidApplicationModelCompatibility, err)
	}
	return ApplicationModelCompatibility{
		document:      document,
		canonicalJSON: canonical,
		digest:        applicationModelCompatibilityDigest(canonical),
		prepared:      true,
	}, nil
}

// Valid reports whether the value is a complete constructor-produced
// compatibility projection.
func (c ApplicationModelCompatibility) Valid() bool {
	if !c.prepared || c.document.Version != applicationModelCompatibilityVersion || !validApplicationModelCompatibilityDigest(c.document.ApplicationModelDigest) {
		return false
	}
	canonical, err := encodeApplicationModelCompatibility(c.document)
	return err == nil && bytes.Equal(c.canonicalJSON, canonical) && c.digest == applicationModelCompatibilityDigest(canonical)
}

// ApplicationModelDigest returns the complete compiled application-model
// identity associated with this bounded compatibility projection.
func (c ApplicationModelCompatibility) ApplicationModelDigest() string {
	if !c.Valid() {
		return ""
	}
	return c.document.ApplicationModelDigest
}

// CanonicalJSON returns a defensive copy of the versioned compatibility
// projection.
func (c ApplicationModelCompatibility) CanonicalJSON() []byte {
	if !c.Valid() {
		return nil
	}
	return append([]byte(nil), c.canonicalJSON...)
}

// Digest returns the lowercase SHA-256 identity of CanonicalJSON.
func (c ApplicationModelCompatibility) Digest() string {
	if !c.Valid() {
		return ""
	}
	return c.digest
}

func validApplicationModelCompatibilityDigest(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func applicationModelCompatibilityDigest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func encodeApplicationModelCompatibility(document applicationModelCompatibilityDocument) ([]byte, error) {
	implementations := make([]map[string]any, len(document.Projection.ImplementationChoices))
	for index, implementation := range document.Projection.ImplementationChoices {
		implementations[index] = map[string]any{
			"constructor": implementation.Constructor,
			"interface":   implementation.Interface,
		}
	}
	policies := make([]map[string]any, len(document.Projection.InterfacePolicies))
	for index, policy := range document.Projection.InterfacePolicies {
		policies[index] = map[string]any{
			"interface": policy.Interface,
			"timeout":   policy.Timeout,
		}
	}
	var cors any
	if document.Projection.HTTPCORS != nil {
		cors = map[string]any{
			"allow_credentials": document.Projection.HTTPCORS.AllowCredentials,
			"allowed_origins":   document.Projection.HTTPCORS.AllowedOrigins,
		}
	}
	return json.Marshal(map[string]any{
		"application_model_digest": document.ApplicationModelDigest,
		"projection": map[string]any{
			"http_cors":              cors,
			"http_exposures":         document.Projection.HTTPExposures,
			"http_transports":        map[string]any{"connect": document.Projection.HTTPTransports.Connect, "rest": document.Projection.HTTPTransports.REST},
			"implementation_choices": implementations,
			"interface_policies":     policies,
			"interface_requirements": document.Projection.InterfaceRequirements,
		},
		"version": document.Version,
	})
}
