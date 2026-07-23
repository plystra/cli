// Package implementationassemblygen renders the static constructor and
// governed Interface assembly for one frozen application model.
package implementationassemblygen

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"go/format"
	"go/token"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/goname"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/modulepath"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
	kernelinvocation "github.com/plystra/kernel/invocation"
	"golang.org/x/mod/module"
)

const (
	// Path is the Project-relative static Implementation assembly source path.
	Path = "generated/go/assembly/interfaces_gen.go"

	kernelModulePath     = "github.com/plystra/kernel"
	kernelIntrinsicPath  = "github.com/plystra/kernel/intrinsic"
	kernelInvocationPath = "github.com/plystra/kernel/invocation"
	kernelLifecyclePath  = "github.com/plystra/kernel/lifecycle"
)

var (
	// ErrRender reports invalid frozen assembly input or a generated source
	// formatting failure.
	ErrRender = errors.New("render static Implementation assembly")
	// ErrInvalidInput reports incomplete or contradictory frozen assembly input.
	ErrInvalidInput = errors.New("invalid static Implementation assembly input")
	// ErrConstructorGraph reports a missing, repeated, or cyclic constructor
	// dependency edge.
	ErrConstructorGraph = errors.New("invalid static constructor graph")
)

// SelectionReason is the closed CLI-time reason for one ordinary Interface
// binding. It mirrors the Kernel provenance values without performing runtime
// selection.
type SelectionReason string

const (
	SelectionExplicit         SelectionReason = "explicit"
	SelectionUniqueCompatible SelectionReason = "unique-compatible"
)

// BindingInput is one exact reachable Interface-to-constructor binding.
type BindingInput struct {
	InterfaceID     interfaceid.Identifier
	PackagePath     string
	Constructor     constructorsymbol.Symbol
	SelectionReason SelectionReason
	ContractDigest  [sha256.Size]byte
}

// DependencyInput is one parameter-ordered required or optional constructor
// dependency. An unavailable optional dependency has no binding.
type DependencyInput struct {
	InterfaceID       interfaceid.Identifier
	PackagePath       string
	ParameterName     string
	ParameterPosition int
	Optional          bool
	Available         bool
}

// ConstructorInput is one reachable constructor and its exact Go Module and
// parameter provenance.
type ConstructorInput struct {
	Symbol           constructorsymbol.Symbol
	ModulePath       string
	ModuleVersion    string
	HasConfiguration bool
	Dependencies     []DependencyInput
}

// Options is the complete frozen input needed to emit one static assembly.
type Options struct {
	ModulePath               string
	ApplicationBuildIdentity string
	KernelModuleVersion      string
	KernelBuildIdentity      string
	DefaultTimeout           time.Duration
	Bindings                 []BindingInput
	Constructors             []ConstructorInput
}

// File is one immutable normalized generated assembly source file.
type File struct {
	data         []byte
	bindings     []BindingInput
	constructors []ConstructorInput
}

// Path returns the canonical Project-relative output path.
func (File) Path() string { return Path }

// Data returns defensive gofmt-formatted generated source.
func (f File) Data() []byte { return append([]byte(nil), f.data...) }

// Bindings returns the normalized Interface-ID-ordered binding plan.
func (f File) Bindings() []BindingInput { return append([]BindingInput(nil), f.bindings...) }

// Constructors returns the normalized dependency-first constructor plan.
func (f File) Constructors() []ConstructorInput { return cloneConstructors(f.constructors) }

type plan struct {
	options      Options
	bindings     []BindingInput
	constructors []ConstructorInput
	bindingByID  map[string]BindingInput
	contractPath map[string]string
	accessors    []string
	imports      map[string]string
}

// Render validates, canonicalizes, and renders one static constructor
// assembly. Input order never creates selection or construction priority.
func Render(options Options) (File, error) {
	planned, err := planAssembly(options)
	if err != nil {
		return File{}, fmt.Errorf("%w: %w", ErrRender, err)
	}
	data, err := render(planned)
	if err != nil {
		return File{}, fmt.Errorf("%w: %v", ErrRender, err)
	}
	return File{
		data:         data,
		bindings:     append([]BindingInput(nil), planned.bindings...),
		constructors: cloneConstructors(planned.constructors),
	}, nil
}

