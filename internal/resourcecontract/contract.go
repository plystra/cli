// Package resourcecontract validates and fingerprints ordinary Go Resource contracts.
package resourcecontract

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/types"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/resourcedecl"
	"golang.org/x/mod/module"
)

const (
	MaximumDepth = 64
	MaximumNodes = 65_536
)

var ErrInvalid = errors.New("invalid Resource contract")

// ErrImplementationMethods reports a valid Resource contract whose ordinary
// implementation method signatures cannot be named from the target package.
var ErrImplementationMethods = errors.New("resource implementation methods are not nameable")

// Contract is an immutable validated consumer contract, not a provider schema.
type Contract struct {
	digest string
	root   *types.Named
}

func (c Contract) Digest() string { return c.digest }

type shape struct {
	Kind           string   `json:"kind"`
	Package        string   `json:"package,omitempty"`
	Name           string   `json:"name,omitempty"`
	Reference      string   `json:"reference,omitempty"`
	Arguments      []shape  `json:"arguments,omitempty"`
	Underlying     *shape   `json:"underlying,omitempty"`
	Element        *shape   `json:"element,omitempty"`
	Key            *shape   `json:"key,omitempty"`
	Length         int64    `json:"length,omitempty"`
	Direction      string   `json:"direction,omitempty"`
	Fields         []field  `json:"fields,omitempty"`
	PromotedFields []field  `json:"promoted_fields,omitempty"`
	Methods        []method `json:"methods,omitempty"`
	PointerMethods []method `json:"pointer_methods,omitempty"`
	Parameters     []shape  `json:"parameters,omitempty"`
	Results        []shape  `json:"results,omitempty"`
	Variadic       bool     `json:"variadic,omitempty"`
}

type field struct {
	Name     string `json:"name"`
	Tag      string `json:"tag"`
	Embedded bool   `json:"embedded,omitempty"`
	Type     shape  `json:"type"`
}

type method struct {
	Package   string `json:"package,omitempty"`
	Name      string `json:"name"`
	Signature shape  `json:"signature"`
}

