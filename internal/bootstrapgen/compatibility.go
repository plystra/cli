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
	HTTPTransports  applicationModelCompatibilityHTTPTransports `json:"http_transports"`
	HTTPCORS        *applicationModelCompatibilityHTTPCORS      `json:"http_cors"`
	HTTPExposures   []string                                    `json:"http_exposures"`
	Requirements    []string                                    `json:"requirements"`
	ProviderChoices []applicationModelCompatibilityProvider     `json:"provider_choices"`
	Aliases         []applicationModelCompatibilityAlias        `json:"aliases"`
}

type applicationModelCompatibilityHTTPTransports struct {
	Connect bool `json:"connect"`
	REST    bool `json:"rest"`
}

type applicationModelCompatibilityHTTPCORS struct {
	AllowedOrigins   []string `json:"allowed_origins"`
	AllowCredentials bool     `json:"allow_credentials"`
}

type applicationModelCompatibilityProvider struct {
	Capability string `json:"capability"`
	PluginID   string `json:"plugin_id"`
}

type applicationModelCompatibilityAlias struct {
	ID         string                                 `json:"id"`
	Target     string                                 `json:"target"`
	Exposure   *applicationModelCompatibilityExposure `json:"exposure"`
	Deprecated string                                 `json:"deprecated"`
}

type applicationModelCompatibilityExposure struct {
	Go         bool `json:"go"`
	HTTP       bool `json:"http"`
	JavaScript bool `json:"javascript"`
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
		HTTPExposures:   make([]string, 0),
		Requirements:    make([]string, 0),
		ProviderChoices: make([]applicationModelCompatibilityProvider, 0),
		Aliases:         make([]applicationModelCompatibilityAlias, 0),
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
	for _, requirement := range manifest.Requirements() {
		projection.Requirements = append(projection.Requirements, requirement.ID().String())
	}
	for _, choice := range manifest.ProviderChoices() {
		projection.ProviderChoices = append(projection.ProviderChoices, applicationModelCompatibilityProvider{
			Capability: choice.Capability().String(),
			PluginID:   choice.PluginID(),
		})
	}
	for _, alias := range manifest.Aliases() {
		record := applicationModelCompatibilityAlias{
			ID:         alias.ID().String(),
			Target:     alias.Target().String(),
			Deprecated: alias.Deprecated(),
		}
		if exposure, exists := alias.Exposure(); exists {
			record.Exposure = &applicationModelCompatibilityExposure{
				Go:         exposure.Go,
				HTTP:       exposure.HTTP,
				JavaScript: exposure.JavaScript,
			}
		}
		projection.Aliases = append(projection.Aliases, record)
	}
	sort.Strings(projection.HTTPExposures)
	sort.Strings(projection.Requirements)
	sort.Slice(projection.ProviderChoices, func(left, right int) bool {
		return projection.ProviderChoices[left].Capability < projection.ProviderChoices[right].Capability
	})
	sort.Slice(projection.Aliases, func(left, right int) bool {
		return projection.Aliases[left].ID < projection.Aliases[right].ID
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
	providers := make([]map[string]any, len(document.Projection.ProviderChoices))
	for index, provider := range document.Projection.ProviderChoices {
		providers[index] = map[string]any{
			"capability": provider.Capability,
			"plugin_id":  provider.PluginID,
		}
	}
	aliases := make([]map[string]any, len(document.Projection.Aliases))
	for index, alias := range document.Projection.Aliases {
		var exposure any
		if alias.Exposure != nil {
			exposure = map[string]any{
				"go":         alias.Exposure.Go,
				"http":       alias.Exposure.HTTP,
				"javascript": alias.Exposure.JavaScript,
			}
		}
		aliases[index] = map[string]any{
			"deprecated": alias.Deprecated,
			"exposure":   exposure,
			"id":         alias.ID,
			"target":     alias.Target,
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
			"aliases":          aliases,
			"http_cors":        cors,
			"http_exposures":   document.Projection.HTTPExposures,
			"http_transports":  map[string]any{"connect": document.Projection.HTTPTransports.Connect, "rest": document.Projection.HTTPTransports.REST},
			"provider_choices": providers,
			"requirements":     document.Projection.Requirements,
		},
		"version": document.Version,
	})
}
