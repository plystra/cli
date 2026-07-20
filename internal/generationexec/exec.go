// Package generationexec compiles and runs trusted plugin generation packages
// in bounded helper processes for reliability isolation. It is not a security
// sandbox.
package generationexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/modulelocate"
	"github.com/plystra/cli/internal/modulepath"
	"golang.org/x/mod/module"
)

const (
	defaultCompileTimeout   = 2 * time.Minute
	defaultExecutionTimeout = 30 * time.Second
	maximumRequestSize      = 64 << 20
	maximumResponseSize     = 2 << 20
	maximumCommandOutput    = 64 << 10
	maximumDiagnosticSize   = 4096
)

var (
	// ErrBuild reports failure to prepare a generation helper.
	ErrBuild = errors.New("build generation helper")
	// ErrUnsupportedAPI reports an execution request for another API version.
	ErrUnsupportedAPI = errors.New("unsupported generation API")
	// ErrCompile reports a helper or extension compilation failure.
	ErrCompile = errors.New("compile generation helper")
	// ErrExecute reports failure while invoking a compiled helper.
	ErrExecute = errors.New("execute generation helper")
	// ErrContext reports a context that does not contain the declared extension
	// plugin with matching Go Module provenance.
	ErrContext = errors.New("generation helper context mismatch")
	// ErrExtension reports an error returned by the extension Generate function.
	ErrExtension = errors.New("generation extension returned an error")
	// ErrCrash reports a panic or abnormal helper-process exit.
	ErrCrash = errors.New("generation helper crashed")
	// ErrTimeout reports a compile or Generate deadline expiry.
	ErrTimeout = errors.New("generation helper timed out")
	// ErrRequestTooLarge reports a normalized context beyond the protocol bound.
	ErrRequestTooLarge = errors.New("generation request exceeds size limit")
	// ErrOutputTooLarge reports helper stdout beyond the protocol bound.
	ErrOutputTooLarge = errors.New("generation output exceeds size limit")
	// ErrMalformedOutput reports an invalid helper response envelope.
	ErrMalformedOutput = errors.New("malformed generation helper output")
	// ErrInvalidOutput reports structured output rejected by generation/v1.
	ErrInvalidOutput = errors.New("invalid generation extension output")
	// ErrClosed reports use of a helper after artifact cleanup.
	ErrClosed = errors.New("generation helper is closed")
	// ErrCleanup reports failure to remove temporary helper artifacts.
	ErrCleanup = errors.New("clean generation helper artifacts")
)

// Spec identifies one selected plugin generation package and its activation
// namespaces without exposing local paths to the extension.
type Spec struct {
	PluginID   string
	API        string
	ModulePath string
	PluginPath string
	Package    string
	Namespaces []string
}

// BuildOptions controls local helper compilation and execution bounds.
type BuildOptions struct {
	ModuleRoot       string
	GoCommand        string
	BuildEnvironment []string
	CompileTimeout   time.Duration
	ExecutionTimeout time.Duration
	TemporaryParent  string
}

type normalizedSpec struct {
	pluginID    generation.PluginID
	api         string
	modulePath  string
	pluginPath  string
	packagePath string
	importPath  string
	namespaces  []string
}

// Helper is one reusable compiled generation process image. Generate launches
// a fresh bounded process for each normalized context. Close removes every
// temporary source and binary artifact.
type Helper struct {
	mutex            sync.RWMutex
	closed           bool
	closeErr         error
	spec             normalizedSpec
	root             string
	workingDirectory string
	executable       string
	executionTimeout time.Duration
}

