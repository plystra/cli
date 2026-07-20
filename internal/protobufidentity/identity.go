// Package protobufidentity projects exact Plystra Capability identities into
// deterministic, reversible Protobuf and Connect surface identities.
package protobufidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/goname"
)

const (
	packagePrefix = "plystra.generated."
	methodName    = "Invoke"
)

var (
	// ErrBuild reports invalid or internally inconsistent Protobuf surface
	// identity input.
	ErrBuild = errors.New("build Protobuf surface identities")
	// ErrCollision reports two authored identities projected onto one generated
	// Protobuf or Connect identity.
	ErrCollision = errors.New("generated Protobuf identity collision")
)

// Surface binds one public canonical or Alias ID to the canonical target whose
// request and response messages it reuses.
type Surface struct {
	PublicID    string
	CanonicalID string
}

// Identity is one immutable public Protobuf service and Connect procedure
// projection. Alias services use their public package while reusing the exact
// canonical target message identities.
type Identity struct {
	publicID     string
	canonicalID  string
	packageName  string
	service      string
	method       string
	requestType  string
	responseType string
	procedure    string
}

// PublicID returns the exact canonical or Alias identity used by callers.
func (i Identity) PublicID() string { return i.publicID }

// CanonicalID returns the exact canonical Capability dispatched by the
// generated application path.
func (i Identity) CanonicalID() string { return i.canonicalID }

// Package returns the public Protobuf package identity.
func (i Identity) Package() string { return i.packageName }

// Service returns the unqualified generated Protobuf service name.
func (i Identity) Service() string { return i.service }

// Method returns the closed unary procedure method name.
func (i Identity) Method() string { return i.method }

// RequestType returns the fully qualified canonical request message identity.
func (i Identity) RequestType() string { return i.requestType }

// ResponseType returns the fully qualified canonical response message identity.
func (i Identity) ResponseType() string { return i.responseType }

// Procedure returns the exact Connect HTTP procedure path.
func (i Identity) Procedure() string { return i.procedure }

// Set is one immutable public-ID-sorted projection with deterministic canonical
// JSON and digest evidence.
type Set struct {
	identities    []Identity
	canonicalJSON []byte
	digest        string
	prepared      bool
}

// Valid reports whether Build produced the set.
func (s Set) Valid() bool { return s.prepared && len(s.canonicalJSON) != 0 && s.digest != "" }

// Identities returns defensive public-ID-sorted identity values.
func (s Set) Identities() []Identity { return append([]Identity(nil), s.identities...) }

// CanonicalJSON returns defensive normalized projection bytes.
func (s Set) CanonicalJSON() []byte { return append([]byte(nil), s.canonicalJSON...) }

// Digest returns the SHA-256 digest of CanonicalJSON.
func (s Set) Digest() string { return s.digest }

type canonicalSet struct {
	Version  int                 `json:"version"`
	Surfaces []canonicalIdentity `json:"surfaces"`
}

type canonicalIdentity struct {
	PublicID     string `json:"public_id"`
	CanonicalID  string `json:"canonical_id"`
	Package      string `json:"package"`
	Service      string `json:"service"`
	Method       string `json:"method"`
	RequestType  string `json:"request_type"`
	ResponseType string `json:"response_type"`
	Procedure    string `json:"procedure"`
}

