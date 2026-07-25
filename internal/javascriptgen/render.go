// Package javascriptgen renders deterministic ESM TypeScript application SDKs
// from the provider-independent shared SDK model.
package javascriptgen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/sdkmodel"
	"github.com/plystra/cli/internal/transportprovenance"
)

const (
	generatedRoot             = "generated/sdk/javascript"
	protobufJavaScriptVersion = "2.12.1"
	connectJavaScriptVersion  = "2.1.2"
	typescriptVersion         = "7.0.2"
)

// ErrRender reports that a normalized SDK model could not produce a complete
// deterministic JavaScript package.
var ErrRender = errors.New("render generated JavaScript SDK")

// Options controls application-owned npm package identity.
type Options struct {
	PackageName             string
	ConfigurationProvenance transportprovenance.Provenance
	Transport               TransportOptions
}

// File is one immutable generated JavaScript SDK source file.
type File struct {
	path string
	data []byte
}

// Path returns the slash-separated application-relative generated path.
func (f File) Path() string { return f.path }

// Data returns a defensive copy of generated file bytes.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

type renderedOperation struct {
	id         generation.CapabilityID
	target     generation.CapabilityID
	operation  sdkmodel.Operation
	deprecated string
	segments   []string
	version    string
	symbol     string
	source     string
	transport  transportOperation
}

func (o renderedOperation) isAlias() bool { return o.id != o.target }

type clientNode struct {
	children  map[string]*clientNode
	operation *renderedOperation
}

// Render validates package identity and renders a complete source package.
// Provider IDs, runtime plugin configuration, build metadata, and Secret
// values are absent from both the input model and final files.
func Render(options Options, model sdkmodel.Model) ([]File, error) {
	if !options.ConfigurationProvenance.Valid() {
		return nil, fmt.Errorf("%w: selected configuration provenance is absent or invalid", ErrRender)
	}
	if !validPackageName(options.PackageName) {
		return nil, fmt.Errorf("%w: npm package name %q is not canonical lower-case package identity", ErrRender, options.PackageName)
	}
	canonical := model.CanonicalJSON()
	if len(canonical) == 0 || model.Digest() != digest(canonical) {
		return nil, fmt.Errorf("%w: SDK model is absent or has an invalid digest", ErrRender)
	}
	operations, err := prepareOperations(model.Operations(), model.Aliases())
	if err != nil {
		return nil, err
	}
	operations, err = bindTransport(operations, options.Transport)
	if err != nil {
		return nil, err
	}
	files := []File{
		newFile(path.Join(generatedRoot, ".npmrc"), []byte(npmrcSource)),
		newFile(path.Join(generatedRoot, "package.json"), renderPackageJSON(options.PackageName)),
		newFile(path.Join(generatedRoot, "tsconfig.json"), []byte(tsconfigSource)),
		newFile(path.Join(generatedRoot, "src", "descriptors.ts"), renderDescriptorSource(options.Transport.DescriptorSet)),
		newFile(path.Join(generatedRoot, "src", "runtime.ts"), []byte(runtimeSource)),
	}
	index, err := renderIndex(operations)
	if err != nil {
		return nil, err
	}
	files = append(files,
		newFile(path.Join(generatedRoot, "src", "index.ts"), index),
		newFile(path.Join(generatedRoot, "README.md"), renderREADME(options.PackageName, operations)),
	)
	for _, operation := range operations {
		var source []byte
		if operation.isAlias() {
			source, err = renderAliasOperation(operation)
		} else {
			source, err = renderOperation(operation)
		}
		if err != nil {
			return nil, err
		}
		files = append(files, newFile(path.Join(generatedRoot, operation.source), source))
	}
	sort.Slice(files, func(left, right int) bool {
		return files[left].path < files[right].path
	})
	for index := 1; index < len(files); index++ {
		if files[index-1].path == files[index].path {
			return nil, fmt.Errorf("%w: generated path collision at %s", ErrRender, files[index].path)
		}
	}
	return files, nil
}

const npmrcSource = "package-lock=false\n"

func prepareOperations(values []sdkmodel.Operation, aliases []sdkmodel.Alias) ([]renderedOperation, error) {
	operations := make([]renderedOperation, 0, len(values)+len(aliases))
	for _, operation := range values {
		operations = append(operations, renderedOperation{
			id:        operation.ID(),
			target:    operation.ID(),
			operation: operation,
		})
	}
	for _, alias := range aliases {
		operations = append(operations, renderedOperation{
			id:         alias.ID(),
			target:     alias.Target(),
			operation:  alias.TargetOperation(),
			deprecated: alias.Deprecated(),
		})
	}
	baseCounts := make(map[string]int, len(operations))
	seen := make(map[string]struct{}, len(operations))
	for index := range operations {
		id := operations[index].id
		if id.String() == "" {
			return nil, fmt.Errorf("%w: operations[%d] has an empty Capability ID", ErrRender, index)
		}
		if _, duplicate := seen[id.String()]; duplicate {
			return nil, fmt.Errorf("%w: operations[%d] duplicates Capability %s", ErrRender, index, id)
		}
		seen[id.String()] = struct{}{}
		segments := strings.Split(id.Name(), ".")
		version := "v" + strconv.FormatUint(id.Major(), 10)
		symbol := operationSymbol(id.Name(), version)
		baseCounts[symbol]++
		sourceComponents := append([]string{"src", "operations"}, segments...)
		sourceComponents = append(sourceComponents, version+".ts")
		operations[index].segments = segments
		operations[index].version = version
		operations[index].symbol = symbol
		operations[index].source = path.Join(sourceComponents...)
	}
	sort.Slice(operations, func(left, right int) bool {
		return operations[left].id.String() < operations[right].id.String()
	})
	for index := range operations {
		if baseCounts[operations[index].symbol] > 1 {
			operations[index].symbol += "_" + hex.EncodeToString([]byte(operations[index].id.String()))
		}
	}
	return operations, nil
}

