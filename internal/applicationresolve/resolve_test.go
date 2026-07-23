package applicationresolve_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/configurationresolve"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/intrinsiccatalog"
	"github.com/plystra/cli/internal/plugininventory"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/providerresolution"
	"github.com/plystra/cli/internal/resolutionevidence"
	kernelconfiguration "github.com/plystra/kernel/configuration"
)

func TestMain(main *testing.M) {
	if mode := os.Getenv("PLYSTRA_APPLICATION_RESOLVE_HELPER"); mode != "" {
		os.Exit(runResolveHelper(mode))
	}
	os.Exit(main.Run())
}

func TestResolveEmptyApplicationDeterministicallyWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/empty")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	before := snapshotTree(t, root)
	options := applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	}

	first, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if first.Module().Path() != root || first.Module().ModulePath() != "example.com/empty" {
		t.Fatalf("Module = %#v", first.Module())
	}
	assertResolvedConfigurationProvenance(t, first)
	if _, exists := first.Manifest().HTTPAddress(); exists || len(first.Manifest().Requirements()) != 0 || len(first.Manifest().Aliases()) != 0 {
		t.Fatalf("Manifest is not empty: %#v", first.Manifest())
	}
	if transports := first.Manifest().HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true}) {
		t.Fatalf("default HTTP transports = %#v", transports)
	}
	if cors, exists := first.Manifest().HTTPCORS(); exists {
		t.Fatalf("default HTTPCORS = %#v, %t", cors, exists)
	}
	if len(first.Inventory().Plugins()) != 0 {
		t.Fatalf("Inventory = %#v", first.Inventory().Plugins())
	}
	if len(first.Dependencies().Modules()) != 0 {
		t.Fatalf("Dependencies = %#v", first.Dependencies().Modules())
	}
	if len(first.Interfaces().Interfaces()) != 0 {
		t.Fatalf("Interfaces = %#v", first.Interfaces().Interfaces())
	}
	if len(first.Implementations().Implementations()) != 0 {
		t.Fatalf("Implementations = %#v", first.Implementations().Implementations())
	}
	resolved := first.Resolution()
	if resolved.Passes() != 1 || len(resolved.Context().Plugins()) != 0 || len(resolved.Context().Requirements()) != 0 || len(resolved.Context().Providers()) != 0 {
		t.Fatalf("empty resolution = passes %d, plugins %#v, requirements %#v, providers %#v", resolved.Passes(), resolved.Context().Plugins(), resolved.Context().Requirements(), resolved.Context().Providers())
	}
	capabilities := resolved.Context().Capabilities()
	if len(capabilities) != 2 || capabilities[0].ID().String() != "kernel.health/v1" || capabilities[1].ID().String() != "kernel.info/v1" {
		t.Fatalf("intrinsic catalog = %#v", capabilities)
	}
	if len(resolved.AliasResolution().Aliases()) != 0 {
		t.Fatalf("Aliases = %#v", resolved.AliasResolution().Aliases())
	}
	if !first.Configurations().Valid() || len(first.Configurations().Bindings()) != 0 || first.Configurations().Digest() == "" {
		t.Fatalf("Configurations = %#v", first.Configurations())
	}
	evidence := first.ResolutionEvidence()
	if !evidence.Valid() || evidence.SelectedModelDigest() != resolved.Context().Digest() || evidence.BuildModelDigest() != resolved.Context().BuildModelDigest() {
		t.Fatalf("ResolutionEvidence = valid %t selected %q build %q", evidence.Valid(), evidence.SelectedModelDigest(), evidence.BuildModelDigest())
	}
	if transports, exists := evidence.HTTPTransports(); !exists || transports != (applicationmeta.HTTPTransports{Connect: true}) {
		t.Fatalf("ResolutionEvidence HTTP transports = %#v, %t", transports, exists)
	}
	assertStaticAssemblyMatchesResolution(t, first)
	if evidence.DiscoveredPluginCount() != 0 || evidence.SelectedPluginCount() != 0 || evidence.CanonicalCapabilityCount() != 2 || evidence.RequirementCount() != 0 || evidence.ProviderCandidateCount() != 0 || evidence.RejectedProviderCount() != 0 || evidence.SelectedProviderCount() != 0 || evidence.CapabilityAliasCount() != 0 || evidence.PublicExposureCount() != 0 {
		t.Fatalf("ResolutionEvidence counts = discovered %d selected %d capabilities %d requirements %d candidates %d rejected %d providers %d aliases %d public %d", evidence.DiscoveredPluginCount(), evidence.SelectedPluginCount(), evidence.CanonicalCapabilityCount(), evidence.RequirementCount(), evidence.ProviderCandidateCount(), evidence.RejectedProviderCount(), evidence.SelectedProviderCount(), evidence.CapabilityAliasCount(), evidence.PublicExposureCount())
	}
	modules := evidence.Modules()
	if evidence.ParticipatingModuleCount() != 1 || len(modules) != 1 || modules[0].Path() != "example.com/empty" || modules[0].Role() != resolutionevidence.ModuleRoleCurrent || modules[0].Source().Module() != "example.com/empty" || modules[0].Source().Path() != "plystra.yaml" {
		t.Fatalf("ResolutionEvidence modules = %#v", modules)
	}

	second, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve repeated: %v", err)
	}
	if !bytes.Equal(resolved.Context().CanonicalJSON(), second.Resolution().Context().CanonicalJSON()) || resolved.Context().Digest() != second.Resolution().Context().Digest() || !bytes.Equal(resolved.AliasResolution().CanonicalJSON(), second.Resolution().AliasResolution().CanonicalJSON()) || first.Configurations().Digest() != second.Configurations().Digest() || !bytes.Equal(evidence.CanonicalJSON(), second.ResolutionEvidence().CanonicalJSON()) || evidence.Digest() != second.ResolutionEvidence().Digest() {
		t.Fatal("repeated empty resolution is not byte-deterministic")
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestResolveIntegratesTypeCheckedInterfacePackageDiscovery(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/interfaces")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "domains", "orders", "create", "v1", "interface.go"), `package createv1

import "context"

//plystra:interface orders.create.execute/v1
type Interface interface {
	Execute(context.Context, Request) (Response, error)
}

type Request struct {
	OrderID string `+"`plystra:\"1,required\" json:\"order_id\"`"+`
}

type Response struct {
	Accepted bool `+"`plystra:\"1\" json:\"accepted\"`"+`
}
`)
	writeFile(t, filepath.Join(root, "domains", "orders", "create", "v1", interfacemeta.Name), "description: Executes an order.\nsemantics:\n  kind: command\nerrors:\n  - code: order_rejected\n    description: The order was rejected.\nconstraints:\n  request.order_id: {min_length: 1}\nexamples:\n  - name: accepted\n    request: {order_id: ord_123}\n    response: {accepted: true}\n  - name: rejected\n    request: {order_id: ord_rejected}\n    error: order_rejected\ndeprecation:\n  message: This Interface will be replaced.\n  since: next-release\nconformance:\n  package: ./conformance\n")
	writeFile(t, filepath.Join(root, "domains", "orders", "create", "v1", "conformance", "suite_test.go"), "package conformance\n")
	before := snapshotTree(t, root)
	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       filepath.Join(root, "domains", "orders"),
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	interfaces := result.Interfaces().Interfaces()
	if len(interfaces) != 1 {
		t.Fatalf("Interfaces = %#v", interfaces)
	}
	discovered := interfaces[0]
	contract := discovered.Contract()
	if discovered.ID() != "orders.create.execute/v1" || discovered.ModulePath() != "example.com/interfaces" || discovered.PackagePath() != "example.com/interfaces/domains/orders/create/v1" || discovered.SourcePath() != "domains/orders/create/v1/interface.go" || !discovered.Local() {
		t.Fatalf("Interface provenance = %#v", discovered)
	}
	if contract.MethodName() != "Execute" || contract.RequestName() != "Request" || contract.ResponseName() != "Response" || len(contract.RequestFields()) != 1 || contract.RequestFields()[0].Number() != 1 || !contract.RequestFields()[0].Required() {
		t.Fatalf("Interface contract = %#v", contract)
	}
	if digest := discovered.ContractDigest(); len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("Interface contract digest = %q", digest)
	}
	if digest := discovered.DocumentationDigest(); len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("Interface documentation digest = %q", digest)
	}
	if digest := discovered.ExampleDigest(); len(digest) != len("sha256:")+64 || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("Interface example digest = %q", digest)
	}
	if description, present := discovered.Description(); !present || description != "Executes an order." {
		t.Fatalf("Interface description = %q, %t", description, present)
	}
	semantics, present := discovered.Semantics()
	if !present || semantics.Kind() != interfacemeta.OperationKindCommand || discovered.MetadataSource() != "example.com/interfaces@local/domains/orders/create/v1/interface.yaml" {
		t.Fatalf("Interface semantics = %#v, %t, source %q", semantics, present, discovered.MetadataSource())
	}
	semanticErrors := discovered.SemanticErrors()
	if len(semanticErrors) != 1 || semanticErrors[0].Code() != "order_rejected" {
		t.Fatalf("Interface semantic errors = %#v", semanticErrors)
	}
	constraints := discovered.ConstraintTargets()
	if len(constraints) != 1 || constraints[0].Path() != "request.order_id" || constraints[0].GoPath() != "Request.OrderID" || constraints[0].Field().Type().Kind() != interfacecontract.TypeString {
		t.Fatalf("Interface constraint targets = %#v", constraints)
	}
	if minimum, ok := constraints[0].Rules().MinLength(); !ok || minimum != 1 {
		t.Fatalf("Interface constraint rules = %#v", constraints)
	}
	examples := discovered.Examples()
	if len(examples) != 2 || examples[0].Name() != "accepted" || examples[0].Request().CanonicalJSON() != `{"order_id":"ord_123"}` || examples[1].Name() != "rejected" {
		t.Fatalf("Interface examples = %#v", examples)
	}
	if response, present := examples[0].Response(); !present || response.CanonicalJSON() != `{"accepted":true}` {
		t.Fatalf("Interface success example = %#v, %t", response, present)
	}
	if code, present := examples[1].ErrorCode(); !present || code != "order_rejected" {
		t.Fatalf("Interface semantic-error example = %q, %t", code, present)
	}
	deprecation, present := discovered.Deprecation()
	if !present || deprecation.Message() != "This Interface will be replaced." {
		t.Fatalf("Interface deprecation = %#v, %t", deprecation, present)
	}
	if replacement, exists := deprecation.Replacement(); exists || replacement.String() != "" {
		t.Fatalf("Interface deprecation replacement = %q, %t", replacement.String(), exists)
	}
	if since, exists := deprecation.Since(); !exists || since != "next-release" {
		t.Fatalf("Interface deprecation since = %q, %t", since, exists)
	}
	conformance, present := discovered.Conformance()
	if !present || conformance.Package() != interfacemeta.CanonicalConformancePackage {
		t.Fatalf("Interface conformance = %#v, %t", conformance, present)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated Interface Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestResolveIntegratesImplementationConstructorPackageDiscovery(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "app")
	dependencyRoot := filepath.Join(parent, "contracts")
	kernelRoot := filepath.Join(parent, "kernel")
	writeModule(t, dependencyRoot, "example.com/contracts")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(dependencyRoot, "interfaces", "orders", "cancel", "v1", "interface.go"), interfaceDeclarationSource("cancelv1", "orders.cancel.execute/v1", "Cancel"))
	writeModule(t, kernelRoot, "github.com/plystra/kernel")
	writeFile(t, filepath.Join(kernelRoot, "optional.go"), "package plystra\n\ntype Optional[T any] struct{}\n")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/implementations\n\ngo 1.26\n\nrequire (\n\texample.com/contracts v1.2.3\n\tgithub.com/plystra/kernel v0.0.0\n)\n\nreplace example.com/contracts => ../contracts\nreplace github.com/plystra/kernel => ../kernel\n")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "interfaces", "orders", "create", "v1", "interface.go"), interfaceDeclarationSource("createv1", "orders.create.execute/v1", "Create"))
	writeFile(t, filepath.Join(root, "domains", "orders", "service", "new.go"), `package service

import (
	"context"
	cancelv1 "example.com/contracts/interfaces/orders/cancel/v1"
	createv1 "example.com/implementations/interfaces/orders/create/v1"
	plystra "github.com/plystra/kernel"
)

type Service struct{}

type Config struct {
	Mode string
}

type Cancel = cancelv1.Interface

func (*Service) Create(context.Context, createv1.Request) (createv1.Response, error) {
	return createv1.Response{}, nil
}

func (*Service) Cancel(context.Context, cancelv1.Request) (cancelv1.Response, error) {
	return cancelv1.Response{}, nil
}

//plystra:implements orders.create.execute/v1
//plystra:implements orders.cancel.execute/v1
func Build(cfg Config, create createv1.Interface, cancel Cancel, audit plystra.Optional[Cancel]) (*Service, error) {
	return &Service{}, nil
}
`)
	before := snapshotTree(t, parent)
	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       filepath.Join(root, "domains", "orders"),
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	implementations := result.Implementations().Implementations()
	if len(implementations) != 1 {
		t.Fatalf("Implementations = %#v", implementations)
	}
	discovered := implementations[0]
	if discovered.Symbol().String() != "example.com/implementations/domains/orders/service.Build" || discovered.ModulePath() != "example.com/implementations" || discovered.ModuleVersion() != "" || discovered.PackagePath() != "example.com/implementations/domains/orders/service" || discovered.PackageName() != "service" || discovered.FunctionName() != "Build" || discovered.SourcePath() != "domains/orders/service/new.go" || !discovered.Local() {
		t.Fatalf("Implementation provenance = %#v", discovered)
	}
	configuration, configured := discovered.Configuration()
	if !configured || configuration.String() != "example.com/implementations/domains/orders/service.Config" || configuration.PackagePath() != discovered.PackagePath() || configuration.TypeName() != "Config" {
		t.Fatalf("Implementation configuration = %#v, %t", configuration, configured)
	}
	required := discovered.RequiredInterfaces()
	if len(required) != 2 || required[0].ID().String() != "orders.create.execute/v1" || required[0].PackagePath() != "example.com/implementations/interfaces/orders/create/v1" || required[0].ParameterName() != "create" || required[0].ParameterPosition() != 2 || required[1].ID().String() != "orders.cancel.execute/v1" || required[1].PackagePath() != "example.com/contracts/interfaces/orders/cancel/v1" || required[1].ParameterName() != "cancel" || required[1].ParameterPosition() != 3 {
		t.Fatalf("required Interfaces = %#v", required)
	}
	required[0] = implementationinventory.RequiredInterface{}
	if result.Implementations().Implementations()[0].RequiredInterfaces()[0].ID().String() != "orders.create.execute/v1" {
		t.Fatal("RequiredInterfaces exposed mutable inventory storage")
	}
	optional := discovered.OptionalInterfaces()
	if len(optional) != 1 || optional[0].ID().String() != "orders.cancel.execute/v1" || optional[0].PackagePath() != "example.com/contracts/interfaces/orders/cancel/v1" || optional[0].ParameterName() != "audit" || optional[0].ParameterPosition() != 4 {
		t.Fatalf("optional Interfaces = %#v", optional)
	}
	optional[0] = implementationinventory.OptionalInterface{}
	if result.Implementations().Implementations()[0].OptionalInterfaces()[0].ID().String() != "orders.cancel.execute/v1" {
		t.Fatal("OptionalInterfaces exposed mutable inventory storage")
	}
	concrete := discovered.ConcreteType()
	if concrete.String() != "*example.com/implementations/domains/orders/service.Service" || concrete.PackagePath() != discovered.PackagePath() || concrete.TypeName() != "Service" {
		t.Fatalf("concrete constructor result = %#v (%s)", concrete, concrete.String())
	}
	declared := discovered.Declaration().ImplementedInterfaces()
	if len(declared) != 2 || declared[0].ID().String() != "orders.create.execute/v1" || declared[1].ID().String() != "orders.cancel.execute/v1" {
		t.Fatalf("implemented Interfaces = %#v", declared)
	}
	if !strings.HasPrefix(discovered.Source(), "example.com/implementations@local/domains/orders/service/new.go:") || strings.Contains(discovered.Source(), root) || strings.Contains(discovered.Source(), filepath.ToSlash(root)) {
		t.Fatalf("Implementation source = %q", discovered.Source())
	}
	view := result.Implementations().Implementations()
	view[0] = implementationinventory.Implementation{}
	if result.Implementations().Implementations()[0].FunctionName() != "Build" {
		t.Fatal("Implementations exposed mutable inventory storage")
	}
	if after := snapshotTree(t, parent); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated Project source:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestResolveRejectsInvalidInterfaceOperationSemantics(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface-semantics")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	packageRoot := filepath.Join(root, "interfaces", "invalid")
	writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceDeclarationSource("invalid", "invalid.semantics.execute/v1", "Execute"))
	writeFile(t, filepath.Join(packageRoot, interfacemeta.Name), "semantics:\n  kind: Query\n")
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalidSemantics) || !strings.Contains(err.Error(), "interfaces/invalid/interface.yaml:2:9") || !strings.Contains(err.Error(), "expected query or command") {
		t.Fatalf("Resolve error = %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated Interface Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsInvalidInterfaceDescriptionWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface-description")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	packageRoot := filepath.Join(root, "interfaces", "invalid")
	writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceDeclarationSource("invalid", "invalid.description.execute/v1", "Execute"))
	writeFile(t, filepath.Join(packageRoot, interfacemeta.Name), "description: []\n")
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalidDescription) || !strings.Contains(err.Error(), "interfaces/invalid/interface.yaml:1:14") || !strings.Contains(err.Error(), "description must be a string") {
		t.Fatalf("Resolve error = %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated Interface Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsInvalidInterfaceSemanticErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface-errors")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	packageRoot := filepath.Join(root, "interfaces", "invalid")
	writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceDeclarationSource("invalid", "invalid.errors.execute/v1", "Execute"))
	writeFile(t, filepath.Join(packageRoot, interfacemeta.Name), "errors:\n  - code: InvalidError\n")
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalidSemanticErrors) || !strings.Contains(err.Error(), "interfaces/invalid/interface.yaml:2:11") || !strings.Contains(err.Error(), "lower snake case") {
		t.Fatalf("Resolve error = %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated Interface Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsInvalidInterfaceConstraintPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface-constraints")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	packageRoot := filepath.Join(root, "interfaces", "invalid")
	writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceDeclarationSource("invalid", "invalid.constraints.execute/v1", "Execute"))
	writeFile(t, filepath.Join(packageRoot, interfacemeta.Name), "constraints:\n  request.missing: {min_length: 1}\n")
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalidConstraints) || !strings.Contains(err.Error(), "interfaces/invalid/interface.yaml:2:3") || !strings.Contains(err.Error(), "does not identify a canonical") {
		t.Fatalf("Resolve error = %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated Interface Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsInvalidInterfaceConstraintRule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface-constraint-rule")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	packageRoot := filepath.Join(root, "interfaces", "invalid")
	writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceDeclarationSource("invalid", "invalid.constraints.rule/v1", "Validate"))
	writeFile(t, filepath.Join(packageRoot, interfacemeta.Name), "constraints:\n  request.Value:\n    minimum: 1\n")
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalidConstraints) || !strings.Contains(err.Error(), "interfaces/invalid/interface.yaml:3:5") || !strings.Contains(err.Error(), `rule "minimum" is not supported for string`) {
		t.Fatalf("Resolve error = %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated Interface Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsInvalidInterfaceExampleWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface-example")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	packageRoot := filepath.Join(root, "interfaces", "invalid")
	writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceDeclarationSource("invalid", "invalid.examples.execute/v1", "Execute"))
	writeFile(t, filepath.Join(packageRoot, interfacemeta.Name), "examples:\n  - name: invalid\n    request: {Value: true}\n    response: {Value: ok}\n")
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalidExamples) || !strings.Contains(err.Error(), "interfaces/invalid/interface.yaml:3:22") || !strings.Contains(err.Error(), "canonical string value") {
		t.Fatalf("Resolve error = %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated Interface Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsInvalidInterfaceDeprecationWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface-deprecation")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	packageRoot := filepath.Join(root, "interfaces", "invalid")
	writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceDeclarationSource("invalid", "invalid.deprecation.execute/v1", "Execute"))
	writeFile(t, filepath.Join(packageRoot, interfacemeta.Name), "deprecation:\n  message: Use invalid.deprecation.execute/v2.\n  replacement: invalid.deprecation.execute/v2\n")
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalidDeprecation) || !strings.Contains(err.Error(), "interfaces/invalid/interface.yaml:3:16") || !strings.Contains(err.Error(), "is not a visible Interface") {
		t.Fatalf("Resolve error = %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated Interface Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsInvalidInterfaceConformanceConfigurationWithoutMutation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface-conformance")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	packageRoot := filepath.Join(root, "interfaces", "invalid")
	writeFile(t, filepath.Join(packageRoot, "interface.go"), interfaceDeclarationSource("invalid", "invalid.conformance.execute/v1", "Execute"))
	writeFile(t, filepath.Join(packageRoot, interfacemeta.Name), "conformance:\n  package: ../shared-tests\n")
	before := snapshotTree(t, root)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacemeta.ErrInvalidConformance) || !strings.Contains(err.Error(), "interfaces/invalid/interface.yaml:2:12") || !strings.Contains(err.Error(), `must be exactly "./conformance"`) {
		t.Fatalf("Resolve error = %v", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed resolution mutated Interface Project:\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestResolveRejectsInvalidDiscoveredInterfaceContract(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/invalid-interface")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "interfaces", "invalid", "interface.go"), `package invalid

import "context"

//plystra:interface invalid.contract.execute/v1
type Interface interface {
	Execute(context.Context, Request) (Response, error)
}

type Request struct { Value int `+"`plystra:\"1\"`"+` }
type Response struct { Value string `+"`plystra:\"1\"`"+` }
`)
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       root,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDiscover) || !errors.Is(err, interfacecontract.ErrInvalid) || !strings.Contains(err.Error(), "unsupported Go scalar type int") {
		t.Fatalf("Resolve error = %v", err)
	}
}

func TestResolveRejectsDuplicateVisibleInterfaceIDs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	applicationRoot := filepath.Join(root, "application")
	dependencyRoot := filepath.Join(root, "dependency")
	writeModule(t, dependencyRoot, "example.com/dependency")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(dependencyRoot, "second", "interface.go"), interfaceDeclarationSource("second", "duplicate.visible.execute/v1", "Execute"))
	writeFile(t, filepath.Join(applicationRoot, "go.mod"), `module example.com/application

go 1.26

require example.com/dependency v1.2.3

replace example.com/dependency => ../dependency
`)
	writeFile(t, filepath.Join(applicationRoot, "plystra.yaml"), "{}\n")
	writeFile(t, filepath.Join(applicationRoot, "first", "interface.go"), interfaceDeclarationSource("first", "duplicate.visible.execute/v1", "Execute"))
	before := snapshotTree(t, root)

	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       applicationRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "GOSUMDB": "off"}),
	})
	if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, interfaceinventory.ErrDuplicateID) {
		t.Fatalf("Resolve error = %v", err)
	}
	var duplicate *interfaceinventory.DuplicateIDError
	if !errors.As(err, &duplicate) || duplicate.ID() != "duplicate.visible.execute/v1" || len(duplicate.Definitions()) != 2 {
		t.Fatalf("duplicate error = %#v, %v", duplicate, err)
	}
	definitions := duplicate.Definitions()
	if definitions[0].PackagePath() != "example.com/application/first" || definitions[0].ModuleVersion() != "" || definitions[1].PackagePath() != "example.com/dependency/second" || definitions[1].ModuleVersion() != "v1.2.3" {
		t.Fatalf("duplicate definitions = %#v", definitions)
	}
	for _, definition := range definitions {
		if !strings.Contains(err.Error(), definition.PackagePath()) || !strings.Contains(err.Error(), definition.Source()) {
			t.Fatalf("Resolve error omits duplicate provenance %#v: %v", definition, err)
		}
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), filepath.ToSlash(root)) {
		t.Fatalf("Resolve error exposed private Project root: %v", err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated duplicate Interface Project:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func interfaceDeclarationSource(packageName, id, method string) string {
	return fmt.Sprintf(`package %s

import "context"

//plystra:interface %s
type Interface interface {
	%s(context.Context, Request) (Response, error)
}

type Request struct { Value string %s }
type Response struct { Value string %s }
`, packageName, id, method, "`plystra:\"1\"`", "`plystra:\"1\"`")
}

func TestResolveUsesCompleteSelectedConfigurationAboveDependencyProjects(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "platform")
	writeModule(t, dependencyRoot, "example.com/platform")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {require: [kernel.health/v1]}\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/platform v1.0.0

replace example.com/platform => ../platform
`)
	rootConfiguration := "http: {address: \":8080\", transports: {connect: false, rest: false}, cors: {allowed_origins: ['*']}}\ncapabilities: {require: [kernel.health/v1]}\n"
	selectedConfiguration := "# selected file remains independently authored\nhttp: {address: \":9090\", transports: {rest: true}, cors: {allowed_origins: [https://customer.example], allow_credentials: true}}\ncapabilities: {require: [kernel.info/v1]}\n"
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), rootConfiguration)
	writeFile(t, filepath.Join(appRoot, "deploy", "customer.yaml"), selectedConfiguration)
	before := snapshotTree(t, appRoot)

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:             filepath.Join(appRoot, "deploy"),
		ConfigurationPath: "deploy/customer.yaml",
		Environment:       goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_CONFIG": "ignored.yaml"}),
	})
	if err != nil {
		t.Fatalf("Resolve explicit configuration: %v", err)
	}
	selection := result.ConfigurationSelection()
	if selection.Mode() != "explicit-config" || selection.Path() != "deploy/customer.yaml" || !strings.HasPrefix(selection.Digest(), "sha256:") {
		t.Fatalf("ConfigurationSelection = mode %q path %q digest %q", selection.Mode(), selection.Path(), selection.Digest())
	}
	assertResolvedConfigurationProvenance(t, result)
	httpAddressEvidence := resolvedConfigurationField(t, result, "http.address")
	if httpAddressEvidence.Owner() != resolutionevidence.ConfigurationOwnerExplicit || httpAddressEvidence.Summary() != "string" || len(httpAddressEvidence.Contributors()) != 1 || httpAddressEvidence.Contributors()[0].Sources()[0].Path() != "deploy/customer.yaml" {
		t.Fatalf("full-replacement HTTP configuration evidence = %#v", httpAddressEvidence)
	}
	for _, field := range result.ResolutionEvidence().ConfigurationFields() {
		for _, contribution := range field.Contributors() {
			if contribution.Owner() == resolutionevidence.ConfigurationOwnerRoot || contribution.Owner() == resolutionevidence.ConfigurationOwnerEnvironment {
				t.Fatalf("full replacement retained an excluded root or environment contribution: %#v", contribution)
			}
		}
	}
	inheritedHealth := resolvedConfigurationField(t, result, `capabilities.require["kernel.health/v1"]`)
	if inheritedHealth.Owner() != resolutionevidence.ConfigurationOwnerDependency || len(inheritedHealth.Contributors()) != 1 || inheritedHealth.Contributors()[0].Sources()[0].Module() != "example.com/platform" {
		t.Fatalf("full-replacement inherited requirement evidence = %#v", inheritedHealth)
	}
	if address, exists := result.Manifest().HTTPAddress(); !exists || address != ":9090" {
		t.Fatalf("effective HTTP address = %q, %t; root replacement leaked", address, exists)
	}
	if transports := result.Manifest().HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true, REST: true}) {
		t.Fatalf("effective replacement HTTP transports = %#v; root replacement leaked", transports)
	}
	cors, exists := result.Manifest().HTTPCORS()
	if !exists || !reflect.DeepEqual(cors.AllowedOrigins, []string{"https://customer.example"}) || !cors.AllowCredentials {
		t.Fatalf("effective replacement HTTPCORS = %#v, %t; root replacement leaked", cors, exists)
	}
	if got := applicationRequirementIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"kernel.health/v1", "kernel.info/v1"}) {
		t.Fatalf("effective requirements = %v", got)
	}
	if !result.ConfigurationMaintenance().Changed() || !bytes.Contains(result.ConfigurationMaintenance().Data(), []byte("kernel.health/v1")) {
		t.Fatalf("selected maintenance = changed %t, data %q", result.ConfigurationMaintenance().Changed(), result.ConfigurationMaintenance().Data())
	}
	if !bytes.Equal(result.RootConfigurationData(), []byte(rootConfiguration)) || !bytes.Equal(result.ConfigurationSource(), []byte(selectedConfiguration)) {
		t.Fatal("root or selected source provenance was not preserved independently")
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated selected configuration:\nbefore: %#v\nafter:  %#v", before, after)
	}

	ambient, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_CONFIG": "deploy/customer.yaml"}),
	})
	if err != nil || ambient.ConfigurationSelection().Path() != "deploy/customer.yaml" {
		t.Fatalf("Resolve ambient configuration = path %q, error %v", ambient.ConfigurationSelection().Path(), err)
	}
}

func TestResolveRecordsCurrentProviderReplacementForEveryConfigurationMode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	contract := "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n"
	for _, provider := range []struct {
		directory string
		id        string
	}{
		{directory: "email-root", id: "example.email-root"},
		{directory: "email-production", id: "example.email-production"},
		{directory: "email-customer", id: "example.email-customer"},
	} {
		writePlugin(t, root, provider.directory, "id: "+provider.id+"\nprovides: [email.send/v1]\n")
		writeCapability(t, root, provider.directory, "email.send/v1", contract)
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), "capabilities: {require: [email.send/v1], use: {email.send/v1: example.email-root}}\n")
	writeFile(t, filepath.Join(root, "plystra.production.yaml"), "capabilities: {use: {email.send/v1: example.email-production}}\n")
	writeFile(t, filepath.Join(root, "deploy", "customer.yaml"), "capabilities: {require: [email.send/v1], use: {email.send/v1: example.email-customer}}\n")

	tests := []struct {
		name       string
		options    applicationresolve.Options
		pluginID   string
		pluginPath string
		choicePath string
	}{
		{name: "root", pluginID: "example.email-root", pluginPath: "email-root", choicePath: "plystra.yaml"},
		{name: "environment", options: applicationresolve.Options{EnvironmentName: "production"}, pluginID: "example.email-production", pluginPath: "email-production", choicePath: "plystra.production.yaml"},
		{name: "full replacement", options: applicationresolve.Options{ConfigurationPath: "deploy/customer.yaml"}, pluginID: "example.email-customer", pluginPath: "email-customer", choicePath: "deploy/customer.yaml"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := test.options
			options.Start = root
			options.Environment = goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})
			result, err := applicationresolve.Resolve(t.Context(), options)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			providers := result.ResolutionEvidence().SelectedProviders()
			if len(providers) != 1 || providers[0].Capability() != "email.send/v1" || providers[0].PluginID() != test.pluginID || providers[0].ProjectModule() != "example.com/app" || providers[0].SelectionReason() != resolutionevidence.ProviderSelectionCurrentProject || providers[0].Intrinsic() || providers[0].ProviderSource().Module() != "example.com/app" || providers[0].ProviderSource().Path() != test.pluginPath+"/capabilities/email.send/v1/capability.yaml" {
				t.Fatalf("selected Provider = %#v", providers)
			}
			sources := providers[0].SelectionSources()
			if len(sources) != 1 || sources[0].ProjectModule() != "example.com/app" || sources[0].Source().Module() != "example.com/app" || sources[0].Source().Path() != test.choicePath || sources[0].Source().Kind() != "provider-selection" || sources[0].Source().Line() != 1 || sources[0].Source().Column() != 1 {
				t.Fatalf("selection sources = %#v", sources)
			}
			if bytes.Contains(result.ResolutionEvidence().CanonicalJSON(), []byte(root)) || bytes.Contains(result.ResolutionEvidence().CanonicalJSON(), []byte(filepath.ToSlash(root))) {
				t.Fatalf("selected Provider evidence contains absolute root: %s", result.ResolutionEvidence().CanonicalJSON())
			}
		})
	}
}

func TestResolveRequiresRootMarkerAndSelectedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writeFile(t, filepath.Join(root, "deploy.yaml"), "{}\n")
	options := applicationresolve.Options{
		Start:             root,
		ConfigurationPath: "deploy.yaml",
		Environment:       goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	}
	if _, err := applicationresolve.Resolve(t.Context(), options); err == nil || !errors.Is(err, projectlocate.ErrNotFound) {
		t.Fatalf("Resolve without root marker error = %v", err)
	}
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	options.ConfigurationPath = "missing.yaml"
	if _, err := applicationresolve.Resolve(t.Context(), options); err == nil || !strings.Contains(err.Error(), "missing.yaml") {
		t.Fatalf("Resolve missing selected file error = %v", err)
	}
}

func TestResolveAppliesOneEnvironmentOverlayAboveRootAndDependencies(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyARoot := filepath.Join(root, "platform-a")
	dependencyBRoot := filepath.Join(root, "platform-b")
	writeModule(t, dependencyARoot, "example.com/platform-a")
	writeModule(t, dependencyBRoot, "example.com/platform-b")
	writeFile(t, filepath.Join(dependencyARoot, "plystra.yaml"), "http: {cors: {allowed_origins: ['*']}, expose: [kernel.info/v1]}\ncapabilities: {require: [kernel.health/v1]}\n")
	writeFile(t, filepath.Join(dependencyARoot, "plystra.production.yaml"), "capabilities: {require: [kernel.info/v1]}\n")
	writeFile(t, filepath.Join(dependencyBRoot, "plystra.yaml"), "capabilities: {require: {remove: [kernel.health/v1]}}\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/platform-a v1.0.0
	example.com/platform-b v1.0.0
)

replace example.com/platform-a => ../platform-a

replace example.com/platform-b => ../platform-b
`)
	rootConfiguration := "# shared root\nhttp: {address: \":8080\", transports: {connect: false, rest: true}, cors: {allowed_origins: [https://shared.example], allow_credentials: true}}\ncapabilities: {require: [kernel.info/v1]}\n"
	overlayConfiguration := "# sparse production overlay\nhttp: {address: \":9090\", transports: {connect: true, rest: null}, cors: {allow_credentials: null}}\ncapabilities:\n  require: {add: [kernel.health/v1], remove: [kernel.info/v1]}\n"
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), rootConfiguration)
	writeFile(t, filepath.Join(appRoot, "plystra.production.yaml"), overlayConfiguration)
	before := snapshotTree(t, appRoot)

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:           appRoot,
		EnvironmentName: "production",
		Environment:     goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_CONFIG": "ignored.yaml", "PLYSTRA_ENV": "ignored"}),
	})
	if err != nil {
		t.Fatalf("Resolve environment: %v", err)
	}
	selection := result.ConfigurationSelection()
	if selection.Mode() != applicationgen.ConfigurationModeEnvironment || selection.Environment() != "production" || selection.Path() != "plystra.production.yaml" || !strings.HasPrefix(selection.Digest(), "sha256:") {
		t.Fatalf("ConfigurationSelection = mode %q environment %q path %q digest %q", selection.Mode(), selection.Environment(), selection.Path(), selection.Digest())
	}
	assertResolvedConfigurationProvenance(t, result)
	httpAddressEvidence := resolvedConfigurationField(t, result, "http.address")
	if httpAddressEvidence.Owner() != resolutionevidence.ConfigurationOwnerEnvironment || len(httpAddressEvidence.Contributors()) != 2 || httpAddressEvidence.Contributors()[0].Owner() != resolutionevidence.ConfigurationOwnerRoot || httpAddressEvidence.Contributors()[1].Owner() != resolutionevidence.ConfigurationOwnerEnvironment || httpAddressEvidence.Contributors()[1].Sources()[0].Path() != "plystra.production.yaml" {
		t.Fatalf("environment HTTP address evidence = %#v", httpAddressEvidence)
	}
	restRemoval := resolvedConfigurationField(t, result, "http.transports.rest")
	if !restRemoval.Effective() || !restRemoval.Removed() || restRemoval.Owner() != resolutionevidence.ConfigurationOwnerEnvironment || restRemoval.Summary() != "removal" || len(restRemoval.Contributors()) != 2 {
		t.Fatalf("environment transport removal evidence = %#v", restRemoval)
	}
	infoRemoval := resolvedConfigurationField(t, result, `capabilities.require["kernel.info/v1"]`)
	if !infoRemoval.Effective() || !infoRemoval.Removed() || infoRemoval.Owner() != resolutionevidence.ConfigurationOwnerEnvironment {
		t.Fatalf("environment requirement removal evidence = %#v", infoRemoval)
	}
	if address, exists := result.Manifest().HTTPAddress(); !exists || address != ":9090" {
		t.Fatalf("effective HTTP address = %q, %t", address, exists)
	}
	if transports := result.Manifest().HTTPTransports(); transports != (applicationmeta.HTTPTransports{Connect: true}) {
		t.Fatalf("effective environment HTTP transports = %#v", transports)
	}
	cors, exists := result.Manifest().HTTPCORS()
	if !exists || !reflect.DeepEqual(cors.AllowedOrigins, []string{"https://shared.example"}) || cors.AllowCredentials {
		t.Fatalf("effective environment HTTPCORS = %#v, %t", cors, exists)
	}
	if got := applicationRequirementIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"kernel.health/v1"}) {
		t.Fatalf("effective requirements = %v", got)
	}
	selectedProviders := result.ResolutionEvidence().SelectedProviders()
	if len(selectedProviders) != 2 || selectedProviders[0].Capability() != "kernel.health/v1" || selectedProviders[0].SelectionReason() != resolutionevidence.ProviderSelectionIntrinsic || !selectedProviders[0].Intrinsic() || selectedProviders[0].ProviderSource().Module() != "github.com/plystra/kernel" || selectedProviders[0].ProviderSource().Path() != "capability/catalog/definitions/kernel.health/v1/capability.yaml" || selectedProviders[1].Capability() != "kernel.info/v1" || selectedProviders[1].SelectionReason() != resolutionevidence.ProviderSelectionIntrinsic || !selectedProviders[1].Intrinsic() {
		t.Fatalf("intrinsic Provider evidence = %#v", selectedProviders)
	}
	if !result.ConfigurationMaintenance().Changed() || result.ConfigurationMaintenancePath() != "plystra.yaml" || !bytes.Equal(result.ConfigurationMaintenanceSource(), []byte(rootConfiguration)) {
		t.Fatalf("root maintenance = changed %t path %q source %q", result.ConfigurationMaintenance().Changed(), result.ConfigurationMaintenancePath(), result.ConfigurationMaintenanceSource())
	}
	if !bytes.Contains(result.RootConfigurationData(), []byte("expose:")) || !bytes.Equal(result.ConfigurationSource(), []byte(overlayConfiguration)) {
		t.Fatal("root or overlay provenance was not preserved independently")
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve environment mutated Project:\nbefore: %#v\nafter: %#v", before, after)
	}

	ambient, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_ENV": "production"}),
	})
	if err != nil || ambient.ConfigurationSelection().Environment() != "production" {
		t.Fatalf("Resolve PLYSTRA_ENV = environment %q, %v", ambient.ConfigurationSelection().Environment(), err)
	}
}

