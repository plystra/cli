package javascriptgen_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/sdkmodel"
)

func TestInferPackageNameFromGoModuleIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		modulePath string
		want       string
	}{
		{modulePath: "github.com/acme/my-app", want: "@acme/my-app-sdk"},
		{modulePath: "my-app", want: "my-app-sdk"},
		{modulePath: "example.com/my-app", want: "my-app-sdk"},
		{modulePath: "example.com/acme/platform/my-app/v2", want: "@acme/platform-my-app-sdk"},
		{modulePath: "gopkg.in/yaml.v3", want: "yaml-sdk"},
	}
	model, err := sdkmodel.BuildCanonical(nil)
	if err != nil {
		t.Fatalf("BuildCanonical: %v", err)
	}
	for _, test := range tests {
		test := test
		t.Run(test.modulePath, func(t *testing.T) {
			t.Parallel()
			got, err := javascriptgen.InferPackageName(test.modulePath)
			if err != nil || got != test.want {
				t.Fatalf("InferPackageName(%q) = %q, %v; want %q", test.modulePath, got, err, test.want)
			}
			if _, err := javascriptgen.Render(javascriptOptions(t, got, nil, nil), model); err != nil {
				t.Fatalf("inferred package %q is not renderable: %v", got, err)
			}
		})
	}
}

func TestInferPackageNameRejectsInvalidAndOversizedModuleIdentity(t *testing.T) {
	t.Parallel()

	for _, modulePath := range []string{
		"",
		"not a module",
		"example.com",
		"github.com/Acme/My-App",
		"example.com/acme/package~tools",
		"example.com/acme/",
		"example.com/" + strings.Repeat("a", 101) + "/application",
		"example.com/acme/" + strings.Repeat("a", 97),
	} {
		modulePath := modulePath
		t.Run(modulePath, func(t *testing.T) {
			t.Parallel()
			first, firstErr := javascriptgen.InferPackageName(modulePath)
			second, secondErr := javascriptgen.InferPackageName(modulePath)
			if first != "" || second != "" || !errors.Is(firstErr, javascriptgen.ErrPackageIdentity) || !errors.Is(secondErr, javascriptgen.ErrPackageIdentity) || firstErr.Error() != secondErr.Error() {
				t.Fatalf("InferPackageName(%q) = %q, %v then %q, %v", modulePath, first, firstErr, second, secondErr)
			}
		})
	}
}