func renderPackageJSON(packageName string) []byte {
	type packageExport struct {
		Types  string `json:"types"`
		Import string `json:"import"`
	}
	type packageExports struct {
		Root packageExport `json:"."`
	}
	type packageScripts struct {
		Build     string `json:"build"`
		Typecheck string `json:"typecheck"`
	}
	type packageRuntimeDependencies struct {
		Protobuf   string `json:"@bufbuild/protobuf"`
		Connect    string `json:"@connectrpc/connect"`
		ConnectWeb string `json:"@connectrpc/connect-web"`
	}
	type packageDevelopmentDependencies struct {
		TypeScript string `json:"typescript"`
	}
	manifest := struct {
		Name            string                         `json:"name"`
		Version         string                         `json:"version"`
		Description     string                         `json:"description"`
		Type            string                         `json:"type"`
		SideEffects     bool                           `json:"sideEffects"`
		Files           []string                       `json:"files"`
		Main            string                         `json:"main"`
		Module          string                         `json:"module"`
		Types           string                         `json:"types"`
		Exports         packageExports                 `json:"exports"`
		Scripts         packageScripts                 `json:"scripts"`
		Dependencies    packageRuntimeDependencies     `json:"dependencies"`
		DevDependencies packageDevelopmentDependencies `json:"devDependencies"`
	}{
		Name:        packageName,
		Version:     "0.0.0",
		Description: "Generated Plystra application SDK.",
		Type:        "module",
		SideEffects: false,
		Files:       []string{"dist", "README.md"},
		Main:        "./dist/index.js",
		Module:      "./dist/index.js",
		Types:       "./dist/index.d.ts",
		Exports: packageExports{Root: packageExport{
			Types:  "./dist/index.d.ts",
			Import: "./dist/index.js",
		}},
		Scripts: packageScripts{
			Build:     "tsc -p tsconfig.json",
			Typecheck: "tsc -p tsconfig.json --noEmit",
		},
		Dependencies: packageRuntimeDependencies{
			Protobuf:   protobufJavaScriptVersion,
			Connect:    connectJavaScriptVersion,
			ConnectWeb: connectJavaScriptVersion,
		},
		DevDependencies: packageDevelopmentDependencies{TypeScript: typescriptVersion},
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(encoded, '\n')
}

const tsconfigSource = `{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "rootDir": "src",
    "outDir": "dist",
    "declaration": true,
    "stripInternal": true,
    "noEmitOnError": true,
    "strict": true,
    "exactOptionalPropertyTypes": true,
    "noUncheckedIndexedAccess": true,
    "noPropertyAccessFromIndexSignature": true,
    "useUnknownInCatchVariables": true,
    "verbatimModuleSyntax": true,
    "isolatedModules": true,
    "moduleDetection": "force",
    "forceConsistentCasingInFileNames": true
  },
  "include": ["src/**/*.ts"]
}
`

func renderOperation(operation renderedOperation) ([]byte, error) {
	if err := validateJavaScriptFields(operation.operation.ID().String()+" request", operation.operation.Request()); err != nil {
		return nil, err
	}
	if err := validateJavaScriptFields(operation.operation.ID().String()+" response", operation.operation.Response()); err != nil {
		return nil, err
	}
	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "import {")
	fmt.Fprintln(&source, "  createRuntime,")
	fmt.Fprintln(&source, "  hasOwn,")
	fmt.Fprintln(&source, "  invoke,")
	if operationUsesStringBounds(operation.operation) {
		fmt.Fprintln(&source, "  isStringWithinUnicodeScalarBounds,")
	}
	if operationUsesInteger(operation.operation) {
		fmt.Fprintln(&source, "  isSignedInteger,")
	}
	if operationUsesJSONValue(operation.operation) {
		fmt.Fprintln(&source, "  isJSONValue,")
	}
	fmt.Fprintln(&source, "  isPlainObject,")
	fmt.Fprintln(&source, "  PlystraError,")
	fmt.Fprintln(&source, "  type ClientOptions,")
	fmt.Fprintln(&source, "  type ErrorContract,")
	if operationUsesJSONValue(operation.operation) {
		fmt.Fprintln(&source, "  type JSONValue,")
	}
	fmt.Fprintln(&source, "  type MessageCodec,")
	fmt.Fprintln(&source, "  type RequestOptions,")
	fmt.Fprintln(&source, "  type Runtime,")
	rootPrefix := strings.Repeat("../", len(operation.segments)+1)
	fmt.Fprintf(&source, "} from %s;\n", jsString(rootPrefix+"runtime.js"))
	fmt.Fprintf(&source, "import { resolveMessage, resolveUnaryMethod } from %s;\n\n", jsString(rootPrefix+"descriptors.js"))
	fmt.Fprintf(&source, "export const capabilityID = %s;\n", jsString(operation.operation.ID().String()))
	fmt.Fprintf(&source, "export const contractDigest = %s;\n", jsString(operation.operation.ContractDigest()))
	renderMethodBinding(&source, operation.transport)
	fmt.Fprintf(&source, "const errorDetailDescriptor = resolveMessage(%s);\n", jsString(protobufdescriptor.ErrorDetailFullName))
	fmt.Fprintln(&source, "const semanticErrorCodes = Object.freeze([")
	for _, semanticErrorCode := range operation.operation.Errors() {
		fmt.Fprintf(&source, "  %s,\n", jsString(semanticErrorCode))
	}
	fmt.Fprintln(&source, "]);")
	fmt.Fprintln(&source)
	renderType(&source, "Request", operation.operation.Request())
	renderType(&source, "Response", operation.operation.Response())
	renderErrorCode(&source, operation.operation.Errors())
	renderValidator(&source, "isRequest", "Request", operation.operation.Request())
	renderValidator(&source, "isResponse", "Response", operation.operation.Response())
	fmt.Fprintln(&source, "export type Operation = (")
	fmt.Fprintln(&source, "  request: Request,")
	fmt.Fprintln(&source, "  options?: RequestOptions,")
	fmt.Fprintln(&source, ") => Promise<Response>;")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "/** @internal */")
	fmt.Fprintln(&source, "export function bindOperation(runtime: Runtime): Operation {")
	fmt.Fprintln(&source, "  return bindOperationMethod(runtime, method, capabilityID);")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "/** @internal */")
	fmt.Fprintln(&source, "export function bindOperationMethod(runtime: Runtime, operationMethod: typeof method, requestedCapabilityID: string): Operation {")
	fmt.Fprintln(&source, "  const errorContract: ErrorContract = {")
	fmt.Fprintln(&source, "    requestedCapabilityID,")
	fmt.Fprintln(&source, "    canonicalCapabilityID: capabilityID,")
	fmt.Fprintln(&source, "    semanticErrorCodes,")
	fmt.Fprintln(&source, "    detailDescriptor: errorDetailDescriptor,")
	fmt.Fprintln(&source, "  };")
	fmt.Fprintln(&source, "  return async (request, options = {}) => {")
	fmt.Fprintln(&source, "    let requestIsValid = false;")
	fmt.Fprintln(&source, "    try {")
	fmt.Fprintln(&source, "      requestIsValid = isRequest(request);")
	fmt.Fprintln(&source, "    } catch {")
	fmt.Fprintln(&source, "      requestIsValid = false;")
	fmt.Fprintln(&source, "    }")
	fmt.Fprintln(&source, "    if (!requestIsValid) {")
	fmt.Fprintf(&source, "      throw new TypeError(%s);\n", jsString("request does not match "+operation.operation.ID().String()))
	fmt.Fprintln(&source, "    }")
	fmt.Fprintln(&source, "    const response = await invoke(runtime, operationMethod, requestCodec, responseCodec, errorContract, request, options);")
	fmt.Fprintln(&source, "    if (!isResponse(response)) {")
	fmt.Fprintln(&source, "      throw new PlystraError(200, \"invalid_response\");")
	fmt.Fprintln(&source, "    }")
	fmt.Fprintln(&source, "    return response;")
	fmt.Fprintln(&source, "  };")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "export function createOperation(options: ClientOptions): Operation {")
	fmt.Fprintln(&source, "  return bindOperation(createRuntime(options));")
	fmt.Fprintln(&source, "}")
	return []byte(source.String()), nil
}