func TestResolveRequiresSelectedEnvironmentOverlay(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeModule(t, root, "example.com/app")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:           root,
		EnvironmentName: "production",
		Environment:     goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err == nil || !errors.Is(err, applicationresolve.ErrConfigurationSelection) || !strings.Contains(err.Error(), "plystra.production.yaml") {
		t.Fatalf("Resolve missing environment overlay error = %v", err)
	}
}

func TestResolveDerivesExposureFromEverySelectedConfigurationMode(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		mode         string
		rootData     string
		selectedPath string
		selectedData string
		configure    func(*applicationresolve.Options)
	}{
		{
			name:         "default",
			mode:         applicationgen.ConfigurationModeDefault,
			rootData:     "http: {expose: [kernel.health/v1]}\n",
			selectedPath: "plystra.yaml",
		},
		{
			name:         "environment overlay",
			mode:         applicationgen.ConfigurationModeEnvironment,
			rootData:     "http: {expose: [kernel.info/v1]}\n",
			selectedPath: "plystra.production.yaml",
			selectedData: "http:\n  expose: {add: [kernel.health/v1], remove: [kernel.info/v1]}\n",
			configure: func(options *applicationresolve.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:         "full replacement",
			mode:         applicationgen.ConfigurationModeExplicit,
			rootData:     "http: {expose: [kernel.info/v1]}\n",
			selectedPath: "deploy/customer.yaml",
			selectedData: "http: {expose: [kernel.health/v1]}\n",
			configure: func(options *applicationresolve.Options) {
				options.ConfigurationPath = "deploy/customer.yaml"
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeModule(t, root, "example.com/selected-exposure/"+strings.ReplaceAll(test.name, " ", "-"))
			writeFile(t, filepath.Join(root, "plystra.yaml"), test.rootData)
			if test.selectedPath != "plystra.yaml" {
				writeFile(t, filepath.Join(root, filepath.FromSlash(test.selectedPath)), test.selectedData)
			}
			before := snapshotTree(t, root)
			options := applicationresolve.Options{
				Start:       filepath.Join(root, filepath.Dir(filepath.FromSlash(test.selectedPath))),
				Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
			}
			if test.configure != nil {
				test.configure(&options)
			}

			result, err := applicationresolve.Resolve(t.Context(), options)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			selection := result.ConfigurationSelection()
			if selection.Mode() != test.mode || selection.Path() != test.selectedPath {
				t.Fatalf("selection = mode %q path %q", selection.Mode(), selection.Path())
			}
			if got := applicationExposureIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"kernel.health/v1"}) {
				t.Fatalf("effective exposures = %v", got)
			}
			if got := applicationRequirementIDs(result.Manifest()); len(got) != 0 {
				t.Fatalf("authored requirements = %v", got)
			}
			if requirements := result.Resolution().Context().Requirements(); len(requirements) != 1 || requirements[0].String() != "kernel.health/v1" {
				t.Fatalf("resolved exposure requirements = %v", requirements)
			}
			healthID := parseGenerationCapability(t, "kernel.health/v1")
			health, exists := result.Resolution().Context().Capability(healthID)
			if !exists || health.Exposure() != (generation.Exposure{Go: true, HTTP: true, JavaScript: true}) {
				t.Fatalf("health exposure = %#v, %t", health.Exposure(), exists)
			}
			infoID := parseGenerationCapability(t, "kernel.info/v1")
			info, exists := result.Resolution().Context().Capability(infoID)
			if !exists || info.Exposure() != (generation.Exposure{Go: true}) {
				t.Fatalf("unselected info exposure = %#v, %t", info.Exposure(), exists)
			}
			publicExposures := result.ResolutionEvidence().PublicExposures()
			if result.ResolutionEvidence().PublicExposureCount() != 1 || len(publicExposures) != 1 || publicExposures[0].Capability() != "kernel.health/v1" || publicExposures[0].Kind() != resolutionevidence.PublicExposureCanonical || publicExposures[0].CanonicalTarget() != "kernel.health/v1" || publicExposures[0].ContractDigest() != health.ContractDigest() || publicExposures[0].Exposure() != health.Exposure() {
				t.Fatalf("public exposure evidence = %#v", publicExposures)
			}
			exposureSources := publicExposures[0].Sources()
			if len(exposureSources) != 1 || exposureSources[0].Kind() != resolutionevidence.PublicExposureSourceHTTPExpose || exposureSources[0].ProjectModule() != result.Module().ModulePath() || exposureSources[0].Source().Module() != result.Module().ModulePath() || exposureSources[0].Source().Path() != test.selectedPath || exposureSources[0].Source().Kind() != "exposure" {
				t.Fatalf("public exposure source = %#v", exposureSources)
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("Resolve mutated Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestResolveRejectsSelectedExposureWithoutHTTPTransport(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		rootData  string
		path      string
		exposeKey string
		selected  string
		configure func(*applicationresolve.Options)
	}{
		{
			name:      "default",
			rootData:  "http: {transports: {connect: false, rest: false}, expose: [kernel.health/v1]}\n",
			path:      "plystra.yaml",
			exposeKey: "http.expose",
		},
		{
			name:      "environment overlay",
			rootData:  "{}\n",
			path:      "plystra.production.yaml",
			exposeKey: "http.expose.add",
			selected:  "http: {transports: {connect: false, rest: false}, expose: {add: [kernel.health/v1]}}\n",
			configure: func(options *applicationresolve.Options) {
				options.EnvironmentName = "production"
			},
		},
		{
			name:      "full replacement",
			rootData:  "{}\n",
			path:      "deploy/customer.yaml",
			exposeKey: "http.expose",
			selected:  "http: {transports: {connect: false, rest: false}, expose: [kernel.health/v1]}\n",
			configure: func(options *applicationresolve.Options) {
				options.ConfigurationPath = "deploy/customer.yaml"
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeModule(t, root, "example.com/no-http-transport/"+strings.ReplaceAll(test.name, " ", "-"))
			writeFile(t, filepath.Join(root, "plystra.yaml"), test.rootData)
			if test.path != "plystra.yaml" {
				writeFile(t, filepath.Join(root, filepath.FromSlash(test.path)), test.selected)
			}
			before := snapshotTree(t, root)
			options := applicationresolve.Options{
				Start:       root,
				Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
			}
			if test.configure != nil {
				test.configure(&options)
			}

			result, err := applicationresolve.Resolve(t.Context(), options)
			if !errors.Is(err, applicationmeta.ErrHTTPTransportSelection) || result.Module().Path() != "" {
				t.Fatalf("Resolve = %#v, %v", result, err)
			}
			for _, want := range []string{
				"http.expose is nonempty",
				"http.transports.connect and http.transports.rest are both false",
				`kernel.health/v1 at ` + test.path + ` ` + test.exposeKey + `["kernel.health/v1"]`,
			} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("Resolve error %q does not contain %q", err, want)
				}
			}
			if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
				t.Fatalf("Resolve mutated rejected Project:\nbefore: %#v\nafter:  %#v", before, after)
			}
		})
	}
}

