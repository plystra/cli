// Package invocationgen renders deterministic canonical application invocation
// handles shared by generated adapters and Capability clients.
package invocationgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/goname"
	"github.com/plystra/cli/internal/modulepath"
)

var (
	// ErrRender reports that a capability declaration could not produce a Go
	// application-invocation file.
	ErrRender = errors.New("render Go application invocation")
	// ErrContribution reports a lowered operation that cannot enter the current
	// canonical invocation surface without losing its declared semantics.
	ErrContribution = errors.New("render lowered invocation contribution")
)

// File is one immutable generated canonical application-invocation output.
type File struct {
	path         string
	packageName  string
	data         []byte
	dependencies []string
}

// Path returns the slash-separated module-relative generated file path.
func (f File) Path() string { return f.path }

// PackageName returns the generated Go package identifier.
func (f File) PackageName() string { return f.packageName }

// Data returns a defensive copy of the formatted generated source.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

// Dependencies returns the ordered canonical application clients required by
// this invocation constructor.
func (f File) Dependencies() []string {
	return append([]string(nil), f.dependencies...)
}

type canonicalContract struct {
	ID       string                    `json:"id"`
	Request  map[string]canonicalField `json:"request"`
	Response map[string]canonicalField `json:"response"`
	Errors   []string                  `json:"errors"`
}

type canonicalField struct {
	Type        generation.GeneratedValueType `json:"type"`
	Items       generation.GeneratedValueType `json:"items,omitempty"`
	Required    bool                          `json:"required,omitempty"`
	Enum        []json.RawMessage             `json:"enum,omitempty"`
	Constraints canonicalFieldConstraints     `json:"constraints,omitempty"`
}

type canonicalFieldConstraints struct {
	MinLength *uint32         `json:"min_length,omitempty"`
	MaxLength *uint32         `json:"max_length,omitempty"`
	Pattern   *string         `json:"pattern,omitempty"`
	Minimum   json.RawMessage `json:"minimum,omitempty"`
	Maximum   json.RawMessage `json:"maximum,omitempty"`
	MinItems  *uint32         `json:"min_items,omitempty"`
	MaxItems  *uint32         `json:"max_items,omitempty"`
}

func (c canonicalFieldConstraints) empty() bool {
	return c.MinLength == nil && c.MaxLength == nil && c.Pattern == nil &&
		len(c.Minimum) == 0 && len(c.Maximum) == 0 && c.MinItems == nil && c.MaxItems == nil
}

// Render validates schema and module identity, then emits the canonical
// application invocation path for one exact Capability. This base path performs
// direct governed dispatch when no generation contributions apply.
func Render(modulePath string, schema []byte) (File, error) {
	return render(modulePath, schema, nil)
}

// RenderPlan emits one canonical application path with the source Capability's
// lowered contributions in their already-resolved semantic order.
func RenderPlan(modulePath string, schema []byte, plan generationlowering.Plan) (File, error) {
	if plan.ModulePath() != modulePath {
		return File{}, fmt.Errorf("%w: %w: lowering plan module %q does not match %q", ErrRender, ErrContribution, plan.ModulePath(), modulePath)
	}
	return render(modulePath, schema, &plan)
}

func render(modulePath string, schema []byte, plan *generationlowering.Plan) (File, error) {
	if err := modulepath.CheckProject(modulePath); err != nil {
		return File{}, fmt.Errorf("%w: invalid Go Module path %q: %v", ErrRender, modulePath, err)
	}
	canonical, err := capabilitymeta.NormalizeSchema(schema)
	if err != nil {
		return File{}, fmt.Errorf("%w: normalize schema: %w", ErrRender, err)
	}
	var contract canonicalContract
	if err := json.Unmarshal(canonical, &contract); err != nil {
		return File{}, fmt.Errorf("%w: decode canonical schema: %w", ErrRender, err)
	}
	identifier, err := capabilityid.Parse(contract.ID)
	if err != nil {
		return File{}, fmt.Errorf("%w: decode canonical identity: %w", ErrRender, err)
	}
	prepared, err := preparePlan(identifier, contract.Request, contract.Response, plan)
	if err != nil {
		return File{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	requestValidation := prepareRequestValidation(contract.Request)
	responseValidation := prepareResponseValidation(contract.Response)

	packageName := goname.Package(identifier)
	contractImport := path.Join(modulePath, generatedDirectory("contracts", identifier))
	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "package %s\n\n", packageName)
	fmt.Fprintln(&source, "import (")
	fmt.Fprintln(&source, "\t\"context\"")
	if prepared.hasValueConversions || requestValidation.hasObjects || responseValidation.hasObjects {
		fmt.Fprintln(&source, "\t\"encoding/json\"")
	}
	fmt.Fprintln(&source, "\t\"errors\"")
	if requestValidation.hasNumbers || responseValidation.hasNumbers {
		fmt.Fprintln(&source, "\t\"math\"")
	}
	if requestValidation.hasPatterns || responseValidation.hasPatterns {
		fmt.Fprintln(&source, "\t\"regexp\"")
	}
	if prepared.hasTimedCalls {
		fmt.Fprintln(&source, "\t\"time\"")
	}
	if requestValidation.hasStrings || responseValidation.hasStrings {
		fmt.Fprintln(&source, "\t\"unicode/utf8\"")
	}
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "\tcontract %s\n", strconv.Quote(contractImport))
	if prepared.hasContextOperations {
		fmt.Fprintf(&source, "\tinvocationcontext %s\n", strconv.Quote(path.Join(modulePath, "generated/go/internal/invocationcontext")))
	}
	for _, dependency := range prepared.dependencies {
		fmt.Fprintf(&source, "\t%s %s\n", dependency.reference.ImportName(), strconv.Quote(dependency.reference.ImportPath()))
		fmt.Fprintf(&source, "\t%s %s\n", dependency.reference.ContractImportName(), strconv.Quote(dependency.reference.ContractImportPath()))
	}
	fmt.Fprintln(&source, "\tkernelinvocation \"github.com/plystra/kernel/invocation\"")
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Handle is the opaque generated application path to one exact canonical Capability.")
	fmt.Fprintln(&source, "type Handle struct {")
	fmt.Fprintln(&source, "\ttarget kernelinvocation.Handle[contract.Request, contract.Response]")
	for _, dependency := range prepared.dependencies {
		fmt.Fprintf(&source, "\t%s %s.Client\n", dependency.field, dependency.reference.ImportName())
	}
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	if prepared.hasHTTPPath {
		fmt.Fprintln(&source, "// AdapterCredentialSource returns one raw trusted-adapter credential by canonical name.")
		fmt.Fprintln(&source, "type AdapterCredentialSource func(string) (string, bool)")
		fmt.Fprintln(&source)
	}
	fmt.Fprintln(&source, "// New binds the application path to its caller-scoped canonical Kernel handle.")
	if len(prepared.dependencies) == 0 {
		fmt.Fprintln(&source, "func New(target kernelinvocation.Handle[contract.Request, contract.Response]) Handle {")
		fmt.Fprintln(&source, "\treturn Handle{target: target}")
	} else {
		fmt.Fprintln(&source, "func New(")
		fmt.Fprintln(&source, "\ttarget kernelinvocation.Handle[contract.Request, contract.Response],")
		for _, dependency := range prepared.dependencies {
			fmt.Fprintf(&source, "\t%s %s.Client,\n", dependency.field, dependency.reference.ImportName())
		}
		fmt.Fprintln(&source, ") Handle {")
		fmt.Fprintln(&source, "\treturn Handle{")
		fmt.Fprintln(&source, "\t\ttarget: target,")
		for _, dependency := range prepared.dependencies {
			fmt.Fprintf(&source, "\t\t%s: %s,\n", dependency.field, dependency.field)
		}
		fmt.Fprintln(&source, "\t}")
	}
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Available reports whether assembly selected the canonical provider.")
	fmt.Fprintln(&source, "func Available(handle Handle) bool {")
	fmt.Fprintln(&source, "\treturn handle.Available()")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Available reports whether assembly selected the canonical provider.")
	fmt.Fprintln(&source, "func (h Handle) Available() bool {")
	fmt.Fprintln(&source, "\treturn h.target.Available()")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	if prepared.hasHTTPPath {
		fmt.Fprintf(&source, "// Invoke runs the internal application path for %s without HTTP-only integration.\n", identifier.String())
		fmt.Fprintln(&source, "func (h Handle) Invoke(ctx context.Context, request contract.Request) (contract.Response, error) {")
		fmt.Fprintln(&source, "\tif requestError := ValidateRequest(request); requestError != nil {")
		fmt.Fprintln(&source, "\t\treturn contract.Response{}, requestError")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\t_, response, invocationError := h.invoke(ctx, request, nil)")
		fmt.Fprintln(&source, "\treturn response, invocationError")
		fmt.Fprintln(&source, "}")
		fmt.Fprintln(&source)
		fmt.Fprintf(&source, "// InvokeHTTP runs the external HTTP path for %s around the same canonical dispatch.\n", identifier.String())
		fmt.Fprintln(&source, "func (h Handle) InvokeHTTP(ctx context.Context, request contract.Request, adapterCredentials AdapterCredentialSource) (contract.Response, error) {")
		fmt.Fprintln(&source, "\tif requestError := ValidateRequest(request); requestError != nil {")
		fmt.Fprintln(&source, "\t\treturn contract.Response{}, requestError")
		fmt.Fprintln(&source, "\t}")
		if err := renderContributions(&source, contract, prepared, prepared.ingress); err != nil {
			return File{}, fmt.Errorf("%w: %w", ErrRender, err)
		}
		fmt.Fprintln(&source, "\tctx, response, invocationError := h.invoke(ctx, request, adapterCredentials)")
		if err := renderContributions(&source, contract, prepared, prepared.egress); err != nil {
			return File{}, fmt.Errorf("%w: %w", ErrRender, err)
		}
		fmt.Fprintln(&source, "\treturn response, invocationError")
		fmt.Fprintln(&source, "}")
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "func (h Handle) invoke(parentContext context.Context, request contract.Request, adapterCredentials AdapterCredentialSource) (context.Context, contract.Response, error) {")
		fmt.Fprintln(&source, "\tvar invocationContext context.Context")
		fmt.Fprintln(&source, "\tresponse, invocationError := func() (contract.Response, error) {")
		fmt.Fprintln(&source, "\t\tctx := parentContext")
		fmt.Fprintln(&source, "\t\tdefer func() { invocationContext = ctx }()")
		if err := renderContributions(&source, contract, prepared, prepared.preparations); err != nil {
			return File{}, fmt.Errorf("%w: %w", ErrRender, err)
		}
		if len(prepared.completions) == 0 {
			fmt.Fprintln(&source, "\t\treturn h.target.Invoke(ctx, request)")
		} else {
			fmt.Fprintln(&source, "\t\tresponse, invocationError := h.target.Invoke(ctx, request)")
			if err := renderContributions(&source, contract, prepared, prepared.completions); err != nil {
				return File{}, fmt.Errorf("%w: %w", ErrRender, err)
			}
			fmt.Fprintln(&source, "\t\treturn response, invocationError")
		}
		fmt.Fprintln(&source, "\t}()")
		fmt.Fprintln(&source, "\tif invocationError != nil {")
		fmt.Fprintln(&source, "\t\treturn invocationContext, contract.Response{}, invocationError")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\tif responseError := plystraValidateResponse(response); responseError != nil {")
		fmt.Fprintln(&source, "\t\treturn invocationContext, contract.Response{}, responseError")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\treturn invocationContext, response, nil")
		fmt.Fprintln(&source, "}")
	} else {
		fmt.Fprintf(&source, "// Invoke runs the application path for %s and dispatches its canonical ID.\n", identifier.String())
		fmt.Fprintln(&source, "func (h Handle) Invoke(ctx context.Context, request contract.Request) (contract.Response, error) {")
		fmt.Fprintln(&source, "\tif requestError := ValidateRequest(request); requestError != nil {")
		fmt.Fprintln(&source, "\t\treturn contract.Response{}, requestError")
		fmt.Fprintln(&source, "\t}")
		if err := renderContributions(&source, contract, prepared, prepared.preparations); err != nil {
			return File{}, fmt.Errorf("%w: %w", ErrRender, err)
		}
		fmt.Fprintln(&source, "\tresponse, invocationError := h.target.Invoke(ctx, request)")
		if len(prepared.completions) != 0 {
			if err := renderContributions(&source, contract, prepared, prepared.completions); err != nil {
				return File{}, fmt.Errorf("%w: %w", ErrRender, err)
			}
		}
		fmt.Fprintln(&source, "\tif invocationError != nil {")
		fmt.Fprintln(&source, "\t\treturn contract.Response{}, invocationError")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\tif responseError := plystraValidateResponse(response); responseError != nil {")
		fmt.Fprintln(&source, "\t\treturn contract.Response{}, responseError")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\treturn response, nil")
		fmt.Fprintln(&source, "}")
	}
	if err := renderRequestValidation(&source, requestValidation); err != nil {
		return File{}, fmt.Errorf("%w: render request validation: %w", ErrRender, err)
	}
	if err := renderResponseValidation(&source, responseValidation, !requestValidation.hasObjects); err != nil {
		return File{}, fmt.Errorf("%w: render response validation: %w", ErrRender, err)
	}
	renderTransportErrorInput(&source, contract.Errors)
	if prepared.hasAdapterCredentials {
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "func plystraAdapterCredential(source AdapterCredentialSource, name string) (credential *string) {")
		fmt.Fprintln(&source, "\tif source == nil {")
		fmt.Fprintln(&source, "\t\treturn nil")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\tdefer func() {")
		fmt.Fprintln(&source, "\t\tif recover() != nil {")
		fmt.Fprintln(&source, "\t\t\tcredential = nil")
		fmt.Fprintln(&source, "\t\t}")
		fmt.Fprintln(&source, "\t}()")
		fmt.Fprintln(&source, "\tvalue, ok := source(name)")
		fmt.Fprintln(&source, "\tif !ok || value == \"\" || len(value) > 64<<10 {")
		fmt.Fprintln(&source, "\t\treturn nil")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\treturn &value")
		fmt.Fprintln(&source, "}")
	}
	if prepared.hasTimedCalls {
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "var plystraErrInvalidContext = errors.New(\"nil generated invocation context\")")
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "func plystraInvokeWithTimeout[Request, Response any](")
		fmt.Fprintln(&source, "\tctx context.Context,")
		fmt.Fprintln(&source, "\ttimeout time.Duration,")
		fmt.Fprintln(&source, "\tinvoke func(context.Context, Request) (Response, error),")
		fmt.Fprintln(&source, "\trequest Request,")
		fmt.Fprintln(&source, ") (Response, error) {")
		fmt.Fprintln(&source, "\tif ctx == nil {")
		fmt.Fprintln(&source, "\t\tvar response Response")
		fmt.Fprintln(&source, "\t\treturn response, plystraErrInvalidContext")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\tcallContext, cancel := context.WithTimeout(ctx, timeout)")
		fmt.Fprintln(&source, "\tdefer cancel()")
		fmt.Fprintln(&source, "\treturn invoke(callContext, request)")
		fmt.Fprintln(&source, "}")
	}
	if prepared.hasValueConversions {
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "var plystraErrInvalidGeneratedValue = errors.New(\"invalid generated invocation value\")")
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "func plystraConvertValue[Target any](value any) (Target, error) {")
		fmt.Fprintln(&source, "\tvar result Target")
		fmt.Fprintln(&source, "\tdata, err := json.Marshal(value)")
		fmt.Fprintln(&source, "\tif err != nil {")
		fmt.Fprintln(&source, "\t\treturn result, plystraErrInvalidGeneratedValue")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\tif err := json.Unmarshal(data, &result); err != nil {")
		fmt.Fprintln(&source, "\t\treturn result, plystraErrInvalidGeneratedValue")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\treturn result, nil")
		fmt.Fprintln(&source, "}")
	}
	if prepared.hasPointerBindings {
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "func plystraPointer[Value any](value Value) *Value {")
		fmt.Fprintln(&source, "\treturn &value")
		fmt.Fprintln(&source, "}")
	}
	if prepared.hasOptionalConversions {
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "func plystraConvertOptional[Source, Target any](value *Source, convert func(Source) Target) *Target {")
		fmt.Fprintln(&source, "\tif value == nil {")
		fmt.Fprintln(&source, "\t\treturn nil")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintln(&source, "\tresult := convert(*value)")
		fmt.Fprintln(&source, "\treturn &result")
		fmt.Fprintln(&source, "}")
	}
	if prepared.hasConditionalFailures {
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "type plystraConditionalError struct {")
		fmt.Fprintln(&source, "\tcode    contract.ErrorCode")
		fmt.Fprintln(&source, "\tmessage string")
		fmt.Fprintln(&source, "}")
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "func (e plystraConditionalError) Error() string { return e.message }")
		fmt.Fprintln(&source)
		fmt.Fprintln(&source, "func (e plystraConditionalError) Unwrap() error { return e.code }")
	}
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return File{}, fmt.Errorf("%w: format generated source: %w", ErrRender, err)
	}
	dependencies := make([]string, len(prepared.dependencies))
	for index, dependency := range prepared.dependencies {
		dependencies[index] = dependency.reference.Capability().String()
	}
	return File{
		path:         path.Join(generatedDirectory("invocation", identifier), "invocation_gen.go"),
		packageName:  packageName,
		data:         append([]byte(nil), formatted...),
		dependencies: dependencies,
	}, nil
}

