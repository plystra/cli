// Package changerequest parses the bounded declarative change request shared
// by compound CLI mutations.
package changerequest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/token"
	"io"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/moduleargument"
	"github.com/plystra/cli/internal/resourcename"
	"golang.org/x/mod/module"
)

const (
	// SchemaV1 is the canonical change-request schema.
	SchemaV1 = "plystra.change/v1"

	// MaximumRequestBytes bounds one encoded request before JSON parsing.
	MaximumRequestBytes = 1 << 20
	// MaximumOperations bounds the declarative operation list.
	MaximumOperations = 1_024
	// MaximumJSONDepth bounds nested request values.
	MaximumJSONDepth = 64
	// MaximumJSONNodes bounds recursive JSON work.
	MaximumJSONNodes = 65_536
	// MaximumStringBytes bounds scalar request strings and object keys.
	MaximumStringBytes = 4_096
	// MaximumPackageBytes bounds a Project-relative implementation package.
	MaximumPackageBytes = 1_024
)

// ErrInvalid reports a malformed or semantically contradictory change request.
var ErrInvalid = errors.New("invalid change request")

// Kind is one installed declarative change operation.
type Kind string

const (
	KindInterfaceCreate         Kind = "interface_create"
	KindImplementationCreate    Kind = "implementation_create"
	KindDependency              Kind = "dependency"
	KindConfiguration           Kind = "configuration"
	KindInterfaceRoot           Kind = "interface_root"
	KindImplementationSelection Kind = "implementation_selection"
	KindResourceSelection       Kind = "resource_selection"
)

// Semantics is the first-version Interface intent.
type Semantics string

const (
	SemanticsQuery   Semantics = "query"
	SemanticsCommand Semantics = "command"
)

// DependencyAction is an ordinary Go Module mutation.
type DependencyAction string

const (
	DependencyAdd    DependencyAction = "add"
	DependencyRemove DependencyAction = "remove"
	DependencyUpdate DependencyAction = "update"
)

// ConfigurationAction changes one exact owner/path value.
type ConfigurationAction string

const (
	ConfigurationSet    ConfigurationAction = "set"
	ConfigurationRemove ConfigurationAction = "remove"
)

// RootAction changes one current-project root.
type RootAction string

const (
	RootPresent RootAction = "present"
	RootAbsent  RootAction = "absent"
)

// RootKind identifies the two supported Interface root collections.
type RootKind string

const (
	RootRequire RootKind = "require"
	RootExpose  RootKind = "expose"
)

// SelectionAction changes one exact selection target.
type SelectionAction string

const (
	SelectionSet    SelectionAction = "set"
	SelectionRemove SelectionAction = "remove"
)

// InterfaceCreate is one Interface scaffold request.
type InterfaceCreate struct {
	name      string
	semantics Semantics
}

// Name returns the normalized exact first-version Interface ID.
func (v InterfaceCreate) Name() string { return v.name }

// Semantics returns the requested Interface intent.
func (v InterfaceCreate) Semantics() Semantics { return v.semantics }

// ImplementationCreate is one Implementation scaffold request.
type ImplementationCreate struct {
	contract    string
	packagePath string
}

// Contract returns the exact visible or same-request contract ID.
func (v ImplementationCreate) Contract() string { return v.contract }

// Package returns the safe Project-relative package path.
func (v ImplementationCreate) Package() string { return v.packagePath }

// Dependency is one ordinary Go Module request.
type Dependency struct {
	action     DependencyAction
	module     string
	modulePath string
}

// Action returns the dependency action.
func (v Dependency) Action() DependencyAction { return v.action }

// Module returns the exact module path or query supplied for the action.
func (v Dependency) Module() string { return v.module }

// Configuration is one typed configuration request.
type Configuration struct {
	action   ConfigurationAction
	owner    string
	path     string
	value    []byte
	hasValue bool
}

// String keeps private requested values out of ordinary formatting.
func (Configuration) String() string { return "<redacted-change-configuration>" }

// GoString keeps private requested values out of Go-syntax formatting.
func (v Configuration) GoString() string { return v.String() }

// Format keeps private requested values out of every formatting verb.
func (v Configuration) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, v.String()) }

