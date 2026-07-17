// Package httpgen renders deterministic canonical HTTP adapters that invoke
// only the CLI-generated application path for an explicitly exposed Capability.
package httpgen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/goname"
	"golang.org/x/mod/module"
)

const maximumBodyBytes = 1 << 20

var (
	// ErrRender reports that a normalized exposed Capability could not produce
	// one strict generated HTTP adapter.
	ErrRender = errors.New("render generated HTTP capability adapter")
	// ErrTarget reports an absent, unexposed, or internally inconsistent
	// canonical target view.
	ErrTarget = errors.New("invalid generated HTTP canonical target")
	// ErrPlan reports a contribution plan that cannot be paired with the
	// generated canonical invocation surface.
	ErrPlan = errors.New("invalid generated HTTP contribution plan")
)

// CanonicalTargetView is the final normalized canonical Capability input used
// to authorize one generated HTTP surface.
type CanonicalTargetView interface {
	ID() generation.CapabilityID
	ContractJSON() []byte
	ContractDigest() string
	Exposure() generation.Exposure
}

// AliasView is the final immutable application-local Alias surface consumed
// after canonical provider and generation-extension resolution has stabilized.
type AliasView interface {
	ID() generation.CapabilityID
	Target() generation.CapabilityID
	TargetContractDigest() string
	Exposure() generation.Exposure
	Deprecated() string
}

// File is one immutable generated canonical HTTP adapter output.
type File struct {
	path        string
	packageName string
	data        []byte
}

// Path returns the slash-separated module-relative generated file path.
func (f File) Path() string { return f.path }

// PackageName returns the generated Go package identifier.
func (f File) PackageName() string { return f.packageName }

// Data returns a defensive copy of the formatted generated source.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

type canonicalContract struct {
	ID       string                    `json:"id"`
	Request  map[string]canonicalField `json:"request"`
	Response map[string]canonicalField `json:"response"`
	Errors   []string                  `json:"errors"`
}

type canonicalField struct {
	Type     string            `json:"type"`
	Items    string            `json:"items,omitempty"`
	Required bool              `json:"required,omitempty"`
	Enum     []json.RawMessage `json:"enum,omitempty"`
}

type preparedField struct {
	wireName string
	goName   string
	field    canonicalField
}

// Render validates one final canonical target view and emits its strict HTTP
// transport. The caller cannot use this entry point for an internal-only target.
func Render(modulePath string, target CanonicalTargetView) (File, error) {
	return render(modulePath, target, nil)
}

// RenderPlan emits the HTTP adapter paired with a lowered canonical invocation
// plan, including its ordered ingress and egress integration path when needed.
func RenderPlan(modulePath string, target CanonicalTargetView, plan generationlowering.Plan) (File, error) {
	return render(modulePath, target, &plan)
}

