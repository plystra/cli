// Package resourcedecl parses authoritative Resource directives from Go source.
package resourcedecl

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

// ErrInvalid reports an invalid Resource declaration.
var ErrInvalid = errors.New("invalid Resource declaration")

// Position identifies a module-relative authored source location.
type Position struct {
	Path   string
	Line   int
	Column int
}

// InvalidError retains an authored position without changing error identity.
type InvalidError struct {
	position Position
	detail   string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("%s: %s:%d:%d: %s", ErrInvalid, e.position.Path, e.position.Line, e.position.Column, e.detail)
}

func (*InvalidError) Unwrap() error        { return ErrInvalid }
func (e *InvalidError) Position() Position { return e.position }

// Declaration is the exact Resource identity and its authoritative source.
type Declaration struct {
	id          string
	packageName string
	position    Position
}

func (d Declaration) ID() string          { return d.id }
func (d Declaration) PackageName() string { return d.packageName }
func (d Declaration) Position() Position  { return d.position }

// ParseFile validates only Resource declarations, not Interface projection rules.
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
			if !strings.HasPrefix(comment.Text, "//plystra:resource") && !strings.HasPrefix(comment.Text, "/*plystra:resource") {
				continue
			}
			value, valid := strings.CutPrefix(comment.Text, "//plystra:resource ")
			if !valid {
				return nil, &InvalidError{position: position(comment.Pos()), detail: "expected //plystra:resource <resource-id>"}
			}
			if _, err := interfaceid.Parse(value); err != nil {
				return nil, &InvalidError{position: position(comment.Pos()), detail: "expected one canonical Resource ID after the directive"}
			}
			directives[comment] = value
			ordered = append(ordered, comment)
		}
	}
	var result []Declaration
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, spec := range general.Specs {
			typeSpec := spec.(*ast.TypeSpec)
			doc := typeSpec.Doc
			if doc == nil && len(general.Specs) == 1 {
				doc = general.Doc
			}
			if doc == nil {
				continue
			}
			var attached []*ast.Comment
			mixed := false
			for _, comment := range doc.List {
				if _, exists := directives[comment]; exists {
					attached = append(attached, comment)
				}
				mixed = mixed || strings.HasPrefix(comment.Text, "//plystra:interface") || strings.HasPrefix(comment.Text, "/*plystra:interface")
			}
			if len(attached) == 0 {
				continue
			}
			p := position(attached[0].Pos())
			if len(attached) != 1 || mixed {
				return nil, &InvalidError{position: p, detail: "type Resource must have exactly one Resource directive and no Interface directive"}
			}
			_, isInterface := typeSpec.Type.(*ast.InterfaceType)
			if typeSpec.Name.Name != "Resource" || !isInterface || typeSpec.Assign.IsValid() || typeSpec.TypeParams != nil {
				return nil, &InvalidError{position: p, detail: "directive must document the exported, non-generic defined Go interface Resource"}
			}
			result = append(result, Declaration{id: directives[attached[0]], packageName: file.Name.Name, position: p})
			delete(directives, attached[0])
		}
	}
	for _, comment := range ordered {
		if _, exists := directives[comment]; exists {
			return nil, &InvalidError{position: position(comment.Pos()), detail: "directive must immediately document type Resource"}
		}
	}
	return result, nil
}