func TestResolveClosesLocalRequirementsThroughDependencyProvidersAndAliases(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	providerRoot := filepath.Join(root, "providers")
	writeModule(t, providerRoot, "example.com/providers")
	writeFile(t, filepath.Join(providerRoot, "plystra.yaml"), `http:
  address: ":9090"
  expose: [email.send/v1]
timeouts: {startup: 1s}
capabilities:
  use: {email.send/v1: example.smtp}
  aliases:
    mail.send/v1: email.send/v1
config:
  example.smtp:
    host: private.smtp.example.com
    password: {env: PLYSTRA_APPLICATION_RESOLVE_PRIVATE_SECRET}
`)
	writeFile(t, filepath.Join(providerRoot, "plystra.production.yaml"), "not: [a valid dependency overlay\n")
	writePlugin(t, providerRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\nconfig: {host: {type: string, required: true}, password: {type: secret, required: true}}\n")
	writeCapability(t, providerRoot, "smtp", "email.send/v1", `id: email.send/v1
request:
  to: {type: string, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [invalid_recipient]
`)
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/providers v1.2.3

replace example.com/providers => ../providers
`)
	writePlugin(t, appRoot, "local", "id: example.local\nrequires: [email.send/v1]\n")
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "http: {address: \":8080\"}\n")
	before := snapshotTree(t, appRoot)
	dependencyBefore := snapshotTree(t, providerRoot)
	options := applicationresolve.Options{
		Start:       filepath.Join(appRoot, "local"),
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off", "PLYSTRA_APPLICATION_RESOLVE_PRIVATE_SECRET": "resolved-private-secret"}),
	}

	first, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertResolvedConfigurationProvenance(t, first)
	hostEvidence := resolvedConfigurationField(t, first, `config["example.smtp"]["host"]`)
	passwordEvidence := resolvedConfigurationField(t, first, `config["example.smtp"]["password"]`)
	if hostEvidence.Owner() != resolutionevidence.ConfigurationOwnerDependency || passwordEvidence.Owner() != resolutionevidence.ConfigurationOwnerDependency || len(hostEvidence.Contributors()) != 1 || len(passwordEvidence.Contributors()) != 1 || hostEvidence.Contributors()[0].Sources()[0].Module() != "example.com/providers" || passwordEvidence.Contributors()[0].Sources()[0].Path() != "plystra.yaml" {
		t.Fatalf("dependency Plugin configuration evidence = host %#v password %#v", hostEvidence, passwordEvidence)
	}
	httpAddressEvidence := resolvedConfigurationField(t, first, "http.address")
	if httpAddressEvidence.Owner() != resolutionevidence.ConfigurationOwnerRoot || len(httpAddressEvidence.Contributors()) != 1 {
		t.Fatalf("current process configuration evidence = %#v", httpAddressEvidence)
	}
	plugins := first.Inventory().Plugins()
	dependencies := first.Dependencies().Modules()
	if len(dependencies) != 1 || dependencies[0].Path() != "example.com/providers" || dependencies[0].SelectedVersion() != "v1.2.3" {
		t.Fatalf("Dependencies = %#v", dependencies)
	}
	if !first.Composition().Valid() || first.Composition().DependencyDigest() == "" || len(first.Composition().Provenance()) == 0 {
		t.Fatalf("Composition = %#v", first.Composition())
	}
	if address, exists := first.Manifest().HTTPAddress(); !exists || address != ":8080" || first.Manifest().StartupTimeout() != applicationmeta.DefaultStartupTimeout || len(first.CurrentManifest().HTTPExposures()) != 1 || len(first.Manifest().HTTPExposures()) != 1 || !first.ConfigurationMaintenance().Changed() {
		t.Fatalf("composed/current manifests = effective %#v, current %#v", first.Manifest(), first.CurrentManifest())
	}
	if got := pluginSummaries(plugins); !reflect.DeepEqual(got, []string{
		"example.local:example.com/app@local:local:true",
		"example.smtp:example.com/providers@v1.2.3:smtp:false",
	}) {
		t.Fatalf("Inventory = %v", got)
	}
	resolved := first.Resolution()
	capability := parseGenerationCapability(t, "email.send/v1")
	provider, exists := resolved.Context().SelectedProvider(capability)
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("SelectedProvider(email.send/v1) = %s, %t", provider, exists)
	}
	if requirements := resolved.Context().Requirements(); len(requirements) != 1 || requirements[0] != capability {
		t.Fatalf("Requirements = %v", requirements)
	}
	target, exists := resolved.Context().Capability(capability)
	if !exists || target.Exposure() != (generation.Exposure{Go: true, HTTP: true, JavaScript: true}) {
		t.Fatalf("target exposure = %#v, %t", target.Exposure(), exists)
	}
	aliases := resolved.AliasResolution().Aliases()
	if len(aliases) != 1 || aliases[0].ID().String() != "mail.send/v1" || aliases[0].Target().String() != "email.send/v1" || aliases[0].Exposure() != target.Exposure() {
		t.Fatalf("Aliases = %#v", aliases)
	}
	if got := configurationBindingIDs(first.Configurations().Bindings()); !reflect.DeepEqual(got, []string{"example.local", "example.smtp"}) {
		t.Fatalf("configuration bindings = %v", got)
	}
	assertStaticAssemblyMatchesResolution(t, first)
	evidenceRequirements := first.ResolutionEvidence().Requirements()
	if len(evidenceRequirements) != 1 || evidenceRequirements[0].Capability() != "email.send/v1" || evidenceRequirements[0].Intrinsic() {
		t.Fatalf("resolution evidence requirements = %#v", evidenceRequirements)
	}
	evidenceSources := evidenceRequirements[0].Sources()
	if len(evidenceSources) != 3 || evidenceSources[0].Kind() != providerresolution.RequirementAliasTarget || evidenceSources[1].Kind() != providerresolution.RequirementExposure || evidenceSources[2].Kind() != providerresolution.RequirementPlugin {
		t.Fatalf("resolution evidence requirement sources = %#v", evidenceSources)
	}
	if source := evidenceSources[0]; source.ProjectModule() != "example.com/providers" || source.Source().Module() != "example.com/providers" || source.Source().Path() != "plystra.yaml" || source.Alias() != "mail.send/v1" {
		t.Fatalf("dependency Alias-target source = %#v", source)
	}
	if source := evidenceSources[1]; source.ProjectModule() != "example.com/providers" || source.Source().Module() != "example.com/providers" || source.Source().Path() != "plystra.yaml" {
		t.Fatalf("dependency exposure source = %#v", source)
	}
	if source := evidenceSources[2]; source.ProjectModule() != "example.com/app" || source.Source().Module() != "example.com/app" || source.Source().Path() != "local/plugin.yaml" || source.PluginID() != "example.local" {
		t.Fatalf("local Plugin requirement source = %#v", source)
	}
	publicExposures := first.ResolutionEvidence().PublicExposures()
	if first.ResolutionEvidence().PublicExposureCount() != 2 || len(publicExposures) != 2 || publicExposures[0].Capability() != "email.send/v1" || publicExposures[0].Kind() != resolutionevidence.PublicExposureCanonical || publicExposures[0].CanonicalTarget() != "email.send/v1" || publicExposures[0].ContractDigest() != target.ContractDigest() || publicExposures[0].Exposure() != target.Exposure() || publicExposures[1].Capability() != "mail.send/v1" || publicExposures[1].Kind() != resolutionevidence.PublicExposureAlias || publicExposures[1].CanonicalTarget() != "email.send/v1" || publicExposures[1].ContractDigest() != target.ContractDigest() || publicExposures[1].Exposure() != target.Exposure() {
		t.Fatalf("dependency-composed public exposures = %#v", publicExposures)
	}
	canonicalExposureSources := publicExposures[0].Sources()
	if len(canonicalExposureSources) != 1 || canonicalExposureSources[0].Kind() != resolutionevidence.PublicExposureSourceHTTPExpose || canonicalExposureSources[0].ProjectModule() != "example.com/providers" || canonicalExposureSources[0].Source().Module() != "example.com/providers" || canonicalExposureSources[0].Source().Path() != "plystra.yaml" || canonicalExposureSources[0].Source().Kind() != "exposure" {
		t.Fatalf("dependency canonical public exposure sources = %#v", canonicalExposureSources)
	}
	aliasExposureSources := publicExposures[1].Sources()
	if len(aliasExposureSources) != 1 || aliasExposureSources[0].Kind() != resolutionevidence.PublicExposureSourceAliasApplication || aliasExposureSources[0].ProjectModule() != "example.com/providers" || aliasExposureSources[0].Source().Module() != "example.com/providers" || aliasExposureSources[0].Source().Path() != "plystra.yaml" || aliasExposureSources[0].Source().Kind() != "alias-target" {
		t.Fatalf("dependency Alias public exposure sources = %#v", aliasExposureSources)
	}
	for _, forbidden := range []string{"private.smtp.example.com", "PLYSTRA_APPLICATION_RESOLVE_PRIVATE_SECRET", "resolved-private-secret", appRoot, providerRoot} {
		if bytes.Contains(resolved.Context().CanonicalJSON(), []byte(forbidden)) || bytes.Contains(first.ResolutionEvidence().CanonicalJSON(), []byte(forbidden)) {
			t.Fatalf("resolution output exposed private configuration %q", forbidden)
		}
	}

	second, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve repeated: %v", err)
	}
	if !bytes.Equal(resolved.Context().CanonicalJSON(), second.Resolution().Context().CanonicalJSON()) || !bytes.Equal(resolved.AliasResolution().CanonicalJSON(), second.Resolution().AliasResolution().CanonicalJSON()) || first.Configurations().Digest() != second.Configurations().Digest() {
		t.Fatal("repeated dependency resolution is not byte-deterministic")
	}
	if after := snapshotTree(t, appRoot); !reflect.DeepEqual(after, before) {
		t.Fatalf("Resolve mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
	if after := snapshotTree(t, providerRoot); !reflect.DeepEqual(after, dependencyBefore) {
		t.Fatalf("Resolve mutated dependency Project:\nbefore: %#v\nafter:  %#v", dependencyBefore, after)
	}
}

func TestResolveComposesDirectAndTransitiveDependencyProjectDeclarations(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	directRoot := filepath.Join(root, "direct")
	transitiveRoot := filepath.Join(root, "transitive")
	ordinaryRoot := filepath.Join(root, "ordinary")

	writeModule(t, transitiveRoot, "example.com/transitive")
	writeFile(t, filepath.Join(transitiveRoot, "plystra.yaml"), "capabilities: {require: [audit.write/v1]}\n")
	writePlugin(t, transitiveRoot, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	writeCapability(t, transitiveRoot, "audit", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n")

	writeFile(t, filepath.Join(directRoot, "go.mod"), "module example.com/direct\n\ngo 1.26\n\nrequire example.com/transitive v1.4.0\n")
	writeFile(t, filepath.Join(directRoot, "plystra.yaml"), `http:
  expose: [email.send/v1]
capabilities:
  use: {email.send/v1: example.smtp}
`)
	writePlugin(t, directRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, directRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writePlugin(t, directRoot, "unused", "id: example.unused\nprovides: [email.send/v1, queue.push/v1]\n")
	writeCapability(t, directRoot, "unused", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCapability(t, directRoot, "unused", "queue.push/v1", "id: queue.push/v1\nrequest: {}\nresponse: {}\nerrors: []\n")

	writeModule(t, ordinaryRoot, "example.com/ordinary")
	writeFile(t, filepath.Join(ordinaryRoot, "looks-like-plugin", "plugin.yaml"), "this is deliberately not a valid Plugin declaration\n")

	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/direct v1.2.0
	example.com/ordinary v1.0.0
)

replace example.com/direct => ../direct
replace example.com/transitive => ../transitive
replace example.com/ordinary => ../ordinary
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")
	writePlugin(t, appRoot, "app", "id: example.app\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	projects := result.Dependencies().Projects()
	if len(projects) != 2 || projects[0].Path() != "example.com/direct" || projects[0].SelectedVersion() != "v1.2.0" || projects[1].Path() != "example.com/transitive" || projects[1].SelectedVersion() != "v1.4.0" || projects[1].Direct() {
		t.Fatalf("dependency Projects = %#v", projects)
	}
	if got := pluginSummaries(result.Inventory().Plugins()); !reflect.DeepEqual(got, []string{
		"example.app:example.com/app@local:app:true",
		"example.audit:example.com/transitive@v1.4.0:audit:false",
		"example.smtp:example.com/direct@v1.2.0:smtp:false",
		"example.unused:example.com/direct@v1.2.0:unused:false",
	}) {
		t.Fatalf("visible Plugins = %v", got)
	}
	if got := applicationRequirementIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"audit.write/v1"}) {
		t.Fatalf("composed requirements = %v", got)
	}
	if got := applicationExposureIDs(result.Manifest()); !reflect.DeepEqual(got, []string{"email.send/v1"}) {
		t.Fatalf("composed exposures = %v", got)
	}
	contextRequirements := result.Resolution().Context().Requirements()
	if len(contextRequirements) != 2 || contextRequirements[0].String() != "audit.write/v1" || contextRequirements[1].String() != "email.send/v1" {
		t.Fatalf("resolved requirements = %v", contextRequirements)
	}
	evidenceRequirements := result.ResolutionEvidence().Requirements()
	if len(evidenceRequirements) != 2 || evidenceRequirements[0].Capability() != "audit.write/v1" || evidenceRequirements[1].Capability() != "email.send/v1" {
		t.Fatalf("resolution evidence requirements = %#v", evidenceRequirements)
	}
	auditSources := evidenceRequirements[0].Sources()
	if len(auditSources) != 1 || auditSources[0].Kind() != providerresolution.RequirementDeclaration || auditSources[0].ProjectModule() != "example.com/transitive" || auditSources[0].Source().Module() != "example.com/transitive" || auditSources[0].Source().Path() != "plystra.yaml" {
		t.Fatalf("transitive requirement sources = %#v", auditSources)
	}
	emailSources := evidenceRequirements[1].Sources()
	if len(emailSources) != 1 || emailSources[0].Kind() != providerresolution.RequirementExposure || emailSources[0].ProjectModule() != "example.com/direct" || emailSources[0].Source().Module() != "example.com/direct" || emailSources[0].Source().Path() != "plystra.yaml" {
		t.Fatalf("direct exposure requirement sources = %#v", emailSources)
	}
	auditConfiguration := resolvedConfigurationField(t, result, `capabilities.require["audit.write/v1"]`)
	emailConfiguration := resolvedConfigurationField(t, result, `http.expose["email.send/v1"]`)
	if auditConfiguration.Owner() != resolutionevidence.ConfigurationOwnerDependency || emailConfiguration.Owner() != resolutionevidence.ConfigurationOwnerDependency || len(auditConfiguration.Contributors()) != 1 || len(emailConfiguration.Contributors()) != 1 || auditConfiguration.Contributors()[0].Sources()[0].Module() != "example.com/transitive" || emailConfiguration.Contributors()[0].Sources()[0].Module() != "example.com/direct" {
		t.Fatalf("direct/transitive configuration evidence = audit %#v email %#v", auditConfiguration, emailConfiguration)
	}
	if provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1")); !exists || provider.String() != "example.smtp" {
		t.Fatalf("email Provider = %s, %t", provider, exists)
	}
	evidenceModules := result.ResolutionEvidence().Modules()
	if len(evidenceModules) != 3 || evidenceModules[0].Path() != "example.com/app" || evidenceModules[0].Role() != resolutionevidence.ModuleRoleCurrent || evidenceModules[1].Path() != "example.com/direct" || !evidenceModules[1].Direct() || evidenceModules[1].RequiredVersion() != "v1.2.0" || evidenceModules[1].SelectedVersion() != "v1.2.0" || evidenceModules[2].Path() != "example.com/transitive" || evidenceModules[2].Direct() || evidenceModules[2].RequiredVersion() != "" || evidenceModules[2].SelectedVersion() != "v1.4.0" {
		t.Fatalf("resolution evidence modules = %#v", evidenceModules)
	}
	for _, module := range evidenceModules[1:] {
		replacement, exists := module.Replacement()
		if !exists || replacement.Kind() != resolutionevidence.ReplacementLocal || replacement.ModulePath() != module.Path() || replacement.Version() != "" || module.Source().Module() != module.Path() || module.Source().Path() != "plystra.yaml" {
			t.Fatalf("resolution evidence replacement for %s = %#v/%t source %#v", module.Path(), replacement, exists, module.Source())
		}
	}
	evidenceCandidates := result.ResolutionEvidence().PluginCandidates()
	if result.ResolutionEvidence().DiscoveredPluginCount() != 4 || result.ResolutionEvidence().SelectedPluginCount() != 3 || len(evidenceCandidates) != 4 || evidenceCandidates[0].ID() != "example.app" || evidenceCandidates[0].ModulePath() != "example.com/app" || evidenceCandidates[0].ModuleRole() != resolutionevidence.ModuleRoleCurrent || !evidenceCandidates[0].Local() || evidenceCandidates[0].Source().Module() != "example.com/app" || evidenceCandidates[0].Source().Path() != "app/plugin.yaml" || evidenceCandidates[1].ID() != "example.audit" || evidenceCandidates[1].ModulePath() != "example.com/transitive" || evidenceCandidates[2].ID() != "example.smtp" || evidenceCandidates[2].ModulePath() != "example.com/direct" || evidenceCandidates[3].ID() != "example.unused" || evidenceCandidates[3].ModulePath() != "example.com/direct" || evidenceCandidates[3].Local() {
		t.Fatalf("resolution evidence Plugin candidates = %#v", evidenceCandidates)
	}
	for _, candidate := range evidenceCandidates {
		if candidate.Source().Kind() != "plugin-declaration" || candidate.Source().Line() != 1 || candidate.Source().Column() != 1 {
			t.Fatalf("resolution evidence Plugin candidate source = %#v", candidate.Source())
		}
	}
	selectedPlugins := result.ResolutionEvidence().SelectedPlugins()
	if len(selectedPlugins) != 3 || selectedPlugins[0].ID() != "example.app" || selectedPlugins[0].ModulePath() != "example.com/app" || selectedPlugins[0].ModuleVersion() != "" || !selectedPlugins[0].Local() || selectedPlugins[0].Source() != evidenceCandidates[0].Source() || selectedPlugins[1].ID() != "example.audit" || selectedPlugins[1].ModulePath() != "example.com/transitive" || selectedPlugins[1].ModuleVersion() != "v1.4.0" || selectedPlugins[2].ID() != "example.smtp" || selectedPlugins[2].ModulePath() != "example.com/direct" || selectedPlugins[2].ModuleVersion() != "v1.2.0" {
		t.Fatalf("resolution evidence selected Plugins = %#v", selectedPlugins)
	}
	if reasons := selectedPlugins[0].Reasons(); len(reasons) != 1 || reasons[0].Kind() != resolutionevidence.PluginSelectionCurrentProject || reasons[0].Capability() != "" {
		t.Fatalf("current Project Plugin reasons = %#v", reasons)
	}
	for index, capability := range []string{"audit.write/v1", "email.send/v1"} {
		if reasons := selectedPlugins[index+1].Reasons(); len(reasons) != 1 || reasons[0].Kind() != resolutionevidence.PluginSelectionProvider || reasons[0].Capability() != capability {
			t.Fatalf("selected Provider Plugin %s reasons = %#v", selectedPlugins[index+1].ID(), reasons)
		}
	}
	providerCandidates := result.ResolutionEvidence().ProviderCandidates()
	if result.ResolutionEvidence().ProviderCandidateCount() != 4 || result.ResolutionEvidence().RejectedProviderCount() != 2 || len(providerCandidates) != 4 {
		t.Fatalf("resolution evidence Provider candidate counts = %d candidates, %d rejected; values %#v", result.ResolutionEvidence().ProviderCandidateCount(), result.ResolutionEvidence().RejectedProviderCount(), providerCandidates)
	}
	if providerCandidates[0].Capability() != "audit.write/v1" || providerCandidates[0].PluginID() != "example.audit" || providerCandidates[0].Rejected() || providerCandidates[0].ProjectModule() != "example.com/transitive" || providerCandidates[0].Source().Path() != "audit/capabilities/audit.write/v1/capability.yaml" || providerCandidates[1].Capability() != "email.send/v1" || providerCandidates[1].PluginID() != "example.smtp" || providerCandidates[1].Rejected() || providerCandidates[1].ProjectModule() != "example.com/direct" || providerCandidates[2].Capability() != "email.send/v1" || providerCandidates[2].PluginID() != "example.unused" || providerCandidates[2].RejectionReason() != resolutionevidence.ProviderRejectionAnotherProviderSelected || providerCandidates[3].Capability() != "queue.push/v1" || providerCandidates[3].PluginID() != "example.unused" || providerCandidates[3].RejectionReason() != resolutionevidence.ProviderRejectionCapabilityNotRequired {
		t.Fatalf("resolution evidence Provider candidates = %#v", providerCandidates)
	}
	for _, candidate := range providerCandidates {
		if candidate.Source().Module() != candidate.ProjectModule() || candidate.Source().Kind() != "provider-declaration" || candidate.Source().Line() != 1 || candidate.Source().Column() != 1 {
			t.Fatalf("resolution evidence Provider candidate source = %#v", candidate)
		}
	}
	selectedProviders := result.ResolutionEvidence().SelectedProviders()
	if len(selectedProviders) != 2 || selectedProviders[0].Capability() != "audit.write/v1" || selectedProviders[0].PluginID() != "example.audit" || selectedProviders[0].ProjectModule() != "example.com/transitive" || selectedProviders[0].SelectionReason() != resolutionevidence.ProviderSelectionSoleProvider || len(selectedProviders[0].SelectionSources()) != 0 {
		t.Fatalf("transitive automatic Provider evidence = %#v", selectedProviders)
	}
	if selectedProviders[1].Capability() != "email.send/v1" || selectedProviders[1].PluginID() != "example.smtp" || selectedProviders[1].ProjectModule() != "example.com/direct" || selectedProviders[1].SelectionReason() != resolutionevidence.ProviderSelectionInherited || len(selectedProviders[1].SelectionSources()) != 1 || selectedProviders[1].SelectionSources()[0].ProjectModule() != "example.com/direct" || selectedProviders[1].SelectionSources()[0].Source().Path() != "plystra.yaml" || selectedProviders[1].SelectionSources()[0].Source().Kind() != "provider-selection" {
		t.Fatalf("direct inherited Provider evidence = %#v", selectedProviders[1])
	}
	assertStaticAssemblyMatchesResolution(t, result)
	if bytes.Contains(result.ResolutionEvidence().CanonicalJSON(), []byte(filepath.ToSlash(root))) || bytes.Contains(result.ResolutionEvidence().CanonicalJSON(), []byte(root)) || bytes.Contains(result.ResolutionEvidence().CanonicalJSON(), []byte("example.com/ordinary")) {
		t.Fatalf("resolution evidence contains an absolute root or ordinary dependency: %s", result.ResolutionEvidence().CanonicalJSON())
	}
	provenance := result.Composition().Provenance()
	for path, source := range map[string]string{
		`http.expose["email.send/v1"]`:           "example.com/direct@v1.2.0/plystra.yaml",
		`capabilities.require["audit.write/v1"]`: "example.com/transitive@v1.4.0/plystra.yaml",
		`capabilities.use["email.send/v1"]`:      "example.com/direct@v1.2.0/plystra.yaml",
	} {
		records := compositionProvenance(provenance, path)
		if len(records) != 1 || len(records[0].Sources()) != 1 || !strings.HasPrefix(records[0].Sources()[0], source) {
			t.Fatalf("provenance for %s = %#v", path, records)
		}
	}
}

func TestResolveReportsInheritedProviderConflictAndAcceptsExactCurrentReplacement(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	moduleRoots := map[string]string{
		"example.com/a": filepath.Join(root, "a"),
		"example.com/b": filepath.Join(root, "b"),
	}
	providers := map[string]string{"example.com/a": "example.smtp-a", "example.com/b": "example.smtp-b"}
	for modulePath, moduleRoot := range moduleRoots {
		writeModule(t, moduleRoot, modulePath)
		provider := providers[modulePath]
		writeFile(t, filepath.Join(moduleRoot, "plystra.yaml"), fmt.Sprintf("capabilities: {use: {email.send/v1: %s}}\n", provider))
		writePlugin(t, moduleRoot, "smtp", fmt.Sprintf("id: %s\nprovides: [email.send/v1]\n", provider))
		writeCapability(t, moduleRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	}
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/a v1.0.0
	example.com/b v1.0.0
)

replace example.com/a => ../a
replace example.com/b => ../b
`)
	manifestPath := filepath.Join(appRoot, "plystra.yaml")
	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1]}\n")
	options := applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})}

	_, err := applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, applicationmeta.ErrInheritedConflict) {
		t.Fatalf("Resolve conflict error = %v", err)
	}
	for _, required := range []string{
		`capabilities.use["email.send/v1"]`,
		"example.smtp-a",
		"example.smtp-b",
		"example.com/a@v1.0.0/plystra.yaml",
		"example.com/b@v1.0.0/plystra.yaml",
	} {
		if !strings.Contains(err.Error(), required) {
			t.Fatalf("conflict error omits %q: %v", required, err)
		}
	}

	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1], use: {email.send/v1: example.smtp-a}}\n")
	result, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve with current replacement: %v", err)
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp-a" {
		t.Fatalf("selected replacement Provider = %s, %t", provider, exists)
	}
	records := compositionProvenance(result.Composition().Provenance(), `capabilities.use["email.send/v1"]`)
	if len(records) != 2 {
		t.Fatalf("inherited conflict provenance = %#v", records)
	}
	selectedProviders := result.ResolutionEvidence().SelectedProviders()
	if len(selectedProviders) != 1 || selectedProviders[0].PluginID() != "example.smtp-a" || selectedProviders[0].SelectionReason() != resolutionevidence.ProviderSelectionCurrentProject || len(selectedProviders[0].SelectionSources()) != 1 || selectedProviders[0].SelectionSources()[0].ProjectModule() != "example.com/app" || selectedProviders[0].SelectionSources()[0].Source().Path() != "plystra.yaml" {
		t.Fatalf("current Provider replacement evidence = %#v", selectedProviders)
	}
}

