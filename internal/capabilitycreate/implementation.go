package capabilitycreate

import (
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/goname"
)

const maximumPluginGoSourceBytes = 1 << 20

// ErrRenderImplementation reports that a capability plan could not produce a
// safe user-owned provider implementation scaffold.
var ErrRenderImplementation = errors.New("render capability implementation scaffold")

// RenderImplementationWrite emits a compile-safe provider method when the
// target Plugin does not already declare the generated operation. Existing
// methods remain untouched so implementation is idempotent and user-owned.
func RenderImplementationWrite(plan Plan) (atomicfs.Write, bool, error) {
	target := plan.Target()
	identifier := plan.Version().Target()
	if plan.ModulePath() == "" || target.ID() == "" || target.Path() == "" || identifier.String() == "" {
		return atomicfs.Write{}, false, fmt.Errorf("%w: plan is empty", ErrRenderImplementation)
	}
	packageName, implemented, err := inspectPluginPackage(target.Path(), goname.Operation(identifier))
	if err != nil {
		return atomicfs.Write{}, false, fmt.Errorf("%w: %w", ErrRenderImplementation, err)
	}
	if implemented {
		return atomicfs.Write{}, false, nil
	}

	contractImport := applicationContractPath(plan.ModulePath(), identifier.Name(), identifier.Major())
	operation := goname.Operation(identifier)
	var source strings.Builder
	fmt.Fprintf(&source, "package %s\n\n", packageName)
	fmt.Fprintln(&source, "import (")
	fmt.Fprintln(&source, "\t\"context\"")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "\tcontract %s\n", strconv.Quote(contractImport))
	fmt.Fprintln(&source, "\tkernelinvocation \"github.com/plystra/kernel/invocation\"")
	fmt.Fprintln(&source, ")")
	fmt.Fprintln(&source)
	fmt.Fprintf(&source, "// %s is the provider entry point for %s.\n", operation, identifier)
	fmt.Fprintf(&source, "func (*Plugin) %s(_ context.Context, _ contract.Request) (contract.Response, error) {\n", operation)
	fmt.Fprintln(&source, "\tfailure, err := kernelinvocation.NewError(kernelinvocation.ErrorUnavailable, \"implementation.unavailable\")")
	fmt.Fprintln(&source, "\tif err != nil {")
	fmt.Fprintln(&source, "\t\treturn contract.Response{}, err")
	fmt.Fprintln(&source, "\t}")
	fmt.Fprintln(&source, "\treturn contract.Response{}, failure")
	fmt.Fprintln(&source, "}")
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return atomicfs.Write{}, false, fmt.Errorf("%w: format scaffold: %v", ErrRenderImplementation, err)
	}
	return atomicfs.Write{
		Path:         path.Join(target.Directory(), implementationFileName(identifier.Name(), identifier.Major())),
		Data:         formatted,
		Mode:         0o644,
		MustNotExist: true,
	}, true, nil
}

func inspectPluginPackage(directory, operation string) (string, bool, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", false, fmt.Errorf("read target plugin: %w", err)
	}
	packageName := ""
	implemented := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		filePath := filepath.Join(directory, name)
		info, err := os.Lstat(filePath)
		if err != nil {
			return "", false, fmt.Errorf("inspect %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 {
			return "", false, fmt.Errorf("%s is not a regular Go source file", name)
		}
		if info.Size() > maximumPluginGoSourceBytes {
			return "", false, fmt.Errorf("%s exceeds %d bytes", name, maximumPluginGoSourceBytes)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", false, fmt.Errorf("read %s: %w", name, err)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, data, parser.SkipObjectResolution)
		if err != nil {
			return "", false, fmt.Errorf("parse %s: %w", name, err)
		}
		if packageName == "" {
			packageName = parsed.Name.Name
		} else if parsed.Name.Name != packageName {
			return "", false, fmt.Errorf("target plugin Go files declare packages %q and %q", packageName, parsed.Name.Name)
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || function.Name.Name != operation || len(function.Recv.List) != 1 {
				continue
			}
			if receiverType(function.Recv.List[0].Type) == "Plugin" {
				implemented = true
			}
		}
	}
	if packageName == "" {
		return "", false, errors.New("target plugin contains no non-test Go source")
	}
	if packageName == "main" {
		return "", false, errors.New("target plugin package main cannot be imported by generated assembly")
	}
	return packageName, implemented, nil
}

func receiverType(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.StarExpr:
		return receiverType(value.X)
	default:
		return ""
	}
}

func applicationContractPath(modulePath, capabilityName string, major uint64) string {
	components := append([]string{modulePath, "generated", "go", "contracts"}, strings.Split(capabilityName, ".")...)
	components = append(components, "v"+strconv.FormatUint(major, 10))
	return path.Join(components...)
}

func implementationFileName(capabilityName string, major uint64) string {
	return "capability_" + capabilityName + "_v" + strconv.FormatUint(major, 10) + ".go"
}
