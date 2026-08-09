package applicationgen

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/apidocgen"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/interfacecompatibility"
	"github.com/plystra/cli/internal/interfaceprovenance"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufwiremap"
)

const (
	interfaceProxyGenerator           = "plystra.interface-proxy/v1"
	implementationAdapterGenerator    = "plystra.implementation-adapter/v1"
	implementationAssemblyGenerator   = "plystra.implementation-assembly/v1"
	interfaceCompatibilityGenerator   = "plystra.interface-compatibility/v1"
	configurationGenerator            = "plystra.configuration/v1"
	dependencyClientGenerator         = "plystra.dependency-client/v1"
	applicationManifestGenerator      = "plystra.application-manifest/v1"
	kernelCompatibilityGenerator      = "plystra.kernel-assembly-compatibility/v1"
	providerAssemblyGenerator         = "plystra.provider-assembly/v1"
	invocationAssemblyGenerator       = "plystra.invocation-assembly/v1"
	runtimeBootstrapGenerator         = "plystra.runtime-bootstrap/v1"
	applicationEntrypointGenerator    = "plystra.application-entrypoint/v1"
	contractGenerator                 = "plystra.contract/v1"
	providerInterfaceGenerator        = "plystra.provider-interface/v1"
	invocationContextGenerator        = "plystra.invocation-context/v1"
	clientGenerator                   = "plystra.client/v1"
	invocationGenerator               = "plystra.invocation/v1"
	httpAdapterGenerator              = "plystra.http-adapter/v1"
	protobufDescriptorGenerator       = protobufdescriptor.ProjectionSchema
	protobufWireMapGenerator          = protobufwiremap.ProjectionSchema
	connectGenerator                  = "plystra.connect-generator/v1"
	javaScriptGenerator               = javascriptgen.GeneratorVersion
	documentationGenerator            = apidocgen.GeneratorVersion
	applicationModelInputPrefix       = "application-model:"
	dependencyCompositionInputPrefix  = "dependency-composition:"
	interfaceProvenanceInputPrefix    = "interface-provenance:"
	transportToolchainInputPrefix     = "transport-toolchain:"
	configurationSelectionInputPrefix = "configuration-selection:"
	interfaceInputPrefix              = "interface:"
	interfaceContractInputPrefix      = "interface-contract:"
	constructorInputPrefix            = "constructor:"
	compatibilityInputPrefix          = "compatibility:"
	protobufWireMapInputPrefix        = "protobuf-wire-map:"
)

type artifactIdentity struct {
	generator string
	kind      generatedfiles.ArtifactKind
}

type artifactEvidence struct {
	inputs  []string
	sources []string
}

type artifactEvidenceIndex struct {
	base   artifactEvidence
	all    artifactEvidence
	byPath map[string]artifactEvidence
}

func newArtifactEvidenceIndex(provenance ManifestProvenance) (artifactEvidenceIndex, error) {
	if err := validateManifestProvenance(provenance); err != nil {
		return artifactEvidenceIndex{}, err
	}
	base := artifactEvidence{
		inputs: []string{
			applicationModelInputPrefix + provenance.ApplicationModelDigest(),
			dependencyCompositionInputPrefix + provenance.DependencyBaseline().Digest(),
			interfaceProvenanceInputPrefix + provenance.InterfaceProvenance().Digest(),
			transportToolchainInputPrefix + provenance.TransportToolchain().Digest(),
			configurationSelectionInputPrefix + provenance.Mode() + ":" + provenance.SelectedPath() + ":" + provenance.SelectedDigest(),
		},
		sources: []string{provenance.SelectedPath()},
	}
	if provenance.Mode() == ConfigurationModeEnvironment {
		base.sources = append(base.sources, provenance.RootPath())
	}
	for _, record := range provenance.DependencyBaseline().Records() {
		base.sources = append(base.sources, record.Sources...)
	}
	base = canonicalArtifactEvidence(base)
	index := artifactEvidenceIndex{base: base, all: base, byPath: make(map[string]artifactEvidence)}

	interfaces := make(map[string]interfaceprovenance.Interface)
	for _, current := range provenance.InterfaceProvenance().Interfaces() {
		interfaces[current.ID()] = current
		index.all = mergeArtifactEvidence(index.all, interfaceArtifactEvidence(current))
	}
	constructors := make(map[string]interfaceprovenance.Constructor)
	for _, constructor := range provenance.InterfaceProvenance().Constructors() {
		constructors[constructor.Symbol()] = constructor
		index.all = mergeArtifactEvidence(index.all, constructorArtifactEvidence(constructor))
	}
	for _, binding := range provenance.InterfaceProvenance().Bindings() {
		current, exists := interfaces[binding.InterfaceID()]
		if !exists {
			return artifactEvidenceIndex{}, fmt.Errorf("binding %s has no Interface provenance", binding.InterfaceID())
		}
		evidence := mergeArtifactEvidence(base, interfaceArtifactEvidence(current), bindingArtifactEvidence(binding))
		if constructor, exists := constructors[binding.Selection().Constructor()]; exists {
			evidence = mergeArtifactEvidence(evidence, constructorArtifactEvidence(constructor))
		}
		index.addMappings(binding.Mappings(), evidence)
	}
	for _, intrinsic := range provenance.InterfaceProvenance().Intrinsics() {
		evidence := mergeArtifactEvidence(base, interfaceArtifactEvidence(intrinsic.Interface()), artifactEvidence{
			inputs: []string{interfaceInputPrefix + intrinsic.Interface().ID()},
			sources: append(
				append(append([]string(nil), intrinsic.RequirementSources()...), intrinsic.ExposureSources()...),
				intrinsic.Policy().Sources()...,
			),
		})
		index.all = mergeArtifactEvidence(index.all, evidence)
		index.addMappings(intrinsic.Mappings(), evidence)
	}
	index.all = canonicalArtifactEvidence(index.all)
	return index, nil
}