// Build validates, projects, collision-checks, and canonicalizes public
// surfaces independently of discovery or declaration order.
func Build(surfaces []Surface) (Set, error) {
	ordered := append([]Surface(nil), surfaces...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].PublicID != ordered[right].PublicID {
			return ordered[left].PublicID < ordered[right].PublicID
		}
		return ordered[left].CanonicalID < ordered[right].CanonicalID
	})

	identities := make([]Identity, len(ordered))
	publicOwners := make(map[string]string, len(ordered))
	packageOwners := make(map[string]string, len(ordered))
	serviceOwners := make(map[string]string, len(ordered))
	procedureOwners := make(map[string]string, len(ordered))
	requestOwners := make(map[string]string, len(ordered))
	responseOwners := make(map[string]string, len(ordered))
	for index, surface := range ordered {
		publicID, err := capabilityid.Parse(surface.PublicID)
		if err != nil {
			return Set{}, fmt.Errorf("%w: surfaces[%d].public_id %q is not canonical: %v", ErrBuild, index, surface.PublicID, err)
		}
		canonicalID, err := capabilityid.Parse(surface.CanonicalID)
		if err != nil {
			return Set{}, fmt.Errorf("%w: surfaces[%d].canonical_id %q is not canonical: %v", ErrBuild, index, surface.CanonicalID, err)
		}
		if publicID.Major() != canonicalID.Major() {
			return Set{}, fmt.Errorf("%w: public Capability %s and canonical target %s must use the same major version", ErrBuild, publicID, canonicalID)
		}

		identity := project(publicID, canonicalID)
		if previous, duplicate := publicOwners[identity.publicID]; duplicate {
			return Set{}, collision("public Capability", identity.publicID, previous, identity.publicID)
		}
		publicOwners[identity.publicID] = identity.publicID
		if err := registerUnique(packageOwners, "package", identity.packageName, identity.publicID); err != nil {
			return Set{}, err
		}
		qualifiedService := identity.packageName + "." + identity.service
		if err := registerUnique(serviceOwners, "service", qualifiedService, identity.publicID); err != nil {
			return Set{}, err
		}
		if err := registerUnique(procedureOwners, "procedure", identity.procedure, identity.publicID); err != nil {
			return Set{}, err
		}
		if err := registerCanonicalMessage(requestOwners, "request message", identity.requestType, identity.canonicalID); err != nil {
			return Set{}, err
		}
		if err := registerCanonicalMessage(responseOwners, "response message", identity.responseType, identity.canonicalID); err != nil {
			return Set{}, err
		}
		identities[index] = identity
	}

	document := canonicalSet{Version: 1, Surfaces: make([]canonicalIdentity, len(identities))}
	for index, identity := range identities {
		document.Surfaces[index] = canonicalIdentity{
			PublicID:     identity.publicID,
			CanonicalID:  identity.canonicalID,
			Package:      identity.packageName,
			Service:      identity.service,
			Method:       identity.method,
			RequestType:  identity.requestType,
			ResponseType: identity.responseType,
			Procedure:    identity.procedure,
		}
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return Set{}, fmt.Errorf("%w: encode canonical projection: %v", ErrBuild, err)
	}
	sum := sha256.Sum256(canonical)
	return Set{
		identities:    identities,
		canonicalJSON: canonical,
		digest:        "sha256:" + hex.EncodeToString(sum[:]),
		prepared:      true,
	}, nil
}

func project(publicID, canonicalID capabilityid.Identifier) Identity {
	publicPackage := Package(publicID)
	canonicalPackage := Package(canonicalID)
	publicBase := typeBase(publicID)
	canonicalBase := typeBase(canonicalID)
	service := publicBase + "Service"
	return Identity{
		publicID:     publicID.String(),
		canonicalID:  canonicalID.String(),
		packageName:  publicPackage,
		service:      service,
		method:       methodName,
		requestType:  canonicalPackage + "." + canonicalBase + "Request",
		responseType: canonicalPackage + "." + canonicalBase + "Response",
		procedure:    "/" + publicPackage + "." + service + "/" + methodName,
	}
}

// Package returns the reversible Protobuf package for one exact canonical ID.
func Package(identifier capabilityid.Identifier) string {
	segments := strings.Split(identifier.Name(), ".")
	for index := range segments {
		segments[index] = strings.ReplaceAll(segments[index], "-", "_h_")
	}
	segments = append(segments, "v"+strconv.FormatUint(identifier.Major(), 10))
	return packagePrefix + strings.Join(segments, ".")
}

