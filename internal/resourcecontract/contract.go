// Package resourcecontract validates and fingerprints ordinary Go Resource contracts.
package resourcecontract

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/types"
	"sort"

	"github.com/plystra/cli/internal/resourcedecl"
)

const (
	MaximumDepth = 64
	MaximumNodes = 65_536
)

var ErrInvalid = errors.New("invalid Resource contract")

// Contract is an immutable validated consumer contract, not a provider schema.
type Contract struct {
	digest string
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
	return Contract{digest: fmt.Sprintf("sha256:%x", sha256.Sum256(data))}, nil
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