// Action returns the configuration action.
func (v Configuration) Action() ConfigurationAction { return v.action }

// Owner returns the exact constructor or Resource owner.
func (v Configuration) Owner() string { return v.owner }

// Path returns the exact typed configuration path.
func (v Configuration) Path() string { return v.path }

// Value returns the canonical JSON value for a set operation.
func (v Configuration) Value() ([]byte, bool) {
	if !v.hasValue {
		return nil, false
	}
	return append([]byte(nil), v.value...), true
}

// InterfaceRoot is one required or exposed Interface root request.
type InterfaceRoot struct {
	action      RootAction
	interfaceID string
	root        RootKind
}

// Action returns the root action.
func (v InterfaceRoot) Action() RootAction { return v.action }

// Interface returns the exact Interface ID.
func (v InterfaceRoot) Interface() string { return v.interfaceID }

// Root returns require or expose.
func (v InterfaceRoot) Root() RootKind { return v.root }

// ImplementationSelection is one Interface constructor selection request.
type ImplementationSelection struct {
	action         SelectionAction
	interfaceID    string
	constructor    string
	hasConstructor bool
}

// Action returns the selection action.
func (v ImplementationSelection) Action() SelectionAction { return v.action }

// Interface returns the exact Interface ID.
func (v ImplementationSelection) Interface() string { return v.interfaceID }

// Constructor returns the exact constructor for a set operation.
func (v ImplementationSelection) Constructor() (string, bool) {
	if !v.hasConstructor {
		return "", false
	}
	return v.constructor, true
}

// ResourceSelection is one named Resource-provider selection request.
type ResourceSelection struct {
	action      SelectionAction
	instance    string
	provider    string
	hasProvider bool
}

// Action returns the selection action.
func (v ResourceSelection) Action() SelectionAction { return v.action }

// Instance returns the exact named Resource instance.
func (v ResourceSelection) Instance() string { return v.instance }

// Provider returns the exact provider constructor for a set operation.
func (v ResourceSelection) Provider() (string, bool) {
	if !v.hasProvider {
		return "", false
	}
	return v.provider, true
}

// Operation is one normalized declarative change operation.
type Operation struct {
	kind                    Kind
	interfaceCreate         *InterfaceCreate
	implementationCreate    *ImplementationCreate
	dependency              *Dependency
	configuration           *Configuration
	interfaceRoot           *InterfaceRoot
	implementationSelection *ImplementationSelection
	resourceSelection       *ResourceSelection
	document                any
	targetKey               string
}

// String keeps private configuration payloads out of ordinary formatting.
func (Operation) String() string { return "<redacted-change-operation>" }

// GoString keeps private configuration payloads out of Go-syntax formatting.
func (o Operation) GoString() string { return o.String() }

// Format keeps private configuration payloads out of every formatting verb.
func (o Operation) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, o.String()) }

// Kind returns the operation kind.
func (o Operation) Kind() Kind { return o.kind }

// InterfaceCreate returns the Interface scaffold payload.
func (o Operation) InterfaceCreate() (InterfaceCreate, bool) {
	if o.interfaceCreate == nil {
		return InterfaceCreate{}, false
	}
	return *o.interfaceCreate, true
}

// ImplementationCreate returns the Implementation scaffold payload.
func (o Operation) ImplementationCreate() (ImplementationCreate, bool) {
	if o.implementationCreate == nil {
		return ImplementationCreate{}, false
	}
	return *o.implementationCreate, true
}

// Dependency returns the dependency payload.
func (o Operation) Dependency() (Dependency, bool) {
	if o.dependency == nil {
		return Dependency{}, false
	}
	return *o.dependency, true
}

// Configuration returns the configuration payload with a defensive value.
func (o Operation) Configuration() (Configuration, bool) {
	if o.configuration == nil {
		return Configuration{}, false
	}
	value := *o.configuration
	value.value = append([]byte(nil), value.value...)
	return value, true
}

// InterfaceRoot returns the root payload.
func (o Operation) InterfaceRoot() (InterfaceRoot, bool) {
	if o.interfaceRoot == nil {
		return InterfaceRoot{}, false
	}
	return *o.interfaceRoot, true
}

