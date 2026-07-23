package implementationinventory_test

import (
	"bytes"
	"errors"
	"fmt"
	"go/token"
	"go/types"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/implementationdecl"
	"github.com/plystra/cli/internal/implementationinventory"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestBuildOrdersAndProtectsDiscoveredImplementations(t *testing.T) {
	t.Parallel()

	alpha := declaration(t, "alpha/new.go", "alpha", "Build", "alpha.service.run/v1")
	zeta := declaration(t, "zeta/new.go", "zeta", "New", "zeta.service.run/v1")
	alphaInterface := canonicalInterface(t, "alpha.service.run/v1", "example.com/interfaces/alpha", "Run")
	zetaInterface := canonicalInterface(t, "zeta.service.run/v1", "example.com/interfaces/zeta", "Run")
	index, err := implementationinventory.Build([]implementationinventory.Input{
		{
			ModulePath:    "example.com/dependency",
			ModuleVersion: "v1.2.3",
			PackagePath:   "example.com/dependency/zeta",
			Declaration:   zeta,
			Types:         compiledPackage("example.com/dependency/zeta", "zeta", "New", "Run"),
		},
		{
			ModulePath:  "example.com/app",
			PackagePath: "example.com/app/alpha",
			Local:       true,
			Declaration: alpha,
			Types:       compiledPackage("example.com/app/alpha", "alpha", "Build", "Run"),
		},
	}, []implementationinventory.InterfaceInput{alphaInterface, zetaInterface})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	implementations := index.Implementations()
	got := []string{
		implementations[0].PackagePath() + "." + implementations[0].FunctionName(),
		implementations[1].PackagePath() + "." + implementations[1].FunctionName(),
	}
	if !slices.Equal(got, []string{"example.com/app/alpha.Build", "example.com/dependency/zeta.New"}) {
		t.Fatalf("Implementation order = %v", got)
	}
	if implementations[0].Symbol().String() != got[0] || implementations[0].Symbol().PackagePath() != implementations[0].PackagePath() || implementations[0].Symbol().FunctionName() != implementations[0].FunctionName() {
		t.Fatalf("constructor Symbol = %#v for %#v", implementations[0].Symbol(), implementations[0])
	}
	if configuration, configured := implementations[0].Configuration(); configured || configuration.String() != "" || configuration.PackagePath() != "" || configuration.TypeName() != "" {
		t.Fatalf("configuration-free Implementation = %#v, %t", configuration, configured)
	}
	if concrete := implementations[0].ConcreteType(); concrete.String() != "*example.com/app/alpha.Service" || concrete.PackagePath() != "example.com/app/alpha" || concrete.TypeName() != "Service" {
		t.Fatalf("concrete type = %#v (%s)", concrete, concrete.String())
	}
	if found, exists := index.BySymbol(implementations[1].Symbol()); !exists || found.Symbol() != implementations[1].Symbol() {
		t.Fatalf("BySymbol = %#v, %t", found, exists)
	}
	missing, err := constructorsymbol.Parse("example.com/missing.New")
	if err != nil {
		t.Fatalf("Parse missing symbol: %v", err)
	}
	if found, exists := index.BySymbol(missing); exists || found.Symbol().String() != "" {
		t.Fatalf("BySymbol(missing) = %#v, %t", found, exists)
	}
	if !implementations[0].Local() || implementations[0].ModuleVersion() != "" || implementations[0].Source() != "example.com/app@local/alpha/new.go:4:6" {
		t.Fatalf("local Implementation = %#v, source %q", implementations[0], implementations[0].Source())
	}
	if implementations[1].Local() || implementations[1].ModuleVersion() != "v1.2.3" || implementations[1].Source() != "example.com/dependency@v1.2.3/zeta/new.go:4:6" {
		t.Fatalf("dependency Implementation = %#v, source %q", implementations[1], implementations[1].Source())
	}
	implementations[0] = implementationinventory.Implementation{}
	if index.Implementations()[0].FunctionName() != "Build" {
		t.Fatal("Implementations exposed mutable inventory storage")
	}
}

func TestBuildClassifiesExactKernelSecretConfigurationField(t *testing.T) {
	t.Parallel()

	secretPackage := types.NewPackage("github.com/plystra/kernel/configuration", "configuration")
	secretName := types.NewTypeName(token.NoPos, secretPackage, "Secret", nil)
	secretType := types.NewNamed(secretName, types.NewStruct(nil, nil), nil)
	secretPackage.Scope().Insert(secretName)
	compiled := compiledConfiguredPackage("example.com/app/service", "service", secretType)
	parsed := declaration(t, "service/implementation.go", "service", "New", "service.operation.run/v1")
	index, err := implementationinventory.Build([]implementationinventory.Input{{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Local:       true,
		Declaration: parsed,
		Types:       compiled,
	}}, []implementationinventory.InterfaceInput{canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	implementations := index.Implementations()
	if len(implementations) != 1 {
		t.Fatalf("Implementations = %#v", implementations)
	}
	configuration, configured := implementations[0].Configuration()
	field, exists := configuration.Lookup("password")
	if !configured || !exists || field.Value().Kind() != implementationinventory.ConfigurationValueSecret || field.TypeIdentity() != "github.com/plystra/kernel/configuration.Secret" || !field.Value().Kind().Valid() {
		t.Fatalf("Secret configuration = %#v, field %#v, %t, %t", configuration, field, configured, exists)
	}
	if implementationinventory.ConfigurationValueKind("").Valid() || implementationinventory.ConfigurationValueKind("unknown").Valid() {
		t.Fatal("unknown configuration value kind reported valid")
	}
}

func TestBuildRejectsSecretConfigurationContainers(t *testing.T) {
	t.Parallel()

	secretPackage := types.NewPackage("github.com/plystra/kernel/configuration", "configuration")
	secretName := types.NewTypeName(token.NoPos, secretPackage, "Secret", nil)
	secretType := types.NewNamed(secretName, types.NewStruct(nil, nil), nil)
	secretPackage.Scope().Insert(secretName)
	for _, test := range []struct {
		name      string
		fieldType types.Type
	}{
		{name: "pointer", fieldType: types.NewPointer(secretType)},
		{name: "slice", fieldType: types.NewSlice(secretType)},
		{name: "array", fieldType: types.NewArray(secretType, 2)},
		{name: "map", fieldType: types.NewMap(types.Typ[types.String], secretType)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			compiled := compiledConfiguredPackage("example.com/app/service", "service", test.fieldType)
			parsed := declaration(t, "service/implementation.go", "service", "New", "service.operation.run/v1")
			_, err := implementationinventory.Build([]implementationinventory.Input{{
				ModulePath:  "example.com/app",
				PackagePath: "example.com/app/service",
				Local:       true,
				Declaration: parsed,
				Types:       compiled,
			}}, []implementationinventory.InterfaceInput{canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")})
			if !errors.Is(err, implementationinventory.ErrInvalidConfiguration) || !strings.Contains(err.Error(), "Secret configuration must be a direct named field") {
				t.Fatalf("Build error = %v", err)
			}
		})
	}
}

func TestBuildCompilesConfigurationFieldMetadata(t *testing.T) {
	t.Parallel()

	secretPackage := types.NewPackage("github.com/plystra/kernel/configuration", "configuration")
	secretName := types.NewTypeName(token.NoPos, secretPackage, "Secret", nil)
	secretType := types.NewNamed(secretName, types.NewStruct(nil, nil), nil)
	secretPackage.Scope().Insert(secretName)

	durationPackage := types.NewPackage("time", "time")
	durationName := types.NewTypeName(token.NoPos, durationPackage, "Duration", nil)
	durationType := types.NewNamed(durationName, types.Typ[types.Int64], nil)
	durationPackage.Scope().Insert(durationName)

	urlPackage := types.NewPackage("net/url", "url")
	urlName := types.NewTypeName(token.NoPos, urlPackage, "URL", nil)
	urlType := types.NewNamed(urlName, types.NewStruct(nil, nil), nil)
	urlPackage.Scope().Insert(urlName)

	compiled := compiledConfigurationPackage("example.com/app/service", "service", []configurationTestField{
		{name: "Host", fieldType: types.Typ[types.String], tag: `yaml:"host" plystra:"required,build-visible"`},
		{name: "Address", fieldType: types.Typ[types.String], tag: `yaml:"address" plystra:"build-visible,required"`},
		{name: "Mode", fieldType: types.Typ[types.String], tag: `yaml:"mode" plystra-default:"sensitive-default"`},
		{name: "Label", fieldType: types.Typ[types.String], tag: `yaml:"label" plystra-default:""`},
		{name: "Enabled", fieldType: types.Typ[types.Bool], tag: `yaml:"enabled" plystra:"build-visible" plystra-default:"true"`},
		{name: "Retries", fieldType: types.Typ[types.Int8], tag: `yaml:"retries" plystra-default:"+007"`},
		{name: "Limit", fieldType: types.Typ[types.Int], tag: `yaml:"limit" plystra-default:"2147483647"`},
		{name: "Ratio", fieldType: types.Typ[types.Float32], tag: `yaml:"ratio" plystra-default:"1.2500"`},
		{name: "Delay", fieldType: durationType, tag: `yaml:"delay" plystra-default:"5000ms"`},
		{name: "Endpoint", fieldType: urlType, tag: `yaml:"endpoint" plystra-default:"https://example.test/path"`},
		{name: "Password", fieldType: secretType, tag: `yaml:"password" plystra:"required"`},
	})
	parsed := declaration(t, "service/implementation.go", "service", "New", "service.operation.run/v1")
	index, err := implementationinventory.Build([]implementationinventory.Input{{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Local:       true,
		Declaration: parsed,
		Types:       compiled,
	}}, []implementationinventory.InterfaceInput{canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	implementation := index.Implementations()[0]
	configuration, configured := implementation.Configuration()
	if !configured {
		t.Fatal("Configuration was not compiled")
	}

	assertField := func(name string, required, buildVisible bool, defaultJSON string) implementationinventory.ConfigurationField {
		t.Helper()
		field, exists := configuration.Lookup(name)
		if !exists || field.Required() != required || field.BuildVisible() != buildVisible || field.HasDefault() != (defaultJSON != "") || string(field.DefaultJSON()) != defaultJSON {
			t.Fatalf("Configuration field %q = %#v, default %q", name, field, field.DefaultJSON())
		}
		return field
	}
	assertField("host", true, true, "")
	assertField("address", true, true, "")
	mode := assertField("mode", false, false, `"sensitive-default"`)
	assertField("label", false, false, `""`)
	assertField("enabled", false, true, "true")
	retries := assertField("retries", false, false, "7")
	limit := assertField("limit", false, false, "2147483647")
	ratio := assertField("ratio", false, false, "1.25")
	assertField("delay", false, false, `"5s"`)
	assertField("endpoint", false, false, `"https://example.test/path"`)
	assertField("password", true, false, "")

	if bits, numeric := retries.Value().NumericBits(); !numeric || bits != 8 || retries.Value().PlatformSized() {
		t.Fatalf("Retries numeric metadata = %d, %t, platform %t", bits, numeric, retries.Value().PlatformSized())
	}
	if bits, numeric := limit.Value().NumericBits(); !numeric || bits != 0 || !limit.Value().PlatformSized() {
		t.Fatalf("Limit numeric metadata = %d, %t, platform %t", bits, numeric, limit.Value().PlatformSized())
	}
	if bits, numeric := ratio.Value().NumericBits(); !numeric || bits != 32 || ratio.Value().PlatformSized() {
		t.Fatalf("Ratio numeric metadata = %d, %t, platform %t", bits, numeric, ratio.Value().PlatformSized())
	}
	if bits, numeric := mode.Value().NumericBits(); numeric || bits != 0 || mode.Value().PlatformSized() {
		t.Fatalf("Mode numeric metadata = %d, %t, platform %t", bits, numeric, mode.Value().PlatformSized())
	}

	defaultCopy := mode.DefaultJSON()
	defaultCopy[1] = 'X'
	if string(mode.DefaultJSON()) != `"sensitive-default"` {
		t.Fatal("DefaultJSON exposed mutable storage")
	}
	for _, formatted := range []string{
		fmt.Sprintf("%v", mode),
		fmt.Sprintf("%#v", mode),
		fmt.Sprintf("%#v", mode.Value()),
		fmt.Sprintf("%#v", configuration),
		fmt.Sprintf("%#v", configuration.Fields()),
		fmt.Sprintf("%+v", implementation),
		fmt.Sprintf("%#v", implementation),
	} {
		if strings.Contains(formatted, "sensitive-default") {
			t.Fatalf("formatted configuration leaked default: %s", formatted)
		}
	}
	var logged bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logged, nil))
	logger.Info("configuration", "field", mode, "schema", configuration)
	if strings.Contains(logged.String(), "sensitive-default") || !strings.Contains(logged.String(), `"has_default":true`) {
		t.Fatalf("structured configuration log = %s", logged.String())
	}
}

func TestBuildRejectsInvalidConfigurationFieldMetadata(t *testing.T) {
	t.Parallel()

	secretPackage := types.NewPackage("github.com/plystra/kernel/configuration", "configuration")
	secretName := types.NewTypeName(token.NoPos, secretPackage, "Secret", nil)
	secretType := types.NewNamed(secretName, types.NewStruct(nil, nil), nil)
	secretPackage.Scope().Insert(secretName)
	secretObject := types.NewStruct([]*types.Var{types.NewVar(token.NoPos, nil, "Password", secretType)}, []string{`yaml:"password"`})
	durationPackage := types.NewPackage("time", "time")
	durationName := types.NewTypeName(token.NoPos, durationPackage, "Duration", nil)
	durationType := types.NewNamed(durationName, types.Typ[types.Int64], nil)
	durationPackage.Scope().Insert(durationName)
	urlPackage := types.NewPackage("net/url", "url")
	urlName := types.NewTypeName(token.NoPos, urlPackage, "URL", nil)
	urlType := types.NewNamed(urlName, types.NewStruct(nil, nil), nil)
	urlPackage.Scope().Insert(urlName)

	tests := []struct {
		name      string
		fieldType types.Type
		tag       string
		want      string
	}{
		{name: "empty policy", fieldType: types.Typ[types.String], tag: `plystra:""`, want: "must name at least one option"},
		{name: "unknown policy", fieldType: types.Typ[types.String], tag: `plystra:"public"`, want: `unknown plystra configuration option "public"`},
		{name: "duplicate policy", fieldType: types.Typ[types.String], tag: `plystra:"required,required"`, want: `duplicate plystra configuration option "required"`},
		{name: "empty option", fieldType: types.Typ[types.String], tag: `plystra:"required,"`, want: `unknown plystra configuration option ""`},
		{name: "duplicate tag key", fieldType: types.Typ[types.String], tag: `plystra:"required" plystra:"build-visible"`, want: `duplicate Go struct tag key "plystra"`},
		{name: "duplicate default key", fieldType: types.Typ[types.String], tag: `plystra-default:"first" plystra-default:"second"`, want: `duplicate Go struct tag key "plystra-default"`},
		{name: "duplicate YAML key", fieldType: types.Typ[types.String], tag: `yaml:"first" yaml:"second"`, want: `duplicate Go struct tag key "yaml"`},
		{name: "malformed tag", fieldType: types.Typ[types.String], tag: `plystra:"required"broken`, want: "entries must be separated by spaces"},
		{name: "required default", fieldType: types.Typ[types.String], tag: `plystra:"required" plystra-default:"value"`, want: "required fields must not declare a default"},
		{name: "Secret default", fieldType: secretType, tag: `plystra-default:"value"`, want: "Secret configuration must not declare a default"},
		{name: "Secret build visible", fieldType: secretType, tag: `plystra:"build-visible"`, want: "Secret-bearing configuration must remain runtime-only"},
		{name: "Secret object build visible", fieldType: secretObject, tag: `plystra:"build-visible"`, want: "Secret-bearing configuration must remain runtime-only"},
		{name: "object default", fieldType: types.NewStruct(nil, nil), tag: `plystra-default:"{}"`, want: "defaults are supported only for scalar configuration fields"},
		{name: "list default", fieldType: types.NewSlice(types.Typ[types.String]), tag: `plystra-default:"[]"`, want: "defaults are supported only for scalar configuration fields"},
		{name: "invalid boolean", fieldType: types.Typ[types.Bool], tag: `plystra-default:"TRUE"`, want: "is not a canonical boolean"},
		{name: "fixed integer overflow", fieldType: types.Typ[types.Int8], tag: `plystra-default:"128"`, want: "outside the 8-bit range"},
		{name: "platform integer overflow", fieldType: types.Typ[types.Int], tag: `plystra-default:"2147483648"`, want: "outside the portable 32-bit range"},
		{name: "negative unsigned integer", fieldType: types.Typ[types.Uint16], tag: `plystra-default:"-1"`, want: "is not a base-10 uint16 value"},
		{name: "nonfinite number", fieldType: types.Typ[types.Float64], tag: `plystra-default:"NaN"`, want: "is not a finite float64 value"},
		{name: "float overflow", fieldType: types.Typ[types.Float32], tag: `plystra-default:"1e100"`, want: "is not a finite float32 value"},
		{name: "invalid integer syntax", fieldType: types.Typ[types.Int64], tag: `plystra-default:"5msec"`, want: "is not a base-10 int64 value"},
		{name: "invalid duration", fieldType: durationType, tag: `plystra-default:"5msec"`, want: "is not a valid time.Duration"},
		{name: "invalid URL", fieldType: urlType, tag: `plystra-default:"https://example.test/\n"`, want: "is not a valid net/url.URL"},
		{name: "ignored metadata", fieldType: types.Typ[types.String], tag: `yaml:"-" plystra:"required"`, want: "excluded with yaml:\"-\" must not declare Plystra configuration metadata"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			compiled := compiledConfigurationPackage("example.com/app/service", "service", []configurationTestField{{
				name:      "Value",
				fieldType: test.fieldType,
				tag:       test.tag,
			}})
			parsed := declaration(t, "service/implementation.go", "service", "New", "service.operation.run/v1")
			_, err := implementationinventory.Build([]implementationinventory.Input{{
				ModulePath:  "example.com/app",
				PackagePath: "example.com/app/service",
				Local:       true,
				Declaration: parsed,
				Types:       compiled,
			}}, []implementationinventory.InterfaceInput{canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")})
			if !errors.Is(err, implementationinventory.ErrInvalidConfiguration) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Build error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBuildRejectsInconsistentCompiledPackageProvenance(t *testing.T) {
	t.Parallel()

	parsed := declaration(t, "service/new.go", "service", "New", "service.operation.run/v1")
	_, err := implementationinventory.Build([]implementationinventory.Input{{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Declaration: parsed,
		Types:       types.NewPackage("example.com/other/service", "service"),
	}}, nil)
	if !errors.Is(err, implementationinventory.ErrInvalidInput) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsConstructorAbsentFromCompiledPackage(t *testing.T) {
	t.Parallel()

	parsed := declaration(t, "service/new.go", "service", "New", "service.operation.run/v1")
	_, err := implementationinventory.Build([]implementationinventory.Input{{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Declaration: parsed,
		Types:       types.NewPackage("example.com/app/service", "service"),
	}}, nil)
	if !errors.Is(err, implementationinventory.ErrInvalidInput) {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsDuplicateFullyQualifiedConstructorSymbol(t *testing.T) {
	t.Parallel()

	parsed := declaration(t, "service/new.go", "service", "New", "service.operation.run/v1")
	compiled := compiledPackage("example.com/app/service", "service", "New", "Run")
	input := implementationinventory.Input{
		ModulePath:  "example.com/app",
		PackagePath: "example.com/app/service",
		Local:       true,
		Declaration: parsed,
		Types:       compiled,
	}
	canonical := canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")
	_, err := implementationinventory.Build([]implementationinventory.Input{input, input}, []implementationinventory.InterfaceInput{canonical})
	if !errors.Is(err, implementationinventory.ErrDuplicateSymbol) || !strings.Contains(err.Error(), "example.com/app/service.New") || !strings.Contains(err.Error(), "example.com/app@local/service/new.go:4:6") {
		t.Fatalf("Build error = %v", err)
	}
}

func TestBuildRejectsInvalidCanonicalInterfaceInputs(t *testing.T) {
	t.Parallel()

	first, err := interfaceid.Parse("service.first.run/v1")
	if err != nil {
		t.Fatalf("Parse first Interface: %v", err)
	}
	second, err := interfaceid.Parse("service.second.run/v1")
	if err != nil {
		t.Fatalf("Parse second Interface: %v", err)
	}
	shared := canonicalInterface(t, "service.first.run/v1", "example.com/interfaces/shared", "Run")
	secondInSharedPackage := shared
	secondInSharedPackage.ID = second
	for _, test := range []struct {
		name   string
		inputs []implementationinventory.InterfaceInput
	}{
		{name: "empty ID", inputs: []implementationinventory.InterfaceInput{{PackagePath: "example.com/interfaces/first"}}},
		{name: "invalid package", inputs: []implementationinventory.InterfaceInput{{ID: first, PackagePath: "../interfaces"}}},
		{name: "missing compiled package", inputs: []implementationinventory.InterfaceInput{{ID: first, PackagePath: "example.com/interfaces/first"}}},
		{name: "mismatched compiled package", inputs: []implementationinventory.InterfaceInput{{ID: first, PackagePath: "example.com/interfaces/first", Types: shared.Types}}},
		{name: "duplicate package", inputs: []implementationinventory.InterfaceInput{shared, secondInSharedPackage}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := implementationinventory.Build(nil, test.inputs); !errors.Is(err, implementationinventory.ErrInvalidInput) {
				t.Fatalf("Build error = %v", err)
			}
		})
	}
}

func TestBuildRejectsMissingAndIncompatibleDeclaredInterfaces(t *testing.T) {
	t.Parallel()

	canonical := canonicalInterface(t, "service.operation.run/v1", "example.com/interfaces/operation", "Run")
	for _, test := range []struct {
		name        string
		interfaceID string
		methods     []string
		want        string
	}{
		{name: "not visible", interfaceID: "service.unknown.run/v1", methods: []string{"Run"}, want: "has no visible canonical Interface"},
		{name: "missing method", interfaceID: "service.operation.run/v1", want: "missing method Run"},
		{name: "wrong signature", interfaceID: "service.operation.run/v1", methods: []string{"WrongRun"}, want: "method Run has an incompatible signature"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parsed := declaration(t, "service/new.go", "service", "New", test.interfaceID)
			compiled := compiledPackage("example.com/app/service", "service", "New", test.methods...)
			_, err := implementationinventory.Build([]implementationinventory.Input{{
				ModulePath:  "example.com/app",
				PackagePath: "example.com/app/service",
				Declaration: parsed,
				Types:       compiled,
			}}, []implementationinventory.InterfaceInput{canonical})
			if !errors.Is(err, implementationinventory.ErrInvalidConformance) || !strings.Contains(err.Error(), "example.com/app/service.New") || !strings.Contains(err.Error(), "example.com/app@local/service/new.go:3:1") || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Build error = %v", err)
			}
		})
	}
}

func declaration(t testing.TB, path, packageName, functionName, interfaceID string) implementationdecl.Declaration {
	t.Helper()
	declarations, err := implementationdecl.ParseFile(path, []byte("package "+packageName+"\n\n//plystra:implements "+interfaceID+"\nfunc "+functionName+"() {}\n"))
	if err != nil || len(declarations) != 1 {
		t.Fatalf("ParseFile = %#v, %v", declarations, err)
	}
	return declarations[0]
}

func canonicalInterface(t testing.TB, id, packagePath, method string) implementationinventory.InterfaceInput {
	t.Helper()
	identifier, err := interfaceid.Parse(id)
	if err != nil {
		t.Fatalf("Parse Interface ID: %v", err)
	}
	compiled := types.NewPackage(packagePath, "operationv1")
	operation := types.NewFunc(token.NoPos, compiled, method, types.NewSignatureType(nil, nil, nil, nil, nil, false))
	interfaceName := types.NewTypeName(token.NoPos, compiled, "Interface", nil)
	_ = types.NewNamed(interfaceName, types.NewInterfaceType([]*types.Func{operation}, nil).Complete(), nil)
	compiled.Scope().Insert(interfaceName)
	return implementationinventory.InterfaceInput{ID: identifier, PackagePath: packagePath, Types: compiled}
}

func compiledPackage(path, name, function string, methods ...string) *types.Package {
	compiled := types.NewPackage(path, name)
	serviceName := types.NewTypeName(token.NoPos, compiled, "Service", nil)
	service := types.NewNamed(serviceName, types.NewStruct(nil, nil), nil)
	compiled.Scope().Insert(serviceName)
	for _, methodName := range methods {
		name := methodName
		if methodName == "WrongRun" {
			name = "Run"
		}
		var parameters *types.Tuple
		if methodName == "WrongRun" {
			parameters = types.NewTuple(types.NewVar(token.NoPos, compiled, "value", types.Typ[types.String]))
		}
		receiver := types.NewVar(token.NoPos, compiled, "service", types.NewPointer(service))
		signature := types.NewSignatureType(receiver, nil, nil, parameters, nil, false)
		service.AddMethod(types.NewFunc(token.NoPos, compiled, name, signature))
	}
	results := types.NewTuple(
		types.NewVar(token.NoPos, compiled, "", types.NewPointer(service)),
		types.NewVar(token.NoPos, compiled, "", types.Universe.Lookup("error").Type()),
	)
	signature := types.NewSignatureType(nil, nil, nil, nil, results, false)
	compiled.Scope().Insert(types.NewFunc(token.NoPos, compiled, function, signature))
	return compiled
}

func compiledConfiguredPackage(path, name string, fieldType types.Type) *types.Package {
	return compiledConfigurationPackage(path, name, []configurationTestField{{
		name:      "Password",
		fieldType: fieldType,
		tag:       `yaml:"password"`,
	}})
}

type configurationTestField struct {
	name      string
	fieldType types.Type
	tag       string
}

func compiledConfigurationPackage(path, name string, fields []configurationTestField) *types.Package {
	compiled := types.NewPackage(path, name)
	configName := types.NewTypeName(token.NoPos, compiled, "Config", nil)
	configFields := make([]*types.Var, len(fields))
	configTags := make([]string, len(fields))
	for index, field := range fields {
		configFields[index] = types.NewVar(token.NoPos, compiled, field.name, field.fieldType)
		configTags[index] = field.tag
	}
	config := types.NewNamed(configName, types.NewStruct(configFields, configTags), nil)
	compiled.Scope().Insert(configName)

	serviceName := types.NewTypeName(token.NoPos, compiled, "Service", nil)
	service := types.NewNamed(serviceName, types.NewStruct(nil, nil), nil)
	compiled.Scope().Insert(serviceName)
	receiver := types.NewVar(token.NoPos, compiled, "service", types.NewPointer(service))
	service.AddMethod(types.NewFunc(token.NoPos, compiled, "Run", types.NewSignatureType(receiver, nil, nil, nil, nil, false)))

	parameters := types.NewTuple(types.NewVar(token.NoPos, compiled, "configuration", config))
	results := types.NewTuple(
		types.NewVar(token.NoPos, compiled, "", types.NewPointer(service)),
		types.NewVar(token.NoPos, compiled, "", types.Universe.Lookup("error").Type()),
	)
	compiled.Scope().Insert(types.NewFunc(token.NoPos, compiled, "New", types.NewSignatureType(nil, nil, nil, parameters, results, false)))
	return compiled
}
