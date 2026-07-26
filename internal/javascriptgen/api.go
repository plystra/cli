package javascriptgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/sdkmodel"
)

const (
	// PublicAPISchema identifies the structured caller-visible JavaScript API
	// projection used by compatibility history.
	PublicAPISchema = "plystra.javascript-public-api/v1"

	publicPackageProjectionSchema = "plystra.javascript-package-surface/v1"
	publicSurfaceProjectionSchema = "plystra.javascript-interface-surface/v1"
	publicTypesProjectionSchema   = "plystra.javascript-interface-types/v1"
	publicErrorsProjectionSchema  = "plystra.javascript-interface-errors/v1"
)

// PublicAPI is one immutable structured description of the generated
// caller-visible JavaScript API. It contains no generated implementation
// source, Implementation identity, configuration, Secret, source location, or
// module version.
type PublicAPI struct {
	record        publicAPIRecord
	canonicalJSON []byte
	digest        string
	prepared      bool
}

// CanonicalJSON returns the deterministic structured public API projection.
func (a PublicAPI) CanonicalJSON() []byte {
	return append([]byte(nil), a.canonicalJSON...)
}

// Digest returns the SHA-256 digest of CanonicalJSON.
func (a PublicAPI) Digest() string { return a.digest }

// PackageDigest returns the shared package-root and public-runtime API digest.
func (a PublicAPI) PackageDigest() string {
	if !a.prepared {
		return ""
	}
	return a.record.Package.Digest
}

// Interfaces returns exact-ID-sorted defensive per-Interface API views.
func (a PublicAPI) Interfaces() []PublicInterfaceAPI {
	result := make([]PublicInterfaceAPI, len(a.record.Interfaces))
	for index, value := range a.record.Interfaces {
		result[index] = PublicInterfaceAPI{record: value}
	}
	return result
}

// Valid reports whether this API is complete and internally canonical.
func (a PublicAPI) Valid() bool {
	if !a.prepared || a.record.Schema != PublicAPISchema ||
		!validAPIDigest(a.record.Package.Digest) ||
		!validAPIDigest(a.digest) {
		return false
	}
	if err := validatePublicAPIInterfaces(a.record.Interfaces); err != nil {
		return false
	}
	packageDigest, err := digestPublicPackage(a.record.Package)
	if err != nil || packageDigest != a.record.Package.Digest {
		return false
	}
	canonical, err := json.Marshal(a.record)
	return err == nil && bytes.Equal(canonical, a.canonicalJSON) && digest(canonical) == a.digest
}

// PublicInterfaceAPI is one immutable classified caller-visible Interface API.
type PublicInterfaceAPI struct {
	record publicAPIInterface
}

// ID returns the exact canonical Interface ID.
func (a PublicInterfaceAPI) ID() string { return a.record.ID }

// SurfaceDigest returns the client path, factory symbol, operation signature,
// and package-root export digest.
func (a PublicInterfaceAPI) SurfaceDigest() string { return a.record.SurfaceDigest }

// TypesDigest returns the request, response, and reachable-message public type
// shape digest.
func (a PublicInterfaceAPI) TypesDigest() string { return a.record.TypesDigest }

// SemanticErrorsDigest returns the declared semantic-error union digest.
func (a PublicInterfaceAPI) SemanticErrorsDigest() string {
	return a.record.SemanticErrorsDigest
}

// BuildPublicAPI describes the exact Interface portion of the generated
// JavaScript API using the same preparation, collision handling, naming, and
// TypeScript mapping functions as Render.
func BuildPublicAPI(
	packageName string,
	model sdkmodel.Model,
	interfaceModel protobufmodel.InterfaceModel,
) (PublicAPI, error) {
	canonicalModel := model.CanonicalJSON()
	if len(canonicalModel) == 0 || model.Digest() != digest(canonicalModel) {
		return PublicAPI{}, fmt.Errorf("%w: SDK model is absent or has an invalid digest", ErrRender)
	}
	if !interfaceModel.Valid() {
		return PublicAPI{}, fmt.Errorf("%w: Interface projection is absent or invalid", ErrRender)
	}
	operations, err := prepareOperations(model.Operations(), model.Aliases())
	if err != nil {
		return PublicAPI{}, err
	}
	interfaces, err := prepareInterfaces(interfaceModel)
	if err != nil {
		return PublicAPI{}, err
	}
	operations = discardSupersededLegacyOperations(operations, interfaces)
	assignSurfaceSymbols(operations, interfaces)

	packageRecord, err := buildPublicPackage(packageName, len(interfaces) != 0)
	if err != nil {
		return PublicAPI{}, err
	}
	interfaceRecords := make([]publicAPIInterface, len(interfaces))
	for index, operation := range interfaces {
		record, err := buildPublicInterface(operation)
		if err != nil {
			return PublicAPI{}, err
		}
		interfaceRecords[index] = record
	}
	record := publicAPIRecord{
		Schema:     PublicAPISchema,
		Package:    packageRecord,
		Interfaces: interfaceRecords,
	}
	return finalizePublicAPI(record)
}