// RenderAlias emits one thin application-local route wrapper around the
// canonical generated HTTP handler. It never owns transport validation,
// application invocation, a provider, or a Kernel registration.
func RenderAlias(modulePath string, alias AliasView, target CanonicalTargetView) (File, error) {
	if err := module.CheckPath(modulePath); err != nil {
		return File{}, fmt.Errorf("%w: invalid Go Module path %q: %v", ErrRender, modulePath, err)
	}
	if alias == nil {
		return File{}, fmt.Errorf("%w: Alias view is absent", ErrRender)
	}
	aliasID, err := capabilityid.Parse(alias.ID().String())
	if err != nil {
		return File{}, fmt.Errorf("%w: Alias ID %q is not canonical: %v", ErrRender, alias.ID().String(), err)
	}
	targetID, err := capabilityid.Parse(alias.Target().String())
	if err != nil {
		return File{}, fmt.Errorf("%w: Alias %s target ID %q is not canonical: %v", ErrRender, aliasID, alias.Target().String(), err)
	}
	resolvedTargetID, _, err := normalizeTarget(target)
	if err != nil {
		return File{}, fmt.Errorf("%w: Alias %s: %w", ErrRender, aliasID, err)
	}
	if targetID != resolvedTargetID {
		return File{}, fmt.Errorf("%w: Alias %s targets %s, but received canonical target %s", ErrRender, aliasID, targetID, resolvedTargetID)
	}
	if aliasID == targetID {
		return File{}, fmt.Errorf("%w: Alias %s cannot target itself", ErrRender, aliasID)
	}
	if strings.HasPrefix(aliasID.Name(), "kernel.") {
		return File{}, fmt.Errorf("%w: Alias %s uses the reserved kernel.* canonical namespace", ErrRender, aliasID)
	}
	if aliasID.Major() != targetID.Major() {
		return File{}, fmt.Errorf("%w: Alias %s and target %s do not use the same version", ErrRender, aliasID, targetID)
	}
	aliasExposure, targetExposure := alias.Exposure(), target.Exposure()
	if !exposureSubset(aliasExposure, targetExposure) {
		return File{}, fmt.Errorf("%w: Alias %s exposure broadens canonical target %s", ErrRender, aliasID, targetID)
	}
	if !aliasExposure.HTTP {
		return File{}, fmt.Errorf("%w: Alias %s is not exposed over HTTP", ErrRender, aliasID)
	}
	if alias.TargetContractDigest() != target.ContractDigest() {
		return File{}, fmt.Errorf("%w: Alias %s target digest does not match canonical target %s", ErrRender, aliasID, targetID)
	}
	if len(alias.Deprecated()) > 1024 || !utf8.ValidString(alias.Deprecated()) || strings.ContainsRune(alias.Deprecated(), '\x00') {
		return File{}, fmt.Errorf("%w: Alias %s deprecation metadata is invalid", ErrRender, aliasID)
	}

	packageName := goname.Package(aliasID)
	canonicalAdapterImport := path.Join(modulePath, generatedDirectory("adapters/http", targetID))
	routePath := "/api/v1/capabilities/" + aliasID.String() + "/invoke"
	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "package %s\n\n", packageName)
	fmt.Fprintln(&source, "import (")
	fmt.Fprintln(&source, "\t\"net/http\"")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "\tcanonicaladapter %s\n", strconv.Quote(canonicalAdapterImport))
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "const (")
	fmt.Fprintf(&source, "\tRoutePath = %s\n", strconv.Quote(routePath))
	fmt.Fprintln(&source, "\tRoutePattern = http.MethodPost + \" \" + RoutePath")
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "// Handler serves application-local Alias %s through canonical target %s.\n", aliasID, targetID)
	renderDeprecation(&source, alias.Deprecated())
	fmt.Fprintln(&source, "type Handler struct {")
	fmt.Fprintln(&source, "\ttarget canonicaladapter.Handler")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "// New binds Alias %s to the canonical generated handler for %s.\n", aliasID, targetID)
	renderDeprecation(&source, alias.Deprecated())
	fmt.Fprintln(&source, "func New(target canonicaladapter.Handler) (Handler, error) {")
	fmt.Fprintln(&source, "\tif !canonicaladapter.Available(target) {")
	fmt.Fprintln(&source, "\t\treturn Handler{}, canonicaladapter.ErrInvalidHandler")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\treturn Handler{target: target}, nil")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Available reports whether the canonical target handler is available.")
	fmt.Fprintln(&source, "func Available(handler Handler) bool {")
	fmt.Fprintln(&source, "\treturn canonicaladapter.Available(handler.target)")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "// ServeHTTP forwards Alias %s through canonical target %s's generated transport.\n", aliasID, targetID)
	fmt.Fprintln(&source, "func (h Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {")
	fmt.Fprintln(&source, "\th.target.ServeRoute(writer, request, RoutePath)")
	fmt.Fprintln(&source, "}")
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return File{}, fmt.Errorf("%w: format generated Alias HTTP source: %w", ErrRender, err)
	}
	return File{
		path:        path.Join(generatedDirectory("adapters/http", aliasID), "handler_gen.go"),
		packageName: packageName,
		data:        append([]byte(nil), formatted...),
	}, nil
}