func renderAliasOperation(alias renderedOperation) ([]byte, error) {
	if !alias.isAlias() || alias.operation.ID() != alias.target || alias.operation.ContractDigest() == "" {
		return nil, fmt.Errorf("%w: Alias %s does not name one complete canonical target", ErrRender, alias.id)
	}
	rootPrefix := strings.Repeat("../", len(alias.segments)+1)
	targetComponents := append([]string{"operations"}, strings.Split(alias.target.Name(), ".")...)
	targetComponents = append(targetComponents, "v"+strconv.FormatUint(alias.target.Major(), 10)+".js")
	targetImport := rootPrefix + path.Join(targetComponents...)
	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "import { resolveUnaryMethod } from "+jsString(rootPrefix+"descriptors.js")+";")
	fmt.Fprintln(&source, "import { createRuntime, type ClientOptions, type Runtime } from "+jsString(rootPrefix+"runtime.js")+";")
	fmt.Fprintln(&source, "import {")
	fmt.Fprintln(&source, "  bindOperationMethod as bindCanonicalOperationMethod,")
	fmt.Fprintln(&source, "  type Operation,")
	fmt.Fprintln(&source, "} from "+jsString(targetImport)+";")
	fmt.Fprintln(&source, "export type { ErrorCode, Operation, Request, Response } from "+jsString(targetImport)+";")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "export const capabilityID = %s;\n", jsString(alias.id.String()))
	fmt.Fprintf(&source, "export const targetCapabilityID = %s;\n", jsString(alias.target.String()))
	fmt.Fprintf(&source, "export const contractDigest = %s;\n", jsString(alias.operation.ContractDigest()))
	renderMethodResolver(&source, alias.transport)
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "/** @internal */")
	fmt.Fprintln(&source, "export function bindOperation(runtime: Runtime): Operation {")
	fmt.Fprintln(&source, "  return bindCanonicalOperationMethod(runtime, method, capabilityID);")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	renderJSDocDeprecation(&source, "", alias.deprecated)
	fmt.Fprintln(&source, "export function createOperation(options: ClientOptions): Operation {")
	fmt.Fprintln(&source, "  return bindOperation(createRuntime(options));")
	fmt.Fprintln(&source, "}")
	return []byte(source.String()), nil
}