// ImplementationSelection returns the Interface selection payload.
func (o Operation) ImplementationSelection() (ImplementationSelection, bool) {
	if o.implementationSelection == nil {
		return ImplementationSelection{}, false
	}
	return *o.implementationSelection, true
}

// ResourceSelection returns the Resource selection payload.
func (o Operation) ResourceSelection() (ResourceSelection, bool) {
	if o.resourceSelection == nil {
		return ResourceSelection{}, false
	}
	return *o.resourceSelection, true
}

// Request is one immutable normalized change request.
type Request struct {
	operations    []Operation
	canonicalJSON []byte
	digest        string
	prepared      bool
}

// String keeps private requested intent out of ordinary formatting.
func (Request) String() string { return "<redacted-change-request>" }

// GoString keeps private requested intent out of Go-syntax formatting.
func (r Request) GoString() string { return r.String() }

// Format keeps private requested intent out of every formatting verb.
func (r Request) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, r.String()) }

// canonicalNumber keeps a JSON number unquoted while preserving its exact
// decimal value during canonical encoding.
type canonicalNumber string

func (n canonicalNumber) MarshalJSON() ([]byte, error) { return []byte(n), nil }

// Parse validates and normalizes one bounded JSON request.
func Parse(data []byte) (Request, error) {
	if len(data) == 0 || len(data) > MaximumRequestBytes {
		return Request{}, invalidf("request must contain between 1 and %d bytes", MaximumRequestBytes)
	}
	if !utf8.Valid(data) {
		return Request{}, invalidf("request is not valid UTF-8")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	budget := decodeBudget{}
	value, err := decodeValue(decoder, 1, &budget)
	if err != nil {
		return Request{}, invalidf("decode request: %v", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return Request{}, invalidf("request contains multiple JSON values")
		}
		return Request{}, invalidf("invalid trailing request data")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return Request{}, invalidf("request must be a JSON object")
	}
	if err := checkFields(object, "schema", "operations"); err != nil {
		return Request{}, invalidf("%v", err)
	}
	schema, err := requiredString(object, "schema")
	if err != nil {
		return Request{}, invalidf("%v", err)
	}
	if schema != SchemaV1 {
		return Request{}, invalidf("schema is unsupported")
	}
	values, ok := object["operations"]
	if !ok {
		return Request{}, invalidf("operations is required")
	}
	items, ok := values.([]any)
	if !ok {
		return Request{}, invalidf("operations must be an array")
	}
	if len(items) > MaximumOperations {
		return Request{}, invalidf("operations exceeds %d entries", MaximumOperations)
	}

	operations := make([]Operation, 0, len(items))
	seenTargets := make(map[string]struct{}, len(items))
	for index, item := range items {
		operation, err := parseOperation(item)
		if err != nil {
			return Request{}, invalidf("operations[%d]: %v", index, err)
		}
		if _, duplicate := seenTargets[operation.targetKey]; duplicate {
			return Request{}, invalidf("operations[%d] duplicates or contradicts its target", index)
		}
		seenTargets[operation.targetKey] = struct{}{}
		operations = append(operations, operation)
	}
	sort.SliceStable(operations, func(left, right int) bool {
		return operationSortKey(operations[left]) < operationSortKey(operations[right])
	})

	documentOperations := make([]any, len(operations))
	for index, operation := range operations {
		documentOperations[index] = operation.document
	}
	canonical, err := json.Marshal(struct {
		Schema     string `json:"schema"`
		Operations []any  `json:"operations"`
	}{Schema: SchemaV1, Operations: documentOperations})
	if err != nil {
		return Request{}, invalidf("encode canonical request: %v", err)
	}
	if len(canonical) > MaximumRequestBytes {
		return Request{}, invalidf("canonical request exceeds %d bytes", MaximumRequestBytes)
	}
	sum := sha256.Sum256(canonical)
	return Request{
		operations:    operations,
		canonicalJSON: canonical,
		digest:        "sha256:" + hex.EncodeToString(sum[:]),
		prepared:      true,
	}, nil
}

// Valid reports whether Parse produced this request.
func (r Request) Valid() bool {
	return r.prepared && len(r.canonicalJSON) > 0 && json.Valid(r.canonicalJSON) && r.digest != ""
}

// Operations returns normalized operations in canonical semantic order.
func (r Request) Operations() []Operation {
	result := append([]Operation(nil), r.operations...)
	for index := range result {
		if result[index].configuration != nil {
			value := *result[index].configuration
			value.value = append([]byte(nil), value.value...)
			result[index].configuration = &value
		}
	}
	return result
}

// CanonicalJSON returns a defensive copy of private normalized requested intent.
// Its configuration values must not be used as a public plan projection.
func (r Request) CanonicalJSON() []byte { return append([]byte(nil), r.canonicalJSON...) }

// Digest returns the SHA-256 identity of CanonicalJSON.
func (r Request) Digest() string { return r.digest }

type interfaceCreateDocument struct {
	Kind      Kind      `json:"kind"`
	Name      string    `json:"name"`
	Semantics Semantics `json:"semantics"`
}

type implementationCreateDocument struct {
	Kind     Kind   `json:"kind"`
	Contract string `json:"contract"`
	Package  string `json:"package"`
}

type dependencyDocument struct {
	Kind   Kind             `json:"kind"`
	Action DependencyAction `json:"action"`
	Module string           `json:"module"`
}

type configurationDocument struct {
	Kind   Kind                `json:"kind"`
	Action ConfigurationAction `json:"action"`
	Owner  string              `json:"owner"`
	Path   string              `json:"path"`
	Value  json.RawMessage     `json:"value,omitempty"`
}

type interfaceRootDocument struct {
	Kind      Kind       `json:"kind"`
	Action    RootAction `json:"action"`
	Interface string     `json:"interface"`
	Root      RootKind   `json:"root"`
}

type implementationSelectionDocument struct {
	Kind        Kind            `json:"kind"`
	Action      SelectionAction `json:"action"`
	Interface   string          `json:"interface"`
	Constructor string          `json:"constructor,omitempty"`
}

type resourceSelectionDocument struct {
	Kind     Kind            `json:"kind"`
	Action   SelectionAction `json:"action"`
	Instance string          `json:"instance"`
	Provider string          `json:"provider,omitempty"`
}

func parseOperation(value any) (Operation, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return Operation{}, errors.New("operation must be an object")
	}
	kindValue, err := requiredString(object, "kind")
	if err != nil {
		return Operation{}, err
	}
	switch Kind(kindValue) {
	case KindInterfaceCreate:
		return parseInterfaceCreate(object)
	case KindImplementationCreate:
		return parseImplementationCreate(object)
	case KindDependency:
		return parseDependency(object)
	case KindConfiguration:
		return parseConfiguration(object)
	case KindInterfaceRoot:
		return parseInterfaceRoot(object)
	case KindImplementationSelection:
		return parseImplementationSelection(object)
	case KindResourceSelection:
		return parseResourceSelection(object)
	default:
		return Operation{}, errors.New("operation kind is unsupported")
	}
}

