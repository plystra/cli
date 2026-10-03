// Package resourceproviderdecl parses authoritative Resource provider constructor
// directives from Go source.
package resourceproviderdecl

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"strings"

	"github.com/plystra/cli/internal/interfaceid"
)

const directivePrefix = "//plystra:implements-resource"

// ErrInvalid reports an invalid Resource provider constructor declaration.
var ErrInvalid = errors.New("invalid Resource provider declaration")

// Position identifies a module-relative authored source location.
type Position struct {
	Path   string
	Line   int
	Column int
}

// InvalidError retains an authored position without exposing malformed source.
type InvalidError struct {
	position Position
	detail   string
}

func (e *InvalidError) Error() string {
	if e == nil {
		return ErrInvalid.Error()
	}
	return fmt.Sprintf("%s: %s:%d:%d: %s", ErrInvalid, e.position.Path, e.position.Line, e.position.Column, e.detail)
}

func (*InvalidError) Unwrap() error { return ErrInvalid }

// Position returns the best available module-relative source position.
func (e *InvalidError) Position() Position {
	if e == nil {
		return Position{}
	}
	return e.position
}

// Declaration is one exact Resource identity on an exported provider constructor.
type Declaration struct {
	id          string
	packageName string
	function    string
	position    Position
}

func (d Declaration) ID() string           { return d.id }
func (d Declaration) PackageName() string  { return d.packageName }
func (d Declaration) FunctionName() string { return d.function }

// Position returns the constructor function-name source location.
func (d Declaration) Position() Position { return d.position }

// ParseFile validates directive identity and attachment to one exported,
// non-generic package-level function. Go package loading owns build selection,
// signature validation, and assignability.
func ParseFile(path string, source []byte) ([]Declaration, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, source, parser.AllErrors|parser.ParseComments)
	position := func(pos token.Pos) Position {
		p := files.Position(pos)
		return Position{Path: path, Line: p.Line, Column: p.Column}
	}
	if err != nil {
		p := Position{Path: path}
		var list scanner.ErrorList
		if errors.As(err, &list) && len(list) > 0 {
			p.Line, p.Column = list[0].Pos.Line, list[0].Pos.Column
		}
		return nil, &InvalidError{position: p, detail: "Go source cannot be parsed"}
	}

	directives := make(map[*ast.Comment]string)
	var ordered []*ast.Comment
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !strings.HasPrefix(comment.Text, directivePrefix) && !strings.HasPrefix(comment.Text, "/*plystra:implements-resource") {
				continue
			}
			value, valid := strings.CutPrefix(comment.Text, directivePrefix+" ")
			if !valid {
				return nil, &InvalidError{position: position(comment.Pos()), detail: "expected //plystra:implements-resource <resource-id>"}
			}
			if _, err := interfaceid.Parse(value); err != nil {
				return nil, &InvalidError{position: position(comment.Pos()), detail: "expected one canonical Resource ID after the directive"}
			}
			directives[comment] = value
			ordered = append(ordered, comment)
		}
	}

	var result []Declaration
	for _, node := range file.Decls {
		function, ok := node.(*ast.FuncDecl)
		if !ok || function.Doc == nil {
			continue
		}
		var attached []*ast.Comment
		mixed := false
		for _, comment := range function.Doc.List {
			if _, exists := directives[comment]; exists {
				attached = append(attached, comment)
				continue
			}
			mixed = mixed || strings.HasPrefix(comment.Text, "//plystra:implements") || strings.HasPrefix(comment.Text, "/*plystra:implements") ||
				strings.HasPrefix(comment.Text, "//plystra:resource") || strings.HasPrefix(comment.Text, "/*plystra:resource") ||
				strings.HasPrefix(comment.Text, "//plystra:interface") || strings.HasPrefix(comment.Text, "/*plystra:interface")
		}
		if len(attached) == 0 {
			continue
		}
		p := position(attached[0].Pos())
		if len(attached) != 1 {
			return nil, &InvalidError{position: position(attached[1].Pos()), detail: "Resource provider constructor must have exactly one Resource directive"}
		}
		if mixed {
			return nil, &InvalidError{position: p, detail: "Resource provider constructor cannot carry ordinary Implementation or contract type directives"}
		}
		if function.Recv != nil {
			return nil, &InvalidError{position: p, detail: "directive must document a package-level constructor function"}
		}
		if !ast.IsExported(function.Name.Name) {
			return nil, &InvalidError{position: p, detail: "constructor function must be exported"}
		}
		if function.Type.TypeParams != nil {
			return nil, &InvalidError{position: p, detail: "constructor function must be non-generic"}
		}
		result = append(result, Declaration{
			id: directives[attached[0]], packageName: file.Name.Name,
			function: function.Name.Name, position: position(function.Name.Pos()),
		})
		delete(directives, attached[0])
	}
	for _, comment := range ordered {
		if _, exists := directives[comment]; exists {
			return nil, &InvalidError{position: position(comment.Pos()), detail: "directive must immediately document an exported package-level constructor function"}
		}
	}
	return result, nil
}