// BuildPublicAPIEmpty returns the canonical state before any Interface has a
// generated JavaScript surface. Compatibility reconciliation uses this only
// when no prior owned history exists.
func BuildPublicAPIEmpty() (PublicAPI, error) {
	packageRecord, err := buildPublicPackage("", false)
	if err != nil {
		return PublicAPI{}, err
	}
	return finalizePublicAPI(publicAPIRecord{
		Schema:     PublicAPISchema,
		Package:    packageRecord,
		Interfaces: []publicAPIInterface{},
	})
}

func finalizePublicAPI(record publicAPIRecord) (PublicAPI, error) {
	canonical, err := json.Marshal(record)
	if err != nil {
		return PublicAPI{}, fmt.Errorf("%w: encode structured public API: %v", ErrRender, err)
	}
	result := PublicAPI{
		record:        clonePublicAPIRecord(record),
		canonicalJSON: canonical,
		digest:        digest(canonical),
		prepared:      true,
	}
	if !result.Valid() {
		return PublicAPI{}, fmt.Errorf("%w: constructed structured public API is invalid", ErrRender)
	}
	return result, nil
}

type publicAPIRecord struct {
	Schema     string               `json:"schema"`
	Package    publicAPIPackage     `json:"package"`
	Interfaces []publicAPIInterface `json:"interfaces"`
}

type publicAPIPackage struct {
	Enabled       bool               `json:"enabled"`
	Name          string             `json:"name,omitempty"`
	Export        publicPackageRoot  `json:"export"`
	ClientType    string             `json:"client_type,omitempty"`
	ClientFactory string             `json:"client_factory,omitempty"`
	ValueExports  []string           `json:"value_exports"`
	TypeExports   []string           `json:"type_exports"`
	Runtime       []publicRuntimeAPI `json:"runtime"`
	Digest        string             `json:"digest"`
}

type publicPackageRoot struct {
	Key    string `json:"key,omitempty"`
	Types  string `json:"types,omitempty"`
	Import string `json:"import,omitempty"`
}

type publicRuntimeAPI struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Definition []string `json:"definition"`
}

type publicAPIInterface struct {
	ID                   string `json:"id"`
	SurfaceDigest        string `json:"surface_digest"`
	TypesDigest          string `json:"types_digest"`
	SemanticErrorsDigest string `json:"semantic_errors_digest"`
}

type publicInterfaceSurface struct {
	Schema           string   `json:"schema"`
	ID               string   `json:"id"`
	ClientPath       []string `json:"client_path"`
	ClientExpression string   `json:"client_expression"`
	Factory          string   `json:"factory"`
	Operation        string   `json:"operation"`
	ValueExports     []string `json:"value_exports"`
	TypeExports      []string `json:"type_exports"`
}

type publicInterfaceTypes struct {
	Schema   string             `json:"schema"`
	ID       string             `json:"id"`
	Request  string             `json:"request"`
	Response string             `json:"response"`
	Messages []publicAPIMessage `json:"messages"`
}

type publicAPIMessage struct {
	Role   string           `json:"role"`
	Name   string           `json:"name"`
	Fields []publicAPIField `json:"fields"`
}

type publicAPIField struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Type     string `json:"type"`
}

type publicInterfaceErrors struct {
	Schema string   `json:"schema"`
	ID     string   `json:"id"`
	Codes  []string `json:"codes"`
}