func renderType(source *strings.Builder, name string, fields []sdkmodel.Field) {
	if len(fields) == 0 {
		fmt.Fprintf(source, "export type %s = Readonly<Record<string, never>>;\n\n", name)
		return
	}
	fmt.Fprintf(source, "export interface %s {\n", name)
	for _, field := range fields {
		optional := ""
		if !field.Required() {
			optional = "?"
		}
		renderConstraintJSDoc(source, "  ", field.Constraints())
		fmt.Fprintf(source, "  readonly %s%s: %s;\n", jsString(field.Name()), optional, typescriptType(field))
	}
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
}

func renderErrorCode(source *strings.Builder, errors []string) {
	if len(errors) == 0 {
		fmt.Fprintln(source, "export type ErrorCode = never;")
		fmt.Fprintln(source)
		return
	}
	values := make([]string, len(errors))
	for index, value := range errors {
		values[index] = jsString(value)
	}
	fmt.Fprintf(source, "export type ErrorCode = %s;\n\n", strings.Join(values, " | "))
}

func renderValidator(source *strings.Builder, functionName, typeName string, fields []sdkmodel.Field) {
	fmt.Fprintf(source, "function %s(value: unknown): value is %s {\n", functionName, typeName)
	fmt.Fprintln(source, "  return (")
	fmt.Fprintln(source, "    isPlainObject(value) &&")
	if len(fields) == 0 {
		fmt.Fprintln(source, "    Object.keys(value).length === 0")
	} else {
		allowed := make([]string, len(fields))
		for index, field := range fields {
			allowed[index] = "key === " + jsString(field.Name())
		}
		fmt.Fprintf(source, "    Object.keys(value).every((key) => %s) &&\n", strings.Join(allowed, " || "))
		for index, field := range fields {
			expression := "value[" + jsString(field.Name()) + "]"
			valid := validValue(field, expression)
			condition := ""
			if field.Required() {
				condition = "hasOwn(value, " + jsString(field.Name()) + ") && " + valid
			} else {
				condition = "(!hasOwn(value, " + jsString(field.Name()) + ") || " + valid + ")"
			}
			suffix := ""
			if index != len(fields)-1 {
				suffix = " &&"
			}
			fmt.Fprintf(source, "    %s%s\n", condition, suffix)
		}
	}
	fmt.Fprintln(source, "  );")
	fmt.Fprintln(source, "}")
	fmt.Fprintln(source)
}

func renderIndex(operations []renderedOperation) ([]byte, error) {
	root, err := buildClientTree(operations)
	if err != nil {
		return nil, err
	}
	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "import { createRuntime, type ClientOptions } from \"./runtime.js\";")
	for _, operation := range operations {
		importPath := indexImportPath(operation)
		fmt.Fprintln(&source, "import {")
		fmt.Fprintf(&source, "  bindOperation as bind%s,\n", operation.symbol)
		if operation.isAlias() {
			fmt.Fprintf(&source, "  createOperation as create%sOperation,\n", operation.symbol)
		} else {
			fmt.Fprintf(&source, "  createOperation as create%s,\n", operation.symbol)
		}
		fmt.Fprintf(&source, "  type Operation as %sOperation,\n", operation.symbol)
		fmt.Fprintf(&source, "} from %s;\n", jsString(importPath))
	}
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "export { PlystraError } from \"./runtime.js\";")
	fmt.Fprintln(&source, "export type { ClientOptions, JSONValue, KernelErrorClass, PlystraErrorDetail, RequestOptions } from \"./runtime.js\";")
	for _, operation := range operations {
		importPath := indexImportPath(operation)
		if operation.isAlias() {
			renderJSDocDeprecation(&source, "", operation.deprecated)
			fmt.Fprintf(&source, "export const create%s = create%sOperation;\n", operation.symbol, operation.symbol)
		} else {
			fmt.Fprintf(&source, "export { create%s };\n", operation.symbol)
		}
		fmt.Fprintln(&source, "export type {")
		fmt.Fprintf(&source, "  ErrorCode as %sErrorCode,\n", operation.symbol)
		fmt.Fprintf(&source, "  Request as %sRequest,\n", operation.symbol)
		fmt.Fprintf(&source, "  Response as %sResponse,\n", operation.symbol)
		fmt.Fprintf(&source, "} from %s;\n", jsString(importPath))
	}
	fmt.Fprintln(&source)
	fmt.Fprint(&source, "export type PlystraClient = ")
	if len(operations) == 0 {
		fmt.Fprintln(&source, "Readonly<Record<string, never>>;")
	} else {
		renderNodeType(&source, root, "")
		fmt.Fprintln(&source, ";")
	}
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "export function createPlystraClient(options: ClientOptions): PlystraClient {")
	fmt.Fprintln(&source, "  const runtime = createRuntime(options);")
	fmt.Fprint(&source, "  return ")
	renderNodeValue(&source, root, "  ")
	fmt.Fprintln(&source, ";")
	fmt.Fprintln(&source, "}")
	return []byte(source.String()), nil
}