func planAssembly(options Options) (plan, error) {
	if err := modulepath.CheckProject(options.ModulePath); err != nil {
		return plan{}, fmt.Errorf("%w: application Go Module path %q: %v", ErrInvalidInput, options.ModulePath, err)
	}
	if options.DefaultTimeout <= 0 {
		return plan{}, fmt.Errorf("%w: default invocation timeout must be positive", ErrInvalidInput)
	}
	if _, err := kernelinvocation.NewModuleBuild(kernelModulePath, options.KernelModuleVersion, options.KernelBuildIdentity); err != nil {
		return plan{}, fmt.Errorf("%w: Kernel build provenance: %v", ErrInvalidInput, err)
	}

	bindings := append([]BindingInput(nil), options.Bindings...)
	slices.SortFunc(bindings, func(left, right BindingInput) int {
		return strings.Compare(left.InterfaceID.String(), right.InterfaceID.String())
	})
	bindingByID := make(map[string]BindingInput, len(bindings))
	contractPath := make(map[string]string, len(bindings))
	accessors := make([]string, len(bindings))
	accessorOwners := make(map[string]string, len(bindings))
	for index, binding := range bindings {
		identifier := binding.InterfaceID.String()
		if identifier == "" || module.CheckImportPath(binding.PackagePath) != nil || binding.Constructor.String() == "" || binding.ContractDigest == [sha256.Size]byte{} {
			return plan{}, fmt.Errorf("%w: binding %d is incomplete", ErrInvalidInput, index)
		}
		if binding.SelectionReason != SelectionExplicit && binding.SelectionReason != SelectionUniqueCompatible {
			return plan{}, fmt.Errorf("%w: Interface %s has selection reason %q", ErrInvalidInput, identifier, binding.SelectionReason)
		}
		if _, duplicate := bindingByID[identifier]; duplicate {
			return plan{}, fmt.Errorf("%w: duplicate Interface binding %s", ErrInvalidInput, identifier)
		}
		bindingByID[identifier] = binding
		contractPath[identifier] = binding.PackagePath
		accessor := interfaceAccessor(binding.InterfaceID)
		if owner, collision := accessorOwners[accessor]; collision {
			return plan{}, fmt.Errorf("%w: Interfaces %s and %s produce accessor %s", ErrInvalidInput, owner, identifier, accessor)
		}
		accessorOwners[accessor] = identifier
		accessors[index] = accessor
	}

	constructors := cloneConstructors(options.Constructors)
	slices.SortFunc(constructors, func(left, right ConstructorInput) int {
		return strings.Compare(left.Symbol.String(), right.Symbol.String())
	})
	constructorBySymbol := make(map[string]ConstructorInput, len(constructors))
	for index := range constructors {
		constructor := &constructors[index]
		symbol := constructor.Symbol.String()
		if symbol == "" || constructor.ModulePath == "" {
			return plan{}, fmt.Errorf("%w: constructor %d is incomplete", ErrInvalidInput, index)
		}
		if _, duplicate := constructorBySymbol[symbol]; duplicate {
			return plan{}, fmt.Errorf("%w: duplicate constructor %s", ErrConstructorGraph, symbol)
		}
		if constructor.Symbol.PackagePath() != constructor.ModulePath && !strings.HasPrefix(constructor.Symbol.PackagePath(), constructor.ModulePath+"/") {
			return plan{}, fmt.Errorf("%w: constructor %s is outside module %s", ErrInvalidInput, symbol, constructor.ModulePath)
		}
		if _, err := kernelinvocation.NewModuleBuild(constructor.ModulePath, constructor.ModuleVersion, options.ApplicationBuildIdentity); err != nil {
			return plan{}, fmt.Errorf("%w: constructor %s module provenance: %v", ErrInvalidInput, symbol, err)
		}
		constructor.Dependencies = append([]DependencyInput(nil), constructor.Dependencies...)
		expectedPosition := 1
		if constructor.HasConfiguration {
			expectedPosition = 2
		}
		for dependencyIndex, dependency := range constructor.Dependencies {
			identifier := dependency.InterfaceID.String()
			if identifier == "" || module.CheckImportPath(dependency.PackagePath) != nil || dependency.ParameterPosition != expectedPosition+dependencyIndex {
				return plan{}, fmt.Errorf("%w: constructor %s dependency %d is incomplete or out of parameter order", ErrConstructorGraph, symbol, dependencyIndex)
			}
			if !dependency.Available && !dependency.Optional {
				return plan{}, fmt.Errorf("%w: constructor %s required Interface %s is unavailable", ErrConstructorGraph, symbol, identifier)
			}
			if existing, known := contractPath[identifier]; known && existing != dependency.PackagePath {
				return plan{}, fmt.Errorf("%w: Interface %s uses both %s and %s", ErrConstructorGraph, identifier, existing, dependency.PackagePath)
			}
			contractPath[identifier] = dependency.PackagePath
			_, bound := bindingByID[identifier]
			if dependency.Available != bound {
				return plan{}, fmt.Errorf("%w: constructor %s Interface %s availability contradicts the binding plan", ErrConstructorGraph, symbol, identifier)
			}
		}
		constructorBySymbol[symbol] = *constructor
	}
	for _, binding := range bindings {
		if _, exists := constructorBySymbol[binding.Constructor.String()]; !exists {
			return plan{}, fmt.Errorf("%w: Interface %s selects absent constructor %s", ErrConstructorGraph, binding.InterfaceID, binding.Constructor)
		}
	}
	for symbol := range constructorBySymbol {
		used := false
		for _, binding := range bindings {
			if binding.Constructor.String() == symbol {
				used = true
				break
			}
		}
		if !used {
			return plan{}, fmt.Errorf("%w: constructor %s has no reachable Interface binding", ErrConstructorGraph, symbol)
		}
	}

	ordered, err := orderConstructors(constructors, bindingByID)
	if err != nil {
		return plan{}, err
	}
	imports := planImports(options.ModulePath, bindings, ordered, contractPath)
	return plan{
		options:      options,
		bindings:     bindings,
		constructors: ordered,
		bindingByID:  bindingByID,
		contractPath: contractPath,
		accessors:    accessors,
		imports:      imports,
	}, nil
}