var sharedRuntimeAPI = []publicRuntimeAPI{
	{
		Kind: "interface",
		Name: "ClientOptions",
		Definition: []string{
			"readonly baseUrl: string | URL",
			"readonly credentialPolicy: CredentialPolicy",
			"readonly fetch?: typeof globalThis.fetch",
		},
	},
	{
		Kind: "type",
		Name: "CredentialPolicy",
		Definition: []string{
			`{ readonly mode: "anonymous" }`,
			`{ readonly mode: "cookie"; readonly fetchCredentials: "same-origin" | "include" }`,
			`{ readonly mode: "bearer"; readonly getAccessToken: () => string | PromiseLike<string> }`,
		},
	},
	{
		Kind: "type",
		Name: "JSONValue",
		Definition: []string{
			"string",
			"number",
			"boolean",
			"null",
			"{ readonly [key: string]: JSONValue }",
			"readonly JSONValue[]",
		},
	},
	{
		Kind: "type",
		Name: "KernelErrorClass",
		Definition: []string{
			`"invalid_argument"`,
			`"not_found"`,
			`"conflict"`,
			`"denied"`,
			`"unauthenticated"`,
			`"unavailable"`,
			`"timeout"`,
			`"cancelled"`,
			`"result_unknown"`,
			`"internal"`,
			`"version_incompatible"`,
		},
	},
	{
		Kind: "class",
		Name: "PlystraError",
		Definition: []string{
			"extends Error",
			"readonly status: number",
			"readonly code: string",
			"readonly detail: PlystraErrorDetail | undefined",
			"constructor(status: number, code: string, detail?: PlystraErrorDetail)",
		},
	},
	{
		Kind: "type",
		Name: "PlystraErrorDetail",
		Definition: []string{
			"readonly requestedCapabilityID: string",
			"readonly canonicalCapabilityID: string",
			"readonly traceID?: string",
			"exactly one of semanticErrorCode: string or kernelErrorClass: KernelErrorClass",
		},
	},
	{
		Kind: "interface",
		Name: "RequestOptions",
		Definition: []string{
			"readonly signal?: AbortSignal",
		},
	},
}

func buildPublicPackage(packageName string, enabled bool) (publicAPIPackage, error) {
	record := publicAPIPackage{
		Enabled:      enabled,
		ValueExports: []string{},
		TypeExports:  []string{},
		Runtime:      []publicRuntimeAPI{},
	}
	if enabled {
		if !validPackageName(packageName) {
			return publicAPIPackage{}, fmt.Errorf("%w: npm package name %q is not canonical lower-case package identity", ErrRender, packageName)
		}
		record.Name = packageName
		record.Export = publicPackageRoot{
			Key:    ".",
			Types:  "./dist/index.d.ts",
			Import: "./dist/index.js",
		}
		record.ClientType = "PlystraClient"
		record.ClientFactory = "createPlystraClient"
		record.ValueExports = []string{"PlystraError", "createPlystraClient"}
		record.TypeExports = []string{
			"ClientOptions",
			"CredentialPolicy",
			"JSONValue",
			"KernelErrorClass",
			"PlystraClient",
			"PlystraErrorDetail",
			"RequestOptions",
		}
		record.Runtime = clonePublicRuntimeAPI(sharedRuntimeAPI)
	}
	packageDigest, err := digestPublicPackage(record)
	if err != nil {
		return publicAPIPackage{}, err
	}
	record.Digest = packageDigest
	return record, nil
}

