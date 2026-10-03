package resourcecontract

import "go/types"

// A recursion guard compares the public projection, not Go's private structural
// identity: otherwise an excluded private field can move a backref. Iterative
// pair memoization closes anonymous recursive method shapes without charging
// discarded private structure against the emitted graph's depth or node budget.
func samePublicType(left, right types.Type) bool {
	c := publicComparison{seen: make(map[typePair]bool), pending: []typePair{{left, right}}}
	for len(c.pending) > 0 {
		pair := c.pending[len(c.pending)-1]
		c.pending = c.pending[:len(c.pending)-1]
		pair.left, pair.right = types.Unalias(pair.left), types.Unalias(pair.right)
		if types.Identical(pair.left, pair.right) || c.seen[pair] {
			continue
		}
		c.seen[pair] = true
		if !c.compare(pair.left, pair.right) {
			return false
		}
	}
	return true
}

type typePair struct{ left, right types.Type }
type publicComparison struct {
	seen    map[typePair]bool
	pending []typePair
}

func (c *publicComparison) add(a, b types.Type) { c.pending = append(c.pending, typePair{a, b}) }

func (c *publicComparison) compare(left, right types.Type) bool {
	switch a := left.(type) {
	case *types.Named:
		b, ok := right.(*types.Named)
		if !ok || a.Obj().Name() != b.Obj().Name() || packagePath(a.Obj()) != packagePath(b.Obj()) || a.TypeArgs().Len() != b.TypeArgs().Len() {
			return false
		}
		// A named identity ends structural comparison. Its actual public definition
		// and arguments remain part of the authoritative shape walk.
		for i := 0; i < a.TypeArgs().Len(); i++ {
			c.add(a.TypeArgs().At(i), b.TypeArgs().At(i))
		}
	case *types.Pointer:
		b, ok := right.(*types.Pointer)
		if !ok {
			return false
		}
		c.add(a.Elem(), b.Elem())
	case *types.Slice:
		b, ok := right.(*types.Slice)
		if !ok {
			return false
		}
		c.add(a.Elem(), b.Elem())
	case *types.Array:
		b, ok := right.(*types.Array)
		if !ok || a.Len() != b.Len() {
			return false
		}
		c.add(a.Elem(), b.Elem())
	case *types.Map:
		b, ok := right.(*types.Map)
		if !ok {
			return false
		}
		c.add(a.Key(), b.Key())
		c.add(a.Elem(), b.Elem())
	case *types.Chan:
		b, ok := right.(*types.Chan)
		if !ok || a.Dir() != b.Dir() {
			return false
		}
		c.add(a.Elem(), b.Elem())
	case *types.Signature:
		b, ok := right.(*types.Signature)
		if !ok || a.TypeParams().Len() != 0 || b.TypeParams().Len() != 0 || a.Variadic() != b.Variadic() || !c.tuple(a.Params(), b.Params()) || !c.tuple(a.Results(), b.Results()) {
			return false
		}
	case *types.Interface:
		b, ok := right.(*types.Interface)
		if !ok || !a.Complete().IsMethodSet() || !b.Complete().IsMethodSet() || a.NumMethods() != b.NumMethods() {
			return false
		}
		for i := 0; i < a.NumMethods(); i++ {
			if a.Method(i).Id() != b.Method(i).Id() {
				return false
			}
			c.add(a.Method(i).Type(), b.Method(i).Type())
		}
	case *types.Struct:
		b, ok := right.(*types.Struct)
		if !ok {
			return false
		}
		public := func(s *types.Struct) []publicField {
			var fields []publicField
			for i := 0; i < s.NumFields(); i++ {
				if s.Field(i).Exported() {
					fields = append(fields, publicField{variable: s.Field(i), tag: s.Tag(i)})
				}
			}
			return fields
		}
		if !c.fields(public(a), public(b)) || !c.fields(promotedFields(a), promotedFields(b)) {
			return false
		}
		if !c.methods(types.NewMethodSet(a), types.NewMethodSet(b)) || !c.methods(types.NewMethodSet(types.NewPointer(a)), types.NewMethodSet(types.NewPointer(b))) {
			return false
		}
	default:
		return false
	}
	return true
}

func packagePath(object types.Object) string {
	if object.Pkg() == nil {
		return ""
	}
	return object.Pkg().Path()
}

func (c *publicComparison) tuple(a, b *types.Tuple) bool {
	if a.Len() != b.Len() {
		return false
	}
	for i := 0; i < a.Len(); i++ {
		c.add(a.At(i).Type(), b.At(i).Type())
	}
	return true
}

func (c *publicComparison) fields(a, b []publicField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].variable.Name() != b[i].variable.Name() || a[i].variable.Embedded() != b[i].variable.Embedded() || a[i].tag != b[i].tag {
			return false
		}
		c.add(a[i].variable.Type(), b[i].variable.Type())
	}
	return true
}

func (c *publicComparison) methods(a, b *types.MethodSet) bool {
	public := func(set *types.MethodSet) []*types.Selection {
		var result []*types.Selection
		for i := 0; i < set.Len(); i++ {
			if set.At(i).Obj().Exported() {
				result = append(result, set.At(i))
			}
		}
		return result
	}
	x, y := public(a), public(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i].Obj().Name() != y[i].Obj().Name() {
			return false
		}
		c.add(x[i].Type(), y[i].Type())
	}
	return true
}