func parseInterfaceCreate(object map[string]any) (Operation, error) {
	if err := checkFields(object, "kind", "name", "semantics"); err != nil {
		return Operation{}, err
	}
	name, err := requiredString(object, "name")
	if err != nil {
		return Operation{}, err
	}
	name, err = normalizeInterfaceName(name)
	if err != nil {
		return Operation{}, err
	}
	semantics, err := requiredString(object, "semantics")
	if err != nil {
		return Operation{}, err
	}
	if semantics != string(SemanticsQuery) && semantics != string(SemanticsCommand) {
		return Operation{}, errors.New("semantics is unsupported")
	}
	value := InterfaceCreate{name: name, semantics: Semantics(semantics)}
	return Operation{
		kind:            KindInterfaceCreate,
		interfaceCreate: &value,
		document:        interfaceCreateDocument{Kind: KindInterfaceCreate, Name: name, Semantics: value.semantics},
		targetKey:       "interface_create\x00" + name,
	}, nil
}

func parseImplementationCreate(object map[string]any) (Operation, error) {
	if err := checkFields(object, "kind", "contract", "package"); err != nil {
		return Operation{}, err
	}
	contract, err := requiredString(object, "contract")
	if err != nil {
		return Operation{}, err
	}
	if !validContractID(contract) {
		return Operation{}, errors.New("contract is not a canonical Interface or Resource ID")
	}
	packagePath, err := requiredString(object, "package")
	if err != nil {
		return Operation{}, err
	}
	if err := validPackagePath(packagePath); err != nil {
		return Operation{}, err
	}
	value := ImplementationCreate{contract: contract, packagePath: packagePath}
	return Operation{
		kind:                 KindImplementationCreate,
		implementationCreate: &value,
		document:             implementationCreateDocument{Kind: KindImplementationCreate, Contract: contract, Package: packagePath},
		targetKey:            "implementation_create\x00" + packagePath,
	}, nil
}