func render(modulePath string, target CanonicalTargetView, plan *generationlowering.Plan) (File, error) {
	if err := module.CheckPath(modulePath); err != nil {
		return File{}, fmt.Errorf("%w: invalid Go Module path %q: %v", ErrRender, modulePath, err)
	}
	if plan != nil && plan.ModulePath() != modulePath {
		return File{}, fmt.Errorf("%w: %w: lowering plan module %q does not match %q", ErrRender, ErrPlan, plan.ModulePath(), modulePath)
	}
	identifier, contract, err := normalizeTarget(target)
	if err != nil {
		return File{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	requestFields, err := prepareFields("request", contract.Request)
	if err != nil {
		return File{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	responseFields, err := prepareFields("response", contract.Response)
	if err != nil {
		return File{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	packageName := goname.Package(identifier)
	contractImport := path.Join(modulePath, generatedDirectory("contracts", identifier))
	invocationImport := path.Join(modulePath, generatedDirectory("invocation", identifier))
	routePath := "/api/v1/capabilities/" + identifier.String() + "/invoke"
	useHTTPPath := plan != nil && plan.RequiresHTTPPath(target.ID())

	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "package %s\n\n", packageName)
	fmt.Fprintln(&source, "import (")
	fmt.Fprintln(&source, "\t\"bytes\"")
	fmt.Fprintln(&source, "\t\"context\"")
	fmt.Fprintln(&source, "\t\"encoding/json\"")
	fmt.Fprintln(&source, "\t\"errors\"")
	fmt.Fprintln(&source, "\t\"io\"")
	fmt.Fprintln(&source, "\t\"mime\"")
	fmt.Fprintln(&source, "\t\"net/http\"")
	fmt.Fprintln(&source, "\t\"strings\"")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "\tcontract %s\n", strconv.Quote(contractImport))
	fmt.Fprintf(&source, "\tapplicationinvocation %s\n", strconv.Quote(invocationImport))
	fmt.Fprintln(&source, "\tkernelinvocation \"github.com/plystra/kernel/invocation\"")
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "const (")
	fmt.Fprintf(&source, "\tRoutePath = %s\n", strconv.Quote(routePath))
	fmt.Fprintln(&source, "\tRoutePattern = http.MethodPost + \" \" + RoutePath")
	fmt.Fprintf(&source, "\tMaximumRequestBytes int64 = %d\n", maximumBodyBytes)
	fmt.Fprintf(&source, "\tMaximumResponseBytes = %d\n", maximumBodyBytes)
	if useHTTPPath {
		fmt.Fprintln(&source, "\tMaximumAdapterCredentialBytes = 64 << 10")
	}
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "var ErrInvalidHandler = errors.New(\"invalid generated HTTP capability handler\")")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// RootContext establishes the trusted Kernel root for one external request.")
	fmt.Fprintln(&source, "type RootContext func(*http.Request) (context.Context, error)")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Handler validates HTTP transport before invoking the canonical generated application path.")
	fmt.Fprintln(&source, "type Handler struct {")
	fmt.Fprintln(&source, "\troot RootContext")
	fmt.Fprintln(&source, "\ttarget applicationinvocation.Handle")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// New binds the canonical exposed route to its generated invocation handle.")
	fmt.Fprintln(&source, "func New(root RootContext, target applicationinvocation.Handle) (Handler, error) {")
	fmt.Fprintln(&source, "\tif root == nil || !applicationinvocation.Available(target) {")
	fmt.Fprintln(&source, "\t\treturn Handler{}, ErrInvalidHandler")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\treturn Handler{root: root, target: target}, nil")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Available reports whether the canonical generated handler is fully bound.")
	fmt.Fprintln(&source, "func Available(handler Handler) bool {")
	fmt.Fprintln(&source, "\treturn handler.root != nil && applicationinvocation.Available(handler.target)")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	renderServeHTTP(&source, useHTTPPath)
	renderRequestDecoder(&source, requestFields)
	renderEnumValidator(&source, "Request", requestFields)
	renderEnumValidator(&source, "Response", responseFields)
	renderInvocationBoundary(&source, contract.Errors, useHTTPPath)
	renderResponseWriters(&source)

	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return File{}, fmt.Errorf("%w: format generated source: %w", ErrRender, err)
	}
	return File{
		path:        path.Join(generatedDirectory("adapters/http", identifier), "handler_gen.go"),
		packageName: packageName,
		data:        append([]byte(nil), formatted...),
	}, nil
}