func TestResolveRecordsEveryCompatibleInheritedProviderSelectionSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	aRoot := filepath.Join(root, "a")
	bRoot := filepath.Join(root, "b")
	for modulePath, moduleRoot := range map[string]string{"example.com/a": aRoot, "example.com/b": bRoot} {
		writeModule(t, moduleRoot, modulePath)
		writeFile(t, filepath.Join(moduleRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}}\n")
	}
	writePlugin(t, aRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, aRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require (
	example.com/a v1.0.0
	example.com/b v1.2.0
)

replace example.com/a => ../a
replace example.com/b => ../b
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities: {require: [email.send/v1]}\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	providers := result.ResolutionEvidence().SelectedProviders()
	if len(providers) != 1 || providers[0].PluginID() != "example.smtp" || providers[0].SelectionReason() != resolutionevidence.ProviderSelectionInherited {
		t.Fatalf("selected Provider = %#v", providers)
	}
	sources := providers[0].SelectionSources()
	if len(sources) != 2 || sources[0].ProjectModule() != "example.com/a" || sources[0].Source().Path() != "plystra.yaml" || sources[1].ProjectModule() != "example.com/b" || sources[1].Source().Path() != "plystra.yaml" {
		t.Fatalf("compatible inherited selection sources = %#v", sources)
	}
}

