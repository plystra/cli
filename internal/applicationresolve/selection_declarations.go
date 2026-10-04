package applicationresolve

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"slices"

	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceinventory"
	"github.com/plystra/cli/internal/plugininventory"
)

// Compare the validated semantic inputs rather than Go compiler object graphs.
// Config defaults are compared privately, never formatted or publicly hashed.
func (s SelectionInputs) recheckDeclarations(ctx context.Context) error {
	current, err := interfaceinventory.DiscoverApplication(ctx, s.module, s.dependencies, interfaceinventory.Options{
		GoCommand: s.dependencyOptions.GoCommand, Environment: s.dependencyOptions.Environment, OutputLimit: s.dependencyOptions.OutputLimit,
	})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return declarationSnapshotError(s.module.ModulePath(), "go.mod")
	}
	if err := compareDeclarations(s.declarations, current); err != nil {
		return err
	}
	plugins, err := plugininventory.Build(s.module, s.dependencies)
	if err != nil {
		return declarationSnapshotError(s.module.ModulePath(), "go.mod")
	}
	before, after := s.inventory.Plugins(), plugins.Plugins()
	if len(before) != len(after) {
		return declarationSnapshotError(s.module.ModulePath(), "go.mod")
	}
	for index, a := range before {
		b := after[index]
		if a.Source() != b.Source() || a.ImportPath() != b.ImportPath() || a.Local() != b.Local() || !bytes.Equal(a.ManifestData(), b.ManifestData()) {
			return declarationSnapshotError(a.ModulePath(), a.Path()+"/plugin.yaml")
		}
	}
	return nil
}

func compareDeclarations(before, after interfaceinventory.Discovery) error {
	interfaces, currentInterfaces := before.Interfaces().Interfaces(), after.Interfaces().Interfaces()
	resources, currentResources := before.Resources().Resources(), after.Resources().Resources()
	implementations, currentImplementations := before.Implementations().Implementations(), after.Implementations().Implementations()
	providers, currentProviders := before.ResourceProviders().Providers(), after.ResourceProviders().Providers()
	// A new or removed declaration changes the candidate set even if unselected.
	if len(interfaces) != len(currentInterfaces) || len(resources) != len(currentResources) || len(implementations) != len(currentImplementations) || len(providers) != len(currentProviders) {
		return fmt.Errorf("%w: authored declaration membership changed", ErrConcurrentChange)
	}
	for index, a := range interfaces {
		b := currentInterfaces[index]
		if a.ID() != b.ID() || a.PackagePath() != b.PackagePath() || a.Source() != b.Source() || a.Local() != b.Local() ||
			a.ContractDigest() != b.ContractDigest() || a.ContractSupplementDigest() != b.ContractSupplementDigest() ||
			a.DocumentationDigest() != b.DocumentationDigest() || a.ExampleDigest() != b.ExampleDigest() || a.MetadataSource() != b.MetadataSource() {
			return declarationSnapshotError(a.ModulePath(), a.SourcePath())
		}
	}
	for index, a := range resources {
		b := currentResources[index]
		if a.ID() != b.ID() || a.PackagePath() != b.PackagePath() || a.Source() != b.Source() || a.Local() != b.Local() || a.ContractDigest() != b.ContractDigest() {
			return declarationSnapshotError(a.ModulePath(), a.SourcePath())
		}
	}
	for index, a := range implementations {
		b := currentImplementations[index]
		ac, ah := a.Configuration()
		bc, bh := b.Configuration()
		if a.Symbol() != b.Symbol() || a.Source() != b.Source() || a.Local() != b.Local() || a.ConcreteType().String() != b.ConcreteType().String() ||
			!reflect.DeepEqual(a.Declaration(), b.Declaration()) || !slices.Equal(a.RequiredInterfaces(), b.RequiredInterfaces()) ||
			!slices.Equal(a.OptionalInterfaces(), b.OptionalInterfaces()) || !slices.Equal(a.RequiredResources(), b.RequiredResources()) ||
			ah != bh || !sameConfigurationSchema(ac, bc) {
			return declarationSnapshotError(a.ModulePath(), a.SourcePath())
		}
	}
	for index, a := range providers {
		b := currentProviders[index]
		ac, ah := a.Configuration()
		bc, bh := b.Configuration()
		if a.ID() != b.ID() || a.Symbol() != b.Symbol() || a.Source() != b.Source() || a.Local() != b.Local() || a.ConcreteType().String() != b.ConcreteType().String() ||
			!reflect.DeepEqual(a.Declaration(), b.Declaration()) || !slices.Equal(a.Dependencies(), b.Dependencies()) || ah != bh || !sameConfigurationSchema(ac, bc) {
			return declarationSnapshotError(a.ModulePath(), a.Declaration().Position().Path)
		}
	}
	return nil
}

func sameConfigurationSchema(a, b implementationinventory.Configuration) bool {
	return a.PackagePath() == b.PackagePath() && a.TypeName() == b.TypeName() && sameConfigurationFields(a.Fields(), b.Fields())
}

func sameConfigurationFields(a, b []implementationinventory.ConfigurationField) bool {
	if len(a) != len(b) {
		return false
	}
	for index, left := range a {
		right := b[index]
		if left.Name() != right.Name() || left.GoName() != right.GoName() || left.Required() != right.Required() || left.BuildVisible() != right.BuildVisible() ||
			!bytes.Equal(left.DefaultJSON(), right.DefaultJSON()) || !sameConfigurationValue(left.Value(), right.Value()) {
			return false
		}
	}
	return true
}

func sameConfigurationValue(a, b implementationinventory.ConfigurationValue) bool {
	al, aa := a.ArrayLength()
	bl, ba := b.ArrayLength()
	ab, an := a.NumericBits()
	bb, bn := b.NumericBits()
	if a.Kind() != b.Kind() || a.TypeIdentity() != b.TypeIdentity() || a.PlatformSized() != b.PlatformSized() ||
		al != bl || aa != ba || ab != bb || an != bn || !sameConfigurationFields(a.Fields(), b.Fields()) {
		return false
	}
	ae, ah := a.Element()
	be, bh := b.Element()
	return ah == bh && (!ah || sameConfigurationValue(ae, be))
}

func declarationSnapshotError(module, path string) error {
	return newManifestSourceError(module, path, "authored-package", 0, 0,
		fmt.Errorf("%w: authored selection declarations changed or cannot be revalidated", ErrConcurrentChange))
}