func renderTransportErrorInput(source *strings.Builder, semanticErrors []string) {
	fmt.Fprintln(source)
	fmt.Fprintln(source, "// TransportErrorInput is the immutable data-free failure projection consumed by generated external adapters.")
	fmt.Fprintln(source, "type TransportErrorInput struct {")
	fmt.Fprintln(source, "\tsemanticErrorCode string")
	fmt.Fprintln(source, "\tkernelErrorClass kernelinvocation.ErrorCode")
	fmt.Fprintln(source, "\tkernelDetailCode string")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "// Valid reports whether the projection contains exactly one closed semantic or Kernel failure classification.")
	fmt.Fprintln(source, "func (i TransportErrorInput) Valid() bool {")
	fmt.Fprintln(source, "\tif i.semanticErrorCode != \"\" {")
	fmt.Fprintln(source, "\t\treturn i.kernelErrorClass == \"\" && i.kernelDetailCode == \"\" && plystraDeclaredSemanticError(i.semanticErrorCode)")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tif !i.kernelErrorClass.Valid() || !kernelinvocation.ValidDetailCode(i.kernelDetailCode) {")
	fmt.Fprintln(source, "\t\treturn false")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\treturn i.kernelErrorClass != kernelinvocation.ErrorDenied || i.kernelDetailCode != \"\"")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "// SemanticErrorCode returns one declared canonical semantic code or an empty string.")
	fmt.Fprintln(source, "func (i TransportErrorInput) SemanticErrorCode() string {")
	fmt.Fprintln(source, "\tif !i.Valid() {")
	fmt.Fprintln(source, "\t\treturn \"\"")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\treturn i.semanticErrorCode")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "// KernelErrorClass returns one closed Kernel failure class or an empty string.")
	fmt.Fprintln(source, "func (i TransportErrorInput) KernelErrorClass() string {")
	fmt.Fprintln(source, "\tif !i.Valid() {")
	fmt.Fprintln(source, "\t\treturn \"\"")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\treturn i.kernelErrorClass.String()")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "// KernelDetailCode returns the optional bounded Kernel detail code or an empty string.")
	fmt.Fprintln(source, "func (i TransportErrorInput) KernelDetailCode() string {")
	fmt.Fprintln(source, "\tif !i.Valid() {")
	fmt.Fprintln(source, "\t\treturn \"\"")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\treturn i.kernelDetailCode")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "// SafeTransportError projects one canonical invocation failure without retaining its text, cause, payload, or Provider data.")
	fmt.Fprintln(source, "func SafeTransportError(err error) (input TransportErrorInput) {")
	fmt.Fprintln(source, "\tinput = plystraInternalTransportError()")
	fmt.Fprintln(source, "\tdefer func() {")
	fmt.Fprintln(source, "\t\tif recover() != nil || !input.Valid() {")
	fmt.Fprintln(source, "\t\t\tinput = plystraInternalTransportError()")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t}()")
	fmt.Fprintln(source, "\tif err == nil {")
	fmt.Fprintln(source, "\t\treturn input")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tvar semantic plystraSemanticErrorCoder")
	fmt.Fprintln(source, "\tif errors.As(err, &semantic) {")
	fmt.Fprintln(source, "\t\tcode := semantic.SemanticErrorCode()")
	fmt.Fprintln(source, "\t\tif plystraDeclaredSemanticError(code) {")
	fmt.Fprintln(source, "\t\t\treturn TransportErrorInput{semanticErrorCode: code}")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t\treturn input")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tvar classified *kernelinvocation.Error")
	fmt.Fprintln(source, "\tif errors.As(err, &classified) {")
	fmt.Fprintln(source, "\t\treturn TransportErrorInput{kernelErrorClass: classified.Code(), kernelDetailCode: classified.DetailCode()}")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tswitch {")
	fmt.Fprintln(source, "\tcase errors.Is(err, context.DeadlineExceeded):")
	fmt.Fprintln(source, "\t\treturn TransportErrorInput{kernelErrorClass: kernelinvocation.ErrorTimeout}")
	fmt.Fprintln(source, "\tcase errors.Is(err, context.Canceled):")
	fmt.Fprintln(source, "\t\treturn TransportErrorInput{kernelErrorClass: kernelinvocation.ErrorCancelled}")
	fmt.Fprintln(source, "\tdefault:")
	fmt.Fprintln(source, "\t\treturn input")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "type plystraSemanticErrorCoder interface {")
	fmt.Fprintln(source, "\terror")
	fmt.Fprintln(source, "\tSemanticErrorCode() string")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraDeclaredSemanticError(code string) bool {")
	if len(semanticErrors) == 0 {
		fmt.Fprintln(source, "\treturn false")
	} else {
		fmt.Fprintln(source, "\tswitch code {")
		fmt.Fprint(source, "\tcase ")
		for index, code := range semanticErrors {
			if index != 0 {
				fmt.Fprint(source, ", ")
			}
			fmt.Fprint(source, strconv.Quote(code))
		}
		fmt.Fprintln(source, ":")
		fmt.Fprintln(source, "\t\treturn true")
		fmt.Fprintln(source, "\tdefault:")
		fmt.Fprintln(source, "\t\treturn false")
		fmt.Fprintln(source, "\t}")
	}
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraInternalTransportError() TransportErrorInput {")
	fmt.Fprintln(source, "\treturn TransportErrorInput{kernelErrorClass: kernelinvocation.ErrorInternal}")
	fmt.Fprintln(source, "}")
}

type responseValidationPlan struct {
	fields      []responseValidationField
	hasNumbers  bool
	hasObjects  bool
	hasPatterns bool
	hasStrings  bool
}

type responseValidationField struct {
	goName      string
	patternName string
	field       canonicalField
}

func prepareRequestValidation(fields map[string]canonicalField) responseValidationPlan {
	return prepareValidation(fields, true, "Request")
}

func prepareResponseValidation(fields map[string]canonicalField) responseValidationPlan {
	return prepareValidation(fields, false, "Response")
}

func prepareValidation(fields map[string]canonicalField, constrainedOnly bool, section string) responseValidationPlan {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	result := responseValidationPlan{fields: make([]responseValidationField, 0, len(names))}
	for _, name := range names {
		field := fields[name]
		if constrainedOnly && field.Constraints.empty() {
			continue
		}
		prepared := responseValidationField{goName: goname.Field(name), field: field}
		if field.Constraints.Pattern != nil {
			prepared.patternName = fmt.Sprintf("plystra%sPattern%d", section, len(result.fields))
			result.hasPatterns = true
		}
		result.fields = append(result.fields, prepared)
		if len(field.Enum) != 0 && field.Constraints.empty() {
			continue
		}
		kind := field.Type
		if kind == generation.GeneratedValueArray {
			kind = field.Items
		}
		switch kind {
		case generation.GeneratedValueNumber:
			result.hasNumbers = true
		case generation.GeneratedValueObject:
			result.hasNumbers = true
			result.hasObjects = true
			result.hasStrings = true
		case generation.GeneratedValueString:
			result.hasStrings = true
		}
	}
	return result
}

func renderRequestValidation(source *strings.Builder, plan responseValidationPlan) error {
	renderConstraintPatterns(source, plan)
	if len(plan.fields) != 0 {
		fmt.Fprintln(source)
		fmt.Fprintln(source, "func plystraInvalidRequestError() error {")
		fmt.Fprintln(source, "\tfailure, _ := kernelinvocation.NewError(kernelinvocation.ErrorInvalidArgument, \"contract.invalid_request\")")
		fmt.Fprintln(source, "\treturn failure")
		fmt.Fprintln(source, "}")
	}
	fmt.Fprintln(source)
	fmt.Fprintln(source, "// ValidateRequest applies the canonical request constraints before trusted application work begins.")
	fmt.Fprintln(source, "func ValidateRequest(request contract.Request) error {")
	fmt.Fprintln(source, "\treturn plystraValidateRequest(request)")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraValidateRequest(request contract.Request) error {")
	for _, prepared := range plan.fields {
		value := "request." + prepared.goName
		indent := "\t"
		if !prepared.field.Required {
			fmt.Fprintf(source, "\tif %s != nil {\n", value)
			value = "*" + value
			indent = "\t\t"
		}
		if err := renderCanonicalFieldValidation(source, "Request", prepared, value, indent, "plystraInvalidRequestError()"); err != nil {
			return err
		}
		if !prepared.field.Required {
			fmt.Fprintln(source, "\t}")
		}
	}
	fmt.Fprintln(source, "\treturn nil")
	fmt.Fprintln(source, "}")
	if plan.hasObjects {
		renderObjectValidation(source)
	}
	return nil
}

func renderResponseValidation(source *strings.Builder, plan responseValidationPlan, renderObjects bool) error {
	renderConstraintPatterns(source, plan)
	fmt.Fprintln(source)
	fmt.Fprintln(source, "var plystraErrInvalidProviderResponse = errors.New(\"invalid canonical Provider response\")")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraValidateResponse(response contract.Response) error {")
	for _, prepared := range plan.fields {
		value := "response." + prepared.goName
		indent := "\t"
		if !prepared.field.Required {
			fmt.Fprintf(source, "\tif %s != nil {\n", value)
			value = "*" + value
			indent = "\t\t"
		}
		if err := renderCanonicalFieldValidation(source, "Response", prepared, value, indent, "plystraErrInvalidProviderResponse"); err != nil {
			return err
		}
		if !prepared.field.Required {
			fmt.Fprintln(source, "\t}")
		}
	}
	fmt.Fprintln(source, "\treturn nil")
	fmt.Fprintln(source, "}")
	if plan.hasObjects && renderObjects {
		renderObjectValidation(source)
	}
	return nil
}

func renderConstraintPatterns(source *strings.Builder, plan responseValidationPlan) {
	for _, prepared := range plan.fields {
		if prepared.patternName == "" {
			continue
		}
		fmt.Fprintln(source)
		fmt.Fprintf(source, "var %s = regexp.MustCompile(%s)\n", prepared.patternName, strconv.Quote(*prepared.field.Constraints.Pattern))
	}
}

func renderCanonicalFieldValidation(source *strings.Builder, section string, prepared responseValidationField, value, indent, failure string) error {
	field := prepared.field
	if len(field.Enum) != 0 {
		fmt.Fprintf(source, "%sswitch %s {\n", indent, value)
		for _, raw := range field.Enum {
			literal, err := responseLiteral(field.Type, raw)
			if err != nil {
				return err
			}
			fmt.Fprintf(source, "%scase contract.%s%s(%s):\n", indent, section, prepared.goName, literal)
		}
		fmt.Fprintf(source, "%sdefault:\n", indent)
		fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
		fmt.Fprintf(source, "%s}\n", indent)
	} else {
		renderFieldShapeValidation(source, prepared, value, indent, failure)
	}
	return renderFieldConstraintValidation(source, prepared, value, indent, failure)
}

func renderFieldShapeValidation(source *strings.Builder, prepared responseValidationField, value, indent, failure string) {
	field := prepared.field
	switch field.Type {
	case generation.GeneratedValueString:
		fmt.Fprintf(source, "%sif !utf8.ValidString(%s) {\n", indent, value)
		fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
		fmt.Fprintf(source, "%s}\n", indent)
	case generation.GeneratedValueNumber:
		renderFiniteNumberValidation(source, value, indent, failure)
	case generation.GeneratedValueObject:
		fmt.Fprintf(source, "%sif !plystraValidObject(%s) {\n", indent, value)
		fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
		fmt.Fprintf(source, "%s}\n", indent)
	case generation.GeneratedValueArray:
		if field.Required {
			fmt.Fprintf(source, "%sif %s == nil {\n", indent, value)
			fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
			fmt.Fprintf(source, "%s}\n", indent)
		}
		switch field.Items {
		case generation.GeneratedValueString:
			fmt.Fprintf(source, "%sfor _, item := range %s {\n", indent, value)
			fmt.Fprintf(source, "%s\tif !utf8.ValidString(item) {\n", indent)
			fmt.Fprintf(source, "%s\t\treturn %s\n", indent, failure)
			fmt.Fprintf(source, "%s\t}\n", indent)
			fmt.Fprintf(source, "%s}\n", indent)
		case generation.GeneratedValueNumber:
			fmt.Fprintf(source, "%sfor _, item := range %s {\n", indent, value)
			renderFiniteNumberValidation(source, "item", indent+"\t", failure)
			fmt.Fprintf(source, "%s}\n", indent)
		case generation.GeneratedValueObject:
			fmt.Fprintf(source, "%sfor _, item := range %s {\n", indent, value)
			fmt.Fprintf(source, "%s\tif !plystraValidObject(item) {\n", indent)
			fmt.Fprintf(source, "%s\t\treturn %s\n", indent, failure)
			fmt.Fprintf(source, "%s\t}\n", indent)
			fmt.Fprintf(source, "%s}\n", indent)
		}
	}
}

func renderFieldConstraintValidation(source *strings.Builder, prepared responseValidationField, value, indent, failure string) error {
	constraints := prepared.field.Constraints
	switch prepared.field.Type {
	case generation.GeneratedValueString:
		if constraints.MinLength != nil || constraints.MaxLength != nil {
			fmt.Fprintf(source, "%s{\n", indent)
			fmt.Fprintf(source, "%s\tplystraLength := utf8.RuneCountInString(%s)\n", indent, value)
			checks := make([]string, 0, 2)
			if constraints.MinLength != nil {
				checks = append(checks, fmt.Sprintf("plystraLength < %d", *constraints.MinLength))
			}
			if constraints.MaxLength != nil {
				checks = append(checks, fmt.Sprintf("plystraLength > %d", *constraints.MaxLength))
			}
			fmt.Fprintf(source, "%s\tif %s {\n", indent, strings.Join(checks, " || "))
			fmt.Fprintf(source, "%s\t\treturn %s\n", indent, failure)
			fmt.Fprintf(source, "%s\t}\n", indent)
			fmt.Fprintf(source, "%s}\n", indent)
		}
		if constraints.Pattern != nil {
			if prepared.patternName == "" {
				return fmt.Errorf("string constraint pattern has no generated identity")
			}
			fmt.Fprintf(source, "%sif !%s.MatchString(%s) {\n", indent, prepared.patternName, value)
			fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
			fmt.Fprintf(source, "%s}\n", indent)
		}
	case generation.GeneratedValueInteger, generation.GeneratedValueNumber:
		if len(constraints.Minimum) != 0 {
			literal, err := numericConstraintLiteral(prepared.field.Type, constraints.Minimum)
			if err != nil {
				return err
			}
			fmt.Fprintf(source, "%sif %s < %s {\n", indent, value, literal)
			fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
			fmt.Fprintf(source, "%s}\n", indent)
		}
		if len(constraints.Maximum) != 0 {
			literal, err := numericConstraintLiteral(prepared.field.Type, constraints.Maximum)
			if err != nil {
				return err
			}
			fmt.Fprintf(source, "%sif %s > %s {\n", indent, value, literal)
			fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
			fmt.Fprintf(source, "%s}\n", indent)
		}
	case generation.GeneratedValueArray:
		checks := make([]string, 0, 2)
		if constraints.MinItems != nil {
			checks = append(checks, fmt.Sprintf("len(%s) < %d", value, *constraints.MinItems))
		}
		if constraints.MaxItems != nil {
			checks = append(checks, fmt.Sprintf("len(%s) > %d", value, *constraints.MaxItems))
		}
		if len(checks) != 0 {
			fmt.Fprintf(source, "%sif %s {\n", indent, strings.Join(checks, " || "))
			fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
			fmt.Fprintf(source, "%s}\n", indent)
		}
	}
	return nil
}

func numericConstraintLiteral(kind generation.GeneratedValueType, raw json.RawMessage) (string, error) {
	source := string(raw)
	switch kind {
	case generation.GeneratedValueInteger:
		value, err := strconv.ParseInt(source, 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid canonical integer constraint %q", source)
		}
		return strconv.FormatInt(value, 10), nil
	case generation.GeneratedValueNumber:
		value, err := strconv.ParseFloat(source, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("invalid canonical number constraint %q", source)
		}
		return strconv.FormatFloat(value, 'g', -1, 64), nil
	default:
		return "", fmt.Errorf("numeric constraint uses unsupported canonical type %q", kind)
	}
}

func renderFiniteNumberValidation(source *strings.Builder, value, indent, failure string) {
	fmt.Fprintf(source, "%sif math.IsNaN(float64(%s)) || math.IsInf(float64(%s), 0) {\n", indent, value, value)
	fmt.Fprintf(source, "%s\treturn %s\n", indent, failure)
	fmt.Fprintf(source, "%s}\n", indent)
}

func renderObjectValidation(source *strings.Builder) {
	fmt.Fprintln(source)
	fmt.Fprintln(source, "const plystraMaximumObjectDepth = 64")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraValidObject(value map[string]any) bool {")
	fmt.Fprintln(source, "\treturn value != nil && plystraValidObjectValue(value, 0)")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
	fmt.Fprintln(source, "func plystraValidObjectValue(value any, depth int) bool {")
	fmt.Fprintln(source, "\tif depth > plystraMaximumObjectDepth {")
	fmt.Fprintln(source, "\t\treturn false")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "\tswitch value := value.(type) {")
	fmt.Fprintln(source, "\tcase nil, bool:")
	fmt.Fprintln(source, "\t\treturn true")
	fmt.Fprintln(source, "\tcase int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:")
	fmt.Fprintln(source, "\t\treturn true")
	fmt.Fprintln(source, "\tcase float32:")
	fmt.Fprintln(source, "\t\treturn !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)")
	fmt.Fprintln(source, "\tcase float64:")
	fmt.Fprintln(source, "\t\treturn !math.IsNaN(value) && !math.IsInf(value, 0)")
	fmt.Fprintln(source, "\tcase json.Number:")
	fmt.Fprintln(source, "\t\tnumber, err := value.Float64()")
	fmt.Fprintln(source, "\t\treturn err == nil && !math.IsNaN(number) && !math.IsInf(number, 0)")
	fmt.Fprintln(source, "\tcase string:")
	fmt.Fprintln(source, "\t\treturn utf8.ValidString(value)")
	fmt.Fprintln(source, "\tcase []byte:")
	fmt.Fprintln(source, "\t\treturn true")
	fmt.Fprintln(source, "\tcase map[string]any:")
	fmt.Fprintln(source, "\t\tfor key, item := range value {")
	fmt.Fprintln(source, "\t\t\tif !utf8.ValidString(key) || !plystraValidObjectValue(item, depth+1) {")
	fmt.Fprintln(source, "\t\t\t\treturn false")
	fmt.Fprintln(source, "\t\t\t}")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t\treturn true")
	fmt.Fprintln(source, "\tcase []any:")
	fmt.Fprintln(source, "\t\tfor _, item := range value {")
	fmt.Fprintln(source, "\t\t\tif !plystraValidObjectValue(item, depth+1) {")
	fmt.Fprintln(source, "\t\t\t\treturn false")
	fmt.Fprintln(source, "\t\t\t}")
	fmt.Fprintln(source, "\t\t}")
	fmt.Fprintln(source, "\t\treturn true")
	fmt.Fprintln(source, "\tdefault:")
	fmt.Fprintln(source, "\t\treturn false")
	fmt.Fprintln(source, "\t}")
	fmt.Fprintln(source, "}")
}

func responseLiteral(kind generation.GeneratedValueType, raw json.RawMessage) (string, error) {
	if kind != generation.GeneratedValueString {
		return string(raw), nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", fmt.Errorf("decode response enum: %w", err)
	}
	return strconv.Quote(value), nil
}

func renderContributions(
	source *strings.Builder,
	contract canonicalContract,
	prepared preparedPlan,
	contributions []generationlowering.Contribution,
) error {
	for _, contribution := range contributions {
		previous := make(map[string]generationlowering.Node)
		for _, node := range contribution.Nodes() {
			var err error
			switch node.Kind() {
			case generation.GeneratedNodeKindCapabilityCall:
				err = renderCapabilityCall(source, contract.Request, contract.Response, prepared.dependencies, contribution, node, previous)
			case generation.GeneratedNodeKindContextDerivation:
				err = renderContextDerivation(source, contract.Request, contract.Response, contribution, node, previous)
			case generation.GeneratedNodeKindConditionalFailure:
				err = renderConditionalFailure(source, contract.Request, contract.Response, contribution, node, previous)
			case generation.GeneratedNodeKindMetadataAttachment:
				err = renderMetadataAttachment(source, contract.Request, contract.Response, contribution, node, previous)
			case generation.GeneratedNodeKindAuditEventCall:
				err = renderAuditEventCall(source, contract.Request, contract.Response, prepared.dependencies, contribution, node, previous)
			default:
				err = fmt.Errorf("%w: contribution %q node %q uses unsupported kind %q", ErrContribution, contribution.ID(), node.ID(), node.Kind())
			}
			if err != nil {
				return err
			}
			previous[node.ID()] = node
		}
	}
	return nil
}

type preparedPlan struct {
	ingress                []generationlowering.Contribution
	preparations           []generationlowering.Contribution
	completions            []generationlowering.Contribution
	egress                 []generationlowering.Contribution
	dependencies           []invocationDependency
	hasTimedCalls          bool
	hasContextOperations   bool
	hasPointerBindings     bool
	hasOptionalConversions bool
	hasConditionalFailures bool
	hasValueConversions    bool
	hasHTTPPath            bool
	hasAdapterCredentials  bool
}

type bindingRequirements struct {
	pointer            bool
	optionalConversion bool
	valueConversion    bool
	contextRead        bool
}

func (p *preparedPlan) includeBinding(requirements bindingRequirements) {
	p.hasPointerBindings = p.hasPointerBindings || requirements.pointer
	p.hasOptionalConversions = p.hasOptionalConversions || requirements.optionalConversion
	p.hasValueConversions = p.hasValueConversions || requirements.valueConversion
	p.hasContextOperations = p.hasContextOperations || requirements.contextRead
}

type invocationDependency struct {
	reference generationlowering.TargetReference
	field     string
}

func preparePlan(
	identifier capabilityid.Identifier,
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	plan *generationlowering.Plan,
) (preparedPlan, error) {
	if plan == nil {
		return preparedPlan{}, nil
	}
	result := preparedPlan{}
	byCapability := make(map[string]invocationDependency)
	for _, contribution := range plan.Contributions() {
		if contribution.Source().String() != identifier.String() {
			continue
		}
		nodes := contribution.Nodes()
		point := contribution.Point()
		if len(nodes) != 0 && point != generation.GenerationPointHTTPIngress &&
			point != generation.GenerationPointInvocationPrepare &&
			point != generation.GenerationPointInvocationComplete &&
			point != generation.GenerationPointHTTPEgress {
			return preparedPlan{}, fmt.Errorf(
				"%w: contribution %q for %s uses unsupported point %q",
				ErrContribution,
				contribution.ID(),
				identifier,
				point,
			)
		}
		previous := make(map[string]generationlowering.Node, len(nodes))
		for _, node := range nodes {
			if node.UsesAdapterCredential() {
				result.hasHTTPPath = true
				result.hasAdapterCredentials = true
			}
			switch node.Kind() {
			case generation.GeneratedNodeKindCapabilityCall:
				operation, ok := node.Generated().CapabilityCall()
				if !ok || operation.OnError != generation.GeneratedCallFailClosed && operation.OnError != generation.GeneratedCallCapture {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q has unsupported Capability-call failure mode", ErrContribution, contribution.ID(), node.ID())
				}
				if operation.Capability.String() == identifier.String() {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q recursively calls its own source %s", ErrContribution, contribution.ID(), node.ID(), identifier)
				}
				reference, ok := node.Target()
				if !ok {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q has no lowered target", ErrContribution, contribution.ID(), node.ID())
				}
				for _, binding := range operation.Request {
					requirements, err := prepareBinding(sourceRequest, sourceResponse, previous, reference, binding, point)
					if err != nil {
						return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q binding %q: %v", ErrContribution, contribution.ID(), node.ID(), binding.Field, err)
					}
					result.includeBinding(requirements)
				}
				key := reference.Capability().String()
				dependency := invocationDependency{reference: reference, field: reference.ImportName() + "Client"}
				if previous, exists := byCapability[key]; exists && previous != dependency {
					return preparedPlan{}, fmt.Errorf("%w: target %s has inconsistent lowered references", ErrContribution, key)
				}
				byCapability[key] = dependency
				result.hasTimedCalls = true
			case generation.GeneratedNodeKindContextDerivation:
				operation, ok := node.Generated().ContextDerivation()
				if !ok {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q lost its context derivation", ErrContribution, contribution.ID(), node.ID())
				}
				needsValueConversion, err := prepareContextDerivation(sourceRequest, sourceResponse, previous, operation, point)
				if err != nil {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), err)
				}
				result.hasContextOperations = true
				result.hasPointerBindings = result.hasPointerBindings || operation.Presence == generation.GeneratedContextOptional
				result.hasValueConversions = result.hasValueConversions || needsValueConversion
			case generation.GeneratedNodeKindConditionalFailure:
				operation, ok := node.Generated().ConditionalFailure()
				if !ok {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q lost its conditional failure", ErrContribution, contribution.ID(), node.ID())
				}
				usesContext, err := prepareConditionalFailure(sourceRequest, sourceResponse, previous, operation, point)
				if err != nil {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), err)
				}
				result.hasConditionalFailures = true
				result.hasContextOperations = result.hasContextOperations || usesContext
			case generation.GeneratedNodeKindMetadataAttachment:
				operation, ok := node.Generated().MetadataAttachment()
				if !ok {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q lost its metadata attachment", ErrContribution, contribution.ID(), node.ID())
				}
				if err := prepareMetadataAttachment(sourceRequest, sourceResponse, previous, operation, point); err != nil {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), err)
				}
				result.hasContextOperations = true
			case generation.GeneratedNodeKindAuditEventCall:
				operation, ok := node.Generated().AuditEventCall()
				if !ok {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q lost its audit-event call", ErrContribution, contribution.ID(), node.ID())
				}
				if operation.Capability.String() == identifier.String() {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q recursively audits through its own source %s", ErrContribution, contribution.ID(), node.ID(), identifier)
				}
				reference, ok := node.Target()
				if !ok {
					return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q has no lowered audit target", ErrContribution, contribution.ID(), node.ID())
				}
				for _, binding := range operation.Request {
					requirements, err := prepareBinding(sourceRequest, sourceResponse, previous, reference, binding, point)
					if err != nil {
						return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q binding %q: %v", ErrContribution, contribution.ID(), node.ID(), binding.Field, err)
					}
					result.includeBinding(requirements)
				}
				key := reference.Capability().String()
				dependency := invocationDependency{reference: reference, field: reference.ImportName() + "Client"}
				if previous, exists := byCapability[key]; exists && previous != dependency {
					return preparedPlan{}, fmt.Errorf("%w: audit target %s has inconsistent lowered references", ErrContribution, key)
				}
				byCapability[key] = dependency
				result.hasTimedCalls = true
			default:
				return preparedPlan{}, fmt.Errorf("%w: contribution %q node %q uses unsupported kind %q", ErrContribution, contribution.ID(), node.ID(), node.Kind())
			}
			previous[node.ID()] = node
		}
		if len(nodes) == 0 {
			continue
		}
		switch point {
		case generation.GenerationPointHTTPIngress:
			result.ingress = append(result.ingress, contribution)
			result.hasHTTPPath = true
		case generation.GenerationPointInvocationPrepare:
			result.preparations = append(result.preparations, contribution)
		case generation.GenerationPointInvocationComplete:
			result.completions = append(result.completions, contribution)
		case generation.GenerationPointHTTPEgress:
			result.egress = append(result.egress, contribution)
			result.hasHTTPPath = true
		}
	}
	keys := make([]string, 0, len(byCapability))
	for key := range byCapability {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		leftValue, rightValue := byCapability[keys[left]], byCapability[keys[right]]
		if leftValue.reference.ImportPath() != rightValue.reference.ImportPath() {
			return leftValue.reference.ImportPath() < rightValue.reference.ImportPath()
		}
		return keys[left] < keys[right]
	})
	result.dependencies = make([]invocationDependency, len(keys))
	for index, key := range keys {
		result.dependencies[index] = byCapability[key]
	}
	return result, nil
}

