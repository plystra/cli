package interfaceinventory

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/resourcecontract"
	"github.com/plystra/cli/internal/resourcedecl"
)

// Resource is one validated consumer contract with stable owning provenance.
type Resource struct {
	modulePath    string
	moduleVersion string
	packagePath   string
	local         bool
	declaration   resourcedecl.Declaration
	contract      resourcecontract.Contract
}

func (r Resource) ID() string                            { return r.declaration.ID() }
func (r Resource) ModulePath() string                    { return r.modulePath }
func (r Resource) ModuleVersion() string                 { return r.moduleVersion }
func (r Resource) PackagePath() string                   { return r.packagePath }
func (r Resource) SourcePath() string                    { return r.declaration.Position().Path }
func (r Resource) Local() bool                           { return r.local }
func (r Resource) Declaration() resourcedecl.Declaration { return r.declaration }
func (r Resource) ContractDigest() string                { return r.contract.Digest() }

func (r Resource) Source() string {
	version := r.moduleVersion
	if version == "" {
		version = "local"
	}
	p := r.declaration.Position()
	return fmt.Sprintf("%s@%s/%s:%d:%d", r.modulePath, version, p.Path, p.Line, p.Column)
}

// ResourceIndex is the immutable visible Resource inventory from shared discovery.
type ResourceIndex struct{ resources []Resource }

func (i ResourceIndex) Resources() []Resource { return append([]Resource(nil), i.resources...) }

var ErrDuplicateResourceID = errors.New("duplicate visible Resource ID")

// DuplicateResourceIDError retains every defining source for the first duplicate
// exact Resource identity, in deterministic identity and provenance order.
type DuplicateResourceIDError struct {
	id          string
	definitions []Resource
}

func (e *DuplicateResourceIDError) ID() string { return e.id }
func (e *DuplicateResourceIDError) Definitions() []Resource {
	return append([]Resource(nil), e.definitions...)
}
func (*DuplicateResourceIDError) Unwrap() error { return ErrDuplicateResourceID }
func (e *DuplicateResourceIDError) Error() string {
	definitions := make([]string, len(e.definitions))
	for i, definition := range e.definitions {
		definitions[i] = fmt.Sprintf("package %q at %s", definition.PackagePath(), definition.Source())
	}
	return fmt.Sprintf("%s %q in [%s]", ErrDuplicateResourceID, e.id, strings.Join(definitions, ", "))
}

func resourceIndex(resources []Resource) (ResourceIndex, error) {
	sort.Slice(resources, func(i, j int) bool {
		if resources[i].ID() != resources[j].ID() {
			return resources[i].ID() < resources[j].ID()
		}
		if resources[i].PackagePath() != resources[j].PackagePath() {
			return resources[i].PackagePath() < resources[j].PackagePath()
		}
		return resources[i].Source() < resources[j].Source()
	})
	for first := 0; first < len(resources); {
		last := first + 1
		for last < len(resources) && resources[last].ID() == resources[first].ID() {
			last++
		}
		if last-first > 1 {
			return ResourceIndex{}, &DuplicateResourceIDError{id: resources[first].ID(), definitions: append([]Resource(nil), resources[first:last]...)}
		}
		first = last
	}
	return ResourceIndex{resources: resources}, nil
}