func (i *artifactEvidenceIndex) addMappings(mapping interfaceprovenance.Mapping, evidence artifactEvidence) {
	for _, filePath := range []string{
		mapping.ProxyPath(),
		mapping.AdapterPath(),
		mapping.AssemblyPath(),
		mapping.ProtobufSchemaPath(),
		mapping.ProtobufDescriptorSetPath(),
		mapping.WireMapPath(),
		mapping.ConnectHandlerPath(),
		mapping.JavaScriptModulePath(),
	} {
		if filePath == "" {
			continue
		}
		i.byPath[filePath] = mergeArtifactEvidence(i.byPath[filePath], evidence)
	}
}

func (i artifactEvidenceIndex) input(filePath string) (generatedfiles.ArtifactInput, error) {
	identity, err := classifyArtifact(filePath)
	if err != nil {
		return generatedfiles.ArtifactInput{}, err
	}
	evidence, specific := i.byPath[filePath]
	if !specific {
		evidence = i.base
		if artifactUsesGlobalEvidence(filePath) {
			evidence = i.all
		}
	}
	evidence = mergeArtifactEvidence(i.base, evidence)
	return generatedfiles.ArtifactInput{
		Generator:      identity.generator,
		Kind:           identity.kind,
		InputRecordIDs: evidence.inputs,
		Sources:        evidence.sources,
	}, nil
}

func artifactUsesGlobalEvidence(filePath string) bool {
	if strings.HasPrefix(filePath, "generated/compatibility/") ||
		strings.HasPrefix(filePath, "generated/docs/") ||
		strings.HasPrefix(filePath, "generated/go/assembly/") ||
		filePath == aliasManifestPath ||
		filePath == "generated/go/application/main_gen.go" ||
		filePath == "generated/go/bootstrap/bootstrap_gen.go" ||
		filePath == "generated/go/internal/connectschema/schema_gen.go" ||
		filePath == protobufdescriptor.DescriptorSetPath ||
		filePath == protobufwiremap.Path {
		return true
	}
	return strings.HasPrefix(filePath, "generated/sdk/javascript/") &&
		!strings.HasPrefix(filePath, "generated/sdk/javascript/src/interfaces/")
}

func interfaceArtifactEvidence(current interfaceprovenance.Interface) artifactEvidence {
	return artifactEvidence{
		inputs: []string{
			interfaceInputPrefix + current.ID(),
			interfaceContractInputPrefix + current.ID() + ":" + current.ContractDigest(),
		},
		sources: nonemptyStrings(current.DirectiveSource(), current.MetadataSource()),
	}
}

func bindingArtifactEvidence(binding interfaceprovenance.Binding) artifactEvidence {
	selection := binding.Selection()
	sources := append([]string(nil), binding.RootSources()...)
	sources = append(sources, binding.ExposureSources()...)
	sources = append(sources, selection.Source())
	sources = append(sources, selection.Sources()...)
	sources = append(sources, binding.Policy().Sources()...)
	return artifactEvidence{
		inputs:  []string{interfaceInputPrefix + binding.InterfaceID(), constructorInputPrefix + selection.Constructor()},
		sources: sources,
	}
}

func constructorArtifactEvidence(constructor interfaceprovenance.Constructor) artifactEvidence {
	return artifactEvidence{
		inputs:  []string{constructorInputPrefix + constructor.Symbol()},
		sources: append(nonemptyStrings(constructor.Source()), constructor.ConfigurationSources()...),
	}
}

func canonicalArtifactEvidence(value artifactEvidence) artifactEvidence {
	value.inputs = sortedUniqueNonempty(value.inputs)
	value.sources = sortedUniqueNonempty(value.sources)
	return value
}

func mergeArtifactEvidence(values ...artifactEvidence) artifactEvidence {
	var result artifactEvidence
	for _, value := range values {
		result.inputs = append(result.inputs, value.inputs...)
		result.sources = append(result.sources, value.sources...)
	}
	return canonicalArtifactEvidence(result)
}