func normalizeTarget(target CanonicalTargetView) (capabilityid.Identifier, canonicalContract, error) {
	if target == nil || target.ID().String() == "" {
		return capabilityid.Identifier{}, canonicalContract{}, fmt.Errorf("%w: target view is absent", ErrTarget)
	}
	if !target.Exposure().HTTP {
		return capabilityid.Identifier{}, canonicalContract{}, fmt.Errorf("%w: target %s is not explicitly exposed to HTTP", ErrTarget, target.ID())
	}
	identifier, err := capabilityid.Parse(target.ID().String())
	if err != nil {
		return capabilityid.Identifier{}, canonicalContract{}, fmt.Errorf("%w: target ID %q is not canonical", ErrTarget, target.ID())
	}
	input := target.ContractJSON()
	canonical, err := capabilitymeta.NormalizeSchema(input)
	if err != nil {
		return capabilityid.Identifier{}, canonicalContract{}, fmt.Errorf("%w: target %s contract is invalid: %v", ErrTarget, identifier, err)
	}
	if !bytes.Equal(input, canonical) {
		return capabilityid.Identifier{}, canonicalContract{}, fmt.Errorf("%w: target %s contract is not canonical", ErrTarget, identifier)
	}
	var contract canonicalContract
	if err := json.Unmarshal(canonical, &contract); err != nil {
		return capabilityid.Identifier{}, canonicalContract{}, fmt.Errorf("%w: decode target %s contract: %v", ErrTarget, identifier, err)
	}
	if contract.ID != identifier.String() {
		return capabilityid.Identifier{}, canonicalContract{}, fmt.Errorf("%w: target view names %s but its contract declares %s", ErrTarget, identifier, contract.ID)
	}
	sum := sha256.Sum256(canonical)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if target.ContractDigest() != digest {
		return capabilityid.Identifier{}, canonicalContract{}, fmt.Errorf("%w: target %s contract digest does not match canonical bytes", ErrTarget, identifier)
	}
	return identifier, contract, nil
}

func prepareFields(section string, values map[string]canonicalField) ([]preparedField, error) {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]preparedField, 0, len(names))
	goNames := make(map[string]string, len(names))
	for _, name := range names {
		goName := goname.Field(name)
		if previous, duplicate := goNames[goName]; duplicate {
			return nil, fmt.Errorf("%w: %s fields %q and %q both map to %s", ErrTarget, section, previous, name, goName)
		}
		goNames[goName] = name
		result = append(result, preparedField{wireName: name, goName: goName, field: values[name]})
	}
	return result, nil
}

func renderServeHTTP(source *strings.Builder, useHTTPPath bool) {
	fmt.Fprintln(source, "// ServeHTTP accepts only the generated exact route and strict JSON contract.")
	fmt.Fprintln(source, "func (h Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {")
	fmt.Fprintln(source, "\th.ServeRoute(writer, request, RoutePath)")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "// ServeRoute serves one CLI-validated canonical or Alias route through this canonical target.")
	fmt.Fprintln(source, "func (h Handler) ServeRoute(writer http.ResponseWriter, request *http.Request, routePath string) {")
	fmt.Fprintln(source, "\tif request == nil || request.URL == nil {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusInternalServerError, \"internal\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tif request.URL.Path != routePath || request.URL.RawPath != \"\" {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusNotFound, \"not_found\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tif request.Method != http.MethodPost {")
	fmt.Fprintln(source, "\t\twriter.Header().Set(\"Allow\", http.MethodPost)")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusMethodNotAllowed, \"method_not_allowed\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tif request.URL.RawQuery != \"\" {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusBadRequest, \"invalid_request\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tcontentEncodings := request.Header.Values(\"Content-Encoding\")")
	fmt.Fprintln(source, "\tif len(contentEncodings) > 1 {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusUnsupportedMediaType, \"unsupported_media_type\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tif len(contentEncodings) == 1 {")
	fmt.Fprintln(source, "\t\tcontentEncoding := strings.TrimSpace(contentEncodings[0])")
	fmt.Fprintln(source, "\t\tif contentEncoding != \"\" && !strings.EqualFold(contentEncoding, \"identity\") {")
	fmt.Fprintln(source, "\t\t\tplystraWriteError(writer, http.StatusUnsupportedMediaType, \"unsupported_media_type\", \"\")")
	fmt.Fprintln(source, "\t\t\treturn")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tcontentTypes := request.Header.Values(\"Content-Type\")")
	fmt.Fprintln(source, "\tif len(contentTypes) != 1 || !plystraJSONContentType(contentTypes[0]) {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusUnsupportedMediaType, \"unsupported_media_type\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tdecoded, status, code := plystraDecodeRequest(writer, request)")
	fmt.Fprintln(source, "\tif code != \"\" {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, status, code, \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tif h.root == nil || !applicationinvocation.Available(h.target) {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusInternalServerError, \"internal\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tctx, err := plystraCreateRoot(h.root, request)")
	fmt.Fprintln(source, "\tif err != nil || ctx == nil {")
	fmt.Fprintln(source, "\t\tplystraWriteInvocationError(writer, err)")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	if useHTTPPath {
		fmt.Fprintln(source, "\tresponse, err := plystraInvokeHTTP(ctx, h.target, decoded, request)")
	} else {
		fmt.Fprintln(source, "\tresponse, err := plystraInvoke(ctx, h.target, decoded)")
	}
	fmt.Fprintln(source, "\tif err != nil {")
	fmt.Fprintln(source, "\t\tplystraWriteInvocationError(writer, err)")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tif !plystraValidResponse(response) {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusInternalServerError, \"internal\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tpayload, err := json.Marshal(response)")
	fmt.Fprintln(source, "\tif err != nil || len(payload) > MaximumResponseBytes {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusInternalServerError, \"internal\", \"\")")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tplystraWriteJSON(writer, http.StatusOK, payload)")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraJSONContentType(value string) bool {")
	fmt.Fprintln(source, "\tmediaType, parameters, err := mime.ParseMediaType(value)")
	fmt.Fprintln(source, "\tif err != nil || mediaType != \"application/json\" || len(parameters) > 1 {")
	fmt.Fprintln(source, "\t\treturn false")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tcharset, hasCharset := parameters[\"charset\"]")
	fmt.Fprintln(source, "\treturn !hasCharset || strings.EqualFold(charset, \"utf-8\")")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
}