func orderConstructors(constructors []ConstructorInput, bindings map[string]BindingInput) ([]ConstructorInput, error) {
	bySymbol := make(map[string]ConstructorInput, len(constructors))
	for _, constructor := range constructors {
		bySymbol[constructor.Symbol.String()] = constructor
	}
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(constructors))
	ordered := make([]ConstructorInput, 0, len(constructors))
	stack := make([]string, 0, len(constructors))
	var visit func(string) error
	visit = func(symbol string) error {
		switch state[symbol] {
		case visiting:
			cycle := append(append([]string(nil), stack...), symbol)
			return fmt.Errorf("%w: constructor cycle %s", ErrConstructorGraph, strings.Join(cycle, " -> "))
		case visited:
			return nil
		}
		constructor, exists := bySymbol[symbol]
		if !exists {
			return fmt.Errorf("%w: absent constructor %s", ErrConstructorGraph, symbol)
		}
		state[symbol] = visiting
		stack = append(stack, symbol)
		dependencies := make([]string, 0, len(constructor.Dependencies))
		for _, dependency := range constructor.Dependencies {
			if !dependency.Available {
				continue
			}
			dependencies = append(dependencies, bindings[dependency.InterfaceID.String()].Constructor.String())
		}
		slices.Sort(dependencies)
		dependencies = slices.Compact(dependencies)
		for _, dependency := range dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		state[symbol] = visited
		ordered = append(ordered, constructor)
		return nil
	}
	for _, constructor := range constructors {
		if err := visit(constructor.Symbol.String()); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}