// Build validates spec, compiles its exported Generate function against the
// versioned API, removes transient source, and returns a reusable helper.
func Build(ctx context.Context, spec Spec, options BuildOptions) (_ *Helper, buildErr error) {
	normalized, err := normalizeSpec(spec)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBuild, err)
	}
	moduleRoot, err := validateModuleRoot(options.ModuleRoot, normalized.modulePath)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBuild, normalized.label(), err)
	}
	if err := validateGenerationDirectory(moduleRoot, normalized.pluginPath, normalized.packagePath); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBuild, normalized.label(), err)
	}
	temporaryRoot, err := os.MkdirTemp(options.TemporaryParent, ".plystra-generation-")
	if err != nil {
		return nil, fmt.Errorf("%w: %s: create temporary directory: %w", ErrBuild, normalized.label(), err)
	}
	keepArtifacts := false
	defer func() {
		if keepArtifacts {
			return
		}
		if err := removeArtifacts(temporaryRoot); err != nil {
			buildErr = errors.Join(buildErr, fmt.Errorf("%w: %s: %w", ErrCleanup, normalized.label(), err))
		}
	}()

	workingDirectory := filepath.Join(temporaryRoot, "work")
	if err := os.Mkdir(workingDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("%w: %s: create helper working directory: %w", ErrBuild, normalized.label(), err)
	}
	sourcePath := filepath.Join(temporaryRoot, "main.go")
	if err := writeExclusive(sourcePath, renderHelperSource(normalized.importPath)); err != nil {
		return nil, fmt.Errorf("%w: %s: write helper source: %w", ErrBuild, normalized.label(), err)
	}
	executablePath := filepath.Join(temporaryRoot, helperExecutableName())
	goCommand := options.GoCommand
	if goCommand == "" {
		goCommand = "go"
	}
	environment := options.BuildEnvironment
	if environment == nil {
		environment = os.Environ()
	}
	compileTimeout := options.CompileTimeout
	if compileTimeout <= 0 {
		compileTimeout = defaultCompileTimeout
	}
	compileContext, cancel := context.WithTimeout(ctx, compileTimeout)
	result := runCommand(
		compileContext,
		goCommand,
		[]string{"build", "-trimpath", "-buildvcs=false", "-o", executablePath, sourcePath},
		moduleRoot,
		environment,
		nil,
		maximumCommandOutput,
		maximumCommandOutput,
	)
	compileContextErr := compileContext.Err()
	cancel()
	if compileContextErr != nil {
		if errors.Is(compileContextErr, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %w: %w: %s", ErrBuild, ErrCompile, ErrTimeout, normalized.label())
		}
		return nil, fmt.Errorf("%w: %w: %s: %w", ErrBuild, ErrCompile, normalized.label(), compileContextErr)
	}
	if result.err != nil {
		diagnostic := sanitizeDiagnostic(commandOutput(result), moduleRoot, temporaryRoot)
		if diagnostic == "" {
			diagnostic = result.err.Error()
		}
		return nil, fmt.Errorf("%w: %w: %s: %s", ErrBuild, ErrCompile, normalized.label(), diagnostic)
	}
	if result.stdoutExceeded || result.stderrExceeded {
		return nil, fmt.Errorf("%w: %w: %s: compiler output exceeded %d bytes", ErrBuild, ErrCompile, normalized.label(), maximumCommandOutput)
	}
	info, err := os.Lstat(executablePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %w: %s: compiler did not create a regular helper executable", ErrBuild, ErrCompile, normalized.label())
	}
	if err := os.Remove(sourcePath); err != nil {
		return nil, fmt.Errorf("%w: %s: remove transient helper source: %w", ErrBuild, normalized.label(), err)
	}
	executionTimeout := options.ExecutionTimeout
	if executionTimeout <= 0 {
		executionTimeout = defaultExecutionTimeout
	}
	helper := &Helper{
		spec:             normalized,
		root:             temporaryRoot,
		workingDirectory: workingDirectory,
		executable:       executablePath,
		executionTimeout: executionTimeout,
	}
	keepArtifacts = true
	return helper, nil
}

