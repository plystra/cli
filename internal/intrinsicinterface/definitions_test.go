package intrinsicinterface_test

import (
	"slices"
	"testing"

	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/intrinsicinterface"
)

func TestDefinitionsAdaptKernelOwnedInterfaceInventory(t *testing.T) {
	t.Parallel()

	definitions := intrinsicinterface.Definitions()
	if got := definitionIDs(definitions); !slices.Equal(got, []string{"kernel.health/v1", "kernel.info/v1"}) {
		t.Fatalf("Definitions = %v", got)
	}
	wantPackages := []string{
		"github.com/plystra/kernel/interfaces/kernel/health/v1",
		"github.com/plystra/kernel/interfaces/kernel/info/v1",
	}
	for index, definition := range definitions {
		if definition.PackagePath() != wantPackages[index] || definition.Source() != wantPackages[index]+" //plystra:interface "+definition.ID().String() {
			t.Fatalf("Definitions[%d] = ID %s package %q source %q", index, definition.ID(), definition.PackagePath(), definition.Source())
		}
	}

	definitions[0] = intrinsicinterface.Definition{}
	if intrinsicinterface.Definitions()[0].ID().String() != "kernel.health/v1" {
		t.Fatal("Definitions exposed adapter storage")
	}
}

func TestLookupUsesExactIntrinsicInterfaceIdentity(t *testing.T) {
	t.Parallel()

	health := mustID(t, "kernel.health/v1")
	definition, ok := intrinsicinterface.Lookup(health)
	if !ok || definition.ID() != health {
		t.Fatalf("Lookup(health) = %#v, %t", definition, ok)
	}
	for _, value := range []string{"kernel.health/v2", "example.health/v1"} {
		id := mustID(t, value)
		if definition, ok := intrinsicinterface.Lookup(id); ok || definition.ID().String() != "" {
			t.Fatalf("Lookup(%s) = %#v, %t", id, definition, ok)
		}
	}
}

func definitionIDs(definitions []intrinsicinterface.Definition) []string {
	result := make([]string, len(definitions))
	for index, definition := range definitions {
		result[index] = definition.ID().String()
	}
	return result
}

func mustID(t testing.TB, value string) interfaceid.Identifier {
	t.Helper()
	id, err := interfaceid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}
