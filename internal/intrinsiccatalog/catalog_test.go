package intrinsiccatalog_test

import (
	"bytes"
	"slices"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/intrinsiccatalog"
)

func TestDefinitionsAdaptAuthoritativeKernelCatalog(t *testing.T) {
	t.Parallel()

	definitions := intrinsiccatalog.Definitions()
	if got := definitionIDs(definitions); !slices.Equal(got, []string{"kernel.health/v1", "kernel.info/v1"}) {
		t.Fatalf("Definitions = %v", got)
	}
	wantDigests := []string{
		"sha256:83ae98630f252c7bb58cffa403e1f4aac55376224b430001dad34266bdccb80c",
		"sha256:3ec0d8f2bfda17c88d8bf5e724f8612049fb0779074999f3a7c9fc9495d3695b",
	}
	for index, definition := range definitions {
		if definition.ContractDigest() != wantDigests[index] || !bytes.Contains(definition.ContractJSON(), []byte(`"id":"`+definition.ID().String()+`"`)) {
			t.Fatalf("definition %s = digest %q, contract %s", definition.ID(), definition.ContractDigest(), definition.ContractJSON())
		}
		if definition.Source() != "github.com/plystra/kernel/capability/catalog "+definition.ID().String() {
			t.Fatalf("definition %s source = %q", definition.ID(), definition.Source())
		}
	}
}

func TestDefinitionsAndContractsAreImmutable(t *testing.T) {
	t.Parallel()

	definitions := intrinsiccatalog.Definitions()
	health := definitions[0]
	definitions[0] = intrinsiccatalog.Definition{}
	if intrinsiccatalog.Definitions()[0].ID() != health.ID() {
		t.Fatal("Definitions exposed catalog storage")
	}
	contract := health.ContractJSON()
	contract[0] = 'x'
	if bytes.Equal(contract, health.ContractJSON()) {
		t.Fatal("ContractJSON exposed catalog storage")
	}
}

func TestLookupUsesExactIntrinsicIdentity(t *testing.T) {
	t.Parallel()

	health := mustID(t, "kernel.health/v1")
	definition, ok := intrinsiccatalog.Lookup(health)
	if !ok || definition.ID() != health {
		t.Fatalf("Lookup(health) = %#v, %t", definition, ok)
	}
	for _, value := range []string{"kernel.health/v2", "example.health/v1"} {
		id := mustID(t, value)
		if definition, ok := intrinsiccatalog.Lookup(id); ok || definition.ID().String() != "" {
			t.Fatalf("Lookup(%s) = %#v, %t", id, definition, ok)
		}
	}
	if definition, ok := intrinsiccatalog.Lookup(capabilityid.Identifier{}); ok || definition.ID().String() != "" {
		t.Fatalf("Lookup(zero) = %#v, %t", definition, ok)
	}
}

func definitionIDs(definitions []intrinsiccatalog.Definition) []string {
	ids := make([]string, len(definitions))
	for index, definition := range definitions {
		ids[index] = definition.ID().String()
	}
	return ids
}

func mustID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}