func prepareBinding(
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	previous map[string]generationlowering.Node,
	reference generationlowering.TargetReference,
	binding generation.GeneratedFieldBinding,
	point generation.GenerationPoint,
) (bindingRequirements, error) {
	target, ok := binding.Target()
	if !ok {
		return bindingRequirements{}, errors.New("normalized target field shape is absent")
	}
	if _, err := generatedFieldGoType(reference.ContractImportName(), "Request", binding.Field, target.Type(), target.Items(), target.Enumerated()); err != nil {
		return bindingRequirements{}, err
	}
	requirements := bindingRequirements{}
	var source prepareValue
	if binding.Value.Invocation != nil && binding.Value.Invocation.Source == generation.GeneratedInvocationContextValue {
		shape, shapeOK := binding.Value.Shape()
		if !shapeOK {
			return bindingRequirements{}, errors.New("normalized context value shape is absent")
		}
		goType, err := generatedValueGoType(shape.Type(), shape.Items())
		if err != nil {
			return bindingRequirements{}, err
		}
		source = prepareValue{goType: goType, optional: shape.Optional()}
		requirements.contextRead = true
	} else {
		var err error
		source, err = resolveInvocationValue(sourceRequest, sourceResponse, previous, binding.Value, point)
		if err != nil {
			return bindingRequirements{}, err
		}
	}
	if source.optional && target.Required() {
		return bindingRequirements{}, fmt.Errorf("optional source cannot populate required target field %q", binding.Field)
	}
	requirements.valueConversion = source.wholeResponse
	if target.Required() {
		return requirements, nil
	}
	if source.optional {
		requirements.optionalConversion = true
		return requirements, nil
	}
	requirements.pointer = true
	return requirements, nil
}