func renderRequestDecoder(source *strings.Builder, fields []preparedField) {
	fmt.Fprintln(source, "func plystraDecodeRequest(writer http.ResponseWriter, request *http.Request) (contract.Request, int, string) {")
	fmt.Fprintln(source, "\tvar result contract.Request")
	fmt.Fprintln(source, "\tif request.Body == nil {")
	fmt.Fprintln(source, "\t\treturn result, http.StatusBadRequest, \"invalid_request\"")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tbody, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, MaximumRequestBytes))")
	fmt.Fprintln(source, "\tif err != nil {")
	fmt.Fprintln(source, "\t\tvar tooLarge *http.MaxBytesError")
	fmt.Fprintln(source, "\t\tif errors.As(err, &tooLarge) {")
	fmt.Fprintln(source, "\t\t\treturn result, http.StatusRequestEntityTooLarge, \"payload_too_large\"")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t\treturn result, http.StatusBadRequest, \"invalid_request\"")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tif !plystraValidRequestObject(body) || json.Unmarshal(body, &result) != nil || !plystraValidRequest(result) {")
	fmt.Fprintln(source, "\t\treturn contract.Request{}, http.StatusBadRequest, \"invalid_request\"")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\treturn result, 0, \"\"")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraValidRequestObject(body []byte) bool {")
	fmt.Fprintln(source, "\tdecoder := json.NewDecoder(bytes.NewReader(body))")
	fmt.Fprintln(source, "\topening, err := decoder.Token()")
	fmt.Fprintln(source, "\tif err != nil || opening != json.Delim('{') {")
	fmt.Fprintln(source, "\t\treturn false")
	fmt.Fprintln(source, "\t}")
	for _, field := range fields {
		fmt.Fprintf(source, "\tvar has%s bool\n", field.goName)
	}
	fmt.Fprintln(source, "\tfor decoder.More() {")
	fmt.Fprintln(source, "\t\ttoken, err := decoder.Token()")
	fmt.Fprintln(source, "\t\tname, ok := token.(string)")
	fmt.Fprintln(source, "\t\tif err != nil || !ok {")
	fmt.Fprintln(source, "\t\t\treturn false")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t\tvar value json.RawMessage")
	fmt.Fprintln(source, "\t\tif decoder.Decode(&value) != nil {")
	fmt.Fprintln(source, "\t\t\treturn false")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t\tswitch name {")
	for _, field := range fields {
		fmt.Fprintf(source, "\t\tcase %s:\n", strconv.Quote(field.wireName))
		fmt.Fprintf(source, "\t\t\tif has%s", field.goName)
		if field.field.Required {
			fmt.Fprint(source, " || bytes.Equal(bytes.TrimSpace(value), []byte(\"null\"))")
		}
		fmt.Fprintln(source, " {")
		fmt.Fprintln(source, "\t\t\t\treturn false")
		fmt.Fprintln(source, "\t\t\t}")
		fmt.Fprintf(source, "\t\t\thas%s = true\n", field.goName)
	}
	fmt.Fprintln(source, "\t\tdefault:")
	fmt.Fprintln(source, "\t\t\treturn false")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tclosing, err := decoder.Token()")
	fmt.Fprintln(source, "\tif err != nil || closing != json.Delim('}') {")
	fmt.Fprintln(source, "\t\treturn false")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tvar trailing any")
	fmt.Fprintln(source, "\tif !errors.Is(decoder.Decode(&trailing), io.EOF) {")
	fmt.Fprintln(source, "\t\treturn false")
	fmt.Fprintln(source, "\t}")
	for _, field := range fields {
		if field.field.Required {
			fmt.Fprintf(source, "\tif !has%s {\n", field.goName)
			fmt.Fprintln(source, "\t\treturn false")
			fmt.Fprintln(source, "\t}")
		}
	}
	fmt.Fprintln(source, "\treturn true")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
}

func renderEnumValidator(source *strings.Builder, section string, fields []preparedField) {
	variable := strings.ToLower(section[:1]) + section[1:]
	fmt.Fprintf(source, "func plystraValid%s(%s contract.%s) bool {\n", section, variable, section)
	for _, field := range fields {
		if len(field.field.Enum) == 0 {
			continue
		}
		expression := variable + "." + field.goName
		if !field.field.Required {
			fmt.Fprintf(source, "\tif %s != nil {\n", expression)
			expression = "*" + expression
		}
		fmt.Fprintf(source, "\t\tswitch %s {\n", enumExpression(field.field.Type, expression))
		fmt.Fprint(source, "\t\tcase ")
		for index, value := range field.field.Enum {
			if index != 0 {
				fmt.Fprint(source, ", ")
			}
			fmt.Fprint(source, string(value))
		}
		fmt.Fprintln(source, ":")
		fmt.Fprintln(source, "\t\tdefault:")
		fmt.Fprintln(source, "\t\t\treturn false")
		fmt.Fprintln(source, "\t\t}")
		if !field.field.Required {
			fmt.Fprintln(source, "\t}")
		}
	}
	fmt.Fprintln(source, "\treturn true")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
}

func enumExpression(typeName, expression string) string {
	switch typeName {
	case "string":
		return "string(" + expression + ")"
	case "integer":
		return "int64(" + expression + ")"
	case "number":
		return "float64(" + expression + ")"
	case "boolean":
		return "bool(" + expression + ")"
	default:
		return expression
	}
}

func renderInvocationBoundary(source *strings.Builder, semanticErrors []string, useHTTPPath bool) {
	fmt.Fprintln(source, "func plystraCreateRoot(root RootContext, request *http.Request) (ctx context.Context, err error) {")
	fmt.Fprintln(source, "\tdefer func() {")
	fmt.Fprintln(source, "\t\tif recover() != nil {")
	fmt.Fprintln(source, "\t\t\tctx = nil")
	fmt.Fprintln(source, "\t\t\terr = ErrInvalidHandler")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t}()")
	fmt.Fprintln(source, "\treturn root(request)")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	if useHTTPPath {
		fmt.Fprintln(source, "func plystraInvokeHTTP(ctx context.Context, target applicationinvocation.Handle, input contract.Request, request *http.Request) (response contract.Response, err error) {")
	} else {
		fmt.Fprintln(source, "func plystraInvoke(ctx context.Context, target applicationinvocation.Handle, request contract.Request) (response contract.Response, err error) {")
	}
	fmt.Fprintln(source, "\tdefer func() {")
	fmt.Fprintln(source, "\t\tif recover() != nil {")
	fmt.Fprintln(source, "\t\t\tresponse = contract.Response{}")
	fmt.Fprintln(source, "\t\t\terr = ErrInvalidHandler")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t}()")
	if useHTTPPath {
		fmt.Fprintln(source, "\treturn target.InvokeHTTP(ctx, input, func(name string) (string, bool) {")
		fmt.Fprintln(source, "\t\treturn plystraAdapterCredential(request, name)")
		fmt.Fprintln(source, "\t})")
	} else {
		fmt.Fprintln(source, "\treturn target.Invoke(ctx, request)")
	}
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	if useHTTPPath {
		fmt.Fprintln(source, "func plystraAdapterCredential(request *http.Request, name string) (string, bool) {")
		fmt.Fprintln(source, "\tif request == nil {")
		fmt.Fprintln(source, "\t\treturn \"\", false")
		fmt.Fprintln(source, "\t}")
		fmt.Fprintln(source, "\theader := http.CanonicalHeaderKey(strings.ReplaceAll(name, \"_\", \"-\"))")
		fmt.Fprintln(source, "\tvalues := request.Header.Values(header)")
		fmt.Fprintln(source, "\tif len(values) != 1 {")
		fmt.Fprintln(source, "\t\treturn \"\", false")
		fmt.Fprintln(source, "\t}")
		fmt.Fprintln(source, "\tvalue := strings.TrimSpace(values[0])")
		fmt.Fprintln(source, "\tif value == \"\" || len(value) > MaximumAdapterCredentialBytes {")
		fmt.Fprintln(source, "\t\treturn \"\", false")
		fmt.Fprintln(source, "\t}")
		fmt.Fprintln(source, "\tfor index := 0; index < len(value); index++ {")
		fmt.Fprintln(source, "\t\tif value[index] < ' ' || value[index] == 0x7f {")
		fmt.Fprintln(source, "\t\t\treturn \"\", false")
		fmt.Fprintln(source, "\t\t}")
		fmt.Fprintln(source, "\t}")
		fmt.Fprintln(source, "\treturn value, true")
		fmt.Fprintln(source, "}")
		fmt.Fprintln(source)
	}
	fmt.Fprintln(source, "func plystraWriteInvocationError(writer http.ResponseWriter, err error) {")
	fmt.Fprintln(source, "\tdefer func() {")
	fmt.Fprintln(source, "\t\tif recover() != nil {")
	fmt.Fprintln(source, "\t\t\tplystraWriteError(writer, http.StatusInternalServerError, \"internal\", \"\")")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t}()")
	fmt.Fprintln(source, "\tif semantic, ok := plystraSemanticError(err); ok {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusUnprocessableEntity, \"capability_error\", semantic)")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tvar classified *kernelinvocation.Error")
	fmt.Fprintln(source, "\tif errors.As(err, &classified) && classified.Code().Valid() && kernelinvocation.ValidDetailCode(classified.DetailCode()) {")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, plystraStatus(classified.Code()), classified.Code().String(), classified.DetailCode())")
	fmt.Fprintln(source, "\t\treturn")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tswitch {")
	fmt.Fprintln(source, "\tcase errors.Is(err, context.DeadlineExceeded):")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusServiceUnavailable, kernelinvocation.ErrorTimeout.String(), \"\")")
	fmt.Fprintln(source, "\tcase errors.Is(err, context.Canceled):")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, 499, kernelinvocation.ErrorCancelled.String(), \"\")")
	fmt.Fprintln(source, "\tdefault:")
	fmt.Fprintln(source, "\t\tplystraWriteError(writer, http.StatusInternalServerError, \"internal\", \"\")")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraStatus(code kernelinvocation.ErrorCode) int {")
	fmt.Fprintln(source, "\tswitch code {")
	fmt.Fprintln(source, "\tcase kernelinvocation.ErrorInvalidArgument:")
	fmt.Fprintln(source, "\t\treturn http.StatusBadRequest")
	fmt.Fprintln(source, "\tcase kernelinvocation.ErrorUnauthenticated:")
	fmt.Fprintln(source, "\t\treturn http.StatusUnauthorized")
	fmt.Fprintln(source, "\tcase kernelinvocation.ErrorDenied:")
	fmt.Fprintln(source, "\t\treturn http.StatusForbidden")
	fmt.Fprintln(source, "\tcase kernelinvocation.ErrorNotFound:")
	fmt.Fprintln(source, "\t\treturn http.StatusNotFound")
	fmt.Fprintln(source, "\tcase kernelinvocation.ErrorConflict, kernelinvocation.ErrorVersionIncompatible:")
	fmt.Fprintln(source, "\t\treturn http.StatusConflict")
	fmt.Fprintln(source, "\tcase kernelinvocation.ErrorTimeout, kernelinvocation.ErrorUnavailable, kernelinvocation.ErrorResultUnknown:")
	fmt.Fprintln(source, "\t\treturn http.StatusServiceUnavailable")
	fmt.Fprintln(source, "\tcase kernelinvocation.ErrorCancelled:")
	fmt.Fprintln(source, "\t\treturn 499")
	fmt.Fprintln(source, "\tdefault:")
	fmt.Fprintln(source, "\t\treturn http.StatusInternalServerError")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "type plystraSemanticErrorCoder interface {")
	fmt.Fprintln(source, "\terror")
	fmt.Fprintln(source, "\tSemanticErrorCode() string")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraSemanticError(err error) (string, bool) {")
	if len(semanticErrors) == 0 {
		fmt.Fprintln(source, "\treturn \"\", false")
	} else {
		fmt.Fprintln(source, "\tvar semantic plystraSemanticErrorCoder")
		fmt.Fprintln(source, "\tif !errors.As(err, &semantic) {")
		fmt.Fprintln(source, "\t\treturn \"\", false")
		fmt.Fprintln(source, "\t}")
		fmt.Fprintln(source, "\tcode := semantic.SemanticErrorCode()")
		fmt.Fprintln(source, "\tswitch code {")
		fmt.Fprint(source, "\tcase ")
		for index, code := range semanticErrors {
			if index != 0 {
				fmt.Fprint(source, ", ")
			}
			fmt.Fprint(source, strconv.Quote(code))
		}
		fmt.Fprintln(source, ":")
		fmt.Fprintln(source, "\t\treturn code, true")
		fmt.Fprintln(source, "\tdefault:")
		fmt.Fprintln(source, "\t\treturn \"\", false")
		fmt.Fprintln(source, "\t}")
	}
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
}

