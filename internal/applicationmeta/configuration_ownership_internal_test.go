package applicationmeta

import "testing"

func TestDependencyConfigurationDeclarationSourceFromReferenceSupportsSetSourceForms(t *testing.T) {
	t.Parallel()

	field := `interfaces.require["audit.write/v1"]`
	for _, reference := range []string{
		`example.com/platform@v1.2.3/plystra.yaml interfaces.require["audit.write/v1"]`,
		`example.com/platform@v1.2.3/plystra.yaml interfaces.require.add["audit.write/v1"]`,
		`example.com/platform@v1.2.3/plystra.yaml interfaces.require.remove["audit.write/v1"]`,
	} {
		source, ok := dependencyConfigurationDeclarationSourceFromReference(reference, field)
		if !ok || source.modulePath != "example.com/platform" || source.path != "plystra.yaml" || source.line != 1 || source.column != 1 {
			t.Fatalf("source from %q = %#v, %t", reference, source, ok)
		}
	}
	for _, reference := range []string{
		`not a dependency source`,
		`example.com/platform@v1.2.3/C:/private/plystra.yaml interfaces.require["audit.write/v1"]`,
		`example.com/platform@v1.2.3/../plystra.yaml interfaces.require["audit.write/v1"]`,
	} {
		if source, ok := dependencyConfigurationDeclarationSourceFromReference(reference, field); ok {
			t.Fatalf("unsafe source from %q = %#v", reference, source)
		}
	}
}
