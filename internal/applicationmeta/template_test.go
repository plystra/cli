package applicationmeta_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestTemplateMetadataHasExactRootSource(t *testing.T) {
	input := []byte("# root\n\ntemplate: example.com/base/v2\ninterfaces: {require: [local.health/v1]}\n")
	for _, parse := range []func(string, []byte) (applicationmeta.Manifest, error){
		applicationmeta.ParseSource, applicationmeta.ParseTemplateSource, applicationmeta.ParseRootMetadataSource,
	} {
		root, err := parse("plystra.yaml", input)
		if err != nil {
			t.Fatal(err)
		}
		root, err = applicationmeta.WithProjectModule(root, "my-app")
		if err != nil {
			t.Fatal(err)
		}
		source := root.TemplateSource()
		if root.Template() != "example.com/base/v2" || source.ModulePath() != "my-app" || source.Path() != "plystra.yaml" || source.Line() != 3 || source.Column() != 1 {
			t.Fatalf("template identity/source = %s / %#v", root.Template(), source)
		}
	}
	empty, err := applicationmeta.WithProjectModule(composeManifest(t, "{}"), "my-app")
	if err != nil || empty.TemplateSource() != (applicationmeta.ConfigurationDeclarationSource{}) {
		t.Fatal("absent relationship has a source")
	}
}

func TestTemplateMetadataRejectsInvalidRelationshipsAtTheirSpan(t *testing.T) {
	for _, value := range []string{"null", "true", "[]", "{}", "' base '", "base/local", "./base", "/base", "C:\\base", "base@latest", "example.com/base@v1.0.0", "https://example.com/base", "example.com/base/v1", "!!null PRIVATE_VALUE"} {
		for _, parse := range []func(string, []byte) (applicationmeta.Manifest, error){applicationmeta.ParseSource, applicationmeta.ParseRootMetadataSource, applicationmeta.ParseTemplateSource} {
			_, err := parse("plystra.yaml", []byte("# root\ntemplate: "+value+"\n"))
			var metadata *applicationmeta.TemplateMetadataError
			if !errors.As(err, &metadata) || !errors.Is(err, applicationmeta.ErrInvalidManifest) || metadata.Source().Path() != "plystra.yaml" || metadata.Source().Line() != 2 || metadata.Source().Column() != 1 || strings.Contains(err.Error(), "PRIVATE_VALUE") {
				t.Fatalf("relationship %q: %v", value, err)
			}
		}
	}
	_, err := applicationmeta.Parse([]byte("template: example.com/first\ntemplate: example.com/second\n"))
	var duplicate *applicationmeta.TemplateMetadataError
	if !errors.As(err, &duplicate) || duplicate.Source().Line() != 2 {
		t.Fatalf("duplicate source: %v", err)
	}
	for _, source := range []string{"deploy/customer.yaml", "plystra.production.yaml", "./plystra.yaml"} {
		_, err := applicationmeta.ParseSource(source, []byte("template: example.com/base\n"))
		var metadata *applicationmeta.TemplateMetadataError
		if !errors.As(err, &metadata) || metadata.Source().Path() != source {
			t.Fatalf("non-root accepted: %v", err)
		}
	}
	for _, source := range []string{"plystra.production.yaml", "plystra.yaml"} {
		if _, err := applicationmeta.ParseOverlaySource(source, []byte("template: example.com/base\n")); err == nil {
			t.Fatal("overlay declared root metadata")
		}
	}
}

func TestReplacementRootMetadataExcludesApplicationValues(t *testing.T) {
	input := []byte("template: example.com/base\ninterfaces: {use: false}\nconfig: {private: [invalid, schema]}\nhttp: {address: false}\nresources: {private: value}\ndata: {private: value}\n")
	root, err := applicationmeta.ParseRootMetadataSource("plystra.yaml", input)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := applicationmeta.ParseSource("deploy/selected.yaml", []byte("interfaces: {require: [selected.health/v1]}"))
	if err != nil {
		t.Fatal(err)
	}
	selected = applicationmeta.WithRootMetadata(selected, root)
	composition, err := applicationmeta.Compose(nil, selected, composeSchemaLookup(nil))
	if err != nil || composition.Manifest().Template() != root.Template() || composition.Manifest().TemplateSource() != root.TemplateSource() || len(composition.Manifest().Configurations()) != 0 || len(composition.Manifest().ImplementationChoices()) != 0 {
		t.Fatalf("replacement composition: %v", err)
	}
	if _, present := composition.Manifest().HTTPAddress(); present {
		t.Fatal("excluded root process value survived")
	}
	for _, bad := range []string{"config: {private: !!int PRIVATE_VALUE}", "config: {a: 1, a: 2}", "config: &a {private: value}", "composition: {}", "unknown: true", "template: example.com/base\n---\n{}"} {
		_, err := applicationmeta.ParseRootMetadataSource("plystra.yaml", []byte(bad))
		if !errors.Is(err, applicationmeta.ErrInvalidManifest) || strings.Contains(err.Error(), "PRIVATE_VALUE") {
			t.Fatalf("excluded invalid syntax: %v", err)
		}
	}
}