func parseDependency(object map[string]any) (Operation, error) {
	if err := checkFields(object, "kind", "action", "module"); err != nil {
		return Operation{}, err
	}
	action, err := requiredString(object, "action")
	if err != nil {
		return Operation{}, err
	}
	moduleValue, err := requiredString(object, "module")
	if err != nil {
		return Operation{}, err
	}
	var modulePath string
	switch DependencyAction(action) {
	case DependencyAdd, DependencyUpdate:
		_, modulePath, err = moduleargument.ParseQuery(moduleValue)
	case DependencyRemove:
		modulePath, err = moduleargument.ParsePath(moduleValue)
	default:
		return Operation{}, errors.New("dependency action is unsupported")
	}
	if err != nil {
		return Operation{}, errors.New("module is not a valid Go Module query or path")
	}
	value := Dependency{action: DependencyAction(action), module: moduleValue, modulePath: modulePath}
	return Operation{
		kind:       KindDependency,
		dependency: &value,
		document:   dependencyDocument{Kind: KindDependency, Action: value.action, Module: moduleValue},
		targetKey:  "dependency\x00" + modulePath,
	}, nil
}

func parseConfiguration(object map[string]any) (Operation, error) {
	if err := checkFields(object, "kind", "action", "owner", "path", "value"); err != nil {
		return Operation{}, err
	}
	action, err := requiredString(object, "action")
	if err != nil {
		return Operation{}, err
	}
	owner, err := requiredString(object, "owner")
	if err != nil {
		return Operation{}, err
	}
	if !validConfigurationOwner(owner) {
		return Operation{}, errors.New("owner is not a canonical constructor or Resource owner")
	}
	configurationPath, err := requiredString(object, "path")
	if err != nil {
		return Operation{}, err
	}
	if err := validConfigurationPath(configurationPath); err != nil {
		return Operation{}, err
	}
	value, hasValue := object["value"]
	var canonicalValue []byte
	if ConfigurationAction(action) == ConfigurationSet {
		if !hasValue {
			return Operation{}, errors.New("set configuration requires value")
		}
		canonicalValue, err = json.Marshal(value)
		if err != nil {
			return Operation{}, errors.New("value cannot be canonically encoded")
		}
	} else if ConfigurationAction(action) == ConfigurationRemove {
		if hasValue {
			return Operation{}, errors.New("remove configuration cannot contain value")
		}
	} else {
		return Operation{}, errors.New("configuration action is unsupported")
	}
	configuration := Configuration{action: ConfigurationAction(action), owner: owner, path: configurationPath, value: canonicalValue, hasValue: hasValue}
	return Operation{
		kind:          KindConfiguration,
		configuration: &configuration,
		document:      configurationDocument{Kind: KindConfiguration, Action: configuration.action, Owner: owner, Path: configurationPath, Value: json.RawMessage(canonicalValue)},
		targetKey:     "configuration\x00" + owner + "\x00" + configurationPath,
	}, nil
}