func prepareContextDerivation(
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	previous map[string]generationlowering.Node,
	operation generation.GeneratedContextDerivation,
	point generation.GenerationPoint,
) (bool, error) {
	if _, err := generatedValueGoType(operation.Type, operation.Items); err != nil {
		return false, err
	}
	if operation.Value.Invocation != nil && operation.Value.Invocation.Source == generation.GeneratedInvocationContextValue {
		_, err := generatedValueGoType(operation.Value.Invocation.Type, operation.Value.Invocation.Items)
		return false, err
	}
	source, err := resolveInvocationValue(sourceRequest, sourceResponse, previous, operation.Value, point)
	return source.wholeResponse, err
}

func prepareMetadataAttachment(
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	previous map[string]generationlowering.Node,
	operation generation.GeneratedMetadataAttachment,
	point generation.GenerationPoint,
) error {
	shape, ok := operation.Value.Shape()
	if !ok {
		return errors.New("normalized metadata value shape is absent")
	}
	if _, err := generatedValueGoType(shape.Type(), shape.Items()); err != nil {
		return err
	}
	if operation.Value.Invocation != nil && operation.Value.Invocation.Source == generation.GeneratedInvocationContextValue {
		_, err := generatedValueGoType(operation.Value.Invocation.Type, operation.Value.Invocation.Items)
		return err
	}
	source, err := resolveInvocationValue(sourceRequest, sourceResponse, previous, operation.Value, point)
	if err != nil {
		return err
	}
	if source.optional || source.wholeResponse {
		return errors.New("metadata source must be one present scalar")
	}
	return nil
}