func TestResolveCurrentProviderRemovalRestoresUniqueProviderSelection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "smtp")
	writeModule(t, dependencyRoot, "example.com/smtp")
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}}\n")
	writePlugin(t, dependencyRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, dependencyRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/smtp v1.0.0

replace example.com/smtp => ../smtp
`)
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities: {require: [email.send/v1], use: {email.send/v1: null}}\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve with Provider removal: %v", err)
	}
	if len(result.Manifest().ProviderChoices()) != 0 {
		t.Fatalf("effective explicit Provider choices = %#v", result.Manifest().ProviderChoices())
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("automatic unique Provider = %s, %t", provider, exists)
	}
	records := compositionProvenance(result.Composition().Provenance(), `capabilities.use["email.send/v1"]`)
	if len(records) != 1 || len(records[0].Sources()) != 1 || !strings.Contains(records[0].Sources()[0], "example.com/smtp@v1.0.0/plystra.yaml") {
		t.Fatalf("inherited Provider provenance = %#v", records)
	}
	selectedProviders := result.ResolutionEvidence().SelectedProviders()
	if len(selectedProviders) != 1 || selectedProviders[0].SelectionReason() != resolutionevidence.ProviderSelectionSoleProvider || len(selectedProviders[0].SelectionSources()) != 0 {
		t.Fatalf("Provider removal automatic evidence = %#v", selectedProviders)
	}
}

func TestResolveConfigurationRemovalStillRunsFinalRequiredFieldValidation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	dependencyRoot := filepath.Join(root, "smtp")
	writeModule(t, dependencyRoot, "example.com/smtp")
	privateHost := "private-dependency.example"
	writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "capabilities: {use: {email.send/v1: example.smtp}}\nconfig: {example.smtp: {host: "+privateHost+"}}\n")
	writePlugin(t, dependencyRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\nconfig:\n  host: {type: string, required: true}\n")
	writeCapability(t, dependencyRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), `module example.com/app

go 1.26

require example.com/smtp v1.0.0

replace example.com/smtp => ../smtp
`)
	manifestPath := filepath.Join(appRoot, "plystra.yaml")
	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1]}\nconfig: {example.smtp: {host: null}}\n")
	options := applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"}),
	}

	_, err := applicationresolve.Resolve(t.Context(), options)
	if !errors.Is(err, kernelconfiguration.ErrMissingField) || strings.Contains(err.Error(), privateHost) {
		t.Fatalf("Resolve removed required field error = %v", err)
	}
	writeFile(t, manifestPath, "capabilities: {require: [email.send/v1]}\nconfig: {example.smtp: {host: current.example}}\n")
	result, err := applicationresolve.Resolve(t.Context(), options)
	if err != nil {
		t.Fatalf("Resolve replacement field: %v", err)
	}
	configured, exists := result.Manifest().Configuration("example.smtp")
	if !exists || !bytes.Contains(configured.YAML(), []byte("host: current.example")) {
		t.Fatalf("effective replacement configuration = %s, %t", configured.YAML(), exists)
	}
}

func TestResolveRejectsMalformedAndUnsafeDependencyProjectManifest(t *testing.T) {
	t.Run("malformed", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		writeFile(t, filepath.Join(dependencyRoot, "plystra.yaml"), "unknown: true\n")
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n\nreplace example.com/dependency => ../dependency\n")
		writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")

		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
		if !errors.Is(err, applicationresolve.ErrManifest) || !errors.Is(err, applicationmeta.ErrInvalidManifest) || !strings.Contains(err.Error(), "example.com/dependency@v1.2.3") || !strings.Contains(err.Error(), `unknown key "unknown"`) {
			t.Fatalf("Resolve malformed dependency error = %v", err)
		}
	})

	t.Run("symbolic", func(t *testing.T) {
		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		target := filepath.Join(root, "outside.yaml")
		writeFile(t, target, "{}\n")
		if err := os.Symlink(target, filepath.Join(dependencyRoot, "plystra.yaml")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/app\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n\nreplace example.com/dependency => ../dependency\n")
		writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "{}\n")

		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: appRoot, Environment: goEnvironment(map[string]string{"GOWORK": "off", "GOPROXY": "off"})})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) || !strings.Contains(err.Error(), "example.com/dependency") {
			t.Fatalf("Resolve unsafe dependency error = %v", err)
		}
	})
}

func TestResolveUsesActiveGoWorkspaceDependencySource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appRoot := filepath.Join(root, "app")
	providerRoot := filepath.Join(root, "providers")
	writeModule(t, providerRoot, "example.com/providers")
	writeFile(t, filepath.Join(providerRoot, "plystra.yaml"), "capabilities: {require: [email.send/v1]}\n")
	writePlugin(t, providerRoot, "smtp", "id: example.smtp\nprovides: [email.send/v1]\n")
	writeCapability(t, providerRoot, "smtp", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/workspace-app\n\ngo 1.26\n")
	writeFile(t, filepath.Join(appRoot, "plystra.yaml"), "capabilities:\n  require: [email.send/v1]\n")
	goWork := filepath.Join(root, "go.work")
	writeFile(t, goWork, "go 1.26\n\nuse (\n\t./app\n\t./providers\n)\n")

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:       appRoot,
		Environment: goEnvironment(map[string]string{"GOWORK": goWork, "GOPROXY": "off"}),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	plugins := result.Inventory().Plugins()
	if len(plugins) != 1 || plugins[0].ID() != "example.smtp" || plugins[0].ModuleRoot() != providerRoot || plugins[0].ModuleVersion() != "" || plugins[0].Source() != "example.com/providers@local/smtp/plugin.yaml" {
		t.Fatalf("workspace plugin = %#v, summaries %v", plugins, pluginSummaries(plugins))
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "email.send/v1"))
	if !exists || provider.String() != "example.smtp" {
		t.Fatalf("workspace provider = %s, %t", provider, exists)
	}
	modules := result.ResolutionEvidence().Modules()
	if len(modules) != 2 || modules[0].Path() != "example.com/workspace-app" || modules[0].Role() != resolutionevidence.ModuleRoleCurrent || modules[1].Path() != "example.com/providers" || modules[1].Role() != resolutionevidence.ModuleRoleDependency || !modules[1].Workspace() || modules[1].SelectedVersion() != "" || modules[1].Direct() || modules[1].Source().Module() != "example.com/providers" {
		t.Fatalf("workspace resolution evidence modules = %#v", modules)
	}
	if _, exists := modules[1].Replacement(); exists {
		t.Fatalf("workspace resolution evidence has replacement provenance: %#v", modules[1])
	}
	candidates := result.ResolutionEvidence().PluginCandidates()
	if len(candidates) != 1 || candidates[0].ID() != "example.smtp" || candidates[0].ModulePath() != "example.com/providers" || candidates[0].ModuleRole() != resolutionevidence.ModuleRoleDependency || candidates[0].Path() != "smtp" || candidates[0].Local() || candidates[0].Source().Module() != "example.com/providers" || candidates[0].Source().Path() != "smtp/plugin.yaml" {
		t.Fatalf("workspace resolution evidence Plugin candidates = %#v", candidates)
	}
	selectedPlugins := result.ResolutionEvidence().SelectedPlugins()
	if len(selectedPlugins) != 1 || selectedPlugins[0].ID() != "example.smtp" || selectedPlugins[0].ModulePath() != "example.com/providers" || selectedPlugins[0].ModuleVersion() != "" || selectedPlugins[0].ModuleRole() != resolutionevidence.ModuleRoleDependency || selectedPlugins[0].Source() != candidates[0].Source() {
		t.Fatalf("workspace resolution evidence selected Plugins = %#v", selectedPlugins)
	}
	if reasons := selectedPlugins[0].Reasons(); len(reasons) != 1 || reasons[0].Kind() != resolutionevidence.PluginSelectionProvider || reasons[0].Capability() != "email.send/v1" {
		t.Fatalf("workspace selected Plugin reasons = %#v", reasons)
	}
	providerCandidates := result.ResolutionEvidence().ProviderCandidates()
	if len(providerCandidates) != 1 || providerCandidates[0].Capability() != "email.send/v1" || providerCandidates[0].PluginID() != "example.smtp" || providerCandidates[0].ProjectModule() != "example.com/providers" || providerCandidates[0].Rejected() || providerCandidates[0].Source().Module() != "example.com/providers" || providerCandidates[0].Source().Path() != "smtp/capabilities/email.send/v1/capability.yaml" {
		t.Fatalf("workspace Provider candidates = %#v", providerCandidates)
	}
	selectedProviders := result.ResolutionEvidence().SelectedProviders()
	if len(selectedProviders) != 1 || selectedProviders[0].PluginID() != "example.smtp" || selectedProviders[0].SelectionReason() != resolutionevidence.ProviderSelectionSoleProvider || selectedProviders[0].ProviderSource() != providerCandidates[0].Source() || len(selectedProviders[0].SelectionSources()) != 0 {
		t.Fatalf("workspace selected Provider evidence = %#v", selectedProviders)
	}
	assertStaticAssemblyMatchesResolution(t, result)
	requirementConfiguration := resolvedConfigurationField(t, result, `capabilities.require["email.send/v1"]`)
	if requirementConfiguration.Owner() != resolutionevidence.ConfigurationOwnerRoot || len(requirementConfiguration.Contributors()) != 2 || requirementConfiguration.Contributors()[0].Owner() != resolutionevidence.ConfigurationOwnerDependency || requirementConfiguration.Contributors()[0].Sources()[0].Module() != "example.com/providers" || requirementConfiguration.Contributors()[1].Owner() != resolutionevidence.ConfigurationOwnerRoot {
		t.Fatalf("workspace configuration evidence = %#v", requirementConfiguration)
	}
}

func TestResolveExecutesSelectedFilesystemGenerationExtension(t *testing.T) {
	root := t.TempDir()
	temporaryParent := t.TempDir()
	cliRoot := repositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	goMod := fmt.Sprintf(
		"module example.com/extension-app\n\ngo 1.26\n\nrequire (\n\tgithub.com/plystra/cli v0.0.0\n\tgithub.com/plystra/kernel v0.0.0\n\tgo.yaml.in/yaml/v3 v3.0.4 // indirect\n\tgolang.org/x/mod v0.38.0 // indirect\n)\n\nreplace github.com/plystra/cli => %s\n\nreplace github.com/plystra/kernel => %s\n",
		strconv.Quote(filepath.ToSlash(cliRoot)),
		strconv.Quote(filepath.ToSlash(kernelRoot)),
	)
	writeFile(t, filepath.Join(root, "go.mod"), goMod)
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("ReadFile(go.sum): %v", err)
	}
	writeFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeFile(t, filepath.Join(root, "plystra.yaml"), "capabilities:\n  require: [order.create/v1]\n  aliases:\n    orders.submit/v1: order.create/v1\n")
	writePlugin(t, root, "business", "id: example.business\nprovides: [order.create/v1]\n")
	writePlugin(t, root, "authn", `id: example.authn
provides: [authn.session.verify/v1]
generation:
  api: v1
  package: ./generation
  activations:
    - namespace: authn
      capability: authn.session.verify/v1
`)
	writePlugin(t, root, "audit", "id: example.audit\nprovides: [audit.write/v1]\n")
	writeCapability(t, root, "business", "order.create/v1", `id: order.create/v1
request: {}
response: {}
errors: []
extensions:
  authn: {authenticated: true}
`)
	writeCapability(t, root, "authn", "authn.session.verify/v1", "id: authn.session.verify/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCapability(t, root, "audit", "audit.write/v1", "id: audit.write/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(root, "authn", "generation", "generate.go"), realExtensionSource)
	before := snapshotTree(t, root)

	result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start:            root,
		Environment:      goEnvironment(map[string]string{"GOWORK": "off"}),
		CompileTimeout:   2 * time.Minute,
		ExecutionTimeout: 10 * time.Second,
		TemporaryParent:  temporaryParent,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	assertResolvedConfigurationProvenance(t, result)
	generated := result.Resolution().GeneratedRequirements()
	if result.Resolution().Passes() != 3 || len(generated) != 1 || generated[0].PluginID() != "example.authn" || generated[0].Capability().String() != "audit.write/v1" {
		t.Fatalf("extension resolution = passes %d, generated %#v", result.Resolution().Passes(), generated)
	}
	provider, exists := result.Resolution().Context().SelectedProvider(parseGenerationCapability(t, "audit.write/v1"))
	if !exists || provider.String() != "example.audit" {
		t.Fatalf("generated audit provider = %s, %t", provider, exists)
	}
	evidenceRequirements := result.ResolutionEvidence().Requirements()
	if len(evidenceRequirements) != 3 || evidenceRequirements[0].Capability() != "audit.write/v1" || evidenceRequirements[1].Capability() != "authn.session.verify/v1" || evidenceRequirements[2].Capability() != "order.create/v1" {
		t.Fatalf("resolution evidence requirements = %#v", evidenceRequirements)
	}
	auditSources := evidenceRequirements[0].Sources()
	if len(auditSources) != 1 || auditSources[0].Kind() != providerresolution.RequirementGenerationRule || auditSources[0].ProjectModule() != "example.com/extension-app" || auditSources[0].Source().Path() != "authn/plugin.yaml" || auditSources[0].PluginID() != "example.authn" || auditSources[0].Namespace() != "authn" || auditSources[0].SourceCapability() != "order.create/v1" || auditSources[0].RuleID() != "authn.require-audit" {
		t.Fatalf("generation-rule requirement source = %#v", auditSources)
	}
	authnSources := evidenceRequirements[1].Sources()
	if len(authnSources) != 1 || authnSources[0].Kind() != providerresolution.RequirementActivation || authnSources[0].ProjectModule() != "example.com/extension-app" || authnSources[0].Source().Path() != "plystra.yaml" || authnSources[0].Namespace() != "authn" || authnSources[0].SourceCapability() != "order.create/v1" {
		t.Fatalf("activation requirement source = %#v", authnSources)
	}
	orderSources := evidenceRequirements[2].Sources()
	if len(orderSources) != 2 || orderSources[0].Kind() != providerresolution.RequirementAliasTarget || orderSources[0].Alias() != "orders.submit/v1" || orderSources[0].ProjectModule() != "example.com/extension-app" || orderSources[0].Source().Path() != "plystra.yaml" || orderSources[1].Kind() != providerresolution.RequirementDeclaration || orderSources[1].ProjectModule() != "example.com/extension-app" || orderSources[1].Source().Path() != "plystra.yaml" {
		t.Fatalf("declaration requirement source = %#v", orderSources)
	}
	activations := result.ResolutionEvidence().GenerationActivations()
	if len(activations) != 1 || activations[0].Namespace() != "authn" || activations[0].SourceCapability() != "order.create/v1" || activations[0].ActivationCapability() != "authn.session.verify/v1" || activations[0].PluginID() != "example.authn" || activations[0].ProjectModule() != "example.com/extension-app" || len(activations[0].Causes()) != 1 || activations[0].Causes()[0].Source().Path() != "plystra.yaml" {
		t.Fatalf("generation activation evidence = %#v", activations)
	}
	generatedEvidence := result.ResolutionEvidence().GeneratedRequirements()
	if len(generatedEvidence) != 1 || generatedEvidence[0].Capability() != "audit.write/v1" || generatedEvidence[0].SourceCapability() != "order.create/v1" || generatedEvidence[0].ActivationCapability() != "authn.session.verify/v1" || generatedEvidence[0].Namespace() != "authn" || generatedEvidence[0].PluginID() != "example.authn" || generatedEvidence[0].ProjectModule() != "example.com/extension-app" || generatedEvidence[0].RuleID() != "authn.require-audit" || generatedEvidence[0].Source().Path() != "authn/plugin.yaml" || generatedEvidence[0].Source().Kind() != "generation-rule" {
		t.Fatalf("generated requirement evidence = %#v", generatedEvidence)
	}
	aliasEvidence := result.ResolutionEvidence().CapabilityAliases()
	if len(aliasEvidence) != 1 || aliasEvidence[0].ID() != "orders.submit/v1" || aliasEvidence[0].Target() != "order.create/v1" || aliasEvidence[0].TargetContractDigest() == "" || aliasEvidence[0].TargetExposure() != (generation.Exposure{Go: true}) || aliasEvidence[0].Exposure() != (generation.Exposure{Go: true}) || aliasEvidence[0].ValidationOutcome() != resolutionevidence.CapabilityAliasValidationValid {
		t.Fatalf("Capability Alias evidence = %#v", aliasEvidence)
	}
	if narrowing, exists := aliasEvidence[0].ExposureNarrowing(); exists || narrowing != (generation.Exposure{}) {
		t.Fatalf("Capability Alias exposure narrowing = %#v, %t", narrowing, exists)
	}
	aliasSources := aliasEvidence[0].Sources()
	if len(aliasSources) != 2 || aliasSources[0].Kind() != generation.AliasSourceApplication || aliasSources[0].ProjectModule() != "example.com/extension-app" || aliasSources[0].ActivationCapability() != "" || aliasSources[0].Source().Module() != "example.com/extension-app" || aliasSources[0].Source().Path() != "plystra.yaml" || aliasSources[0].Source().Kind() != "alias-target" || aliasSources[1].Kind() != generation.AliasSourceGenerationExtension || aliasSources[1].ProjectModule() != "example.com/extension-app" || aliasSources[1].PluginID() != "example.authn" || aliasSources[1].ContributionID() != "authn.order-shortcut" || aliasSources[1].Namespace() != "authn" || aliasSources[1].SourceCapability() != "order.create/v1" || aliasSources[1].ActivationCapability() != "authn.session.verify/v1" || aliasSources[1].Source().Module() != "example.com/extension-app" || aliasSources[1].Source().Path() != "authn/plugin.yaml" || aliasSources[1].Source().Kind() != "generation-alias-contribution" {
		t.Fatalf("Capability Alias sources = %#v", aliasSources)
	}
	if publicExposures := result.ResolutionEvidence().PublicExposures(); result.ResolutionEvidence().PublicExposureCount() != 0 || len(publicExposures) != 0 {
		t.Fatalf("Go-only Alias was recorded as public exposure: %#v", publicExposures)
	}
	entries, err := os.ReadDir(temporaryParent)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary extension artifacts = %v, %v", entries, err)
	}
	if after := snapshotTree(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("extension resolution mutated application tree:\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func TestResolveRejectsMissingUnsafeAndChangingManifest(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/missing")
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrNotFound) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/directory")
		if err := os.Mkdir(filepath.Join(root, "plystra.yaml"), 0o755); err != nil {
			t.Fatalf("Mkdir(plystra.yaml): %v", err)
		}
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("symbolic", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/symbolic")
		target := filepath.Join(t.TempDir(), "application.yaml")
		writeFile(t, target, "{}\n")
		if err := os.Symlink(target, filepath.Join(root, "plystra.yaml")); err != nil {
			t.Skipf("symbolic links unavailable: %v", err)
		}
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, projectlocate.ErrInvalidManifest) {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		root := t.TempDir()
		writeModule(t, root, "example.com/oversized")
		writeFile(t, filepath.Join(root, "plystra.yaml"), strings.Repeat(" ", applicationmeta.MaximumSize+1))
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root})
		if !errors.Is(err, applicationresolve.ErrManifest) || !errors.Is(err, applicationresolve.ErrUnsafeManifest) || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("Resolve error = %v", err)
		}
	})

	t.Run("changed before completion", func(t *testing.T) {
		root := t.TempDir()
		appRoot := filepath.Join(root, "app")
		dependencyRoot := filepath.Join(root, "dependency")
		writeModule(t, dependencyRoot, "example.com/dependency")
		writeFile(t, filepath.Join(appRoot, "go.mod"), "module example.com/changing\n\ngo 1.26\n\nrequire example.com/dependency v1.2.3\n")
		manifestPath := filepath.Join(appRoot, "plystra.yaml")
		writeFile(t, manifestPath, "{}\n")
		_, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
			Start:     appRoot,
			GoCommand: os.Args[0],
			Environment: goEnvironment(map[string]string{
				"GOWORK":                             "off",
				"PLYSTRA_APPLICATION_RESOLVE_HELPER": "change-manifest",
				"PLYSTRA_APPLICATION_MANIFEST":       manifestPath,
				"PLYSTRA_APPLICATION_MODULE_ROOT":    dependencyRoot,
			}),
		})
		if !errors.Is(err, applicationresolve.ErrResolve) || !errors.Is(err, applicationresolve.ErrConcurrentChange) || !strings.Contains(err.Error(), "plystra.yaml") {
			t.Fatalf("Resolve error = %v", err)
		}
	})
}

func runResolveHelper(mode string) int {
	if mode != "change-manifest" {
		return 9
	}
	want := []string{"list", "-m", "-json", "-mod=readonly", "all"}
	if len(os.Args) != len(want)+1 {
		return 10
	}
	for index, value := range want {
		if os.Args[index+1] != value {
			return 11
		}
	}
	if err := os.WriteFile(os.Getenv("PLYSTRA_APPLICATION_MANIFEST"), []byte("timeouts: {}\n"), 0o644); err != nil {
		return 12
	}
	applicationRoot, err := os.Getwd()
	if err != nil {
		return 13
	}
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]any{
		"Path":  "example.com/changing",
		"Main":  true,
		"Dir":   applicationRoot,
		"GoMod": filepath.Join(applicationRoot, "go.mod"),
	}); err != nil {
		return 14
	}
	root := os.Getenv("PLYSTRA_APPLICATION_MODULE_ROOT")
	if err := encoder.Encode(map[string]any{
		"Path":    "example.com/dependency",
		"Version": "v1.2.3",
		"Dir":     root,
		"GoMod":   filepath.Join(root, "go.mod"),
	}); err != nil {
		return 15
	}
	return 0
}

func writeModule(t testing.TB, root, modulePath string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "go.mod"), "module "+modulePath+"\n\ngo 1.26\n")
}

func writePlugin(t testing.TB, moduleRoot, name, manifest string) {
	t.Helper()
	writeFile(t, filepath.Join(moduleRoot, name, "plugin.yaml"), manifest)
}

func writeCapability(t testing.TB, moduleRoot, plugin, value, source string) {
	t.Helper()
	identifier, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("capabilityid.Parse(%s): %v", value, err)
	}
	writeFile(t, filepath.Join(moduleRoot, plugin, "capabilities", filepath.FromSlash(identifier.Name()), "v"+strconv.FormatUint(identifier.Major(), 10), "capability.yaml"), withQuerySemantics(source))
}

func withQuerySemantics(source string) string {
	if strings.Contains(source, "\nsemantics:") {
		return source
	}
	if !strings.HasSuffix(source, "\n") {
		source += "\n"
	}
	return source + querySemanticsYAML
}

const querySemanticsYAML = `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func writeFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", filepath.Dir(name), err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", name, err)
	}
}

