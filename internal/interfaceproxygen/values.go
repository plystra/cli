package interfaceproxygen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/interfacecontract"
	"github.com/plystra/cli/internal/interfacemeta"
)

type valueRenderer struct {
	source  strings.Builder
	imports map[string]bool
	targets []interfacemeta.ConstraintTarget
}

func renderValues(input Input) (string, []string, error) {
	targets, err := interfacemeta.ResolveConstraintTargets(input.Metadata, input.Contract)
	if err != nil {
		return "", nil, err
	}
	r := valueRenderer{imports: map[string]bool{"fmt": true, "log/slog": true}, targets: targets}
	r.source.WriteString(`// ValueError identifies a contract violation without retaining the submitted value.
type ValueError struct {
	side, path, rule string
	boundary *kernelinvocation.Error
}

func (e *ValueError) Error() string {
	if e == nil { return "contract validation failed" }
	return "Interface " + InterfaceID + " " + e.side + " validation failed at " + e.path + ": " + e.rule
}
func (e *ValueError) Interface() string { return InterfaceID }
func (e *ValueError) Side() string { if e == nil { return "" }; return e.side }
func (e *ValueError) Path() string { if e == nil { return "" }; return e.path }
func (e *ValueError) Rule() string { if e == nil { return "" }; return e.rule }
func (e *ValueError) Unwrap() error { if e == nil { return nil }; return e.boundary }
func (e *ValueError) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(e.Error())) }
func (e *ValueError) LogValue() slog.Value { return slog.StringValue(e.Error()) }

type valueTraversal struct {
	remaining int
	copyValues bool
	side string
}

func (b *valueTraversal) failure(path, rule string) error {
	var boundary *kernelinvocation.Error
	if b.side == "response" {
		boundary, _ = kernelinvocation.NewError(kernelinvocation.ErrorInternal, "contract.response_invalid")
	} else {
		boundary, _ = kernelinvocation.NewNotStartedError(kernelinvocation.ErrorInvalidArgument, "contract.request_invalid")
	}
	return &ValueError{side: b.side, path: path, rule: rule, boundary: boundary}
}

func (b *valueTraversal) node(path string, depth int) error {
	if depth > 64 { return b.failure(path, "maximum_depth") }
	if b.remaining == 0 { return b.failure(path, "maximum_nodes") }
	b.remaining--
	return nil
}

`)
	for index, target := range targets {
		if pattern, exists := target.Rules().Pattern(); exists {
			r.imports["regexp"] = true
			fmt.Fprintf(&r.source, "var valuePattern%d = regexp.MustCompile(%s)\n", index, strconv.Quote(pattern))
		}
	}
	for _, side := range []struct{ name, message string }{{"request", input.RequestName}, {"response", input.ResponseName}} {
		name := "Copy" + strings.ToUpper(side.name[:1]) + side.name[1:]
		fmt.Fprintf(&r.source, "\n// %s validates and returns an independent canonical %s graph.\n", name, side.name)
		fmt.Fprintf(&r.source, "func %s(value contract.%s) (contract.%s, error) {\n", name, side.message, side.message)
		fmt.Fprintln(&r.source, "// Check the complete shape before applying constraints or allocating copies.")
		fmt.Fprintf(&r.source, "b := valueTraversal{remaining: 65536, side: %q}\n", side.name)
		fmt.Fprintf(&r.source, "if _, err := visit%s(value, %q, %q, 1, &b); err != nil { return contract.%s{}, err }\n", side.message, side.message, side.name, side.message)
		fmt.Fprintln(&r.source, "b.remaining, b.copyValues = 65536, true")
		fmt.Fprintf(&r.source, "return visit%s(value, %q, %q, 1, &b)\n}\n", side.message, side.message, side.name)
	}
	for _, message := range input.Contract.Messages() {
		r.message(message)
	}
	imports := make([]string, 0, len(r.imports))
	for name := range r.imports {
		imports = append(imports, name)
	}
	slices.Sort(imports)
	return r.source.String(), imports, nil
}