func indexImportPath(operation renderedOperation) string {
	return "./" + strings.TrimSuffix(strings.TrimPrefix(operation.source, "src/"), ".ts") + ".js"
}

func buildClientTree(operations []renderedOperation) (*clientNode, error) {
	root := &clientNode{children: make(map[string]*clientNode)}
	for index := range operations {
		operation := &operations[index]
		components := append(append([]string(nil), operation.segments...), operation.version)
		node := root
		for _, component := range components {
			child := node.children[component]
			if child == nil {
				child = &clientNode{children: make(map[string]*clientNode)}
				node.children[component] = child
			}
			node = child
		}
		if node.operation != nil {
			return nil, fmt.Errorf("%w: client path collision for %s and %s", ErrRender, node.operation.id, operation.id)
		}
		node.operation = operation
	}
	return root, nil
}

func renderNodeType(source *strings.Builder, node *clientNode, indent string) {
	if node.operation != nil {
		fmt.Fprintf(source, "%sOperation", node.operation.symbol)
		if len(node.children) != 0 {
			fmt.Fprint(source, " & ")
		}
	}
	if node.operation == nil || len(node.children) != 0 {
		fmt.Fprintln(source, "{")
		for _, name := range sortedChildNames(node) {
			child := node.children[name]
			if child.operation != nil {
				renderJSDocDeprecation(source, indent+"  ", child.operation.deprecated)
			}
			fmt.Fprintf(source, "%s  readonly %s: ", indent, jsString(name))
			renderNodeType(source, child, indent+"  ")
			fmt.Fprintln(source, ";")
		}
		fmt.Fprintf(source, "%s}", indent)
	}
}

func renderNodeValue(source *strings.Builder, node *clientNode, indent string) {
	switch {
	case node.operation != nil && len(node.children) == 0:
		fmt.Fprintf(source, "bind%s(runtime)", node.operation.symbol)
	case node.operation != nil:
		fmt.Fprintf(source, "Object.freeze(Object.assign(bind%s(runtime), ", node.operation.symbol)
		renderChildrenValue(source, node, indent)
		fmt.Fprint(source, "))")
	default:
		fmt.Fprint(source, "Object.freeze(")
		renderChildrenValue(source, node, indent)
		fmt.Fprint(source, ")")
	}
}

func renderChildrenValue(source *strings.Builder, node *clientNode, indent string) {
	if len(node.children) == 0 {
		fmt.Fprint(source, "{}")
		return
	}
	fmt.Fprintln(source, "{")
	names := sortedChildNames(node)
	for _, name := range names {
		fmt.Fprintf(source, "%s  %s: ", indent, jsString(name))
		renderNodeValue(source, node.children[name], indent+"  ")
		fmt.Fprintln(source, ",")
	}
	fmt.Fprintf(source, "%s}", indent)
}