// Generate invokes the trusted extension with one bounded, immutable context
// and validates its result before returning it to resolution or generation.
func (h *Helper) Generate(ctx context.Context, generationContext generation.Context) (output generation.NormalizedOutput, generateErr error) {
	if h == nil {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: nil helper", ErrExecute)
	}
	h.mutex.RLock()
	defer h.mutex.RUnlock()
	if h.closed {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s", ErrExecute, ErrClosed, h.spec.label())
	}
	plugin, exists := generationContext.Plugin(h.spec.pluginID)
	if !exists {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: extension plugin is not selected", ErrExecute, ErrContext, h.spec.label())
	}
	if modulePath := plugin.Module().Path(); modulePath != h.spec.modulePath {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: context module %q differs from declared module %q", ErrExecute, ErrContext, h.spec.label(), modulePath, h.spec.modulePath)
	}
	request := helperRequest{
		API:           h.spec.api,
		ContextDigest: generationContext.Digest(),
		Context:       inputFromContext(generationContext),
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %s: encode request: %w", ErrExecute, h.spec.label(), err)
	}
	if len(payload) > maximumRequestSize {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %d bytes exceeds %d", ErrExecute, ErrRequestTooLarge, h.spec.label(), len(payload), maximumRequestSize)
	}
	runDirectory, err := os.MkdirTemp(h.workingDirectory, "run-")
	if err != nil {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %s: create isolated working directory: %w", ErrExecute, h.spec.label(), err)
	}
	defer func() {
		if err := removeArtifacts(runDirectory); err != nil {
			output = generation.NormalizedOutput{}
			generateErr = errors.Join(generateErr, fmt.Errorf("%w: %s: %w", ErrCleanup, h.spec.label(), err))
		}
	}()
	executionContext, cancel := context.WithTimeout(ctx, h.executionTimeout)
	result := runCommand(
		executionContext,
		h.executable,
		nil,
		runDirectory,
		runtimeEnvironment(),
		payload,
		maximumResponseSize,
		maximumCommandOutput,
	)
	executionContextErr := executionContext.Err()
	cancel()
	if executionContextErr != nil {
		if errors.Is(executionContextErr, context.DeadlineExceeded) {
			return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s", ErrExecute, ErrTimeout, h.spec.label())
		}
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %s: %w", ErrExecute, h.spec.label(), executionContextErr)
	}
	if result.stdoutExceeded {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: stdout exceeds %d bytes", ErrExecute, ErrOutputTooLarge, h.spec.label(), maximumResponseSize)
	}
	if result.err != nil {
		diagnostic := sanitizeDiagnostic(commandOutput(result), h.root)
		if diagnostic == "" {
			diagnostic = result.err.Error()
		}
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %s", ErrExecute, ErrCrash, h.spec.label(), diagnostic)
	}
	if result.stderrExceeded {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: stderr exceeds %d bytes", ErrExecute, ErrMalformedOutput, h.spec.label(), maximumCommandOutput)
	}
	if len(bytes.TrimSpace(result.stderr)) != 0 {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: unexpected stderr: %s", ErrExecute, ErrMalformedOutput, h.spec.label(), sanitizeDiagnostic(string(result.stderr), h.root))
	}
	response, err := decodeResponse(result.stdout)
	if err != nil {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %v", ErrExecute, ErrMalformedOutput, h.spec.label(), err)
	}
	if response.API != h.spec.api {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: response API %q differs from %q", ErrExecute, ErrMalformedOutput, h.spec.label(), response.API, h.spec.api)
	}
	if err := validateResponseShape(response); err != nil {
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %v", ErrExecute, ErrMalformedOutput, h.spec.label(), err)
	}
	switch response.Status {
	case "success":
		normalized, err := generation.NormalizeOutput(generationContext, response.Output)
		if err != nil {
			return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %w", ErrExecute, ErrInvalidOutput, h.spec.label(), err)
		}
		return normalized, nil
	case "extension-error":
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %s", ErrExecute, ErrExtension, h.spec.label(), responseDiagnostic(response.Error, h.root))
	case "panic":
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %s", ErrExecute, ErrCrash, h.spec.label(), responseDiagnostic(response.Error, h.root))
	case "invalid-output":
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %s", ErrExecute, ErrInvalidOutput, h.spec.label(), responseDiagnostic(response.Error, h.root))
	case "protocol-error":
		return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: %s", ErrExecute, ErrMalformedOutput, h.spec.label(), responseDiagnostic(response.Error, h.root))
	}
	return generation.NormalizedOutput{}, fmt.Errorf("%w: %w: %s: unsupported response status %q", ErrExecute, ErrMalformedOutput, h.spec.label(), response.Status)
}