func parseInterfaceRoot(object map[string]any) (Operation, error) {
	if err := checkFields(object, "kind", "action", "interface", "root"); err != nil {
		return Operation{}, err
	}
	action, err := requiredString(object, "action")
	if err != nil {
		return Operation{}, err
	}
	if action != string(RootPresent) && action != string(RootAbsent) {
		return Operation{}, errors.New("root action is unsupported")
	}
	interfaceID, err := requiredInterfaceID(object, "interface")
	if err != nil {
		return Operation{}, err
	}
	root, err := requiredString(object, "root")
	if err != nil {
		return Operation{}, err
	}
	if root != string(RootRequire) && root != string(RootExpose) {
		return Operation{}, errors.New("root is unsupported")
	}
	value := InterfaceRoot{action: RootAction(action), interfaceID: interfaceID, root: RootKind(root)}
	return Operation{
		kind:          KindInterfaceRoot,
		interfaceRoot: &value,
		document:      interfaceRootDocument{Kind: KindInterfaceRoot, Action: value.action, Interface: interfaceID, Root: value.root},
		targetKey:     "interface_root\x00" + interfaceID + "\x00" + root,
	}, nil
}

func parseImplementationSelection(object map[string]any) (Operation, error) {
	if err := checkFields(object, "kind", "action", "interface", "constructor"); err != nil {
		return Operation{}, err
	}
	action, err := requiredString(object, "action")
	if err != nil {
		return Operation{}, err
	}
	if action != string(SelectionSet) && action != string(SelectionRemove) {
		return Operation{}, errors.New("selection action is unsupported")
	}
	interfaceID, err := requiredInterfaceID(object, "interface")
	if err != nil {
		return Operation{}, err
	}
	constructorValue, hasConstructor := object["constructor"]
	constructor := ""
	if action == string(SelectionSet) {
		var ok bool
		constructor, ok = constructorValue.(string)
		if !ok || !validString(constructor) {
			return Operation{}, errors.New("set selection requires a constructor")
		}
		if _, err := constructorsymbol.Parse(constructor); err != nil {
			return Operation{}, errors.New("constructor is not canonical")
		}
	} else if hasConstructor {
		return Operation{}, errors.New("remove selection cannot contain constructor")
	}
	value := ImplementationSelection{action: SelectionAction(action), interfaceID: interfaceID, constructor: constructor, hasConstructor: action == string(SelectionSet)}
	return Operation{
		kind:                    KindImplementationSelection,
		implementationSelection: &value,
		document:                implementationSelectionDocument{Kind: KindImplementationSelection, Action: value.action, Interface: interfaceID, Constructor: constructor},
		targetKey:               "implementation_selection\x00" + interfaceID,
	}, nil
}

func parseResourceSelection(object map[string]any) (Operation, error) {
	if err := checkFields(object, "kind", "action", "instance", "provider"); err != nil {
		return Operation{}, err
	}
	action, err := requiredString(object, "action")
	if err != nil {
		return Operation{}, err
	}
	if action != string(SelectionSet) && action != string(SelectionRemove) {
		return Operation{}, errors.New("selection action is unsupported")
	}
	instance, err := requiredString(object, "instance")
	if err != nil {
		return Operation{}, err
	}
	if err := resourcename.Check(instance); err != nil {
		return Operation{}, errors.New("instance is not a canonical Resource name")
	}
	providerValue, hasProvider := object["provider"]
	provider := ""
	if action == string(SelectionSet) {
		var ok bool
		provider, ok = providerValue.(string)
		if !ok || !validString(provider) {
			return Operation{}, errors.New("set Resource selection requires a provider")
		}
		if _, err := constructorsymbol.Parse(provider); err != nil {
			return Operation{}, errors.New("provider is not canonical")
		}
	} else if hasProvider {
		return Operation{}, errors.New("remove Resource selection cannot contain provider")
	}
	value := ResourceSelection{action: SelectionAction(action), instance: instance, provider: provider, hasProvider: action == string(SelectionSet)}
	return Operation{
		kind:              KindResourceSelection,
		resourceSelection: &value,
		document:          resourceSelectionDocument{Kind: KindResourceSelection, Action: value.action, Instance: instance, Provider: provider},
		targetKey:         "resource_selection\x00" + instance,
	}, nil
}

