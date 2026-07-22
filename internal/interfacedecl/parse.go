// Package interfacedecl parses authoritative Interface directives from Go source.
package interfacedecl

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/interfaceid"
)

const directivePrefix = "//plystra:interface"

// ErrInvalid reports an invalid Interface declaration.
var ErrInvalid = errors.New("invalid Interface declaration")

// Position identifies one project or module source location.
type Position struct {
	Path   string
	Line   int
	Column int
}

// Declaration is one authoritative mapping from a Go type to an Interface ID.
type Declaration struct {
	id          interfaceid.Identifier
	packageName string
	typeName    string
	position    Position
}

// ID returns the exact declared Interface ID.
func (d Declaration) ID() interfaceid.Identifier { return d.id }

// PackageName returns the Go package clause name.
func (d Declaration) PackageName() string { return d.packageName }

// TypeName returns the marked Go type name.
func (d Declaration) TypeName() string { return d.typeName }

// Position returns the directive source location.
func (d Declaration) Position() Position { return d.position }

// ParseFile parses Interface directives from one authored Go source file.
func ParseFile(path string, source []byte) ([]Declaration, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, source, parser.AllErrors|parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("%w: parse %s: %v", ErrInvalid, path, err)
	}

	directives, err := collectDirectives(files, file)
	if err != nil {
		return nil, err
	}
	if len(directives) == 0 {
		return nil, nil
	}

	consumed := make(map[*ast.Comment]bool, len(directives))
	declarations := make([]Declaration, 0, len(directives))
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpecification, ok := specification.(*ast.TypeSpec)
			if !ok {
				continue
			}
			documentation := typeSpecification.Doc
			if documentation == nil && len(general.Specs) == 1 {
				documentation = general.Doc
			}
			attached := attachedDirectives(documentation, directives)
			if len(attached) == 0 {
				continue
			}
			for _, directive := range attached {
				consumed[directive.comment] = true
			}
			if len(attached) != 1 {
				return nil, declarationError(attached[1].position, "type Interface must have exactly one directive")
			}
			directive := attached[0]
			if typeSpecification.Name.Name != "Interface" {
				return nil, declarationError(directive.position, "directive must document the exported type Interface")
			}
			if _, ok := typeSpecification.Type.(*ast.InterfaceType); !ok || typeSpecification.Assign.IsValid() {
				return nil, declarationError(directive.position, "type Interface must be a defined Go interface")
			}
			declarations = append(declarations, Declaration{
				id:          directive.id,
				packageName: file.Name.Name,
				typeName:    typeSpecification.Name.Name,
				position:    directive.position,
			})
		}
	}

	for _, directive := range directives {
		if !consumed[directive.comment] {
			return nil, declarationError(directive.position, "directive must immediately document type Interface")
		}
	}

	sort.Slice(declarations, func(left, right int) bool {
		return positionLess(declarations[left].position, declarations[right].position)
	})
	return declarations, nil
}

type parsedDirective struct {
	comment  *ast.Comment
	id       interfaceid.Identifier
	position Position
}

func collectDirectives(files *token.FileSet, file *ast.File) ([]parsedDirective, error) {
	var directives []parsedDirective
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if !strings.HasPrefix(comment.Text, directivePrefix) && !strings.HasPrefix(comment.Text, "/*plystra:interface") {
				continue
			}
			position := sourcePosition(files.Position(comment.Pos()))
			if !strings.HasPrefix(comment.Text, directivePrefix+" ") {
				return nil, declarationError(position, "expected //plystra:interface <interface-id>")
			}
			value := strings.TrimPrefix(comment.Text, directivePrefix+" ")
			if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n") {
				return nil, declarationError(position, "expected one canonical Interface ID after the directive")
			}
			identifier, err := interfaceid.Parse(value)
			if err != nil {
				return nil, declarationError(position, err.Error())
			}
			directives = append(directives, parsedDirective{comment: comment, id: identifier, position: position})
		}
	}
	sort.Slice(directives, func(left, right int) bool {
		return positionLess(directives[left].position, directives[right].position)
	})
	return directives, nil
}

func attachedDirectives(group *ast.CommentGroup, directives []parsedDirective) []parsedDirective {
	if group == nil {
		return nil
	}
	comments := make(map[*ast.Comment]bool, len(group.List))
	for _, comment := range group.List {
		comments[comment] = true
	}
	var attached []parsedDirective
	for _, directive := range directives {
		if comments[directive.comment] {
			attached = append(attached, directive)
		}
	}
	return attached
}

func sourcePosition(position token.Position) Position {
	return Position{Path: position.Filename, Line: position.Line, Column: position.Column}
}

func positionLess(left, right Position) bool {
	if left.Path != right.Path {
		return left.Path < right.Path
	}
	if left.Line != right.Line {
		return left.Line < right.Line
	}
	return left.Column < right.Column
}

func declarationError(position Position, message string) error {
	return fmt.Errorf("%w: %s:%d:%d: %s", ErrInvalid, position.Path, position.Line, position.Column, message)
}