func sortedChildNames(node *clientNode) []string {
	names := make([]string, 0, len(node.children))
	for name := range node.children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func renderREADME(packageName string, operations []renderedOperation) []byte {
	var readme strings.Builder
	fmt.Fprintf(&readme, "# %s\n\n", packageName)
	fmt.Fprintln(&readme, "Generated Plystra application SDK. Do not edit generated files.")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "## Validate")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "```sh")
	fmt.Fprintln(&readme, "npm install --ignore-scripts --no-audit --no-fund")
	fmt.Fprintln(&readme, "npm run typecheck")
	fmt.Fprintln(&readme, "npm run build")
	fmt.Fprintln(&readme, "npm pack --dry-run --json")
	fmt.Fprintln(&readme, "```")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "The generated `.npmrc` disables lockfile creation because this package is CLI-owned. Installation may create only the ignored `node_modules/` and `dist/` validation outputs.")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "The Plystra wrapper resolves generated Protobuf descriptors and sends binary Connect requests through its pinned `@bufbuild/protobuf`, `@connectrpc/connect`, and `@connectrpc/connect-web` dependencies. Application code does not construct raw Protobuf messages or Connect clients, and raw Connect errors are normalized before they cross the wrapper boundary. Import only the package root; the export map blocks internal subpaths and generated declarations omit transport, descriptor, codec, and binder internals.")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "Generated application failures expose only an immutable Plystra-owned safe detail. On the wire, `requested_interface_id` records the requested canonical Interface or temporary pre-removal Alias, `canonical_interface_id` records the canonical Interface target, and exactly one declared semantic code or closed Kernel class is present. Implementation text, causes, payloads, panic data, configuration, credentials, Secrets, internal Kernel detail codes, and raw Connect details are excluded. Missing, duplicate, malformed, unknown, mismatched, or undeclared details fail closed to `internal`; inspect `PlystraError.detail` rather than parsing an error message.")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "Canonical `integer` fields and integer array items are signed 64-bit values exposed as JavaScript `bigint`, including enum literals such as `0n`. Pass `bigint`, not `number`, so request and response values remain exact across the full range.")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "Generated request and response declarations retain each exact normalized constraint object in a `@plystraConstraints` field annotation. The wrapper preflights Unicode scalar-value length, numeric bounds, and array item counts before sending a request and applies the same portable checks to decoded responses. Canonical `pattern` uses Go regular-expression semantics, so it is declared for tools and developers but remains enforced authoritatively by the generated server rather than reinterpreted through JavaScript `RegExp`.")
	fmt.Fprintln(&readme)
	if len(operations) == 0 {
		fmt.Fprintln(&readme, "This application currently exposes no JavaScript Capability operations.")
		return []byte(readme.String())
	}
	first := operations[0]
	for _, operation := range operations {
		if !operation.isAlias() {
			first = operation
			break
		}
	}
	fmt.Fprintln(&readme, "## Usage")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "```ts")
	fmt.Fprintf(&readme, "import { createPlystraClient } from %s;\n\n", jsString(packageName))
	fmt.Fprintln(&readme, "const client = createPlystraClient({")
	fmt.Fprintln(&readme, "  baseUrl: \"https://api.example.com\",")
	fmt.Fprintln(&readme, "  getAccessToken: async () => rawAccessToken,")
	fmt.Fprintln(&readme, "});")
	fmt.Fprintln(&readme)
	request := exampleRequest(first.operation.Request())
	fmt.Fprintf(&readme, "const response = await client%s(%s);\n", clientAccessor(first), request)
	fmt.Fprintln(&readme, "```")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "`getAccessToken` returns only the raw token value. The generated transport adds the `Bearer` authorization scheme; returning a value that already includes that scheme fails before the request is sent.")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "Pass an `AbortSignal` as the operation's second argument to cancel before dispatch or while the request is in flight. Cancellation rejects with `PlystraError` code `cancelled`; once server invocation has begun, it reaches the generated Connect handler, canonical invocation, and Provider context. Cancellation is best-effort interruption and does not promise Provider rollback.")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "Only explicitly exposed canonical operations and validated application-local Alias surfaces are generated. Alias methods reuse their direct canonical target contract and invoke the matching generated Alias Connect procedure. Provider packages, server configuration, verified internal context, and Secret values are never included.")
	fmt.Fprintln(&readme)
	fmt.Fprintln(&readme, "## Canonical operations")
	fmt.Fprintln(&readme)
	for _, operation := range operations {
		if !operation.isAlias() {
			fmt.Fprintf(&readme, "- `%s` (`%s`)\n", operation.id, operation.operation.ContractDigest())
		}
	}
	aliasCount := 0
	for _, operation := range operations {
		if operation.isAlias() {
			aliasCount++
		}
	}
	if aliasCount != 0 {
		fmt.Fprintln(&readme)
		fmt.Fprintln(&readme, "## Capability aliases")
		fmt.Fprintln(&readme)
		for _, operation := range operations {
			if !operation.isAlias() {
				continue
			}
			fmt.Fprintf(&readme, "- `%s` -> `%s` (`%s`)", operation.id, operation.target, operation.operation.ContractDigest())
			if operation.deprecated != "" {
				fmt.Fprintf(&readme, " - deprecated: %s", jsString(operation.deprecated))
			}
			fmt.Fprintln(&readme)
		}
	}
	return []byte(readme.String())
}

func typescriptType(field sdkmodel.Field) string {
	enum := field.EnumJSON()
	if len(enum) != 0 {
		values := make([]string, len(enum))
		for index, value := range enum {
			values[index] = typescriptScalarLiteral(field.Kind(), value)
		}
		return strings.Join(values, " | ")
	}
	if field.Kind() == sdkmodel.KindArray {
		return "ReadonlyArray<" + typescriptKind(field.Items()) + ">"
	}
	return typescriptKind(field.Kind())
}