type prepareValue struct {
	expression    string
	goType        string
	optional      bool
	wholeResponse bool
}

func resolveInvocationValue(
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	previous map[string]generationlowering.Node,
	value generation.GeneratedValue,
	point generation.GenerationPoint,
) (prepareValue, error) {
	shape, ok := value.Shape()
	if !ok {
		return prepareValue{}, errors.New("normalized value shape is absent")
	}
	switch {
	case value.Literal != nil:
		expression, err := renderLiteral(value)
		if err != nil {
			return prepareValue{}, err
		}
		goType, err := generatedValueGoType(shape.Type(), shape.Items())
		return prepareValue{expression: expression, goType: goType}, err
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationRequestField:
		name := value.Invocation.Name
		field, exists := sourceRequest[name]
		if !exists {
			return prepareValue{}, fmt.Errorf("request field %q is absent from the source contract", name)
		}
		goType, err := canonicalFieldGoType("contract", "Request", name, field)
		return prepareValue{expression: "request." + goname.Field(name), goType: goType, optional: !field.Required}, err
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationResponseField:
		if !pointHasInvocationOutcome(point) {
			return prepareValue{}, fmt.Errorf("value source %q is unavailable at point %q", value.Invocation.Source, point)
		}
		name := value.Invocation.Name
		field, exists := sourceResponse[name]
		if !exists {
			return prepareValue{}, fmt.Errorf("response field %q is absent from the source contract", name)
		}
		goType, err := canonicalFieldGoType("contract", "Response", name, field)
		return prepareValue{expression: "response." + goname.Field(name), goType: goType, optional: !field.Required}, err
	case value.Node != nil:
		prior, exists := previous[value.Node.ID]
		if !exists {
			return prepareValue{}, fmt.Errorf("referenced node %q is absent from earlier rendered nodes", value.Node.ID)
		}
		switch value.Node.Output {
		case generation.GeneratedNodeDerived:
			identifier, ok := prior.Identifier(generation.GeneratedNodeDerived)
			if !ok {
				return prepareValue{}, fmt.Errorf("node %q has no derived-value identifier", value.Node.ID)
			}
			goType, err := generatedValueGoType(shape.Type(), shape.Items())
			return prepareValue{expression: identifier, goType: goType, optional: shape.Optional()}, err
		case generation.GeneratedNodeResponse:
			identifier, identifierOK := prior.Identifier(generation.GeneratedNodeResponse)
			reference, referenceOK := prior.Target()
			if !identifierOK || !referenceOK {
				return prepareValue{}, fmt.Errorf("node %q has no response reference", value.Node.ID)
			}
			if value.Node.Field == "" {
				return prepareValue{
					expression:    identifier,
					goType:        reference.ContractImportName() + ".Response",
					wholeResponse: true,
				}, nil
			}
			goType, err := generatedFieldGoType(
				reference.ContractImportName(),
				"Response",
				value.Node.Field,
				shape.Type(),
				shape.Items(),
				shape.Enumerated(),
			)
			return prepareValue{
				expression: identifier + "." + goname.Field(value.Node.Field),
				goType:     goType,
				optional:   shape.Optional(),
			}, err
		default:
			return prepareValue{}, fmt.Errorf("node output %q is not renderable as contract data", value.Node.Output)
		}
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationAdapterCredential:
		return prepareValue{
			expression: "plystraAdapterCredential(adapterCredentials, " + strconv.Quote(value.Invocation.Name) + ")",
			goType:     "string",
			optional:   true,
		}, nil
	case value.Invocation != nil:
		return prepareValue{}, fmt.Errorf("value source %q is not renderable at %s", value.Invocation.Source, point)
	default:
		return prepareValue{}, fmt.Errorf("value source is not renderable at %s", point)
	}
}