func (r *valueRenderer) message(message interfacecontract.Message) {
	fmt.Fprintf(&r.source, "\nfunc visit%s(value contract.%s, goPath, path string, depth int, b *valueTraversal) (contract.%s, error) {\n", message.Name(), message.Name(), message.Name())
	fmt.Fprintf(&r.source, "var zero contract.%s\n", message.Name())
	fmt.Fprintln(&r.source, "if err := b.node(path, depth); err != nil { return zero, err }")
	for _, field := range message.Fields() {
		jsonName := field.Name()
		if field.HasExplicitJSONName() {
			jsonName = field.JSONName()
		}
		fmt.Fprintln(&r.source, "{")
		fmt.Fprintf(&r.source, "fieldPath := path + %q\nfieldGoPath := goPath + %q\n", "."+jsonName, "."+field.Name())
		fmt.Fprintln(&r.source, "_ = fieldGoPath")
		fmt.Fprintln(&r.source, "if err := b.node(fieldPath, depth+1); err != nil { return zero, err }")
		if field.PointerDepth() > 0 && field.Required() {
			fmt.Fprintf(&r.source, "if value.%s == nil { return zero, b.failure(fieldPath, \"required\") }\n", field.Name())
		}
		r.pointer(field, "value."+field.Name(), int(field.PointerDepth()))
		fmt.Fprintln(&r.source, "}")
	}
	fmt.Fprintln(&r.source, "return value, nil\n}")
}

func (r *valueRenderer) pointer(field interfacecontract.Field, value string, depth int) {
	if depth == 0 {
		r.value(field.Type(), value, "fieldPath", "fieldGoPath", "depth+1", func() { r.constraints(field, value) })
		return
	}
	fmt.Fprintf(&r.source, "if %s == nil {\n", value)
	fmt.Fprintln(&r.source, "if err := b.node(fieldPath, depth+1); err != nil { return zero, err }")
	fmt.Fprintf(&r.source, "} else {\npointer%d := *%s\n", depth, value)
	r.pointer(field, fmt.Sprintf("pointer%d", depth), depth-1)
	fmt.Fprintf(&r.source, "if b.copyValues { %s = &pointer%d }\n}\n", value, depth)
}

func (r *valueRenderer) value(kind interfacecontract.Type, value, path, goPath, depth string, constraints func()) {
	if kind.Kind() != interfacecontract.TypeMessage {
		fmt.Fprintf(&r.source, "if err := b.node(%s, %s); err != nil { return zero, err }\n", path, depth)
	}
	if constraints != nil {
		constraints()
	}
	switch kind.Kind() {
	case interfacecontract.TypeString:
		r.imports["unicode/utf8"] = true
		fmt.Fprintf(&r.source, "if !utf8.ValidString(%s) { return zero, b.failure(%s, \"utf8\") }\n", value, path)
	case interfacecontract.TypeFloat32, interfacecontract.TypeFloat64:
		r.imports["math"] = true
		fmt.Fprintf(&r.source, "if math.IsNaN(float64(%s)) || math.IsInf(float64(%s), 0) { return zero, b.failure(%s, \"finite\") }\n", value, value, path)
	case interfacecontract.TypeBytes:
		fmt.Fprintf(&r.source, "if b.copyValues { %s = append([]byte(nil), %s...) }\n", value, value)
	case interfacecontract.TypeTimestamp:
		fmt.Fprintf(&r.source, "if %s.UTC().Year() < 1 || %s.UTC().Year() > 9999 { return zero, b.failure(%s, \"timestamp\") }\n", value, value, path)
		fmt.Fprintf(&r.source, "if b.copyValues { %s = %s.Round(0).UTC() }\n", value, value)
	case interfacecontract.TypeMessage:
		name, _ := kind.MessageName()
		fmt.Fprintf(&r.source, "copied, err := visit%s(%s, %s, %s, %s, b)\nif err != nil { return zero, err }\nif b.copyValues { %s = copied }\n", name, value, goPath, path, depth, value)
	case interfacecontract.TypeRepeated:
		r.imports["slices"], r.imports["strconv"] = true, true
		fmt.Fprintf(&r.source, "if len(%s) > b.remaining { return zero, b.failure(%s, \"maximum_nodes\") }\n", value, path)
		fmt.Fprintf(&r.source, "if b.copyValues { if len(%s) == 0 { %s = nil } else { %s = slices.Clone(%s) } }\n", value, value, value, value)
		fmt.Fprintf(&r.source, "for index := range %s {\nitem := %s[index]\nitemPath := %s + \"[\" + strconv.Itoa(index) + \"]\"\n", value, value, path)
		element, _ := kind.Element()
		r.value(element, "item", "itemPath", goPath, "("+depth+")+1", nil)
		fmt.Fprintf(&r.source, "if b.copyValues { %s[index] = item }\n}\n", value)
	case interfacecontract.TypeMap:
		r.imports["maps"], r.imports["slices"], r.imports["sort"], r.imports["strconv"] = true, true, true, true
		fmt.Fprintf(&r.source, "if len(%s) > b.remaining/2 { return zero, b.failure(%s, \"maximum_nodes\") }\n", value, path)
		fmt.Fprintf(&r.source, "if b.copyValues { if len(%s) == 0 { %s = nil } else { %s = maps.Clone(%s) } }\n", value, value, value, value)
		fmt.Fprintf(&r.source, "keys := slices.Collect(maps.Keys(%s))\n", value)
		key, _ := kind.Key()
		fmt.Fprintf(&r.source, "sort.Slice(keys, func(a, c int) bool { return %s < %s })\n", canonicalMapKey(key, "keys[a]"), canonicalMapKey(key, "keys[c]"))
		fmt.Fprintf(&r.source, "for index, key := range keys {\nitemPath := %s + \"[\" + strconv.Itoa(index) + \"]\"\n", path)
		fmt.Fprintf(&r.source, "if err := b.node(itemPath, (%s)+1); err != nil { return zero, err }\n", depth)
		if key.Kind() == interfacecontract.TypeString {
			r.imports["unicode/utf8"] = true
			fmt.Fprintln(&r.source, "if !utf8.ValidString(key) { return zero, b.failure(itemPath, \"utf8\") }")
		}
		fmt.Fprintf(&r.source, "item := %s[key]\n", value)
		element, _ := kind.Value()
		r.value(element, "item", "itemPath", goPath, "("+depth+")+1", nil)
		fmt.Fprintf(&r.source, "if b.copyValues { %s[key] = item }\n}\n", value)
	}
}