// FieldJSONName returns the deterministic ProtoJSON lower-camel identity for
// one canonical lower snake-case field name.
func FieldJSONName(value string) string {
	var result strings.Builder
	result.Grow(len(value))
	uppercaseNext := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '_' {
			uppercaseNext = true
			continue
		}
		if uppercaseNext && character >= 'a' && character <= 'z' {
			character -= 'a' - 'A'
		}
		uppercaseNext = false
		result.WriteByte(character)
	}
	return result.String()
}

// EnumType returns the fully qualified generated enum identity owned by one
// canonical message field.
func EnumType(messageType, fieldName string) string {
	return messageType + goname.Field(fieldName) + "Enum"
}

// DecodePackage reverses a generated package identity into its exact canonical
// Capability ID and rejects noncanonical encodings.
func DecodePackage(value string) (string, error) {
	encoded, found := strings.CutPrefix(value, packagePrefix)
	if !found {
		return "", fmt.Errorf("%w: package %q does not start with %q", ErrBuild, value, packagePrefix)
	}
	segments := strings.Split(encoded, ".")
	if len(segments) < 3 {
		return "", fmt.Errorf("%w: package %q does not contain a canonical name and version", ErrBuild, value)
	}
	version := segments[len(segments)-1]
	if len(version) < 2 || version[0] != 'v' || version[1] == '0' {
		return "", fmt.Errorf("%w: package %q has a noncanonical version segment", ErrBuild, value)
	}
	major, err := strconv.ParseUint(version[1:], 10, 64)
	if err != nil || major == 0 {
		return "", fmt.Errorf("%w: package %q has an invalid version segment", ErrBuild, value)
	}
	nameSegments := segments[:len(segments)-1]
	for index, segment := range nameSegments {
		decoded, err := decodeSegment(segment)
		if err != nil {
			return "", fmt.Errorf("%w: package %q segment %d: %v", ErrBuild, value, index, err)
		}
		nameSegments[index] = decoded
	}
	identifier, err := capabilityid.New(strings.Join(nameSegments, "."), major)
	if err != nil || Package(identifier) != value {
		return "", fmt.Errorf("%w: package %q is not a canonical reversible encoding", ErrBuild, value)
	}
	return identifier.String(), nil
}

func typeBase(identifier capabilityid.Identifier) string {
	words := strings.FieldsFunc(identifier.Name(), func(character rune) bool {
		return character == '.' || character == '-'
	})
	for index, word := range words {
		words[index] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, "") + "V" + strconv.FormatUint(identifier.Major(), 10)
}

func decodeSegment(value string) (string, error) {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return "", errors.New("encoded segment must start with a lower-case letter")
	}
	var decoded strings.Builder
	for index := 0; index < len(value); {
		character := value[index]
		if character == '_' {
			if !strings.HasPrefix(value[index:], "_h_") {
				return "", errors.New("underscore must be the canonical _h_ hyphen escape")
			}
			decoded.WriteByte('-')
			index += len("_h_")
			continue
		}
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return "", fmt.Errorf("encoded segment contains invalid character %q", character)
		}
		decoded.WriteByte(character)
		index++
	}
	return decoded.String(), nil
}

func registerUnique(owners map[string]string, kind, value, owner string) error {
	if previous, exists := owners[value]; exists && previous != owner {
		return collision(kind, value, previous, owner)
	}
	owners[value] = owner
	return nil
}

func registerCanonicalMessage(owners map[string]string, kind, value, canonicalID string) error {
	if previous, exists := owners[value]; exists && previous != canonicalID {
		return collision(kind, value, previous, canonicalID)
	}
	owners[value] = canonicalID
	return nil
}

func collision(kind, value, left, right string) error {
	return fmt.Errorf("%w: %w: %s %q is produced by %s and %s", ErrBuild, ErrCollision, kind, value, left, right)
}