func prepareConditionalFailure(
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	previous map[string]generationlowering.Node,
	operation generation.GeneratedConditionalFailure,
	point generation.GenerationPoint,
) (bool, error) {
	value := operation.Condition.Value
	if _, ok := value.Shape(); !ok {
		return false, errors.New("normalized condition value shape is absent")
	}
	switch {
	case value.Literal != nil:
		_, err := renderLiteral(value)
		return false, err
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationRequestField:
		field, exists := sourceRequest[value.Invocation.Name]
		if !exists {
			return false, fmt.Errorf("request field %q is absent from the source contract", value.Invocation.Name)
		}
		_, err := canonicalFieldGoType("contract", "Request", value.Invocation.Name, field)
		return false, err
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationResponseField:
		if !pointHasInvocationOutcome(point) {
			return false, fmt.Errorf("value source %q is unavailable at point %q", value.Invocation.Source, point)
		}
		field, exists := sourceResponse[value.Invocation.Name]
		if !exists {
			return false, fmt.Errorf("response field %q is absent from the source contract", value.Invocation.Name)
		}
		_, err := canonicalFieldGoType("contract", "Response", value.Invocation.Name, field)
		return false, err
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationError:
		if !pointHasInvocationOutcome(point) {
			return false, fmt.Errorf("value source %q is unavailable at point %q", value.Invocation.Source, point)
		}
		return false, nil
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationContextValue:
		_, err := generatedValueGoType(value.Invocation.Type, value.Invocation.Items)
		return true, err
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationAdapterCredential:
		return false, nil
	case value.Node != nil:
		if _, exists := previous[value.Node.ID]; !exists {
			return false, fmt.Errorf("referenced node %q is absent from earlier rendered nodes", value.Node.ID)
		}
		return false, nil
	case value.Invocation != nil:
		return false, fmt.Errorf("value source %q is not renderable at %s", value.Invocation.Source, point)
	default:
		return false, fmt.Errorf("value source is not renderable at %s", point)
	}
}

func pointHasInvocationOutcome(point generation.GenerationPoint) bool {
	return point == generation.GenerationPointInvocationComplete || point == generation.GenerationPointHTTPEgress
}

func renderContextDerivation(
	source *strings.Builder,
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	contribution generationlowering.Contribution,
	node generationlowering.Node,
	previous map[string]generationlowering.Node,
) error {
	operation, ok := node.Generated().ContextDerivation()
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q lost its context derivation", ErrContribution, contribution.ID(), node.ID())
	}
	derivedIdentifier, derivedOK := node.Identifier(generation.GeneratedNodeDerived)
	errorIdentifier, errorOK := node.Identifier(generation.GeneratedNodeError)
	if !derivedOK || !errorOK {
		return fmt.Errorf("%w: contribution %q node %q has incomplete lowered identifiers", ErrContribution, contribution.ID(), node.ID())
	}
	targetType, err := generatedValueGoType(operation.Type, operation.Items)
	if err != nil {
		return fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), err)
	}

	expression := ""
	optionalPointer := false
	optionalPresence := false
	presenceIdentifier := ""
	wholeResponse := false
	if operation.Value.Invocation != nil && operation.Value.Invocation.Source == generation.GeneratedInvocationContextValue {
		sourceIdentifier, sourceOK := node.SourceIdentifier()
		var presenceOK bool
		presenceIdentifier, presenceOK = node.PresenceIdentifier()
		if !sourceOK || !presenceOK {
			return fmt.Errorf("%w: contribution %q node %q has incomplete context-read identifiers", ErrContribution, contribution.ID(), node.ID())
		}
		sourceType, typeErr := generatedValueGoType(operation.Value.Invocation.Type, operation.Value.Invocation.Items)
		err = typeErr
		if err == nil {
			fmt.Fprintf(
				source,
				"\t%s, %s := invocationcontext.Value[%s](ctx, %s)\n",
				sourceIdentifier,
				presenceIdentifier,
				sourceType,
				strconv.Quote(operation.Value.Invocation.Name),
			)
			expression = sourceIdentifier
			optionalPresence = true
		}
	} else {
		resolved, resolveErr := resolveInvocationValue(sourceRequest, sourceResponse, previous, operation.Value, contribution.Point())
		err = resolveErr
		expression = resolved.expression
		optionalPointer = resolved.optional
		wholeResponse = resolved.wholeResponse
	}
	if err != nil {
		return fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), err)
	}
	if wholeResponse {
		conversionType := targetType
		storedExpression := derivedIdentifier
		if operation.Presence == generation.GeneratedContextOptional {
			conversionType = "*" + targetType
			storedExpression = "*" + derivedIdentifier
		}
		fmt.Fprintf(source, "\t%s, %s := plystraConvertValue[%s](%s)\n", derivedIdentifier, errorIdentifier, conversionType, expression)
		fmt.Fprintf(source, "\tif %s != nil {\n", errorIdentifier)
		fmt.Fprintf(source, "\t\treturn contract.Response{}, %s\n", errorIdentifier)
		fmt.Fprintln(source, "\t}")
		fmt.Fprintf(
			source,
			"\tctx, %s = invocationcontext.WithValue(ctx, %s, %s, %d)\n",
			errorIdentifier,
			strconv.Quote(operation.Key),
			storedExpression,
			operation.MaximumBytes,
		)
		fmt.Fprintf(source, "\tif %s != nil {\n", errorIdentifier)
		fmt.Fprintf(source, "\t\treturn contract.Response{}, %s\n", errorIdentifier)
		fmt.Fprintln(source, "\t}")
		return nil
	}

	valueExpression := expression
	if optionalPointer {
		valueExpression = "*" + expression
	}
	converted := targetType + "(" + valueExpression + ")"
	if operation.Presence == generation.GeneratedContextRequired {
		if optionalPointer {
			fmt.Fprintf(source, "\tif %s == nil {\n", expression)
			fmt.Fprintln(source, "\t\treturn contract.Response{}, invocationcontext.ErrInvalidValue")
			fmt.Fprintln(source, "\t}")
		}
		if optionalPresence {
			fmt.Fprintf(source, "\tif !%s {\n", presenceIdentifier)
			fmt.Fprintln(source, "\t\treturn contract.Response{}, invocationcontext.ErrInvalidValue")
			fmt.Fprintln(source, "\t}")
		}
		fmt.Fprintf(source, "\t%s := %s\n", derivedIdentifier, converted)
		fmt.Fprintf(
			source,
			"\tctx, %s := invocationcontext.WithValue(ctx, %s, %s, %d)\n",
			errorIdentifier,
			strconv.Quote(operation.Key),
			derivedIdentifier,
			operation.MaximumBytes,
		)
		fmt.Fprintf(source, "\tif %s != nil {\n", errorIdentifier)
		fmt.Fprintf(source, "\t\treturn contract.Response{}, %s\n", errorIdentifier)
		fmt.Fprintln(source, "\t}")
		return nil
	}

	fmt.Fprintf(source, "\tvar %s *%s\n", derivedIdentifier, targetType)
	fmt.Fprintf(source, "\tvar %s error\n", errorIdentifier)
	condition := "true"
	if optionalPointer {
		condition = expression + " != nil"
	} else if optionalPresence {
		condition = presenceIdentifier
	}
	indent := "\t"
	if condition != "true" {
		fmt.Fprintf(source, "\tif %s {\n", condition)
		indent = "\t\t"
	}
	fmt.Fprintf(source, "%s%s = plystraPointer(%s)\n", indent, derivedIdentifier, converted)
	fmt.Fprintf(
		source,
		"%sctx, %s = invocationcontext.WithValue(ctx, %s, *%s, %d)\n",
		indent,
		errorIdentifier,
		strconv.Quote(operation.Key),
		derivedIdentifier,
		operation.MaximumBytes,
	)
	if condition != "true" {
		fmt.Fprintln(source, "\t}")
	}
	fmt.Fprintf(source, "\tif %s != nil {\n", errorIdentifier)
	fmt.Fprintf(source, "\t\treturn contract.Response{}, %s\n", errorIdentifier)
	fmt.Fprintln(source, "\t}")
	return nil
}