func parseGenerationCapability(t testing.TB, value string) generation.CapabilityID {
	t.Helper()
	identifier, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("generation.ParseCapabilityID(%s): %v", value, err)
	}
	return identifier
}

func configurationBindingIDs(bindings []configurationresolve.Binding) []string {
	result := make([]string, len(bindings))
	for index, binding := range bindings {
		result[index] = binding.PluginID()
	}
	return result
}

func applicationRequirementIDs(manifest applicationmeta.Manifest) []string {
	requirements := manifest.Requirements()
	result := make([]string, len(requirements))
	for index, requirement := range requirements {
		result[index] = requirement.ID().String()
	}
	return result
}

func applicationExposureIDs(manifest applicationmeta.Manifest) []string {
	exposures := manifest.HTTPExposures()
	result := make([]string, len(exposures))
	for index, exposure := range exposures {
		result[index] = exposure.ID().String()
	}
	return result
}

func compositionProvenance(values []applicationmeta.Provenance, path string) []applicationmeta.Provenance {
	var result []applicationmeta.Provenance
	for _, value := range values {
		if value.Path() == path {
			result = append(result, value)
		}
	}
	return result
}

func pluginSummaries(plugins []plugininventory.Plugin) []string {
	result := make([]string, len(plugins))
	for index, plugin := range plugins {
		version := plugin.ModuleVersion()
		if version == "" {
			version = "local"
		}
		result[index] = fmt.Sprintf("%s:%s@%s:%s:%t", plugin.ID(), plugin.ModulePath(), version, plugin.Path(), plugin.Local())
	}
	return result
}

