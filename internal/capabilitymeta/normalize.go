package capabilitymeta

import kernelmanifest "github.com/plystra/kernel/plugin/manifest"

// NormalizeSchema returns the authoritative deterministic exact contract
// projection. Kernel owns the public Capability parser and normalized model;
// CLI consumes that model instead of maintaining a second schema language.
func NormalizeSchema(data []byte) ([]byte, error) {
	canonical, _, err := NormalizeSchemaAndManifest(data)
	return canonical, err
}

// NormalizeSchemaAndManifest parses one contract exactly once and returns both
// Kernel's canonical exact-contract bytes and the immutable CLI planning view.
// Callers that need both must use this boundary instead of reparsing the
// canonical bytes.
func NormalizeSchemaAndManifest(data []byte) ([]byte, Manifest, error) {
	declaration, err := kernelmanifest.ParseCapability(data)
	if err != nil {
		return nil, Manifest{}, invalid("%v", err)
	}
	canonical, err := declaration.CanonicalSchemaJSON()
	if err != nil {
		return nil, Manifest{}, invalid("normalize canonical schema: %v", err)
	}
	manifest, err := manifestFromCapability(declaration)
	if err != nil {
		return nil, Manifest{}, err
	}
	return canonical, manifest, nil
}