func renderMetadataAttachment(
	source *strings.Builder,
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	contribution generationlowering.Contribution,
	node generationlowering.Node,
	previous map[string]generationlowering.Node,
) error {
	operation, ok := node.Generated().MetadataAttachment()
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q lost its metadata attachment", ErrContribution, contribution.ID(), node.ID())
	}
	errorIdentifier, errorOK := node.Identifier(generation.GeneratedNodeError)
	if !errorOK {
		return fmt.Errorf("%w: contribution %q node %q has no metadata error identifier", ErrContribution, contribution.ID(), node.ID())
	}
	shape, shapeOK := operation.Value.Shape()
	if !shapeOK {
		return fmt.Errorf("%w: contribution %q node %q has no normalized metadata value shape", ErrContribution, contribution.ID(), node.ID())
	}
	targetType, err := generatedValueGoType(shape.Type(), shape.Items())
	if err != nil {
		return fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), err)
	}

	expression := ""
	if operation.Value.Invocation != nil && operation.Value.Invocation.Source == generation.GeneratedInvocationContextValue {
		sourceIdentifier, sourceOK := node.SourceIdentifier()
		presenceIdentifier, presenceOK := node.PresenceIdentifier()
		if !sourceOK || !presenceOK {
			return fmt.Errorf("%w: contribution %q node %q has incomplete metadata-source identifiers", ErrContribution, contribution.ID(), node.ID())
		}
		sourceType, typeErr := generatedValueGoType(operation.Value.Invocation.Type, operation.Value.Invocation.Items)
		if typeErr != nil {
			return fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), typeErr)
		}
		fmt.Fprintf(
			source,
			"\t%s, %s := invocationcontext.Value[%s](ctx, %s)\n",
			sourceIdentifier,
			presenceIdentifier,
			sourceType,
			strconv.Quote(operation.Value.Invocation.Name),
		)
		fmt.Fprintf(source, "\tif !%s {\n", presenceIdentifier)
		fmt.Fprintln(source, "\t\treturn contract.Response{}, invocationcontext.ErrInvalidValue")
		fmt.Fprintln(source, "\t}")
		expression = sourceIdentifier
	} else {
		resolved, resolveErr := resolveInvocationValue(sourceRequest, sourceResponse, previous, operation.Value, contribution.Point())
		if resolveErr != nil {
			return fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), resolveErr)
		}
		if resolved.optional || resolved.wholeResponse {
			return fmt.Errorf("%w: contribution %q node %q metadata source is not one present scalar", ErrContribution, contribution.ID(), node.ID())
		}
		expression = resolved.expression
	}
	fmt.Fprintf(
		source,
		"\tctx, %s := invocationcontext.WithMetadata(ctx, %s, %s(%s), %d)\n",
		errorIdentifier,
		strconv.Quote(operation.Key),
		targetType,
		expression,
		operation.MaximumBytes,
	)
	fmt.Fprintf(source, "\tif %s != nil {\n", errorIdentifier)
	fmt.Fprintf(source, "\t\treturn contract.Response{}, %s\n", errorIdentifier)
	fmt.Fprintln(source, "\t}")
	return nil
}

func renderConditionalFailure(
	source *strings.Builder,
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	contribution generationlowering.Contribution,
	node generationlowering.Node,
	previous map[string]generationlowering.Node,
) error {
	operation, ok := node.Generated().ConditionalFailure()
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q lost its conditional failure", ErrContribution, contribution.ID(), node.ID())
	}
	shape, ok := operation.Condition.Value.Shape()
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q has no normalized condition value shape", ErrContribution, contribution.ID(), node.ID())
	}
	expression := ""
	optionalPointer := false
	optionalPresence := false
	presenceIdentifier := ""
	value := operation.Condition.Value
	var err error
	switch {
	case value.Literal != nil:
		expression, err = renderLiteral(value)
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationRequestField:
		field, exists := sourceRequest[value.Invocation.Name]
		if !exists {
			return fmt.Errorf("%w: contribution %q node %q request field %q is absent from the source contract", ErrContribution, contribution.ID(), node.ID(), value.Invocation.Name)
		}
		_, err = canonicalFieldGoType("contract", "Request", value.Invocation.Name, field)
		expression = "request." + goname.Field(value.Invocation.Name)
		optionalPointer = !field.Required
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationResponseField:
		if !pointHasInvocationOutcome(contribution.Point()) {
			return fmt.Errorf("%w: contribution %q node %q cannot read the canonical response at point %q", ErrContribution, contribution.ID(), node.ID(), contribution.Point())
		}
		field, exists := sourceResponse[value.Invocation.Name]
		if !exists {
			return fmt.Errorf("%w: contribution %q node %q response field %q is absent from the source contract", ErrContribution, contribution.ID(), node.ID(), value.Invocation.Name)
		}
		_, err = canonicalFieldGoType("contract", "Response", value.Invocation.Name, field)
		expression = "response." + goname.Field(value.Invocation.Name)
		optionalPointer = !field.Required
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationError:
		if !pointHasInvocationOutcome(contribution.Point()) {
			return fmt.Errorf("%w: contribution %q node %q cannot read the canonical invocation error at point %q", ErrContribution, contribution.ID(), node.ID(), contribution.Point())
		}
		expression = "invocationError"
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationContextValue:
		sourceIdentifier, sourceOK := node.SourceIdentifier()
		var presenceOK bool
		presenceIdentifier, presenceOK = node.PresenceIdentifier()
		if !sourceOK || !presenceOK {
			return fmt.Errorf("%w: contribution %q node %q has incomplete context-read identifiers", ErrContribution, contribution.ID(), node.ID())
		}
		sourceType, typeErr := generatedValueGoType(value.Invocation.Type, value.Invocation.Items)
		err = typeErr
		if err == nil {
			valueIdentifier := sourceIdentifier
			if operation.Condition.Operator == generation.GeneratedConditionMissing || operation.Condition.Operator == generation.GeneratedConditionPresent {
				valueIdentifier = "_"
			}
			fmt.Fprintf(
				source,
				"\t%s, %s := invocationcontext.Value[%s](ctx, %s)\n",
				valueIdentifier,
				presenceIdentifier,
				sourceType,
				strconv.Quote(value.Invocation.Name),
			)
			expression = sourceIdentifier
			optionalPresence = true
			if !shape.Optional() {
				fmt.Fprintf(source, "\tif !%s {\n", presenceIdentifier)
				fmt.Fprintln(source, "\t\treturn contract.Response{}, invocationcontext.ErrInvalidValue")
				fmt.Fprintln(source, "\t}")
			}
		}
	case value.Invocation != nil && value.Invocation.Source == generation.GeneratedInvocationAdapterCredential:
		expression = "plystraAdapterCredential(adapterCredentials, " + strconv.Quote(value.Invocation.Name) + ")"
		optionalPointer = true
	case value.Node != nil:
		prior, exists := previous[value.Node.ID]
		if !exists {
			return fmt.Errorf("%w: contribution %q node %q references absent earlier node %q", ErrContribution, contribution.ID(), node.ID(), value.Node.ID)
		}
		switch value.Node.Output {
		case generation.GeneratedNodeError:
			expression, ok = prior.Identifier(generation.GeneratedNodeError)
		case generation.GeneratedNodeDerived:
			expression, ok = prior.Identifier(generation.GeneratedNodeDerived)
			if derivation, derivationOK := prior.Generated().ContextDerivation(); derivationOK {
				optionalPointer = derivation.Presence == generation.GeneratedContextOptional
			}
		case generation.GeneratedNodeResponse:
			responseIdentifier, responseOK := prior.Identifier(generation.GeneratedNodeResponse)
			if responseOK && value.Node.Field != "" {
				expression = responseIdentifier + "." + goname.Field(value.Node.Field)
				optionalPointer = shape.Optional()
				ok = true
			}
		}
		if !ok || expression == "" {
			return fmt.Errorf("%w: contribution %q node %q cannot render output %q from node %q", ErrContribution, contribution.ID(), node.ID(), value.Node.Output, value.Node.ID)
		}
	default:
		return fmt.Errorf("%w: contribution %q node %q uses a condition value not renderable at %s", ErrContribution, contribution.ID(), node.ID(), contribution.Point())
	}
	if err != nil {
		return fmt.Errorf("%w: contribution %q node %q: %v", ErrContribution, contribution.ID(), node.ID(), err)
	}

	condition := ""
	switch operation.Condition.Operator {
	case generation.GeneratedConditionMissing:
		if optionalPresence {
			condition = "!" + presenceIdentifier
		} else if optionalPointer {
			condition = expression + " == nil"
		}
	case generation.GeneratedConditionPresent:
		if optionalPresence {
			condition = presenceIdentifier
		} else if optionalPointer {
			condition = expression + " != nil"
		}
	case generation.GeneratedConditionTrue:
		condition = expression
	case generation.GeneratedConditionFalse:
		condition = "!" + expression
	case generation.GeneratedConditionError:
		condition = expression + " != nil"
	}
	if condition == "" {
		return fmt.Errorf("%w: contribution %q node %q cannot render condition operator %q", ErrContribution, contribution.ID(), node.ID(), operation.Condition.Operator)
	}
	fmt.Fprintf(source, "\tif %s {\n", condition)
	fmt.Fprintf(
		source,
		"\t\treturn contract.Response{}, plystraConditionalError{code: contract.Err%s, message: %s}\n",
		goname.Field(operation.ErrorCode),
		strconv.Quote(operation.Message),
	)
	fmt.Fprintln(source, "\t}")
	return nil
}

func renderAuditEventCall(
	source *strings.Builder,
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	dependencies []invocationDependency,
	contribution generationlowering.Contribution,
	node generationlowering.Node,
	previous map[string]generationlowering.Node,
) error {
	operation, ok := node.Generated().AuditEventCall()
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q lost its audit-event call", ErrContribution, contribution.ID(), node.ID())
	}
	reference, ok := node.Target()
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q lost its audit target", ErrContribution, contribution.ID(), node.ID())
	}
	dependency, ok := findDependency(dependencies, reference.Capability().String())
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q audit target %s has no generated client dependency", ErrContribution, contribution.ID(), node.ID(), reference.Capability())
	}
	errorIdentifier, errorOK := node.Identifier(generation.GeneratedNodeError)
	if !errorOK {
		return fmt.Errorf("%w: contribution %q node %q has no audit error identifier", ErrContribution, contribution.ID(), node.ID())
	}
	fmt.Fprintf(source, "\tvar %s error\n", errorIdentifier)
	bindings, err := renderCallBindings(source, sourceRequest, sourceResponse, contribution, node, previous, reference, errorIdentifier, operation.Request)
	if err != nil {
		return err
	}
	fmt.Fprintf(source, "\t_, %s = plystraInvokeWithTimeout(\n", errorIdentifier)
	fmt.Fprintln(source, "\t\tctx,")
	fmt.Fprintf(source, "\t\t%d*time.Millisecond,\n", operation.TimeoutMilliseconds)
	fmt.Fprintf(source, "\t\th.%s.%s,\n", dependency.field, reference.Operation())
	fmt.Fprintf(source, "\t\t%s.Request{\n", reference.ContractImportName())
	for index, binding := range operation.Request {
		fmt.Fprintf(source, "\t\t\t%s: %s,\n", goname.Field(binding.Field), bindings[index])
	}
	fmt.Fprintln(source, "\t\t},")
	fmt.Fprintln(source, "\t)")
	if operation.OnError == generation.GeneratedCallFailClosed {
		fmt.Fprintf(source, "\tif %s != nil {\n", errorIdentifier)
		fmt.Fprintf(source, "\t\treturn contract.Response{}, %s\n", errorIdentifier)
		fmt.Fprintln(source, "\t}")
	} else {
		fmt.Fprintf(source, "\t_ = %s\n", errorIdentifier)
	}
	return nil
}