func renderResponseWriters(source *strings.Builder) {
	fmt.Fprintln(source, "type plystraErrorEnvelope struct {")
	fmt.Fprintln(source, "\tError plystraErrorBody `json:\"error\"`")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "type plystraErrorBody struct {")
	fmt.Fprintln(source, "\tCode string `json:\"code\"`")
	fmt.Fprintln(source, "\tDetailCode string `json:\"detail_code,omitempty\"`")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraWriteError(writer http.ResponseWriter, status int, code, detailCode string) {")
	fmt.Fprintln(source, "\tpayload, err := json.Marshal(plystraErrorEnvelope{Error: plystraErrorBody{Code: code, DetailCode: detailCode}})")
	fmt.Fprintln(source, "\tif err != nil {")
	fmt.Fprintln(source, "\t\tpayload = []byte(`{\"error\":{\"code\":\"internal\"}}`)")
	fmt.Fprintln(source, "\t\tstatus = http.StatusInternalServerError")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tplystraWriteJSON(writer, status, payload)")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraWriteJSON(writer http.ResponseWriter, status int, payload []byte) {")
	fmt.Fprintln(source, "\twriter.Header().Set(\"Content-Type\", \"application/json\")")
	fmt.Fprintln(source, "\twriter.Header().Set(\"Cache-Control\", \"no-store\")")
	fmt.Fprintln(source, "\twriter.Header().Set(\"X-Content-Type-Options\", \"nosniff\")")
	fmt.Fprintln(source, "\twriter.WriteHeader(status)")
	fmt.Fprintln(source, "\t_, _ = writer.Write(payload)")
	fmt.Fprintln(source, "}")
}

func generatedDirectory(category string, identifier capabilityid.Identifier) string {
	components := append([]string{"generated", "go", category}, strings.Split(identifier.Name(), ".")...)
	components = append(components, "v"+strconv.FormatUint(identifier.Major(), 10))
	return path.Join(components...)
}

func exposureSubset(alias, target generation.Exposure) bool {
	return (!alias.Go || target.Go) && (!alias.HTTP || target.HTTP) && (!alias.JavaScript || target.JavaScript)
}

func renderDeprecation(source *strings.Builder, message string) {
	if message == "" {
		return
	}
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	lines := strings.Split(message, "\n")
	fmt.Fprintln(source, "//")
	fmt.Fprintf(source, "// Deprecated: %s\n", lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(source, "// %s\n", line)
	}
}