func assertResolvedConfigurationProvenance(t testing.TB, result applicationresolve.Result) {
	t.Helper()
	provenance, exists := result.Resolution().Context().ConfigurationProvenance()
	selection := result.ConfigurationSelection()
	if !exists {
		t.Fatal("filesystem-backed resolution omitted configuration provenance")
	}
	rootDigest, err := applicationgen.ConfigurationDigest(result.RootConfigurationData())
	if err != nil {
		t.Fatalf("ConfigurationDigest(root): %v", err)
	}
	if provenance.Mode() != generation.ConfigurationMode(selection.Mode()) || provenance.Environment() != selection.Environment() || provenance.RootPath() != "plystra.yaml" || provenance.RootDigest() != rootDigest || provenance.SelectedPath() != selection.Path() || provenance.SelectedDigest() != selection.Digest() || provenance.DependencyCompositionDigest() != result.Composition().DependencyDigest() {
		t.Fatalf("configuration provenance = mode %q environment %q root %q/%q selected %q/%q dependency %q; selection = mode %q environment %q path %q digest %q", provenance.Mode(), provenance.Environment(), provenance.RootPath(), provenance.RootDigest(), provenance.SelectedPath(), provenance.SelectedDigest(), provenance.DependencyCompositionDigest(), selection.Mode(), selection.Environment(), selection.Path(), selection.Digest())
	}
	evidenceSelection, evidenceExists := result.ResolutionEvidence().ConfigurationSelection()
	if !evidenceExists || evidenceSelection.Mode() != provenance.Mode() || evidenceSelection.Environment() != provenance.Environment() || evidenceSelection.RootPath() != provenance.RootPath() || evidenceSelection.RootDigest() != provenance.RootDigest() || evidenceSelection.SelectedPath() != provenance.SelectedPath() || evidenceSelection.SelectedDigest() != provenance.SelectedDigest() || evidenceSelection.DependencyCompositionDigest() != provenance.DependencyCompositionDigest() {
		t.Fatalf("resolution-evidence configuration selection = %#v, %t; context provenance = %#v", evidenceSelection, evidenceExists, provenance)
	}
	if result.Resolution().Context().Digest() == result.Resolution().Context().BuildModelDigest() {
		t.Fatal("filesystem configuration provenance did not enter the extension context digest")
	}
}