func renderCapabilityCall(
	source *strings.Builder,
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	dependencies []invocationDependency,
	contribution generationlowering.Contribution,
	node generationlowering.Node,
	previous map[string]generationlowering.Node,
) error {
	operation, ok := node.Generated().CapabilityCall()
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q lost its Capability call", ErrContribution, contribution.ID(), node.ID())
	}
	reference, ok := node.Target()
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q lost its target", ErrContribution, contribution.ID(), node.ID())
	}
	dependency, ok := findDependency(dependencies, reference.Capability().String())
	if !ok {
		return fmt.Errorf("%w: contribution %q node %q target %s has no generated client dependency", ErrContribution, contribution.ID(), node.ID(), reference.Capability())
	}
	responseIdentifier, responseOK := node.Identifier(generation.GeneratedNodeResponse)
	errorIdentifier, errorOK := node.Identifier(generation.GeneratedNodeError)
	if !responseOK || !errorOK {
		return fmt.Errorf("%w: contribution %q node %q has incomplete lowered identifiers", ErrContribution, contribution.ID(), node.ID())
	}
	bindings, err := renderCallBindings(source, sourceRequest, sourceResponse, contribution, node, previous, reference, errorIdentifier, operation.Request)
	if err != nil {
		return err
	}
	fmt.Fprintf(source, "\t%s, %s := plystraInvokeWithTimeout(\n", responseIdentifier, errorIdentifier)
	fmt.Fprintln(source, "\t\tctx,")
	fmt.Fprintf(source, "\t\t%d*time.Millisecond,\n", operation.TimeoutMilliseconds)
	fmt.Fprintf(source, "\t\th.%s.%s,\n", dependency.field, reference.Operation())
	fmt.Fprintf(source, "\t\t%s.Request{\n", reference.ContractImportName())
	for index, binding := range operation.Request {
		fmt.Fprintf(source, "\t\t\t%s: %s,\n", goname.Field(binding.Field), bindings[index])
	}
	fmt.Fprintln(source, "\t\t},")
	fmt.Fprintln(source, "\t)")
	if operation.OnError == generation.GeneratedCallFailClosed {
		fmt.Fprintf(source, "\tif %s != nil {\n", errorIdentifier)
		fmt.Fprintf(source, "\t\treturn contract.Response{}, %s\n", errorIdentifier)
		fmt.Fprintln(source, "\t}")
	}
	fmt.Fprintf(source, "\t_ = %s\n", responseIdentifier)
	return nil
}

func renderCallBindings(
	source *strings.Builder,
	sourceRequest map[string]canonicalField,
	sourceResponse map[string]canonicalField,
	contribution generationlowering.Contribution,
	node generationlowering.Node,
	previous map[string]generationlowering.Node,
	reference generationlowering.TargetReference,
	errorIdentifier string,
	request []generation.GeneratedFieldBinding,
) ([]string, error) {
	bindings := make([]string, len(request))
	for index, binding := range request {
		resolved := prepareValue{}
		var err error
		if binding.Value.Invocation != nil && binding.Value.Invocation.Source == generation.GeneratedInvocationContextValue {
			shape, shapeOK := binding.Value.Shape()
			if !shapeOK {
				return nil, fmt.Errorf("%w: contribution %q node %q binding %q has no normalized context value shape", ErrContribution, contribution.ID(), node.ID(), binding.Field)
			}
			sourceType, typeErr := generatedValueGoType(shape.Type(), shape.Items())
			if typeErr != nil {
				return nil, fmt.Errorf("%w: contribution %q node %q binding %q: %v", ErrContribution, contribution.ID(), node.ID(), binding.Field, typeErr)
			}
			sourceIdentifier, sourceOK := node.BindingSourceIdentifier(binding.Field)
			presenceIdentifier, presenceOK := node.BindingPresenceIdentifier(binding.Field)
			if !sourceOK || !presenceOK {
				return nil, fmt.Errorf("%w: contribution %q node %q binding %q has incomplete context-read identifiers", ErrContribution, contribution.ID(), node.ID(), binding.Field)
			}
			fmt.Fprintf(
				source,
				"\t%s, %s := invocationcontext.Value[%s](ctx, %s)\n",
				sourceIdentifier,
				presenceIdentifier,
				sourceType,
				strconv.Quote(binding.Value.Invocation.Name),
			)
			resolved = prepareValue{expression: sourceIdentifier, goType: sourceType, optional: shape.Optional()}
			if shape.Optional() {
				optionalIdentifier, identifierOK := node.BindingIdentifier(binding.Field)
				if !identifierOK {
					return nil, fmt.Errorf("%w: contribution %q node %q binding %q has no optional context identifier", ErrContribution, contribution.ID(), node.ID(), binding.Field)
				}
				fmt.Fprintf(source, "\tvar %s *%s\n", optionalIdentifier, sourceType)
				fmt.Fprintf(source, "\tif %s {\n", presenceIdentifier)
				fmt.Fprintf(source, "\t\t%s = &%s\n", optionalIdentifier, sourceIdentifier)
				fmt.Fprintln(source, "\t}")
				resolved.expression = optionalIdentifier
			} else {
				fmt.Fprintf(source, "\tif !%s {\n", presenceIdentifier)
				fmt.Fprintln(source, "\t\treturn contract.Response{}, invocationcontext.ErrInvalidValue")
				fmt.Fprintln(source, "\t}")
			}
		} else {
			resolved, err = resolveInvocationValue(sourceRequest, sourceResponse, previous, binding.Value, contribution.Point())
			if err != nil {
				return nil, fmt.Errorf("%w: contribution %q node %q binding %q: %v", ErrContribution, contribution.ID(), node.ID(), binding.Field, err)
			}
		}
		if resolved.wholeResponse {
			target, targetOK := binding.Target()
			if !targetOK {
				return nil, fmt.Errorf("%w: contribution %q node %q binding %q has no normalized target shape", ErrContribution, contribution.ID(), node.ID(), binding.Field)
			}
			targetType, typeErr := generatedFieldGoType(reference.ContractImportName(), "Request", binding.Field, target.Type(), target.Items(), target.Enumerated())
			if typeErr != nil {
				return nil, fmt.Errorf("%w: contribution %q node %q binding %q: %v", ErrContribution, contribution.ID(), node.ID(), binding.Field, typeErr)
			}
			conversionIdentifier, identifierOK := node.BindingIdentifier(binding.Field)
			if !identifierOK {
				return nil, fmt.Errorf("%w: contribution %q node %q binding %q has no conversion identifier", ErrContribution, contribution.ID(), node.ID(), binding.Field)
			}
			fmt.Fprintf(source, "\t%s, %s := plystraConvertValue[%s](%s)\n", conversionIdentifier, errorIdentifier, targetType, resolved.expression)
			fmt.Fprintf(source, "\tif %s != nil {\n", errorIdentifier)
			fmt.Fprintf(source, "\t\treturn contract.Response{}, %s\n", errorIdentifier)
			fmt.Fprintln(source, "\t}")
			resolved = prepareValue{expression: conversionIdentifier, goType: targetType}
		}
		bindings[index], err = renderBindingValue(reference, binding, resolved)
		if err != nil {
			return nil, fmt.Errorf("%w: contribution %q node %q binding %q: %v", ErrContribution, contribution.ID(), node.ID(), binding.Field, err)
		}
	}
	return bindings, nil
}

func renderBindingValue(
	reference generationlowering.TargetReference,
	binding generation.GeneratedFieldBinding,
	source prepareValue,
) (string, error) {
	target, ok := binding.Target()
	if !ok {
		return "", errors.New("normalized target field shape is absent")
	}
	targetType, err := generatedFieldGoType(reference.ContractImportName(), "Request", binding.Field, target.Type(), target.Items(), target.Enumerated())
	if err != nil {
		return "", err
	}

	if target.Required() {
		if source.optional {
			return "", errors.New("optional source cannot populate a required target field")
		}
		return targetType + "(" + source.expression + ")", nil
	}
	if source.optional {
		return fmt.Sprintf(
			"plystraConvertOptional(%s, func(value %s) %s { return %s(value) })",
			source.expression,
			source.goType,
			targetType,
			targetType,
		), nil
	}
	return "plystraPointer(" + targetType + "(" + source.expression + "))", nil
}

func renderLiteral(value generation.GeneratedValue) (string, error) {
	if value.Literal == nil {
		return "", errors.New("value is not a literal")
	}
	switch {
	case value.Literal.String != nil:
		return strconv.Quote(*value.Literal.String), nil
	case value.Literal.Integer != nil:
		return strconv.FormatInt(*value.Literal.Integer, 10), nil
	case value.Literal.Boolean != nil:
		return strconv.FormatBool(*value.Literal.Boolean), nil
	default:
		return "", errors.New("literal has no supported scalar value")
	}
}

func canonicalFieldGoType(importName, section, fieldName string, field canonicalField) (string, error) {
	return generatedFieldGoType(importName, section, fieldName, field.Type, field.Items, len(field.Enum) != 0)
}

func generatedFieldGoType(
	importName string,
	section string,
	fieldName string,
	typeName generation.GeneratedValueType,
	items generation.GeneratedValueType,
	enumerated bool,
) (string, error) {
	if enumerated {
		return importName + "." + section + goname.Field(fieldName), nil
	}
	return generatedValueGoType(typeName, items)
}

func generatedValueGoType(typeName, items generation.GeneratedValueType) (string, error) {
	switch typeName {
	case generation.GeneratedValueString:
		return "string", nil
	case generation.GeneratedValueInteger:
		return "int64", nil
	case generation.GeneratedValueNumber:
		return "float64", nil
	case generation.GeneratedValueBoolean:
		return "bool", nil
	case generation.GeneratedValueObject:
		return "map[string]any", nil
	case generation.GeneratedValueArray:
		itemType, err := generatedValueGoType(items, "")
		if err != nil {
			return "", fmt.Errorf("array item type: %w", err)
		}
		return "[]" + itemType, nil
	default:
		return "", fmt.Errorf("unsupported generated value type %q", typeName)
	}
}

func findDependency(values []invocationDependency, capability string) (invocationDependency, bool) {
	for _, value := range values {
		if value.reference.Capability().String() == capability {
			return value, true
		}
	}
	return invocationDependency{}, false
}

func generatedDirectory(category string, identifier capabilityid.Identifier) string {
	components := append([]string{"generated", "go", category}, strings.Split(identifier.Name(), ".")...)
	components = append(components, "v"+strconv.FormatUint(identifier.Major(), 10))
	return path.Join(components...)
}