func typescriptKind(kind sdkmodel.Kind) string {
	switch kind {
	case sdkmodel.KindString:
		return "string"
	case sdkmodel.KindInteger:
		return "bigint"
	case sdkmodel.KindNumber:
		return "number"
	case sdkmodel.KindBoolean:
		return "boolean"
	case sdkmodel.KindObject:
		return "Readonly<Record<string, JSONValue>>"
	default:
		panic("unsupported normalized SDK kind " + string(kind))
	}
}

func validValue(field sdkmodel.Field, expression string) string {
	var valid string
	enum := field.EnumJSON()
	if len(enum) != 0 {
		values := make([]string, len(enum))
		for index, value := range enum {
			values[index] = expression + " === " + typescriptScalarLiteral(field.Kind(), value)
		}
		valid = "(" + strings.Join(values, " || ") + ")"
		if field.Kind() == sdkmodel.KindInteger {
			valid = "isSignedInteger(" + expression + ") && " + valid
		}
	} else if field.Kind() == sdkmodel.KindArray {
		valid = "Array.isArray(" + expression + ") && " + expression + ".every((item) => " + validKind(field.Items(), "item") + ")"
	} else {
		valid = validKind(field.Kind(), expression)
	}
	checks := constraintChecks(field, expression)
	if len(checks) == 0 {
		return valid
	}
	return "(" + valid + " && " + strings.Join(checks, " && ") + ")"
}

func constraintChecks(field sdkmodel.Field, expression string) []string {
	constraints := field.Constraints()
	checks := make([]string, 0, 2)
	switch field.Kind() {
	case sdkmodel.KindString:
		minimum, hasMinimum := constraints.MinLength()
		maximum, hasMaximum := constraints.MaxLength()
		if hasMinimum || hasMaximum {
			checks = append(checks, fmt.Sprintf(
				"isStringWithinUnicodeScalarBounds(%s, %s, %s)",
				expression,
				optionalCountLiteral(minimum, hasMinimum),
				optionalCountLiteral(maximum, hasMaximum),
			))
		}
	case sdkmodel.KindInteger, sdkmodel.KindNumber:
		if minimum := constraints.MinimumJSON(); len(minimum) != 0 {
			checks = append(checks, expression+" >= "+typescriptNumericConstraintLiteral(field.Kind(), minimum))
		}
		if maximum := constraints.MaximumJSON(); len(maximum) != 0 {
			checks = append(checks, expression+" <= "+typescriptNumericConstraintLiteral(field.Kind(), maximum))
		}
	case sdkmodel.KindArray:
		if minimum, exists := constraints.MinItems(); exists {
			checks = append(checks, fmt.Sprintf("%s.length >= %d", expression, minimum))
		}
		if maximum, exists := constraints.MaxItems(); exists {
			checks = append(checks, fmt.Sprintf("%s.length <= %d", expression, maximum))
		}
	}
	return checks
}

func optionalCountLiteral(value uint32, exists bool) string {
	if !exists {
		return "undefined"
	}
	return strconv.FormatUint(uint64(value), 10)
}

func typescriptNumericConstraintLiteral(kind sdkmodel.Kind, value []byte) string {
	if kind == sdkmodel.KindInteger {
		return string(value) + "n"
	}
	return string(value)
}

func renderConstraintJSDoc(source *strings.Builder, indent string, constraints sdkmodel.FieldConstraints) {
	canonical := constraints.CanonicalJSON()
	if len(canonical) == 0 {
		return
	}
	safe := strings.ReplaceAll(string(canonical), "*/", "*\\/")
	fmt.Fprintf(source, "%s/** @plystraConstraints %s */\n", indent, safe)
}

func validateJavaScriptFields(section string, fields []sdkmodel.Field) error {
	for _, field := range fields {
		if field.Kind() != sdkmodel.KindInteger {
			continue
		}
		for _, raw := range field.EnumJSON() {
			if _, err := strconv.ParseInt(string(raw), 10, 64); err != nil {
				return fmt.Errorf("%w: %s field %q enum value %s is outside the signed 64-bit integer range", ErrRender, section, field.Name(), raw)
			}
		}
	}
	return nil
}

func validKind(kind sdkmodel.Kind, expression string) string {
	switch kind {
	case sdkmodel.KindString:
		return "typeof " + expression + " === \"string\""
	case sdkmodel.KindInteger:
		return "isSignedInteger(" + expression + ")"
	case sdkmodel.KindNumber:
		return "typeof " + expression + " === \"number\" && Number.isFinite(" + expression + ")"
	case sdkmodel.KindBoolean:
		return "typeof " + expression + " === \"boolean\""
	case sdkmodel.KindObject:
		return "isPlainObject(" + expression + ") && isJSONValue(" + expression + ")"
	default:
		panic("unsupported normalized SDK kind " + string(kind))
	}
}

func operationUsesJSONValue(operation sdkmodel.Operation) bool {
	for _, fields := range [][]sdkmodel.Field{operation.Request(), operation.Response()} {
		for _, field := range fields {
			if field.Kind() == sdkmodel.KindObject || field.Kind() == sdkmodel.KindArray && field.Items() == sdkmodel.KindObject {
				return true
			}
		}
	}
	return false
}

