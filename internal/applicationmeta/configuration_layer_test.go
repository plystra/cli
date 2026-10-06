package applicationmeta_test

import (
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestCompleteConfigurationRejectsTemplateSparseRequirementsAndRemovals(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		data string
		want string
	}{
		{name: "template field", data: "template: example.com/base\n", want: "unknown root field"},
		{name: "sparse requirements", data: "interfaces: {require: {add: [email.send/v1]}}\n", want: "complete sequence"},
		{name: "exposure removal", data: "http: {expose: {email.send/v1: {$remove: true}}}\n", want: "removal markers"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, source := range []string{"plystra.yaml", "deploy/replacement.yaml"} {
				if _, err := applicationmeta.ParseCompleteSource(source, []byte(test.data)); err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("ParseCompleteSource(%q) = %v, want %q", source, err, test.want)
				}
			}
		})
	}
}

func TestEnvironmentOverlayAcceptsSparseRequirementsAndRemovals(t *testing.T) {
	t.Parallel()
	_, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", []byte(`
interfaces:
  require:
    add: [email.send/v1]
    remove: [audit.write/v1]
  use:
    email.send/v1: {$remove: true}
http:
  expose:
    email.send/v1: {$remove: true}
`))
	if err != nil {
		t.Fatalf("ParseOverlaySource rejected valid sparse overlay: %v", err)
	}
}

func TestConfigurationFieldTemplateIsUnknownInEveryDocumentMode(t *testing.T) {
	t.Parallel()
	for _, parse := range []struct {
		name  string
		parse func(string, []byte) (applicationmeta.Manifest, error)
	}{
		{name: "root layer", parse: applicationmeta.ParseSource},
		{name: "complete root", parse: applicationmeta.ParseCompleteSource},
		{name: "overlay", parse: applicationmeta.ParseOverlaySource},
	} {
		t.Run(parse.name, func(t *testing.T) {
			if _, err := parse.parse("plystra.yaml", []byte("template: example.com/base\n")); err == nil || !strings.Contains(err.Error(), "unknown root field") {
				t.Fatalf("template field error = %v", err)
			}
		})
	}
}
