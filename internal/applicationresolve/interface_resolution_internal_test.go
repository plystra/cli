package applicationresolve

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationinput"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/interfaceresolution"
)

func TestValidateConstructorConfigurationOwnersReportsEveryContributingDocument(t *testing.T) {
	t.Parallel()

	const (
		currentModule = "example.com/application"
		constructor   = "example.com/implementation/smtp.New"
		privateTarget = "PRIVATE_DEPENDENCY_SECRET_TARGET"
	)
	manifest, err := applicationmeta.ParseSource("plystra.production.yaml", []byte("config: {"+constructor+": {password: {env: "+privateTarget+"}}}\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	manifest, err = applicationmeta.WithProjectModule(manifest, currentModule)
	if err != nil {
		t.Fatalf("WithProjectModule: %v", err)
	}
	path := `config["` + constructor + `"]`
	sourceContext := applicationinput.SourceContext{
		CurrentModulePath: currentModule,
		Dependencies: []applicationinput.DependencySource{
			{ModulePath: "example.com/alpha", Version: "v1.0.0"},
			{ModulePath: "example.com/zeta", Version: "v1.1.0"},
		},
		DependencyProvenance: []applicationinput.DependencyProvenance{{
			Path: path,
			Sources: []string{
				`example.com/alpha@v1.0.0/plystra.yaml config["example.com/implementation/smtp.New"]`,
				`example.com/zeta@v1.1.0/plystra.yaml config["example.com/implementation/smtp.New"]`,
			},
		}},
		CurrentProjectPaths: []string{path},
	}
	err = validateConstructorConfigurationOwners(manifest, interfaceresolution.Result{}, sourceContext)
	var unowned *UnownedConstructorConfigurationError
	if !errors.As(err, &unowned) || unowned == nil || !errors.Is(err, ErrUnownedConstructorConfiguration) || unowned.Constructor().String() != constructor || strings.Contains(err.Error(), privateTarget) {
		t.Fatalf("unowned dependency configuration error = %v", err)
	}
	wantSources := []applicationinput.ConfigurationSource{
		{Reference: `example.com/alpha@v1.0.0/plystra.yaml config["example.com/implementation/smtp.New"]`, ModulePath: "example.com/alpha", Path: "plystra.yaml", Line: 1, Column: 1},
		{Reference: `plystra.production.yaml config["example.com/implementation/smtp.New"]`, ModulePath: currentModule, Path: "plystra.production.yaml", Line: 1, Column: 1},
		{Reference: `example.com/zeta@v1.1.0/plystra.yaml config["example.com/implementation/smtp.New"]`, ModulePath: "example.com/zeta", Path: "plystra.yaml", Line: 1, Column: 1},
	}
	sources := unowned.Sources()
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("unowned dependency configuration sources = %#v, want %#v", sources, wantSources)
	}
	sources[0].Path = "changed"
	if !reflect.DeepEqual(unowned.Sources(), wantSources) {
		t.Fatal("UnownedConstructorConfigurationError.Sources exposed mutable storage")
	}
}