func assertStaticAssemblyMatchesResolution(t testing.TB, result applicationresolve.Result) {
	t.Helper()
	evidence := result.ResolutionEvidence()
	assembly, exists := evidence.StaticAssembly()
	if !exists {
		t.Fatal("filesystem-backed resolution omitted static assembly evidence")
	}
	configurationBindings := result.Configurations().Bindings()
	plugins := assembly.Plugins()
	if evidence.AssemblyPluginCount() != len(configurationBindings) || len(plugins) != len(configurationBindings) {
		t.Fatalf("assembly Plugin count = evidence %d values %d configurations %d", evidence.AssemblyPluginCount(), len(plugins), len(configurationBindings))
	}
	selectedPlugins := make(map[string]resolutionevidence.SelectedPlugin, evidence.SelectedPluginCount())
	for _, plugin := range evidence.SelectedPlugins() {
		selectedPlugins[plugin.ID()] = plugin
	}
	providerBindings := make(map[string][]string)
	ordinaryProviders := 0
	selectedProviderByCapability := make(map[string]resolutionevidence.SelectedProvider, evidence.SelectedProviderCount())
	for _, provider := range evidence.SelectedProviders() {
		selectedProviderByCapability[provider.Capability()] = provider
		if !provider.Intrinsic() {
			ordinaryProviders++
			providerBindings[provider.PluginID()] = append(providerBindings[provider.PluginID()], provider.Capability())
		}
	}
	for index, binding := range configurationBindings {
		plugin := plugins[index]
		selected, selectedExists := selectedPlugins[binding.PluginID()]
		inventoryPlugin, inventoryExists := result.Inventory().ByID(binding.PluginID())
		if !selectedExists || !inventoryExists || plugin.PluginID() != binding.PluginID() || plugin.ProjectModule() != binding.ModulePath() || plugin.ModuleVersion() != binding.ModuleVersion() || plugin.ImportPath() != binding.ImportPath() || plugin.Source() != selected.Source() || plugin.ConstructorOrder() != index+1 || !plugin.LifecycleProbe() {
			t.Fatalf("assembly Plugin %d = %#v; configuration %#v selected %#v/%t inventory %t", index, plugin, binding, selected, selectedExists, inventoryExists)
		}
		required := inventoryPlugin.Requires()
		wantClients := make([]string, len(required))
		for requiredIndex, capability := range required {
			wantClients[requiredIndex] = capability.String()
		}
		sort.Strings(wantClients)
		wantBindings := append([]string(nil), providerBindings[binding.PluginID()]...)
		sort.Strings(wantBindings)
		if !slices.Equal(plugin.RequiredClients(), wantClients) || !slices.Equal(plugin.ProviderBindings(), wantBindings) {
			t.Fatalf("assembly Plugin %q clients/providers = %v/%v; want %v/%v", plugin.PluginID(), plugin.RequiredClients(), plugin.ProviderBindings(), wantClients, wantBindings)
		}
	}

	bindings := assembly.Bindings()
	if evidence.AssemblyBindingCount() != len(intrinsiccatalog.Definitions())+ordinaryProviders || len(bindings) != evidence.AssemblyBindingCount() {
		t.Fatalf("assembly binding count = evidence %d values %d; want %d intrinsics plus %d ordinary", evidence.AssemblyBindingCount(), len(bindings), len(intrinsiccatalog.Definitions()), ordinaryProviders)
	}
	byCapability := make(map[string]resolutionevidence.AssemblyBinding, len(bindings))
	for index, binding := range bindings {
		if index > 0 && bindings[index-1].Capability() >= binding.Capability() {
			t.Fatalf("assembly bindings are not in canonical order: %#v", bindings)
		}
		byCapability[binding.Capability()] = binding
	}
	for _, definition := range intrinsiccatalog.Definitions() {
		binding, found := byCapability[definition.ID().String()]
		selected, required := selectedProviderByCapability[definition.ID().String()]
		if !found || !binding.Intrinsic() || binding.Required() != required || binding.ContractDigest() != definition.ContractDigest() || binding.PluginID() != "" || binding.ProjectModule() != "" || binding.SelectionReason() != resolutionevidence.ProviderSelectionIntrinsic || binding.ProviderSource().Module() != "github.com/plystra/kernel" || binding.ProviderSource().Kind() != "intrinsic-provider" || required && (!selected.Intrinsic() || selected.ContractDigest() != binding.ContractDigest()) {
			t.Fatalf("intrinsic assembly binding %s = %#v/%t selected %#v/%t", definition.ID(), binding, found, selected, required)
		}
	}
	for capability, selected := range selectedProviderByCapability {
		if selected.Intrinsic() {
			continue
		}
		binding, found := byCapability[capability]
		if !found || binding.Intrinsic() || !binding.Required() || binding.ContractDigest() != selected.ContractDigest() || binding.PluginID() != selected.PluginID() || binding.ProjectModule() != selected.ProjectModule() || binding.SelectionReason() != selected.SelectionReason() || binding.ProviderSource() != selected.ProviderSource() {
			t.Fatalf("ordinary assembly binding %s = %#v/%t selected %#v", capability, binding, found, selected)
		}
	}
}

func resolvedConfigurationField(t testing.TB, result applicationresolve.Result, path string) resolutionevidence.ConfigurationField {
	t.Helper()
	for _, field := range result.ResolutionEvidence().ConfigurationFields() {
		if field.Path() == path {
			return field
		}
	}
	t.Fatalf("configuration field %s is absent from %#v", path, result.ResolutionEvidence().ConfigurationFields())
	return resolutionevidence.ConfigurationField{}
}

type treeEntry struct {
	path     string
	mode     fs.FileMode
	modified time.Time
	data     []byte
}

func snapshotTree(t testing.TB, root string) []treeEntry {
	t.Helper()
	var result []treeEntry
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		state := treeEntry{path: filepath.ToSlash(relative), mode: info.Mode()}
		if info.Mode().IsRegular() {
			state.modified = info.ModTime()
			state.data, err = os.ReadFile(name)
			if err != nil {
				return err
			}
		}
		result = append(result, state)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return result
}

func goEnvironment(overrides map[string]string) []string {
	defaults := map[string]string{
		"GOENV":       "off",
		"GOTOOLCHAIN": "local",
	}
	for key, value := range overrides {
		defaults[strings.ToUpper(key)] = value
	}
	environment := make([]string, 0, len(os.Environ())+len(defaults))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := defaults[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+defaults[key])
	}
	return environment
}

func repositoryRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository root): %v", err)
	}
	return root
}

const realExtensionSource = `package extension

import (
	"fmt"

	generation "github.com/plystra/cli/generation/v1"
)

func Generate(context generation.GenerationContext) (generation.Output, error) {
	provenance, exists := context.ConfigurationProvenance()
	if !exists || provenance.Mode() != generation.ConfigurationModeDefault || provenance.Environment() != "" || provenance.RootPath() != "plystra.yaml" || provenance.SelectedPath() != "plystra.yaml" || provenance.RootDigest() == "" || provenance.SelectedDigest() != provenance.RootDigest() || provenance.DependencyCompositionDigest() == "" {
		return generation.Output{}, fmt.Errorf("invalid configuration provenance: present=%t mode=%s environment=%q root=%q selected=%q", exists, provenance.Mode(), provenance.Environment(), provenance.RootPath(), provenance.SelectedPath())
	}
	order, _ := generation.ParseCapabilityID("order.create/v1")
	audit, _ := generation.ParseCapabilityID("audit.write/v1")
	alias, _ := generation.ParseCapabilityID("orders.submit/v1")
	if _, exists := context.Capability(order); !exists {
		return generation.Output{}, nil
	}
	return generation.Output{
		Requirements: []generation.Requirement{{
			RuleID: "authn.require-audit", Namespace: "authn", Source: order, Capability: audit,
		}},
		AliasContributions: []generation.CapabilityAliasContribution{{
			ID: "authn.order-shortcut", Namespace: "authn", Source: order, Alias: alias, Target: order,
		}},
	}, nil
}
`