func buildPublicInterface(operation renderedInterface) (publicAPIInterface, error) {
	identifier := operation.operation.ID().String()
	components := append(append([]string(nil), operation.segments...), operation.version)
	factory := "create" + operation.symbol
	typeExports := []string{operation.symbol + "ErrorCode"}
	messages := operation.operation.Messages()
	publicMessages := make([]publicAPIMessage, len(messages))
	requestName := ""
	responseName := ""
	for messageIndex, message := range messages {
		role := "reachable"
		switch message.GoName() {
		case operation.operation.RequestGoName():
			role = "request"
		case operation.operation.ResponseGoName():
			role = "response"
		}
		publicName := interfacePublicTypeName(operation, message)
		switch role {
		case "request":
			requestName = publicName
		case "response":
			responseName = publicName
		}
		typeExports = append(typeExports, publicName)
		fields := message.Fields()
		publicFields := make([]publicAPIField, len(fields))
		for fieldIndex, field := range fields {
			fieldType, err := interfaceTypeScriptType(operation.operation, field.Type())
			if err != nil {
				return publicAPIInterface{}, fmt.Errorf(
					"%w: Interface %s message %s field %s: %v",
					ErrRender,
					identifier,
					message.GoName(),
					field.GoName(),
					err,
				)
			}
			publicFields[fieldIndex] = publicAPIField{
				Name:     field.JSONName(),
				Required: field.Required(),
				Type:     fieldType,
			}
		}
		publicMessages[messageIndex] = publicAPIMessage{
			Role:   role,
			Name:   publicName,
			Fields: publicFields,
		}
	}
	if requestName == "" || responseName == "" {
		return publicAPIInterface{}, fmt.Errorf("%w: Interface %s public API omits its request or response", ErrRender, identifier)
	}
	sort.Strings(typeExports)
	surface := publicInterfaceSurface{
		Schema:           publicSurfaceProjectionSchema,
		ID:               identifier,
		ClientPath:       components,
		ClientExpression: "client" + interfaceClientAccessor(operation),
		Factory:          factory,
		Operation:        "(request: " + requestName + ", options?: RequestOptions) => Promise<" + responseName + ">",
		ValueExports:     []string{factory},
		TypeExports:      typeExports,
	}
	types := publicInterfaceTypes{
		Schema:   publicTypesProjectionSchema,
		ID:       identifier,
		Request:  requestName,
		Response: responseName,
		Messages: publicMessages,
	}
	errors := publicInterfaceErrors{
		Schema: publicErrorsProjectionSchema,
		ID:     identifier,
		Codes:  operation.operation.SemanticErrors(),
	}
	surfaceDigest, err := digestJSON(surface)
	if err != nil {
		return publicAPIInterface{}, fmt.Errorf("%w: Interface %s JavaScript surface: %v", ErrRender, identifier, err)
	}
	typesDigest, err := digestJSON(types)
	if err != nil {
		return publicAPIInterface{}, fmt.Errorf("%w: Interface %s JavaScript types: %v", ErrRender, identifier, err)
	}
	errorsDigest, err := digestJSON(errors)
	if err != nil {
		return publicAPIInterface{}, fmt.Errorf("%w: Interface %s JavaScript semantic errors: %v", ErrRender, identifier, err)
	}
	return publicAPIInterface{
		ID:                   identifier,
		SurfaceDigest:        surfaceDigest,
		TypesDigest:          typesDigest,
		SemanticErrorsDigest: errorsDigest,
	}, nil
}

func digestPublicPackage(record publicAPIPackage) (string, error) {
	projection := struct {
		Schema        string             `json:"schema"`
		Enabled       bool               `json:"enabled"`
		Name          string             `json:"name,omitempty"`
		Export        publicPackageRoot  `json:"export"`
		ClientType    string             `json:"client_type,omitempty"`
		ClientFactory string             `json:"client_factory,omitempty"`
		ValueExports  []string           `json:"value_exports"`
		TypeExports   []string           `json:"type_exports"`
		Runtime       []publicRuntimeAPI `json:"runtime"`
	}{
		Schema:        publicPackageProjectionSchema,
		Enabled:       record.Enabled,
		Name:          record.Name,
		Export:        record.Export,
		ClientType:    record.ClientType,
		ClientFactory: record.ClientFactory,
		ValueExports:  record.ValueExports,
		TypeExports:   record.TypeExports,
		Runtime:       record.Runtime,
	}
	return digestJSON(projection)
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}

func validatePublicAPIInterfaces(values []publicAPIInterface) error {
	if values == nil {
		return fmt.Errorf("Interfaces must be a non-nil array")
	}
	for index, value := range values {
		if index > 0 && values[index-1].ID >= value.ID {
			return fmt.Errorf("Interfaces must be unique and sorted by exact ID")
		}
		if value.ID == "" ||
			!validAPIDigest(value.SurfaceDigest) ||
			!validAPIDigest(value.TypesDigest) ||
			!validAPIDigest(value.SemanticErrorsDigest) {
			return fmt.Errorf("Interfaces[%d] is invalid", index)
		}
	}
	return nil
}

func validAPIDigest(value string) bool {
	if len(value) != len("sha256:")+64 || value[:len("sha256:")] != "sha256:" {
		return false
	}
	for _, character := range value[len("sha256:"):] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func clonePublicAPIRecord(record publicAPIRecord) publicAPIRecord {
	result := record
	result.Package.ValueExports = append([]string{}, record.Package.ValueExports...)
	result.Package.TypeExports = append([]string{}, record.Package.TypeExports...)
	result.Package.Runtime = clonePublicRuntimeAPI(record.Package.Runtime)
	result.Interfaces = append([]publicAPIInterface{}, record.Interfaces...)
	return result
}

func clonePublicRuntimeAPI(values []publicRuntimeAPI) []publicRuntimeAPI {
	result := make([]publicRuntimeAPI, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Definition = append([]string(nil), value.Definition...)
	}
	return result
}