func canonicalMapKey(kind interfacecontract.Type, value string) string {
	switch kind.Kind() {
	case interfacecontract.TypeBoolean:
		return "strconv.FormatBool(" + value + ")"
	case interfacecontract.TypeInt32, interfacecontract.TypeInt64:
		return "strconv.FormatInt(int64(" + value + "), 10)"
	case interfacecontract.TypeUint32, interfacecontract.TypeUint64:
		return "strconv.FormatUint(uint64(" + value + "), 10)"
	default:
		return value
	}
}

func (r *valueRenderer) constraints(field interfacecontract.Field, value string) {
	for index, target := range r.targets {
		if target.Field().Name() != field.Name() || target.Field().Type().Canonical() != field.Type().Canonical() {
			continue
		}
		rules := target.Rules()
		if rules.Empty() {
			continue
		}
		side, _, _ := strings.Cut(target.Path(), ".")
		fmt.Fprintf(&r.source, "if b.copyValues && b.side == %q && fieldGoPath == %q {\n", side, target.GoPath())
		checks := make(map[string]string)
		length := "uint64(len(" + value + "))"
		if field.Type().Kind() == interfacecontract.TypeString {
			r.imports["unicode/utf8"] = true
			length = "uint64(utf8.RuneCountInString(" + value + "))"
		}
		if bound, ok := rules.MinLength(); ok {
			checks["min_length"] = fmt.Sprintf("%s < %d", length, bound)
		}
		if bound, ok := rules.MaxLength(); ok {
			checks["max_length"] = fmt.Sprintf("%s > %d", length, bound)
		}
		if bound, ok := rules.MinItems(); ok {
			checks["min_items"] = fmt.Sprintf("uint64(len(%s)) < %d", value, bound)
		}
		if bound, ok := rules.MaxItems(); ok {
			checks["max_items"] = fmt.Sprintf("uint64(len(%s)) > %d", value, bound)
		}
		if bound, ok := rules.Minimum(); ok {
			checks["minimum"] = value + " < " + bound.Canonical()
		}
		if bound, ok := rules.Maximum(); ok {
			checks["maximum"] = value + " > " + bound.Canonical()
		}
		if _, ok := rules.Pattern(); ok {
			checks["pattern"] = fmt.Sprintf("!valuePattern%d.MatchString(%s)", index, value)
		}
		names := make([]string, 0, len(checks))
		for name := range checks {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			fmt.Fprintf(&r.source, "if %s { return zero, b.failure(fieldPath, %q) }\n", checks[name], name)
		}
		fmt.Fprintln(&r.source, "}")
	}
}