func planImports(modulePath string, bindings []BindingInput, constructors []ConstructorInput, contracts map[string]string) map[string]string {
	paths := make([]string, 0, len(bindings)*3+len(constructors)+len(contracts))
	for _, packagePath := range contracts {
		paths = append(paths, packagePath)
	}
	for _, constructor := range constructors {
		paths = append(paths, constructor.Symbol.PackagePath())
	}
	for _, binding := range bindings {
		paths = append(paths, adapterPath(modulePath, binding.InterfaceID), proxyPath(modulePath, binding.InterfaceID))
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	result := make(map[string]string, len(paths))
	for index, packagePath := range paths {
		result[packagePath] = fmt.Sprintf("package%d", index)
	}
	return result
}

func render(planned plan) ([]byte, error) {
	var source strings.Builder
	fmt.Fprintln(&source, "// Code generated by Plystra CLI. DO NOT EDIT.")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "package assembly")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "import (")
	fmt.Fprintln(&source, "\t\"context\"")
	fmt.Fprintln(&source, "\t\"errors\"")
	fmt.Fprintln(&source, "\t\"fmt\"")
	fmt.Fprintln(&source, "\t\"log/slog\"")
	fmt.Fprintln(&source, "\t\"time\"")
	fmt.Fprintln(&source)
	paths := make([]string, 0, len(planned.imports))
	for packagePath := range planned.imports {
		paths = append(paths, packagePath)
	}
	slices.Sort(paths)
	for _, packagePath := range paths {
		fmt.Fprintf(&source, "\t%s %s\n", planned.imports[packagePath], strconv.Quote(packagePath))
	}
	if hasOptionalDependency(planned.constructors) {
		fmt.Fprintf(&source, "\tplystra %s\n", strconv.Quote(kernelModulePath))
	}
	fmt.Fprintf(&source, "\tkernelintrinsic %s\n", strconv.Quote(kernelIntrinsicPath))
	fmt.Fprintf(&source, "\tkernelinvocation %s\n", strconv.Quote(kernelInvocationPath))
	fmt.Fprintf(&source, "\tkernellifecycle %s\n", strconv.Quote(kernelLifecyclePath))
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "const defaultInterfaceInvocationTimeout = time.Duration(%d)\n", planned.options.DefaultTimeout)
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "var (")
	fmt.Fprintln(&source, "\t// ErrInterfaceAssembly reports a safe static constructor or binding failure.")
	fmt.Fprintln(&source, "\tErrInterfaceAssembly = errors.New(\"assemble static Interface runtime\")")
	fmt.Fprintln(&source, "\t// ErrInvalidInterfaceRuntime reports an absent or incomplete static Interface runtime.")
	fmt.Fprintln(&source, "\tErrInvalidInterfaceRuntime = errors.New(\"invalid static Interface runtime\")")
	fmt.Fprintln(&source, "\t// ErrInterfaceStart reports a safe static Implementation startup failure.")
	fmt.Fprintln(&source, "\tErrInterfaceStart = errors.New(\"start static Interface runtime\")")
	fmt.Fprintln(&source, "\t// ErrInterfaceStop reports a safe static Implementation shutdown failure.")
	fmt.Fprintln(&source, "\tErrInterfaceStop = errors.New(\"stop static Interface runtime\")")
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// ConstructorConfiguration supplies already validated typed Config values to selected constructors.")
	fmt.Fprintln(&source, "type ConstructorConfiguration struct {")
	for index, constructor := range planned.constructors {
		if !constructor.HasConfiguration {
			continue
		}
		fmt.Fprintf(&source, "\t// Config%d belongs to %s.\n", index, constructor.Symbol)
		fmt.Fprintf(&source, "\tConfig%d %s.Config\n", index, planned.imports[constructor.Symbol.PackagePath()])
	}
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// InterfaceRuntime owns one frozen catalog and the typed proxies backed by it.")
	fmt.Fprintln(&source, "type InterfaceRuntime struct {")
	fmt.Fprintln(&source, "\tcatalog    kernelinvocation.Catalog")
	fmt.Fprintln(&source, "\tdispatcher *kernelinvocation.Dispatcher")
	fmt.Fprintln(&source, "\tlifecycle  *kernellifecycle.Manager")
	for index, binding := range planned.bindings {
		fmt.Fprintf(&source, "\tinterface%d %s.Interface\n", index, planned.imports[binding.PackagePath])
	}
	fmt.Fprintln(&source, "\tinitialized bool")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Valid reports whether the complete immutable catalog is live.")
	fmt.Fprintln(&source, "func (runtime InterfaceRuntime) Valid() bool {")
	fmt.Fprintf(&source, "\treturn runtime.initialized && runtime.dispatcher != nil && runtime.dispatcher.Published() && runtime.lifecycle != nil && runtime.lifecycle.State().Valid() && len(runtime.catalog.Bindings()) == %d\n", len(kernelintrinsic.InterfaceDefinitions())+len(planned.bindings))
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// State returns the current static Implementation lifecycle state.")
	fmt.Fprintln(&source, "func (runtime InterfaceRuntime) State() kernellifecycle.State {")
	fmt.Fprintln(&source, "\tif !runtime.Valid() {")
	fmt.Fprintln(&source, "\t\treturn \"\"")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\treturn runtime.lifecycle.State()")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Start starts lifecycle-aware Implementations in constructor dependency order.")
	fmt.Fprintln(&source, "func (runtime InterfaceRuntime) Start(ctx context.Context) error {")
	fmt.Fprintln(&source, "\tif !runtime.Valid() {")
	fmt.Fprintln(&source, "\t\treturn fmt.Errorf(\"%w: %w\", ErrInterfaceStart, ErrInvalidInterfaceRuntime)")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\tif err := runtime.lifecycle.Start(ctx); err != nil {")
	fmt.Fprintln(&source, "\t\treturn fmt.Errorf(\"%w: %w\", ErrInterfaceStart, err)")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\treturn nil")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Stop stops active lifecycle-aware Implementations in reverse dependency order.")
	fmt.Fprintln(&source, "func (runtime InterfaceRuntime) Stop(ctx context.Context) error {")
	fmt.Fprintln(&source, "\tif !runtime.Valid() {")
	fmt.Fprintln(&source, "\t\treturn fmt.Errorf(\"%w: %w\", ErrInterfaceStop, ErrInvalidInterfaceRuntime)")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\tif err := runtime.lifecycle.Stop(ctx); err != nil {")
	fmt.Fprintln(&source, "\t\treturn fmt.Errorf(\"%w: %w\", ErrInterfaceStop, err)")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\treturn nil")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// Catalog returns the frozen exact Interface binding registry.")
	fmt.Fprintln(&source, "func (runtime InterfaceRuntime) Catalog() kernelinvocation.Catalog {")
	fmt.Fprintln(&source, "\tif !runtime.Valid() {")
	fmt.Fprintln(&source, "\t\treturn kernelinvocation.Catalog{}")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\treturn runtime.catalog")
	fmt.Fprintln(&source, "}")
	for index, binding := range planned.bindings {
		fmt.Fprintln(&source)
		fmt.Fprintf(&source, "// %s returns the governed typed Interface for %s.\n", planned.accessors[index], binding.InterfaceID)
		fmt.Fprintf(&source, "func (runtime InterfaceRuntime) %s() %s.Interface {\n", planned.accessors[index], planned.imports[binding.PackagePath])
		fmt.Fprintln(&source, "\tif !runtime.Valid() {")
		fmt.Fprintln(&source, "\t\treturn nil")
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintf(&source, "\treturn runtime.interface%d\n", index)
		fmt.Fprintln(&source, "}")
	}
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// String redacts runtime internals.")
	fmt.Fprintln(&source, "func (InterfaceRuntime) String() string { return \"<generated-interface-runtime>\" }")
	fmt.Fprintln(&source, "// GoString redacts runtime internals from Go-syntax formatting.")
	fmt.Fprintln(&source, "func (InterfaceRuntime) GoString() string { return \"<generated-interface-runtime>\" }")
	fmt.Fprintln(&source, "// Format redacts runtime internals for every fmt verb.")
	fmt.Fprintln(&source, "func (InterfaceRuntime) Format(state fmt.State, _ rune) {")
	fmt.Fprintln(&source, "\t_, _ = state.Write([]byte(\"<generated-interface-runtime>\"))")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source, "// LogValue redacts runtime internals for structured standard-library logging.")
	fmt.Fprintln(&source, "func (InterfaceRuntime) LogValue() slog.Value {")
	fmt.Fprintln(&source, "\treturn slog.StringValue(\"<generated-interface-runtime>\")")
	fmt.Fprintln(&source, "}")
	fmt.Fprintln(&source)
	fmt.Fprintln(&source, "// NewInterfaceRuntime constructs every selected Implementation once in dependency order, then publishes all bindings atomically.")
	fmt.Fprintln(&source, "func NewInterfaceRuntime(configuration ConstructorConfiguration, rollbackTimeout time.Duration) (InterfaceRuntime, error) {")
	fmt.Fprintln(&source, "\t_ = configuration")
	fmt.Fprintln(&source, "\tif err := RequireKernelCompatibility(); err != nil {")
	fmt.Fprintln(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%w: Kernel compatibility: %w\", ErrInterfaceAssembly, err)")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\tdispatcher, err := kernelinvocation.NewDispatcher(kernelinvocation.DispatcherOptions{DefaultTimeout: defaultInterfaceInvocationTimeout})")
	fmt.Fprintln(&source, "\tif err != nil {")
	fmt.Fprintln(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%w: governed dispatcher\", ErrInterfaceAssembly)")
	fmt.Fprintln(&source, "\t}")
	for index, binding := range planned.bindings {
		adapter := planned.imports[adapterPath(planned.options.ModulePath, binding.InterfaceID)]
		proxy := planned.imports[proxyPath(planned.options.ModulePath, binding.InterfaceID)]
		contract := planned.imports[binding.PackagePath]
		fmt.Fprintf(&source, "\thandle%d, err := kernelinvocation.NewHandle(dispatcher, %s.Contract(), true)\n", index, adapter)
		fmt.Fprintln(&source, "\tif err != nil {")
		fmt.Fprintf(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%%w: governed handle %s\", ErrInterfaceAssembly)\n", binding.InterfaceID)
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintf(&source, "\tinterface%d := %s.Interface(%s.New(handle%d))\n", index, contract, proxy, index)
	}
	constructorIndex := make(map[string]int, len(planned.constructors))
	for index, constructor := range planned.constructors {
		constructorIndex[constructor.Symbol.String()] = index
		implementation := planned.imports[constructor.Symbol.PackagePath()]
		fmt.Fprintf(&source, "\timplementation%d, constructorError := %s.%s(", index, implementation, constructor.Symbol.FunctionName())
		arguments := make([]string, 0, len(constructor.Dependencies)+1)
		if constructor.HasConfiguration {
			arguments = append(arguments, fmt.Sprintf("configuration.Config%d", index))
		}
		for _, dependency := range constructor.Dependencies {
			contract := planned.imports[dependency.PackagePath]
			if !dependency.Available {
				arguments = append(arguments, fmt.Sprintf("plystra.Optional[%s.Interface]{}", contract))
				continue
			}
			bindingIndex := bindingIndex(planned.bindings, dependency.InterfaceID)
			if dependency.Optional {
				arguments = append(arguments, fmt.Sprintf("plystra.NewOptional[%s.Interface](interface%d)", contract, bindingIndex))
			} else {
				arguments = append(arguments, fmt.Sprintf("interface%d", bindingIndex))
			}
		}
		fmt.Fprint(&source, strings.Join(arguments, ", "))
		fmt.Fprintln(&source, ")")
		fmt.Fprintln(&source, "\tif constructorError != nil {")
		fmt.Fprintf(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%%w: constructor %s failed\", ErrInterfaceAssembly)\n", constructor.Symbol)
		fmt.Fprintln(&source, "\t}")
	}
	fmt.Fprintf(&source, "\tlifecycleBindings := make([]kernellifecycle.Binding, 0, %d)\n", len(planned.constructors))
	for index, constructor := range planned.constructors {
		fmt.Fprintf(&source, "\tif instance, ok := any(implementation%d).(kernellifecycle.Instance); ok {\n", index)
		fmt.Fprintf(&source, "\t\tbinding, err := kernellifecycle.NewBinding(%s, instance)\n", strconv.Quote(constructor.Symbol.String()))
		fmt.Fprintln(&source, "\t\tif err != nil {")
		fmt.Fprintf(&source, "\t\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%%w: constructor %s lifecycle: %%w\", ErrInterfaceAssembly, err)\n", constructor.Symbol)
		fmt.Fprintln(&source, "\t\t}")
		fmt.Fprintln(&source, "\t\tlifecycleBindings = append(lifecycleBindings, binding)")
		fmt.Fprintln(&source, "\t}")
	}
	fmt.Fprintln(&source, "\tlifecycle, err := kernellifecycle.NewManager(kernellifecycle.ManagerOptions{RollbackTimeout: rollbackTimeout}, lifecycleBindings)")
	fmt.Fprintln(&source, "\tif err != nil {")
	fmt.Fprintln(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%w: implementation lifecycle: %w\", ErrInterfaceAssembly, err)")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\tbindings, err := kernelintrinsic.NewBindings(kernelintrinsic.BindingOptions{")
	fmt.Fprintf(&source, "\t\tModuleVersion: %s,\n", strconv.Quote(planned.options.KernelModuleVersion))
	fmt.Fprintf(&source, "\t\tBuildIdentity: %s,\n", strconv.Quote(planned.options.KernelBuildIdentity))
	fmt.Fprintln(&source, "\t})")
	fmt.Fprintln(&source, "\tif err != nil {")
	fmt.Fprintln(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%w: intrinsic bindings\", ErrInterfaceAssembly)")
	fmt.Fprintln(&source, "\t}")
	for index, constructor := range planned.constructors {
		fmt.Fprintf(&source, "\tmoduleBuild%d, err := kernelinvocation.NewModuleBuild(%s, %s, %s)\n", index, strconv.Quote(constructor.ModulePath), strconv.Quote(constructor.ModuleVersion), strconv.Quote(planned.options.ApplicationBuildIdentity))
		fmt.Fprintln(&source, "\tif err != nil {")
		fmt.Fprintf(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%%w: constructor %s module provenance\", ErrInterfaceAssembly)\n", constructor.Symbol)
		fmt.Fprintln(&source, "\t}")
	}
	for index, binding := range planned.bindings {
		adapter := planned.imports[adapterPath(planned.options.ModulePath, binding.InterfaceID)]
		implementationIndex := constructorIndex[binding.Constructor.String()]
		fmt.Fprintf(&source, "\tendpoint%d, err := %s.NewEndpoint(implementation%d)\n", index, adapter, implementationIndex)
		fmt.Fprintln(&source, "\tif err != nil {")
		fmt.Fprintf(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%%w: Implementation adapter %s\", ErrInterfaceAssembly)\n", binding.InterfaceID)
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintf(&source, "\tbinding%d, err := kernelinvocation.NewBinding(kernelinvocation.BindingOptions{\n", index)
		fmt.Fprintln(&source, "\t\tKind:            kernelinvocation.BindingKindImplementation,")
		fmt.Fprintf(&source, "\t\tConstructor:     %s,\n", strconv.Quote(binding.Constructor.String()))
		fmt.Fprintf(&source, "\t\tModuleBuild:     moduleBuild%d,\n", implementationIndex)
		fmt.Fprintf(&source, "\t\tSelectionReason: kernelinvocation.%s,\n", kernelSelectionName(binding.SelectionReason))
		fmt.Fprintf(&source, "\t\tContractDigest:  %s,\n", digestLiteral(binding.ContractDigest))
		fmt.Fprintf(&source, "\t}, endpoint%d)\n", index)
		fmt.Fprintln(&source, "\tif err != nil {")
		fmt.Fprintf(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%%w: exact binding %s\", ErrInterfaceAssembly)\n", binding.InterfaceID)
		fmt.Fprintln(&source, "\t}")
		fmt.Fprintf(&source, "\tbindings = append(bindings, binding%d)\n", index)
	}
	fmt.Fprintln(&source, "\tcatalog, err := kernelinvocation.NewCatalog(bindings)")
	fmt.Fprintln(&source, "\tif err != nil {")
	fmt.Fprintln(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%w: immutable catalog\", ErrInterfaceAssembly)")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\tif err := dispatcher.Publish(catalog); err != nil {")
	fmt.Fprintln(&source, "\t\treturn InterfaceRuntime{}, fmt.Errorf(\"%w: publish immutable catalog\", ErrInterfaceAssembly)")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\treturn InterfaceRuntime{")
	fmt.Fprintln(&source, "\t\tcatalog:     catalog,")
	fmt.Fprintln(&source, "\t\tdispatcher:  dispatcher,")
	fmt.Fprintln(&source, "\t\tlifecycle:   lifecycle,")
	for index := range planned.bindings {
		fmt.Fprintf(&source, "\t\tinterface%d:  interface%d,\n", index, index)
	}
	fmt.Fprintln(&source, "\t\tinitialized: true,")
	fmt.Fprintln(&source, "\t}, nil")
	fmt.Fprintln(&source, "}")

	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return nil, fmt.Errorf("format generated source: %v", err)
	}
	return formatted, nil
}

func interfaceAccessor(identifier interfaceid.Identifier) string {
	parts := strings.FieldsFunc(identifier.Name(), func(character rune) bool { return character == '.' || character == '-' })
	for index := range parts {
		parts[index] = goname.ExportedWord(parts[index])
	}
	accessor := strings.Join(parts, "") + "V" + strconv.FormatUint(identifier.Major(), 10)
	if !token.IsIdentifier(accessor) {
		return ""
	}
	return accessor
}

func adapterPath(modulePath string, identifier interfaceid.Identifier) string {
	segments := append([]string{modulePath, "generated", "go", "adapters", "implementations"}, strings.Split(identifier.Name(), ".")...)
	segments = append(segments, "v"+strconv.FormatUint(identifier.Major(), 10))
	return path.Join(segments...)
}

func proxyPath(modulePath string, identifier interfaceid.Identifier) string {
	segments := append([]string{modulePath, "generated", "go", "proxies"}, strings.Split(identifier.Name(), ".")...)
	segments = append(segments, "v"+strconv.FormatUint(identifier.Major(), 10))
	return path.Join(segments...)
}

func bindingIndex(bindings []BindingInput, identifier interfaceid.Identifier) int {
	index, found := slices.BinarySearchFunc(bindings, identifier.String(), func(binding BindingInput, value string) int {
		return strings.Compare(binding.InterfaceID.String(), value)
	})
	if !found {
		return -1
	}
	return index
}

func hasOptionalDependency(constructors []ConstructorInput) bool {
	for _, constructor := range constructors {
		for _, dependency := range constructor.Dependencies {
			if dependency.Optional {
				return true
			}
		}
	}
	return false
}

func kernelSelectionName(reason SelectionReason) string {
	switch reason {
	case SelectionExplicit:
		return "SelectionReasonExplicit"
	case SelectionUniqueCompatible:
		return "SelectionReasonUniqueCompatible"
	default:
		return ""
	}
}

func digestLiteral(digest [sha256.Size]byte) string {
	var result strings.Builder
	result.WriteString("[32]byte{")
	for index, value := range digest {
		if index != 0 {
			result.WriteString(", ")
		}
		fmt.Fprintf(&result, "0x%02x", value)
	}
	result.WriteByte('}')
	return result.String()
}

func cloneConstructors(values []ConstructorInput) []ConstructorInput {
	result := append([]ConstructorInput(nil), values...)
	for index := range result {
		result[index].Dependencies = append([]DependencyInput(nil), result[index].Dependencies...)
	}
	return result
}