// Close waits for active Generate calls and removes all helper artifacts.
func (h *Helper) Close() error {
	if h == nil {
		return nil
	}
	h.mutex.Lock()
	defer h.mutex.Unlock()
	if h.closed {
		return h.closeErr
	}
	h.closed = true
	if err := removeArtifacts(h.root); err != nil {
		h.closeErr = fmt.Errorf("%w: %s: %w", ErrCleanup, h.spec.label(), err)
	}
	return h.closeErr
}

type helperRequest struct {
	API           string           `json:"api"`
	ContextDigest string           `json:"context_digest"`
	Context       generation.Input `json:"context"`
}

type helperResponse struct {
	API    string            `json:"api"`
	Status string            `json:"status"`
	Output generation.Output `json:"output"`
	Error  string            `json:"error,omitempty"`
}

func normalizeSpec(spec Spec) (normalizedSpec, error) {
	pluginID, err := generation.ParsePluginID(spec.PluginID)
	if err != nil {
		return normalizedSpec{}, fmt.Errorf("plugin ID %q is not canonical: %w", spec.PluginID, err)
	}
	if spec.API != generation.Version {
		return normalizedSpec{}, fmt.Errorf("%w: plugin %q generation api %q is not supported; supported API is %q", ErrUnsupportedAPI, spec.PluginID, spec.API, generation.Version)
	}
	if err := modulepath.CheckProject(spec.ModulePath); err != nil {
		return normalizedSpec{}, fmt.Errorf("plugin %q module path %q is not canonical: %w", spec.PluginID, spec.ModulePath, err)
	}
	if !validRelativeSlashPath(spec.PluginPath) {
		return normalizedSpec{}, fmt.Errorf("plugin %q module-relative path %q is not canonical", spec.PluginID, spec.PluginPath)
	}
	if !strings.HasPrefix(spec.Package, "./") || !validRelativeSlashPath(strings.TrimPrefix(spec.Package, "./")) {
		return normalizedSpec{}, fmt.Errorf("plugin %q generation package %q is not canonical", spec.PluginID, spec.Package)
	}
	packagePath := path.Join(spec.PluginPath, strings.TrimPrefix(spec.Package, "./"))
	importPath := spec.ModulePath + "/" + packagePath
	if err := module.CheckImportPath(importPath); err != nil {
		return normalizedSpec{}, fmt.Errorf("plugin %q generation import %q is not canonical: %w", spec.PluginID, importPath, err)
	}
	namespaces := append([]string(nil), spec.Namespaces...)
	if len(namespaces) == 0 {
		return normalizedSpec{}, fmt.Errorf("plugin %q generation extension has no activation namespaces", spec.PluginID)
	}
	for _, namespace := range namespaces {
		if !validNamespace(namespace) {
			return normalizedSpec{}, fmt.Errorf("plugin %q generation namespace %q is not canonical lower kebab case", spec.PluginID, namespace)
		}
	}
	sort.Strings(namespaces)
	for index := 1; index < len(namespaces); index++ {
		if namespaces[index] == namespaces[index-1] {
			return normalizedSpec{}, fmt.Errorf("plugin %q generation namespace %q is duplicated", spec.PluginID, namespaces[index])
		}
	}
	return normalizedSpec{
		pluginID:    pluginID,
		api:         spec.API,
		modulePath:  spec.ModulePath,
		pluginPath:  spec.PluginPath,
		packagePath: spec.Package,
		importPath:  importPath,
		namespaces:  namespaces,
	}, nil
}

