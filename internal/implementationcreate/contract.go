package implementationcreate

import (
	"fmt"
	"go/format"
	"go/types"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
)

type selectedContract struct {
	kind      string
	operation interfaceinventory.Interface
	resource  interfaceinventory.Resource
}

func findContract(discovery interfaceinventory.Discovery, id string) (selectedContract, error) {
	operation, hasInterface := findInterface(discovery.Interfaces(), id)
	var resource interfaceinventory.Resource
	hasResource := false
	for _, candidate := range discovery.Resources().Resources() {
		if candidate.ID() == id {
			resource, hasResource = candidate, true
			break
		}
	}
	if hasInterface && hasResource {
		return selectedContract{}, fmt.Errorf("%w: %s; Interface at %s; Resource at %s", ErrAmbiguousContract, id, operation.Source(), resource.Source())
	}
	if hasInterface {
		return selectedContract{kind: "interface", operation: operation}, nil
	}
	if hasResource {
		return selectedContract{kind: "resource", resource: resource}, nil
	}
	return selectedContract{}, fmt.Errorf("%w: %s; add the Go Module that defines it or author the contract first", ErrContractNotFound, id)
}

func (c selectedContract) digest() string {
	if c.kind == "resource" {
		return c.resource.ContractDigest()
	}
	return c.operation.ContractDigest()
}

func (c selectedContract) render(id interfaceid.Identifier, target targetPackage) ([]byte, error) {
	if c.kind == "resource" {
		return renderResource(id, c.resource, target)
	}
	if !canImport(c.operation.PackagePath(), target.importPath) {
		return nil, fmt.Errorf("%w: %s is internal to another package tree", ErrUnimplementableContract, c.operation.PackagePath())
	}
	return render(id, c.operation, target.packageName)
}

func canImport(imported, target string) bool {
	segments := strings.Split(imported, "/")
	for index, segment := range segments {
		if segment != "internal" {
			continue
		}
		parent := strings.Join(segments[:index], "/")
		if parent == "" || target != parent && !strings.HasPrefix(target, parent+"/") {
			return false
		}
	}
	return true
}

func renderResource(id interfaceid.Identifier, resource interfaceinventory.Resource, target targetPackage) ([]byte, error) {
	if !canImport(resource.PackagePath(), target.importPath) {
		return nil, fmt.Errorf("%w: %s is internal to another package tree", ErrUnimplementableContract, resource.PackagePath())
	}
	aliases := map[string]string{"errors": "errors", resource.PackagePath(): "contract"}
	methods, err := resource.Contract().ImplementationMethods(target.importPath, func(pkg *types.Package) string {
		if alias, exists := aliases[pkg.Path()]; exists {
			return alias
		}
		alias := fmt.Sprintf("dependency%d", len(aliases)-1)
		aliases[pkg.Path()] = alias
		return alias
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %s at %s: %w", ErrUnimplementableContract, id, resource.Source(), err)
	}
	var source strings.Builder
	fmt.Fprintf(&source, "package %s\n\nimport (\n", target.packageName)
	paths := make([]string, 0, len(aliases))
	for path := range aliases {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		fmt.Fprintf(&source, "%s %q\n", aliases[path], path)
	}
	fmt.Fprintf(&source, `)

var errNotImplemented = errors.New(%q)

type Provider struct{}

//plystra:implements-resource %s
func New() (*Provider, error) {
	return nil, errNotImplemented
}

`, id.String()+" Resource provider is not implemented", id)
	for _, method := range methods {
		fmt.Fprintf(&source, "func (*Provider) %s%s {\n panic(errNotImplemented)\n}\n\n", method.Name(), strings.TrimPrefix(method.Signature(), "func"))
	}
	source.WriteString("var _ contract.Resource = (*Provider)(nil)\n")
	return format.Source([]byte(source.String()))
}