// Validate uses the compiled Go method set, including embeddings, without applying
// Interface transport restrictions or examining any provider implementation.
func Validate(declaration resourcedecl.Declaration, pkg *types.Package) (Contract, error) {
	if declaration.ID() == "" || pkg == nil || pkg.Name() != declaration.PackageName() {
		return Contract{}, fmt.Errorf("%w: missing declaration or owning Go package", ErrInvalid)
	}
	object, ok := pkg.Scope().Lookup("Resource").(*types.TypeName)
	if !ok || object.IsAlias() {
		return Contract{}, fmt.Errorf("%w: Resource must be a defined Go interface", ErrInvalid)
	}
	named, ok := object.Type().(*types.Named)
	if !ok || named.TypeParams().Len() != 0 {
		return Contract{}, fmt.Errorf("%w: Resource must be a non-generic defined Go interface", ErrInvalid)
	}
	iface, ok := named.Underlying().(*types.Interface)
	if !ok || !iface.Complete().IsMethodSet() {
		return Contract{}, fmt.Errorf("%w: Resource must be an ordinary method-set interface", ErrInvalid)
	}
	for i := 0; i < iface.NumMethods(); i++ {
		switch iface.Method(i).Name() {
		case "Start", "Stop", "Shutdown", "Close":
			return Contract{}, fmt.Errorf("%w: consumer Resource exposes lifecycle-control method %s", ErrInvalid, iface.Method(i).Name())
		}
	}
	walker := walker{}
	root, err := walker.walk(named, 1)
	if err != nil {
		return Contract{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	data, err := json.Marshal(struct {
		Version int    `json:"version"`
		ID      string `json:"id"`
		Package string `json:"package"`
		Type    shape  `json:"type"`
	}{1, declaration.ID(), pkg.Path(), root})
	if err != nil {
		return Contract{}, fmt.Errorf("%w: encode public shape: %w", ErrInvalid, err)
	}
	return Contract{digest: fmt.Sprintf("sha256:%x", sha256.Sum256(data)), root: named}, nil
}

// Method is one ordinary Resource method signature suitable for a generated
// implementation declaration. Its fields are intentionally private so callers
// cannot mutate the returned method values.
type Method struct {
	name      string
	signature string
}

// Name returns the exact exported Resource method name.
func (m Method) Name() string { return m.name }

// Signature returns the complete method function signature, including func.
func (m Method) Signature() string { return m.signature }

// ImplementationMethods returns the exact effective Resource method set in
// deterministic name order. targetImportPath is the package that will contain
// the generated implementation. The qualifier is used by go/types when
// spelling imported named types.
func (c Contract) ImplementationMethods(targetImportPath string, qualifier types.Qualifier) ([]Method, error) {
	if c.root == nil || c.root.Obj() == nil || c.root.Obj().Pkg() == nil {
		return nil, fmt.Errorf("%w: contract has no compiled Resource root", ErrImplementationMethods)
	}
	if err := module.CheckImportPath(targetImportPath); err != nil {
		return nil, fmt.Errorf("%w: invalid target import path %q", ErrImplementationMethods, targetImportPath)
	}
	resourcePackage := c.root.Obj().Pkg().Path()
	if !importVisible(resourcePackage, targetImportPath) {
		return nil, fmt.Errorf("%w: Resource package %q is not importable from %q", ErrImplementationMethods, resourcePackage, targetImportPath)
	}
	iface, ok := c.root.Underlying().(*types.Interface)
	if !ok || !iface.Complete().IsMethodSet() {
		return nil, fmt.Errorf("%w: compiled Resource root is not an ordinary interface", ErrImplementationMethods)
	}
	checker := signatureNameability{targetImportPath: targetImportPath}
	result := make([]Method, 0, iface.NumMethods())
	for index := 0; index < iface.NumMethods(); index++ {
		method := iface.Method(index)
		if !method.Exported() && method.Pkg() != nil && method.Pkg().Path() != targetImportPath {
			return nil, fmt.Errorf("%w: sealed Resource method %q belongs to %q", ErrImplementationMethods, method.Name(), method.Pkg().Path())
		}
		signature, ok := method.Type().(*types.Signature)
		if !ok {
			return nil, fmt.Errorf("%w: method %q has no Go signature", ErrImplementationMethods, method.Name())
		}
		if err := checker.checkSignature(signature); err != nil {
			return nil, fmt.Errorf("%w: method %q: %w", ErrImplementationMethods, method.Name(), err)
		}
		// go/types includes authored parameter and result names in TypeString.
		// Rebuild only the outer tuple; nested function types retain their exact
		// own signatures while generated method bodies cannot be shadowed.
		spelled := types.NewSignatureType(signature.Recv(), typeParams(signature.RecvTypeParams()), typeParams(signature.TypeParams()),
			unnamedTuple(signature.Params()), unnamedTuple(signature.Results()), signature.Variadic())
		result = append(result, Method{name: method.Name(), signature: types.TypeString(spelled, detachedQualifier(qualifier))})
	}
	return result, nil
}

func typeParams(list *types.TypeParamList) []*types.TypeParam {
	if list == nil || list.Len() == 0 {
		return nil
	}
	result := make([]*types.TypeParam, list.Len())
	for index := range result {
		result[index] = list.At(index)
	}
	return result
}

func unnamedTuple(tuple *types.Tuple) *types.Tuple {
	if tuple == nil || tuple.Len() == 0 {
		return tuple
	}
	vars := make([]*types.Var, tuple.Len())
	for index := range vars {
		variable := tuple.At(index)
		vars[index] = types.NewVar(variable.Pos(), variable.Pkg(), "", variable.Type())
	}
	return types.NewTuple(vars...)
}

// detachedQualifier prevents a caller's qualifier from receiving the retained
// compiler package graph. Qualifiers should use Package.Path or Package.Name,
// as required by go/types. The type graph itself is never exposed by Contract.
func detachedQualifier(qualifier types.Qualifier) types.Qualifier {
	if qualifier == nil {
		return nil
	}
	return func(pkg *types.Package) string {
		if pkg == nil {
			return qualifier(nil)
		}
		return qualifier(types.NewPackage(pkg.Path(), pkg.Name()))
	}
}

type signatureNameability struct {
	targetImportPath string
	active           map[types.Type]bool
	nodes            int
}

func (c *signatureNameability) checkSignature(signature *types.Signature) error {
	if signature == nil {
		return errors.New("nil signature")
	}
	if signature.TypeParams().Len() != 0 {
		return errors.New("generic method signatures are not nameable")
	}
	if err := c.checkTuple(signature.Params(), false); err != nil {
		return err
	}
	return c.checkTuple(signature.Results(), true)
}

func (c *signatureNameability) checkTuple(tuple *types.Tuple, results bool) error {
	if tuple == nil {
		return nil
	}
	for index := 0; index < tuple.Len(); index++ {
		if err := c.check(tuple.At(index).Type(), 0); err != nil {
			return fmt.Errorf("%s %d: %w", map[bool]string{false: "parameter", true: "result"}[results], index+1, err)
		}
	}
	return nil
}

func (c *signatureNameability) check(t types.Type, depth int) error {
	if t == nil {
		return errors.New("nil type")
	}
	if depth > MaximumDepth {
		return fmt.Errorf("signature type shape exceeds %d type-reference levels", MaximumDepth)
	}
	c.nodes++
	if c.nodes > MaximumNodes {
		return fmt.Errorf("signature type shape exceeds %d nodes", MaximumNodes)
	}
	if c.active == nil {
		c.active = make(map[types.Type]bool)
	}
	if c.active[t] {
		return nil
	}
	c.active[t] = true
	defer delete(c.active, t)
	child := func(child types.Type) error { return c.check(child, depth+1) }
	switch typ := t.(type) {
	case *types.Basic:
		if typ.Kind() == types.Invalid || typ.Info()&types.IsUntyped != 0 {
			return errors.New("invalid or untyped signature type")
		}
	case *types.Named:
		if err := c.checkNamed(typ.Obj(), typ.TypeArgs(), child); err != nil {
			return err
		}
	case *types.Alias:
		if err := c.checkNamed(typ.Obj(), typ.TypeArgs(), child); err != nil {
			return err
		}
	case *types.Pointer:
		return child(typ.Elem())
	case *types.Slice:
		return child(typ.Elem())
	case *types.Array:
		return child(typ.Elem())
	case *types.Map:
		if err := child(typ.Key()); err != nil {
			return err
		}
		return child(typ.Elem())
	case *types.Chan:
		return child(typ.Elem())
	case *types.Signature:
		if err := c.checkSignature(typ); err != nil {
			return err
		}
	case *types.Interface:
		if !typ.Complete().IsMethodSet() {
			return errors.New("signature interface is not an ordinary method set")
		}
		if err := c.checkInterface(typ, child); err != nil {
			return err
		}
	case *types.Struct:
		for index := 0; index < typ.NumFields(); index++ {
			field := typ.Field(index)
			if !field.Exported() && !samePackage(field.Pkg(), c.targetImportPath) {
				return fmt.Errorf("unexported struct field %q belongs to %q", field.Name(), signaturePackagePath(field.Pkg()))
			}
			if err := child(field.Type()); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unsupported signature type %T", t)
	}
	return nil
}

func (c *signatureNameability) checkNamed(object *types.TypeName, arguments *types.TypeList, child func(types.Type) error) error {
	if object == nil {
		return errors.New("unnamed named type")
	}
	// Predeclared named interfaces such as error have no owning package but
	// are always nameable in generated Go source.
	if object.Pkg() == nil {
		return nil
	}
	if !object.Exported() && object.Pkg().Path() != c.targetImportPath {
		return fmt.Errorf("unexported named type %q belongs to %q", object.Name(), object.Pkg().Path())
	}
	if !importVisible(object.Pkg().Path(), c.targetImportPath) {
		return fmt.Errorf("named type package %q is not importable from %q", object.Pkg().Path(), c.targetImportPath)
	}
	if arguments != nil {
		for index := 0; index < arguments.Len(); index++ {
			if err := child(arguments.At(index)); err != nil {
				return fmt.Errorf("type argument %d: %w", index+1, err)
			}
		}
	}
	return nil
}

func (c *signatureNameability) checkInterface(iface *types.Interface, child func(types.Type) error) error {
	for index := 0; index < iface.NumEmbeddeds(); index++ {
		if err := child(iface.EmbeddedType(index)); err != nil {
			return err
		}
	}
	for index := 0; index < iface.NumMethods(); index++ {
		method := iface.Method(index)
		if !method.Exported() && !samePackage(method.Pkg(), c.targetImportPath) && !embeddedMethod(iface, method) {
			return fmt.Errorf("unexported interface method %q belongs to %q", method.Name(), signaturePackagePath(method.Pkg()))
		}
		signature, ok := method.Type().(*types.Signature)
		if !ok {
			return errors.New("interface method has no Go signature")
		}
		if err := c.checkSignature(signature); err != nil {
			return err
		}
	}
	return nil
}

func embeddedMethod(iface *types.Interface, method *types.Func) bool {
	for index := 0; index < iface.NumEmbeddeds(); index++ {
		embedded := types.Unalias(iface.EmbeddedType(index))
		named, ok := embedded.(*types.Named)
		if !ok || !named.Obj().Exported() || named.Obj().Pkg() == nil {
			continue
		}
		set := types.NewMethodSet(named)
		for methodIndex := 0; methodIndex < set.Len(); methodIndex++ {
			candidate := set.At(methodIndex).Obj().(*types.Func)
			if candidate.Name() == method.Name() && candidate.Pkg() != nil && method.Pkg() != nil && candidate.Pkg().Path() == method.Pkg().Path() && types.Identical(candidate.Type(), method.Type()) {
				return true
			}
		}
	}
	return false
}

func samePackage(pkg *types.Package, path string) bool {
	return pkg != nil && pkg.Path() == path
}

func signaturePackagePath(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}

func importVisible(importPath, targetImportPath string) bool {
	if strings.HasPrefix(importPath, "internal/") {
		return false
	}
	for start := 0; ; {
		relative := strings.Index(importPath[start:], "/internal/")
		if relative < 0 {
			break
		}
		index := start + relative
		parent := importPath[:index]
		if parent == "" || (targetImportPath != parent && !strings.HasPrefix(targetImportPath, parent+"/")) {
			return false
		}
		start = index + len("/internal/")
	}
	return true
}

type walker struct {
	nodes  int
	active []activeType
}

type activeType struct {
	typ   types.Type
	depth int
}

func (w *walker) visit(depth int) error {
	if depth > MaximumDepth {
		return fmt.Errorf("public type shape exceeds %d type-reference levels", MaximumDepth)
	}
	w.nodes++
	if w.nodes > MaximumNodes {
		return fmt.Errorf("public type shape exceeds %d nodes", MaximumNodes)
	}
	return nil
}

func (w *walker) walk(t types.Type, depth int) (shape, error) {
	if err := w.visit(depth); err != nil {
		return shape{}, err
	}
	t = types.Unalias(t)
	// Relative ancestor references close named and anonymous recursion without
	// discovery-order IDs, private type spellings, or a global visited shortcut.
	for i := len(w.active) - 1; i >= 0; i-- {
		if samePublicType(t, w.active[i].typ) {
			return shape{Kind: "reference", Reference: fmt.Sprintf("ancestor:%d", depth-w.active[i].depth)}, nil
		}
	}
	w.active = append(w.active, activeType{typ: t, depth: depth})
	defer func() { w.active = w.active[:len(w.active)-1] }()
	result := shape{}
	child := func(t types.Type) (*shape, error) {
		next, err := w.walk(t, depth+1)
		return &next, err
	}
	var err error
	switch t := t.(type) {
	case *types.Basic:
		if t.Kind() == types.Invalid || t.Info()&types.IsUntyped != 0 {
			return shape{}, errors.New("invalid public basic type")
		}
		result.Kind, result.Name = "basic", types.Typ[t.Kind()].Name()
	case *types.Named:
		if t.TypeParams().Len() != t.TypeArgs().Len() {
			return shape{}, errors.New("uninstantiated generic public type")
		}
		result.Kind, result.Name = "named", t.Obj().Name()
		if t.Obj().Pkg() != nil {
			result.Package = t.Obj().Pkg().Path()
		}
		for i := 0; i < t.TypeArgs().Len(); i++ {
			argument, e := w.walk(t.TypeArgs().At(i), depth+1)
			if e != nil {
				return shape{}, e
			}
			result.Arguments = append(result.Arguments, argument)
		}
		result.Underlying, err = child(t.Underlying())
		if err != nil {
			return shape{}, err
		}
		if _, iface := t.Underlying().(*types.Interface); !iface {
			result.Methods, err = w.methods(types.NewMethodSet(t), depth+1)
			if err != nil {
				return shape{}, err
			}
			result.PointerMethods, err = w.methods(types.NewMethodSet(types.NewPointer(t)), depth+1)
		}
	case *types.Pointer:
		result.Kind = "pointer"
		result.Element, err = child(t.Elem())
	case *types.Slice:
		result.Kind = "slice"
		result.Element, err = child(t.Elem())
	case *types.Array:
		result.Kind, result.Length = "array", t.Len()
		result.Element, err = child(t.Elem())
	case *types.Map:
		result.Kind = "map"
		result.Key, err = child(t.Key())
		if err == nil {
			result.Element, err = child(t.Elem())
		}
	case *types.Chan:
		result.Kind = "channel"
		switch t.Dir() {
		case types.SendRecv:
			result.Direction = "both"
		case types.SendOnly:
			result.Direction = "send"
		case types.RecvOnly:
			result.Direction = "receive"
		}
		result.Element, err = child(t.Elem())
	case *types.Signature:
		if t.TypeParams().Len() != 0 {
			return shape{}, errors.New("free public function type parameter")
		}
		result.Kind, result.Variadic = "signature", t.Variadic()
		for i := 0; i < t.Params().Len(); i++ {
			value, e := w.walk(t.Params().At(i).Type(), depth+1)
			if e != nil {
				return shape{}, e
			}
			result.Parameters = append(result.Parameters, value)
		}
		for i := 0; i < t.Results().Len(); i++ {
			value, e := w.walk(t.Results().At(i).Type(), depth+1)
			if e != nil {
				return shape{}, e
			}
			result.Results = append(result.Results, value)
		}
	case *types.Interface:
		if !t.Complete().IsMethodSet() {
			return shape{}, errors.New("public interface is not an ordinary method set")
		}
		result.Kind = "interface"
		for i := 0; i < t.NumMethods(); i++ {
			value, e := w.method(t.Method(i), depth+1)
			if e != nil {
				return shape{}, e
			}
			result.Methods = append(result.Methods, value)
		}
		sortMethods(result.Methods)
	case *types.Struct:
		result.Kind = "struct"
		for i := 0; i < t.NumFields(); i++ {
			if !t.Field(i).Exported() {
				continue
			}
			value, e := w.field(t.Field(i), t.Tag(i), depth+1)
			if e != nil {
				return shape{}, e
			}
			result.Fields = append(result.Fields, value)
		}
		for _, promoted := range promotedFields(t) {
			value, e := w.field(promoted.variable, promoted.tag, depth+1)
			if e != nil {
				return shape{}, e
			}
			result.PromotedFields = append(result.PromotedFields, value)
		}
		result.Methods, err = w.methods(types.NewMethodSet(t), depth+1)
		if err != nil {
			return shape{}, err
		}
		result.PointerMethods, err = w.methods(types.NewMethodSet(types.NewPointer(t)), depth+1)
	default:
		return shape{}, errors.New("unsupported free public Go type")
	}
	return result, err
}

func (w *walker) field(v *types.Var, tag string, depth int) (field, error) {
	if err := w.visit(depth); err != nil {
		return field{}, err
	}
	t, err := w.walk(v.Type(), depth)
	return field{Name: v.Name(), Tag: tag, Embedded: v.Embedded(), Type: t}, err
}

func (w *walker) method(f *types.Func, depth int) (method, error) {
	if err := w.visit(depth); err != nil {
		return method{}, err
	}
	signature, err := w.walk(f.Type(), depth)
	result := method{Name: f.Name(), Signature: signature}
	if !f.Exported() && f.Pkg() != nil {
		result.Package = f.Pkg().Path()
	}
	return result, err
}

func (w *walker) methods(set *types.MethodSet, depth int) ([]method, error) {
	var result []method
	for i := 0; i < set.Len(); i++ {
		f := set.At(i).Obj().(*types.Func)
		if !f.Exported() {
			continue
		}
		value, err := w.method(f, depth)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	sortMethods(result)
	return result, nil
}

func sortMethods(methods []method) {
	sort.Slice(methods, func(i, j int) bool {
		if methods[i].Package != methods[j].Package {
			return methods[i].Package < methods[j].Package
		}
		return methods[i].Name < methods[j].Name
	})
}

// Effective promoted fields record selector ambiguity as well as hidden
// embeddings. The iterative probe visits each Go type once; only emitted public
// records consume the shape budget, never discarded private structure.
type publicField struct {
	variable *types.Var
	tag      string
}

func promotedFields(root *types.Struct) []publicField {
	names := make(map[string]bool)
	seen := make(map[types.Type]bool)
	pending := []types.Type{root}
	for len(pending) > 0 {
		t := types.Unalias(pending[len(pending)-1])
		pending = pending[:len(pending)-1]
		if p, ok := t.(*types.Pointer); ok {
			t = types.Unalias(p.Elem())
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		s, ok := t.Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := 0; i < s.NumFields(); i++ {
			f := s.Field(i)
			if f.Exported() {
				names[f.Name()] = true
			}
			if f.Embedded() {
				pending = append(pending, f.Type())
			}
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	var result []publicField
	for _, name := range ordered {
		object, indices, _ := types.LookupFieldOrMethod(root, false, nil, name)
		f, ok := object.(*types.Var)
		if !ok || len(indices) < 2 {
			continue
		}
		owner := root
		for _, index := range indices[:len(indices)-1] {
			t := types.Unalias(owner.Field(index).Type())
			if p, ok := t.(*types.Pointer); ok {
				t = types.Unalias(p.Elem())
			}
			owner = t.Underlying().(*types.Struct)
		}
		result = append(result, publicField{variable: f, tag: owner.Tag(indices[len(indices)-1])})
	}
	return result
}