func (s normalizedSpec) label() string {
	return fmt.Sprintf("plugin %q generation api %q package %q namespaces [%s]", s.pluginID.String(), s.api, s.packagePath, strings.Join(s.namespaces, ", "))
}

func validateModuleRoot(value, expectedModulePath string) (string, error) {
	if value == "" {
		return "", errors.New("module root is required")
	}
	moduleRoot, err := modulelocate.Find(value)
	if err != nil {
		return "", fmt.Errorf("locate module root: %w", err)
	}
	requestedInfo, err := os.Stat(value)
	if err != nil {
		return "", fmt.Errorf("inspect requested module root: %w", err)
	}
	rootInfo, err := os.Stat(moduleRoot.Path())
	if err != nil {
		return "", fmt.Errorf("inspect located module root: %w", err)
	}
	if !requestedInfo.IsDir() || !rootInfo.IsDir() || !os.SameFile(requestedInfo, rootInfo) {
		return "", errors.New("module root is not a directory")
	}
	if moduleRoot.ModulePath() != expectedModulePath {
		return "", fmt.Errorf("declared module path %q differs from go.mod module path %q", expectedModulePath, moduleRoot.ModulePath())
	}
	return moduleRoot.Path(), nil
}

func validateGenerationDirectory(moduleRoot, pluginPath, packagePath string) error {
	relativePackage := path.Join(pluginPath, strings.TrimPrefix(packagePath, "./"))
	components := strings.Split(relativePackage, "/")
	for index := range components {
		componentPath := filepath.Join(append([]string{moduleRoot}, components[:index+1]...)...)
		info, err := os.Lstat(componentPath)
		if err != nil {
			return fmt.Errorf("inspect generation package component %s: %w", path.Join(components[:index+1]...), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("generation package resolves through symbolic component %s", path.Join(components[:index+1]...))
		}
		if !info.IsDir() {
			return fmt.Errorf("generation package resolves through non-directory component %s", path.Join(components[:index+1]...))
		}
	}
	return nil
}

func validRelativeSlashPath(value string) bool {
	return value != "" && !path.IsAbs(value) && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../") && !strings.Contains(value, "\\")
}

func validNamespace(value string) bool {
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	previousHyphen := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			previousHyphen = false
		case character == '-' && !previousHyphen:
			previousHyphen = true
		default:
			return false
		}
	}
	return !previousHyphen
}

func writeExclusive(name string, data []byte) error {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	return errors.Join(writeErr, closeErr)
}

