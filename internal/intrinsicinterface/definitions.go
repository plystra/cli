// Package intrinsicinterface adapts the Kernel-owned reserved Interface
// inventory to the CLI's canonical Interface identity model.
package intrinsicinterface

import (
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/interfaceid"
	kernelintrinsic "github.com/plystra/kernel/intrinsic"
	"golang.org/x/mod/module"
)

var definitions = mustLoad()

// Definition identifies one immutable canonical intrinsic Kernel Interface.
type Definition struct {
	id          interfaceid.Identifier
	packagePath string
	source      string
}

// ID returns the exact reserved Interface ID.
func (d Definition) ID() interfaceid.Identifier { return d.id }

// PackagePath returns the canonical Go Interface package import path.
func (d Definition) PackagePath() string { return d.packagePath }

// Source returns stable Kernel-owned declaration provenance.
func (d Definition) Source() string { return d.source }

// Definitions returns every intrinsic Interface sorted by exact ID.
func Definitions() []Definition {
	return append([]Definition(nil), definitions...)
}

// Lookup returns one exact intrinsic Interface definition.
func Lookup(id interfaceid.Identifier) (Definition, bool) {
	value := id.String()
	index := sort.Search(len(definitions), func(index int) bool {
		return definitions[index].id.String() >= value
	})
	if value == "" || index >= len(definitions) || definitions[index].id != id {
		return Definition{}, false
	}
	return definitions[index], true
}

func mustLoad() []Definition {
	kernelDefinitions := kernelintrinsic.InterfaceDefinitions()
	loaded := make([]Definition, 0, len(kernelDefinitions))
	for _, definition := range kernelDefinitions {
		id, err := interfaceid.Parse(definition.ID())
		if err != nil {
			panic(fmt.Sprintf("adapt intrinsic Kernel Interface %q: %v", definition.ID(), err))
		}
		if !strings.HasPrefix(id.Name(), "kernel.") {
			panic(fmt.Sprintf("adapt intrinsic Kernel Interface %s: ID is outside the reserved namespace", id))
		}
		if err := module.CheckImportPath(definition.PackagePath()); err != nil {
			panic(fmt.Sprintf("adapt intrinsic Kernel Interface %s: invalid package path %q: %v", id, definition.PackagePath(), err))
		}
		if definition.Source() == "" || strings.ContainsAny(definition.Source(), "\x00\r\n") {
			panic(fmt.Sprintf("adapt intrinsic Kernel Interface %s: unsafe source provenance", id))
		}
		loaded = append(loaded, Definition{
			id:          id,
			packagePath: definition.PackagePath(),
			source:      definition.Source(),
		})
	}
	sort.Slice(loaded, func(left, right int) bool {
		return loaded[left].id.String() < loaded[right].id.String()
	})
	for index := 1; index < len(loaded); index++ {
		if loaded[index-1].id == loaded[index].id {
			panic("adapt intrinsic Kernel Interface: duplicate " + loaded[index].id.String())
		}
	}
	return loaded
}