func TestPrivateTemplateIncludesReusableValuesButNoProcessInputs(t *testing.T) {
	input := []byte("# do not deploy this comment\ntemplate: example.com/oldest\nhttp:\n  address: {invalid: process-shape}\n  cors: {allowed_origins: [https://app.example]}\n  expose: {local.health/v1: {transport: connect}}\ntimeouts: {startup: [invalid, process-shape]}\ninterfaces: {require: {remove: [old.health/v1]}, use: {local.health/v1: example.com/base/local.New}}\nconfig: {example.com/base/local.New: {password: {env: PRIVATE_TARGET}, field: {$remove: true}}}\n")
	before := append([]byte(nil), input...)
	manifest, err := applicationmeta.ParseTemplateSource("plystra.yaml", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := manifest.HTTPAddress(); exists || manifest.StartupTimeout() != applicationmeta.DefaultStartupTimeout {
		t.Fatal("inherited process settings")
	}
	if len(manifest.HTTPExposures()) != 1 || len(manifest.Configurations()) != 1 {
		t.Fatal("lost reusable values")
	}
	data, err := applicationmeta.PrivateTemplateYAML(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"template:", "address:", "timeouts:", "invalid", "do not deploy"} {
		if bytes.Contains(data, []byte(absent)) {
			t.Fatalf("private baseline contains excluded %s", absent)
		}
	}
	for _, present := range []string{"cors:", "expose:", "interfaces:", "PRIVATE_TARGET", "$remove: true"} {
		if !bytes.Contains(data, []byte(present)) {
			t.Fatalf("private baseline omitted %s", present)
		}
	}
	repeated, err := applicationmeta.PrivateTemplateYAML(data)
	if err != nil || !bytes.Equal(repeated, data) || !bytes.Equal(input, before) {
		t.Fatal("normalization is not immutable and idempotent")
	}
}

func TestUnsupportedBaselineAndRemovedCompositionFailExplicitly(t *testing.T) {
	for _, field := range []string{"resources", "data"} {
		for _, parse := range []func(string, []byte) (applicationmeta.Manifest, error){applicationmeta.ParseSource, applicationmeta.ParseTemplateSource, applicationmeta.ParseOverlaySource} {
			_, err := parse("plystra.yaml", []byte(field+": {}"))
			if err == nil || !strings.Contains(err.Error(), field+" configuration is not supported") {
				t.Fatalf("unsupported baseline was dropped: %v", err)
			}
		}
		if _, err := applicationmeta.PrivateTemplateYAML([]byte(field + ": {}")); err == nil {
			t.Fatal("unsupported baseline silently normalized")
		}
	}
	for _, text := range []string{"composition: {}", "composition: {exports: {defaults: {}}}", "composition: {adopt: []}"} {
		for _, parse := range []func(string, []byte) (applicationmeta.Manifest, error){applicationmeta.ParseSource, applicationmeta.ParseTemplateSource, applicationmeta.ParseRootMetadataSource, applicationmeta.ParseOverlaySource} {
			if _, err := parse("plystra.yaml", []byte(text)); err == nil {
				t.Fatal("obsolete composition accepted")
			}
		}
	}
}

func TestSetTemplatePreservesAuthoredDeltaAndBounds(t *testing.T) {
	input := []byte("# retained\ninterfaces: {require: [local.health/v1]}\nconfig: {example.com/local/service.New: {password: PRIVATE_VALUE}}\n")
	result, err := applicationmeta.SetTemplate(input, "example.com/base")
	if err != nil {
		t.Fatal(err)
	}
	manifest := composeManifest(t, string(result))
	if manifest.Template() != "example.com/base" || !reflect.DeepEqual(interfaceRequirementIDs(manifest.InterfaceRequirements()), []string{"local.health/v1"}) || !bytes.Contains(result, []byte("# retained")) || !bytes.Contains(result, []byte("PRIVATE_VALUE")) {
		t.Fatal("relationship write changed authored delta")
	}
	same, err := applicationmeta.SetTemplate(result, "example.com/base")
	if err != nil || !bytes.Equal(same, result) {
		t.Fatal("relationship write is not idempotent")
	}
	replaced, err := applicationmeta.SetTemplate(result, "example.com/next")
	if err != nil || bytes.Count(replaced, []byte("template:")) != 1 || composeManifest(t, string(replaced)).Template() != "example.com/next" {
		t.Fatal("relationship replacement duplicated root metadata")
	}
	for _, module := range []string{"", "short-project/local", "short-project@latest", "example.com/base@latest", strings.Repeat("a", 4097)} {
		if data, err := applicationmeta.SetTemplate(input, module); err == nil || data != nil {
			t.Fatal("invalid relationship produced output")
		}
	}
	if _, err := applicationmeta.SetTemplate(bytes.Repeat([]byte(" "), applicationmeta.MaximumSize+1), "example.com/base"); err == nil {
		t.Fatal("oversized input accepted")
	}
}