func operationUsesInteger(operation sdkmodel.Operation) bool {
	for _, fields := range [][]sdkmodel.Field{operation.Request(), operation.Response()} {
		for _, field := range fields {
			if field.Kind() == sdkmodel.KindInteger || field.Kind() == sdkmodel.KindArray && field.Items() == sdkmodel.KindInteger {
				return true
			}
		}
	}
	return false
}

func operationUsesStringBounds(operation sdkmodel.Operation) bool {
	for _, fields := range [][]sdkmodel.Field{operation.Request(), operation.Response()} {
		for _, field := range fields {
			if field.Kind() != sdkmodel.KindString {
				continue
			}
			constraints := field.Constraints()
			if _, exists := constraints.MinLength(); exists {
				return true
			}
			if _, exists := constraints.MaxLength(); exists {
				return true
			}
		}
	}
	return false
}

func operationSymbol(name, version string) string {
	var result strings.Builder
	upper := true
	for _, character := range name {
		switch {
		case character >= 'a' && character <= 'z':
			if upper {
				character -= 'a' - 'A'
			}
			result.WriteRune(character)
			upper = false
		case character >= '0' && character <= '9':
			result.WriteRune(character)
			upper = false
		default:
			upper = true
		}
	}
	result.WriteString(strings.ToUpper(version[:1]))
	result.WriteString(version[1:])
	return result.String()
}

func clientAccessor(operation renderedOperation) string {
	components := append(append([]string(nil), operation.segments...), operation.version)
	var result strings.Builder
	for _, component := range components {
		if javascriptIdentifier(component) {
			result.WriteByte('.')
			result.WriteString(component)
		} else {
			result.WriteByte('[')
			result.WriteString(jsString(component))
			result.WriteByte(']')
		}
	}
	return result.String()
}

func exampleRequest(fields []sdkmodel.Field) string {
	var result strings.Builder
	result.WriteByte('{')
	written := 0
	for _, field := range fields {
		if !field.Required() {
			continue
		}
		if written != 0 {
			result.WriteByte(',')
		}
		result.WriteString(jsString(field.Name()))
		result.WriteByte(':')
		result.WriteString(exampleValue(field))
		written++
	}
	result.WriteByte('}')
	return result.String()
}

func exampleValue(field sdkmodel.Field) string {
	if values := field.EnumJSON(); len(values) != 0 {
		return typescriptScalarLiteral(field.Kind(), values[0])
	}
	switch field.Kind() {
	case sdkmodel.KindString:
		return jsString("value")
	case sdkmodel.KindInteger:
		return "0n"
	case sdkmodel.KindNumber:
		return "0"
	case sdkmodel.KindBoolean:
		return "false"
	case sdkmodel.KindObject:
		return "{}"
	case sdkmodel.KindArray:
		return "[]"
	default:
		panic("unsupported normalized SDK kind " + string(field.Kind()))
	}
}

func typescriptScalarLiteral(kind sdkmodel.Kind, value json.RawMessage) string {
	if kind == sdkmodel.KindInteger {
		return string(value) + "n"
	}
	return string(value)
}

func validPackageName(value string) bool {
	if value == "" || len(value) > 214 || value != strings.ToLower(value) {
		return false
	}
	if strings.HasPrefix(value, "@") {
		parts := strings.Split(value, "/")
		return len(parts) == 2 && validPackagePart(strings.TrimPrefix(parts[0], "@")) && validPackagePart(parts[1])
	}
	return !strings.Contains(value, "/") && validPackagePart(value)
}

func validPackagePart(value string) bool {
	if value == "" || len(value) > 100 || !asciiLowerOrDigit(value[0]) || !asciiLowerOrDigit(value[len(value)-1]) || strings.Contains(value, "..") {
		return false
	}
	for index := 1; index < len(value)-1; index++ {
		character := value[index]
		if !asciiLowerOrDigit(character) && character != '-' && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func asciiLowerOrDigit(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func javascriptIdentifier(value string) bool {
	if value == "" || !(value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z' || value[0] == '_' || value[0] == '$') {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '$') {
			return false
		}
	}
	return true
}

func renderJSDocDeprecation(source *strings.Builder, indent, message string) {
	if message == "" {
		return
	}
	message = strings.ReplaceAll(message, "\r\n", "\n")
	message = strings.ReplaceAll(message, "\r", "\n")
	message = strings.ReplaceAll(message, "*/", "*\\/")
	lines := strings.Split(message, "\n")
	if len(lines) == 1 {
		fmt.Fprintf(source, "%s/** @deprecated %s */\n", indent, lines[0])
		return
	}
	fmt.Fprintf(source, "%s/**\n", indent)
	fmt.Fprintf(source, "%s * @deprecated %s\n", indent, lines[0])
	for _, line := range lines[1:] {
		fmt.Fprintf(source, "%s * %s\n", indent, line)
	}
	fmt.Fprintf(source, "%s */\n", indent)
}

func jsString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func newFile(filePath string, data []byte) File {
	return File{path: filePath, data: append([]byte(nil), data...)}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
