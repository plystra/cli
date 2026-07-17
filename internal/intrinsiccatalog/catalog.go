// Package intrinsiccatalog adapts the Kernel-owned intrinsic Capability
// catalog to the CLI's normalized contract model.
package intrinsiccatalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/capabilitymeta"
	kernelcatalog "github.com/plystra/kernel/capability/catalog"
)

var definitions = mustLoad()

// Definition is one immutable intrinsic Kernel Capability contract.
type Definition struct {
	id           capabilityid.Identifier
	contractJSON []byte
	digest       string
	source       string
}

// ID returns the exact reserved intrinsic Capability ID.
func (d Definition) ID() capabilityid.Identifier { return d.id }

// ContractJSON returns a defensive copy of the normalized exact contract.
func (d Definition) ContractJSON() []byte {
	return append([]byte(nil), d.contractJSON...)
}

// ContractDigest returns the SHA-256 digest of ContractJSON.
func (d Definition) ContractDigest() string { return d.digest }

// Source returns stable Kernel catalog provenance for diagnostics.
func (d Definition) Source() string { return d.source }

// Definitions returns every intrinsic definition sorted by canonical ID.
func Definitions() []Definition {
	return append([]Definition(nil), definitions...)
}

// Lookup returns one exact intrinsic definition.
func Lookup(id capabilityid.Identifier) (Definition, bool) {
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
	kernelDefinitions := kernelcatalog.Definitions()
	loaded := make([]Definition, 0, len(kernelDefinitions))
	for _, definition := range kernelDefinitions {
		id, err := capabilityid.Parse(definition.ID().String())
		if err != nil {
			panic(fmt.Sprintf("adapt intrinsic Kernel catalog: parse %q: %v", definition.ID(), err))
		}
		if !strings.HasPrefix(id.Name(), "kernel.") {
			panic(fmt.Sprintf("adapt intrinsic Kernel catalog: %s is not reserved", id))
		}
		canonical, err := capabilitymeta.NormalizeSchema(definition.Source())
		if err != nil {
			panic(fmt.Sprintf("adapt intrinsic Kernel catalog: normalize %s: %v", id, err))
		}
		digestBytes := sha256.Sum256(canonical)
		if digestBytes != definition.SchemaDigest() {
			panic(fmt.Sprintf("adapt intrinsic Kernel catalog: %s digest differs between Kernel and CLI contract models", id))
		}
		loaded = append(loaded, Definition{
			id:           id,
			contractJSON: append([]byte(nil), canonical...),
			digest:       "sha256:" + hex.EncodeToString(digestBytes[:]),
			source:       "github.com/plystra/kernel/capability/catalog " + id.String(),
		})
	}
	sort.Slice(loaded, func(left, right int) bool {
		return loaded[left].id.String() < loaded[right].id.String()
	})
	for index := 1; index < len(loaded); index++ {
		if loaded[index-1].id == loaded[index].id {
			panic("adapt intrinsic Kernel catalog: duplicate " + loaded[index].id.String())
		}
	}
	return loaded
}
