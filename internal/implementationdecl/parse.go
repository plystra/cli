// Package implementationdecl parses authoritative Implementation constructor
// directives from Go source.
package implementationdecl

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

const directivePrefix = "//plystra:implements"

// ErrInvalid reports an invalid Implementation constructor declaration.
var ErrInvalid = errors.New("invalid Implementation declaration")

// Position identifies one project or module source location.
type Position struct {
	Path   string
	Line   int
	Column int
}

// ImplementedInterface records one exact Interface directive on a constructor.
type ImplementedInterface struct {
	id       interfaceid.Identifier
	position Position
}

// ID returns the exact declared Interface ID.
func (i ImplementedInterface) ID() interfaceid.Identifier { return i.id }

// Position returns the directive source location.
func (i ImplementedInterface) Position() Position { return i.position }

// Declaration is one exported package-level constructor marked as implementing
// one or more Interfaces.
type Declaration struct {
	packageName string
	function    string
	position    Position
	interfaces  []ImplementedInterface
}

// PackageName returns the Go package clause name.
func (d Declaration) PackageName() string { return d.packageName }

// FunctionName returns the exported constructor function name.
func (d Declaration) FunctionName() string { return d.function }

// Position returns the constructor function-name source location.
func (d Declaration) Position() Position { return d.position }

// ImplementedInterfaces returns a defensive source-ordered view of the exact
// Interface directives attached to the constructor.
func (d Declaration) ImplementedInterfaces() []ImplementedInterface {
	return append([]ImplementedInterface(nil), d.interfaces...)
}

// ParseFile parses Implementation constructor directives from one authored Go
// source file. It validates directive attachment and exported package-level
// function identity; constructor signatures and Go assignability are validated
// after package type loading.
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
	for _, node := range file.Decls {
		function, ok := node.(*ast.FuncDecl)
		if !ok {
			continue
		}
		attached := attachedDirectives(function.Doc, directives)
		if len(attached) == 0 {
			continue
		}
		for _, directive := range attached {
			consumed[directive.comment] = true
		}
		if function.Recv != nil {
			return nil, declarationError(attached[0].position, "directive must document a package-level constructor function")
		}
		if !ast.IsExported(function.Name.Name) {
			return nil, declarationError(attached[0].position, "constructor function must be exported")
		}

		seen := make(map[string]Position, len(attached))
		implemented := make([]ImplementedInterface, len(attached))
		for index, directive := range attached {
			identifier := directive.id.String()
			if first, exists := seen[identifier]; exists {
				return nil, declarationError(directive.position, fmt.Sprintf("constructor declares Interface %s more than once; first declared at %s:%d:%d", identifier, first.Path, first.Line, first.Column))
			}
			seen[identifier] = directive.position
			implemented[index] = ImplementedInterface{id: directive.id, position: directive.position}
		}
		declarations = append(declarations, Declaration{
			packageName: file.Name.Name,
			function:    function.Name.Name,
			position:    sourcePosition(files.Position(function.Name.Pos())),
			interfaces:  implemented,
		})
	}

	for _, directive := range directives {
		if !consumed[directive.comment] {
			return nil, declarationError(directive.position, "directive must immediately document an exported package-level constructor function")
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
			if !strings.HasPrefix(comment.Text, directivePrefix) && !strings.HasPrefix(comment.Text, "/*plystra:implements") {
				continue
			}
			position := sourcePosition(files.Position(comment.Pos()))
			if !strings.HasPrefix(comment.Text, directivePrefix+" ") {
				return nil, declarationError(position, "expected //plystra:implements <interface-id>")
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