func normalizeInterfaceName(value string) (string, error) {
	if !validString(value) {
		return "", errors.New("name is invalid")
	}
	if identifier, err := interfaceid.Parse(value); err == nil {
		return identifier.String(), nil
	}
	identifier, err := interfaceid.New(value, 1)
	if err != nil {
		return "", errors.New("name is not a canonical unversioned Interface name")
	}
	if len(identifier.String()) > MaximumStringBytes {
		return "", errors.New("normalized name exceeds the string bound")
	}
	return identifier.String(), nil
}

func validContractID(value string) bool {
	_, err := interfaceid.Parse(value)
	return err == nil
}

func requiredInterfaceID(object map[string]any, field string) (string, error) {
	value, err := requiredString(object, field)
	if err != nil {
		return "", err
	}
	identifier, err := interfaceid.Parse(value)
	if err != nil {
		return "", errors.New("interface is not a canonical Interface ID")
	}
	return identifier.String(), nil
}

func validPackagePath(value string) error {
	if len(value) == 0 || len(value) > MaximumPackageBytes || strings.TrimSpace(value) != value || !strings.HasPrefix(value, "./") || strings.Contains(value, `\`) || path.IsAbs(value) {
		return errors.New("package is not a safe Project-relative path")
	}
	relative := strings.TrimPrefix(value, "./")
	if relative == "." || path.Clean(relative) != relative || !fs.ValidPath(relative) {
		return errors.New("package is not a canonical child path")
	}
	if err := module.CheckImportPath(relative); err != nil {
		return errors.New("package is not a valid Go import path")
	}
	for _, segment := range strings.Split(relative, "/") {
		if interfaceinventory.ReservedDirectory(segment) {
			return errors.New("package uses a reserved directory")
		}
	}
	last := path.Base(relative)
	if !isGoIdentifier(last) {
		return errors.New("package name is not a Go identifier")
	}
	return nil
}

func isGoIdentifier(value string) bool {
	return token.IsIdentifier(value) && !token.Lookup(value).IsKeyword()
}

func validConfigurationOwner(value string) bool {
	if _, err := constructorsymbol.Parse(value); err == nil {
		return true
	}
	return resourcename.Check(value) == nil
}

func validConfigurationPath(value string) error {
	if !validString(value) || strings.ContainsAny(value, `/\\`) || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return errors.New("path is not a canonical configuration path")
	}
	for _, segment := range strings.Split(value, ".") {
		if segment == "" || !validConfigurationSegment(segment) {
			return errors.New("path is not a canonical configuration path")
		}
	}
	return nil
}

func validConfigurationSegment(value string) bool {
	if len(value) == 0 || len(value) > 128 || value[0] < 'a' || value[0] > 'z' || strings.HasSuffix(value, "_") || strings.Contains(value, "__") {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_') {
			return false
		}
	}
	return true
}

func validString(value string) bool {
	return value != "" && len(value) <= MaximumStringBytes && utf8.ValidString(value) && strings.TrimSpace(value) == value && !strings.ContainsRune(value, '\x00') && strings.IndexFunc(value, unicode.IsControl) < 0
}

func requiredString(object map[string]any, field string) (string, error) {
	value, ok := object[field]
	if !ok {
		return "", fmt.Errorf("%s is required", field)
	}
	stringValue, ok := value.(string)
	if !ok || !validString(stringValue) {
		return "", fmt.Errorf("%s must be a bounded non-empty string", field)
	}
	return stringValue, nil
}

func checkFields(object map[string]any, allowed ...string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		known[field] = struct{}{}
	}
	for field := range object {
		if _, ok := known[field]; !ok {
			return errors.New("object contains an unknown field")
		}
	}
	return nil
}

func operationSortKey(operation Operation) string {
	return operation.targetKey
}

func (kind Kind) String() string { return string(kind) }

func invalidf(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, arguments...))
}

type decodeBudget struct {
	nodes int
	bytes int
}

func (b *decodeBudget) consume(size int) error {
	if size > MaximumRequestBytes-b.bytes {
		return errors.New("canonical JSON exceeds the request bound")
	}
	b.bytes += size
	return nil
}

func decodeValue(decoder *json.Decoder, depth int, budget *decodeBudget) (any, error) {
	if depth > MaximumJSONDepth {
		return nil, fmt.Errorf("JSON exceeds maximum depth %d", MaximumJSONDepth)
	}
	budget.nodes++
	if budget.nodes > MaximumJSONNodes {
		return nil, fmt.Errorf("JSON exceeds maximum node count %d", MaximumJSONNodes)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, errors.New("invalid JSON syntax")
	}
	switch token := token.(type) {
	case json.Delim:
		if err := budget.consume(2); err != nil {
			return nil, err
		}
		switch token {
		case '{':
			object := make(map[string]any)
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, errors.New("invalid JSON object key")
				}
				key, ok := keyToken.(string)
				if !ok || len(key) > MaximumStringBytes {
					return nil, errors.New("object key is not a bounded string")
				}
				if _, duplicate := object[key]; duplicate {
					return nil, errors.New("object contains a duplicate key")
				}
				encodedKey, _ := json.Marshal(key)
				overhead := 1
				if len(object) > 0 {
					overhead++
				}
				if err := budget.consume(len(encodedKey) + overhead); err != nil {
					return nil, err
				}
				value, err := decodeValue(decoder, depth+1, budget)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim('}') {
				return nil, errors.New("object is not closed")
			}
			return object, nil
		case '[':
			array := make([]any, 0)
			for decoder.More() {
				if len(array) > 0 {
					if err := budget.consume(1); err != nil {
						return nil, err
					}
				}
				value, err := decodeValue(decoder, depth+1, budget)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			closing, err := decoder.Token()
			if err != nil || closing != json.Delim(']') {
				return nil, errors.New("array is not closed")
			}
			return array, nil
		default:
			return nil, fmt.Errorf("unexpected JSON delimiter %q", token)
		}
	case json.Number:
		number, err := normalizeNumber(token, MaximumRequestBytes-budget.bytes)
		if err != nil {
			return nil, err
		}
		return number, budget.consume(len(number))
	case string:
		if len(token) > MaximumStringBytes {
			return nil, errors.New("string exceeds the byte bound")
		}
		encoded, _ := json.Marshal(token)
		return token, budget.consume(len(encoded))
	case bool, nil:
		encoded, _ := json.Marshal(token)
		return token, budget.consume(len(encoded))
	default:
		return nil, fmt.Errorf("unsupported JSON token %T", token)
	}
}

func normalizeNumber(number json.Number, remaining int) (canonicalNumber, error) {
	value := number.String()
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = value[1:]
	}
	mantissa := value
	exponent := int64(0)
	if marker := strings.IndexAny(value, "eE"); marker >= 0 {
		mantissa = value[:marker]
		parsed, err := strconv.ParseInt(value[marker+1:], 10, 64)
		if err != nil {
			return "", errors.New("number exponent is outside the supported range")
		}
		exponent = parsed
	}
	parts := strings.Split(mantissa, ".")
	integer := strings.TrimLeft(parts[0], "0")
	if integer == "" {
		integer = "0"
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	digits := integer + fraction
	leading := len(digits) - len(strings.TrimLeft(digits, "0"))
	if leading == len(digits) {
		return canonicalNumber("0"), nil
	}
	digits = strings.TrimRight(digits[leading:], "0")
	// Bound expansion before allocating or adding the exponent to the decimal position.
	if exponent > 2*MaximumRequestBytes || exponent < -2*MaximumRequestBytes {
		return "", errors.New("number expansion exceeds the request bound")
	}
	position := int64(len(integer)-leading) + exponent
	size := int64(len(digits) + 1)
	if position <= 0 {
		size = 2 - position + int64(len(digits))
	}
	if position >= int64(len(digits)) {
		size = position
	}
	if size+int64(len(sign)) > int64(remaining) {
		return "", errors.New("number expansion exceeds the request bound")
	}
	var normalized string
	switch {
	case position <= 0:
		normalized = "0." + strings.Repeat("0", int(-position)) + digits
	case position >= int64(len(digits)):
		normalized = digits + strings.Repeat("0", int(position)-len(digits))
	default:
		cut := int(position)
		normalized = digits[:cut] + "." + digits[cut:]
	}
	return canonicalNumber(sign + normalized), nil
}