func nonemptyStrings(values ...string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func sortedUniqueNonempty(values []string) []string {
	result := nonemptyStrings(values...)
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if write != 0 && result[write-1] == value {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func classifyArtifact(filePath string) (artifactIdentity, error) {
	switch filePath {
	case interfacecompatibility.Path,
		interfacecompatibility.MetadataPath,
		interfacecompatibility.TransportPath,
		interfacecompatibility.JavaScriptPath,
		interfacecompatibility.DocumentationPath:
		return artifactIdentity{generator: interfaceCompatibilityGenerator, kind: generatedfiles.ArtifactKindCompatibilityBaseline}, nil
	case protobufwiremap.Path:
		return artifactIdentity{generator: protobufWireMapGenerator, kind: generatedfiles.ArtifactKindWireMap}, nil
	case protobufdescriptor.DescriptorSetPath:
		return artifactIdentity{generator: protobufDescriptorGenerator, kind: generatedfiles.ArtifactKindProtobufDescriptor}, nil
	case aliasManifestPath:
		return artifactIdentity{generator: applicationManifestGenerator, kind: generatedfiles.ArtifactKindApplicationManifest}, nil
	case assemblyCompatibilityPath:
		return artifactIdentity{generator: kernelCompatibilityGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case "generated/go/assembly/providers_gen.go":
		return artifactIdentity{generator: providerAssemblyGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case "generated/go/assembly/invocations_gen.go":
		return artifactIdentity{generator: invocationAssemblyGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case "generated/go/assembly/interfaces_gen.go":
		return artifactIdentity{generator: implementationAssemblyGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case "generated/go/bootstrap/bootstrap_gen.go":
		return artifactIdentity{generator: runtimeBootstrapGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case "generated/go/application/main_gen.go":
		return artifactIdentity{generator: applicationEntrypointGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case "generated/go/internal/invocationcontext/context_gen.go":
		return artifactIdentity{generator: invocationContextGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case "generated/go/internal/connectschema/schema_gen.go":
		return artifactIdentity{generator: protobufDescriptorGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	}

	switch {
	case strings.HasPrefix(filePath, "generated/go/proxies/"):
		return artifactIdentity{generator: interfaceProxyGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/go/adapters/implementations/"):
		return artifactIdentity{generator: implementationAdapterGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/go/adapters/connect/"):
		return artifactIdentity{generator: connectGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/go/adapters/http/"):
		return artifactIdentity{generator: httpAdapterGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/go/configuration/"):
		return artifactIdentity{generator: configurationGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasSuffix(filePath, "/dependencies_gen.go"):
		return artifactIdentity{generator: dependencyClientGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/go/contracts/"):
		return artifactIdentity{generator: contractGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/go/providers/"):
		return artifactIdentity{generator: providerInterfaceGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/go/clients/"):
		return artifactIdentity{generator: clientGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/go/invocation/"):
		return artifactIdentity{generator: invocationGenerator, kind: generatedfiles.ArtifactKindGoSource}, nil
	case strings.HasPrefix(filePath, "generated/proto/") && path.Ext(filePath) == ".proto":
		return artifactIdentity{generator: protobufDescriptorGenerator, kind: generatedfiles.ArtifactKindProtobufSource}, nil
	case strings.HasPrefix(filePath, "generated/sdk/javascript/src/") && path.Ext(filePath) == ".ts":
		return artifactIdentity{generator: javaScriptGenerator, kind: generatedfiles.ArtifactKindJavaScriptSource}, nil
	case strings.HasPrefix(filePath, "generated/sdk/javascript/"):
		return artifactIdentity{generator: javaScriptGenerator, kind: generatedfiles.ArtifactKindJavaScriptPackage}, nil
	case strings.HasPrefix(filePath, "generated/docs/"):
		return artifactIdentity{generator: documentationGenerator, kind: generatedfiles.ArtifactKindDocumentation}, nil
	default:
		return artifactIdentity{}, fmt.Errorf("generated artifact %s has no stable owning generator and output kind", filePath)
	}
}

func artifactCompatibilityEvidence(filePath string, options Options) artifactEvidence {
	switch filePath {
	case interfacecompatibility.Path:
		return artifactEvidence{inputs: []string{compatibilityInputPrefix + "interface-shape:" + options.InterfaceCompatibility.Digest()}}
	case interfacecompatibility.MetadataPath:
		return artifactEvidence{inputs: []string{compatibilityInputPrefix + "interface-metadata:" + options.InterfaceMetadata.Digest()}}
	case interfacecompatibility.TransportPath:
		return artifactEvidence{inputs: []string{compatibilityInputPrefix + "interface-transport:" + options.InterfaceTransport.Digest()}}
	case interfacecompatibility.JavaScriptPath:
		return artifactEvidence{inputs: []string{compatibilityInputPrefix + "interface-javascript:" + options.InterfaceJavaScript.Digest()}}
	case interfacecompatibility.DocumentationPath:
		return artifactEvidence{inputs: []string{compatibilityInputPrefix + "interface-documentation"}}
	case protobufwiremap.Path:
		return artifactEvidence{inputs: []string{protobufWireMapInputPrefix + options.ProtobufWireMap.Digest()}}
	default:
		return artifactEvidence{}
	}
}