func removeArtifacts(root string) error {
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	for {
		if err := os.RemoveAll(root); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			return lastErr
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func helperExecutableName() string {
	if runtime.GOOS == "windows" {
		return "helper.exe"
	}
	return "helper"
}

func runtimeEnvironment() []string {
	return []string{
		"LANG=C",
		"LC_ALL=C",
		"SOURCE_DATE_EPOCH=0",
		"TZ=UTC",
	}
}

func inputFromContext(context generation.Context) generation.Input {
	var configurationProvenance *generation.ConfigurationProvenanceInput
	if provenance, exists := context.ConfigurationProvenance(); exists {
		configurationProvenance = &generation.ConfigurationProvenanceInput{
			Mode:                        provenance.Mode(),
			Environment:                 provenance.Environment(),
			RootPath:                    provenance.RootPath(),
			RootDigest:                  provenance.RootDigest(),
			SelectedPath:                provenance.SelectedPath(),
			SelectedDigest:              provenance.SelectedDigest(),
			DependencyCompositionDigest: provenance.DependencyCompositionDigest(),
		}
	}
	pluginViews := context.Plugins()
	plugins := make([]generation.PluginInput, len(pluginViews))
	for index, plugin := range pluginViews {
		moduleView := plugin.Module()
		plugins[index] = generation.PluginInput{
			ID:                plugin.ID().String(),
			ModulePath:        moduleView.Path(),
			ModuleVersion:     moduleView.Version(),
			Provides:          capabilityStrings(plugin.Provides()),
			Requires:          capabilityStrings(plugin.Requires()),
			BuildMetadataJSON: json.RawMessage(plugin.BuildMetadataJSON()),
		}
	}
	capabilityViews := context.Capabilities()
	capabilities := make([]generation.CapabilityInput, len(capabilityViews))
	for index, capability := range capabilityViews {
		capabilities[index] = generation.CapabilityInput{
			ContractJSON: json.RawMessage(capability.ContractJSON()),
			Sources:      capability.Sources(),
			Intrinsic:    capability.Intrinsic(),
			Exposure:     capability.Exposure(),
		}
	}
	providerViews := context.Providers()
	providers := make([]generation.ProviderInput, len(providerViews))
	for index, provider := range providerViews {
		providers[index] = generation.ProviderInput{
			Capability: provider.Capability().String(),
			Plugin:     provider.Plugin().String(),
		}
	}
	aliasViews := context.CapabilityAliases()
	aliases := make([]generation.CapabilityAliasInput, len(aliasViews))
	for index, alias := range aliasViews {
		sourceViews := alias.Sources()
		sources := make([]generation.AliasSourceInput, len(sourceViews))
		for sourceIndex, source := range sourceViews {
			sources[sourceIndex] = generation.AliasSourceInput{Kind: source.Kind(), ID: source.ID()}
		}
		aliases[index] = generation.CapabilityAliasInput{
			ID:         alias.ID().String(),
			Target:     alias.Target().String(),
			Exposure:   alias.Exposure(),
			Deprecated: alias.Deprecated(),
			Sources:    sources,
		}
	}
	return generation.Input{
		ConfigurationProvenance: configurationProvenance,
		Plugins:                 plugins,
		Capabilities:            capabilities,
		Requirements:            capabilityStrings(context.Requirements()),
		Providers:               providers,
		CapabilityAliases:       aliases,
	}
}

func capabilityStrings(values []generation.CapabilityID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func decodeResponse(data []byte) (helperResponse, error) {
	if err := validateSingleJSONValue(data); err != nil {
		return helperResponse{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var response helperResponse
	if err := decoder.Decode(&response); err != nil {
		return helperResponse{}, fmt.Errorf("decode response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return helperResponse{}, errors.New("response contains multiple JSON values")
		}
		return helperResponse{}, fmt.Errorf("decode trailing response: %w", err)
	}
	return response, nil
}

func validateResponseShape(response helperResponse) error {
	switch response.Status {
	case "success":
		if response.Error != "" {
			return errors.New("successful response contains an error")
		}
		return nil
	case "extension-error", "panic", "invalid-output", "protocol-error":
		if response.Error == "" {
			return fmt.Errorf("%s response contains no error", response.Status)
		}
		if len(response.Output.Requirements) != 0 || len(response.Output.Diagnostics) != 0 || len(response.Output.Contributions) != 0 {
			return fmt.Errorf("%s response contains output", response.Status)
		}
		return nil
	default:
		return fmt.Errorf("unsupported response status %q", response.Status)
	}
}

func commandOutput(result commandResult) string {
	parts := make([]string, 0, 2)
	if len(bytes.TrimSpace(result.stdout)) != 0 {
		parts = append(parts, string(result.stdout))
	}
	if len(bytes.TrimSpace(result.stderr)) != 0 {
		parts = append(parts, string(result.stderr))
	}
	return strings.Join(parts, "\n")
}

func responseDiagnostic(value string, privatePaths ...string) string {
	if value == "" {
		return "extension did not provide an error message"
	}
	if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return "extension returned an invalid error message"
	}
	return sanitizeDiagnostic(value, privatePaths...)
}

func sanitizeDiagnostic(value string, privatePaths ...string) string {
	message := gocommand.SanitizeOutput(value, privatePaths...)
	if len(message) > maximumDiagnosticSize {
		message = message[:maximumDiagnosticSize] + "..."
	}
	return message
}
