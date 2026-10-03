package applicationgenerate_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/runtimebaseline"
	"go.yaml.in/yaml/v3"
)

func TestGeneratedBinaryValidatesDormantConfiguration(t *testing.T) {
	const module = "example.com/dormant-runtime"
	const symbol = module + "/configowner.New"
	root := filepath.Join(t.TempDir(), "source")
	writeApplicationModule(t, root, module)
	writeConstructorConfigurationOwner(t, root, module, true)
	implementation := filepath.Join(root, "configowner/implementation.go")
	source := string(readAbsoluteFile(t, implementation))
	source = strings.Replace(source, "Endpoint string", "Endpoint string `plystra:\"required\"`", 1)
	source = strings.Replace(source, "Label string", "Label string `plystra-default:\"private-default\"`\n\tCount int8 `plystra:\"build-visible\"`\n\tPointer *Nested\n\tMapping map[string]Nested", 1)
	source = strings.Replace(source, "type Service struct{}", "type Nested struct { Value string `plystra:\"required\"` }\ntype Service struct{}", 1)
	source = strings.Replace(source, "return &Service{}, nil", "panic(\"dormant constructor entered\")", 1)
	writeFile(t, implementation, source)
	withoutConfig := writeAlternativeConstructorConfigurationOwner(t, root, module)
	alternative := filepath.Join(root, "configowneralt/implementation.go")
	writeFile(t, alternative, strings.Replace(string(readAbsoluteFile(t, alternative)), "New(Config)", "New()", 1))
	choice := "interfaces: {use: {configuration.owner/v1: " + symbol + "}}\n"
	config := func(value string) string { return "config: {" + symbol + ": " + value + "}\n" }
	writeFile(t, filepath.Join(root, "plystra.yaml"), choice+config("{endpoint: private-endpoint, password: {env: PRIVATE_DORMANT_SECRET}}"))
	var stdout, stderr bytes.Buffer
	if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
		t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
	}
	for _, file := range snapshotGenerated(t, root) {
		for _, private := range []string{"private-default", "private-endpoint", "PRIVATE_DORMANT_SECRET"} {
			if bytes.Contains(file.data, []byte(private)) {
				t.Fatalf("public artifact %s contains private data", file.path)
			}
		}
	}
	for _, path := range []string{"generated/go/bootstrap/bootstrap_gen.go", "generated/go/assembly/interfaces_gen.go"} {
		if bytes.Contains(readFile(t, root, path), []byte(symbol)) {
			t.Fatal("dormant constructor entered generated bootstrap or assembly")
		}
	}
	deployment := t.TempDir()
	binary := filepath.Join(deployment, "app")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(t.Context(), "go", "build", "-race", "-mod=readonly", "-o", binary, "./generated/go/application")
	build.Dir, build.Env = root, goEnvironment(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	baseline := filepath.Join(deployment, "baseline.json")
	copyPrivateBaseline(t, filepath.Join(root, "dist/runtime-baseline.json"), baseline)
	baselineBytes := readAbsoluteFile(t, baseline)
	if err := os.Rename(root, root+"-unavailable"); err != nil {
		t.Fatal(err)
	}
	adopted := func(first, second string) string {
		return "composition: {exports: {first: {interfaces: {use: {configuration.owner/v1: " + symbol + "}}, config: {" + symbol + ": " + first + "}}, second: {config: {" + symbol + ": " + second + "}}}, adopt: [{module: " + module + ", export: first}, {module: " + module + ", export: second}]}\n"
	}
	for _, mode := range []string{"default", "environment", "replacement"} {
		type runtimeCase struct{ name, document, rule, overlay string }
		cases := []runtimeCase{
			{name: "absent object", document: choice},
			{name: "valid private reference", document: choice + config("{endpoint: private-endpoint, password: {env: PRIVATE_DORMANT_SECRET}}")},
			{name: "dormant build visible change", document: choice + config("{endpoint: private-endpoint, count: 127, pointer: null}")},
			{name: "required", document: choice + config("{}"), rule: "required field is absent"},
			{name: "unknown", document: choice + config("{endpoint: private-endpoint, private_unknown: private-value}"), rule: "unknown field"},
			{name: "overflow", document: choice + config("{endpoint: private-endpoint, count: 128}"), rule: "compiled Go type"},
			{name: "secret", document: choice + config("{endpoint: private-endpoint, password: {env: 'private-invalid-name'}}"), rule: "compiled Go type"},
			{name: "nested required", document: choice + config("{endpoint: private-endpoint, pointer: {}}"), rule: "required field is absent"},
			{name: "map required", document: choice + config("{endpoint: private-endpoint, mapping: {private-key: {}}}"), rule: "required field is absent"},
			{name: "unowned", document: config("{endpoint: private-endpoint}"), rule: "no effective constructor owner"},
			{name: "unknown owner", document: "interfaces: {use: {configuration.owner/v1: example.com/absent.New}}\n", rule: "constructor inventory"},
			{name: "incompatible owner", document: "interfaces: {use: {other.owner/v1: " + symbol + "}}\n", rule: "constructor inventory"},
			{name: "no configuration parameter", document: "interfaces: {use: {configuration.owner/v1: " + withoutConfig + "}}\nconfig: {" + withoutConfig + ": {}}\n", rule: "constructor inventory"},
			{name: "whole removal", document: config("{$remove: true}")},
			{name: "adopted required", document: "composition: {exports: {first: {interfaces: {use: {configuration.owner/v1: " + symbol + "}}, config: {" + symbol + ": {}}}}, adopt: [{module: " + module + ", export: first}]}\n", rule: "required field is absent"},
			{name: "adopted partials", document: adopted("{endpoint: private-endpoint}", "{password: {env: PRIVATE_DORMANT_SECRET}}")},
			{name: "adopted conflict", document: adopted("{endpoint: private-first}", "{endpoint: private-second}"), rule: "conflict"},
			{name: "adopted replacement", document: adopted("{endpoint: private-first}", "{endpoint: private-second}") + config("{endpoint: private-local}")},
			{name: "adopted removal", document: adopted("{endpoint: private-first}", "{endpoint: private-second}") + config("{$remove: true}")},
			{name: "adopted invalid suppressed", document: adopted("{endpoint: private-first, count: 128}", "{}") + config("{count: 1}"), rule: "compiled Go type"},
		}
		if mode == "environment" {
			cases = append(cases,
				runtimeCase{name: "required supplied by overlay", document: choice + config("{}"), overlay: config("{endpoint: private-local}")},
				runtimeCase{name: "overlay required removal", document: choice + config("{endpoint: private-local}"), overlay: config("{endpoint: {$remove: true}}"), rule: "required field is absent"},
				runtimeCase{name: "overlay whole removal", document: choice + config("{endpoint: private-local}"), overlay: config("{$remove: true}")},
				runtimeCase{name: "overlay ownership removal", document: choice + config("{endpoint: private-local}"), overlay: "interfaces: {use: {configuration.owner/v1: {$remove: true}}}\n", rule: "no effective constructor owner"},
				runtimeCase{name: "overlay ownership replacement", document: choice + config("{endpoint: private-local}"), overlay: "interfaces: {use: {configuration.owner/v1: " + withoutConfig + "}}\n", rule: "no effective constructor owner"},
				runtimeCase{name: "overlay ownership replacement and removal", document: choice + config("{endpoint: private-local}"), overlay: "interfaces: {use: {configuration.owner/v1: " + withoutConfig + "}}\n" + config("{$remove: true}")},
				runtimeCase{name: "overlay pointer replacement", document: choice + config("{endpoint: private-local, pointer: {value: private-lower}}"), overlay: config("{pointer: {}}"), rule: "required field is absent"},
			)
		}
		for _, tc := range cases {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				configurationRoot := t.TempDir()
				args := []string{"--smoke", "--configuration-root", configurationRoot, "--runtime-baseline", baseline}
				document := tc.document
				switch mode {
				case "environment":
					overlay := tc.overlay
					if overlay == "" {
						overlay = "{}\n"
					}
					writeFile(t, filepath.Join(configurationRoot, "plystra.test.yaml"), overlay)
					args = append(args, "--env", "test")
				case "replacement":
					var selected map[string]any
					if err := yaml.Unmarshal([]byte(document), &selected); err != nil {
						t.Fatal(err)
					}
					rootFields := map[string]any{"config": map[string]any{"private-invalid-root": 123}}
					if composition, ok := selected["composition"].(map[string]any); ok {
						rootFields["composition"] = map[string]any{"exports": composition["exports"]}
						delete(composition, "exports")
					}
					selectedData, err := yaml.Marshal(selected)
					if err != nil {
						t.Fatal(err)
					}
					writeFile(t, filepath.Join(configurationRoot, "selected.yaml"), string(selectedData))
					rootData, err := yaml.Marshal(rootFields)
					if err != nil {
						t.Fatal(err)
					}
					document = string(rootData)
					args = append(args, "--config", "selected.yaml")
				}
				writeFile(t, filepath.Join(configurationRoot, "plystra.yaml"), document)
				before := snapshotTree(t, configurationRoot)
				process := exec.CommandContext(t.Context(), binary, args...)
				process.Dir = deployment
				for _, entry := range goEnvironment(nil) {
					name, _, _ := strings.Cut(entry, "=")
					if strings.EqualFold(name, "PLYSTRA_ENV") || strings.EqualFold(name, "PLYSTRA_CONFIG") || strings.EqualFold(name, "PRIVATE_DORMANT_SECRET") {
						continue
					}
					process.Env = append(process.Env, entry)
				}
				output, err := process.CombinedOutput()
				if !reflect.DeepEqual(before, snapshotTree(t, configurationRoot)) {
					t.Fatal("startup modified authored configuration")
				}
				if tc.rule == "" && err != nil || tc.rule != "" && (err == nil || !bytes.Contains(output, []byte(tc.rule))) {
					t.Fatalf("startup = %v; want %q\n%s", err, tc.rule, output)
				}
				for _, private := range []string{"private-", "PRIVATE_", configurationRoot, baseline, "dormant constructor entered"} {
					if bytes.Contains(output, []byte(private)) {
						t.Fatal("startup exposed private values or activated dormant constructor")
					}
				}
			})
		}
	}
	for _, value := range []string{`{}`, `{"/label":128}`, `{"/label":"private-default","private-key":true}`} {
		t.Run("invalid private defaults/"+value, func(t *testing.T) {
			document, err := runtimebaseline.Decode(baselineBytes)
			if err != nil {
				t.Fatal(err)
			}
			document.Defaults[symbol] = []byte(value)
			data, err := runtimebaseline.Encode(document)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(baseline, data, 0600); err != nil {
				t.Fatal(err)
			}
			for _, mode := range []string{"default", "environment", "replacement"} {
				t.Run(mode, func(t *testing.T) {
					configurationRoot := t.TempDir()
					selected := choice + config("{endpoint: private-endpoint}")
					writeFile(t, filepath.Join(configurationRoot, "plystra.yaml"), selected)
					args := []string{"--smoke", "--configuration-root", configurationRoot, "--runtime-baseline", baseline}
					switch mode {
					case "environment":
						writeFile(t, filepath.Join(configurationRoot, "plystra.test.yaml"), "{}\n")
						args = append(args, "--env", "test")
					case "replacement":
						writeFile(t, filepath.Join(configurationRoot, "selected.yaml"), selected)
						args = append(args, "--config", "selected.yaml")
					}
					process := exec.CommandContext(t.Context(), binary, args...)
					process.Dir, process.Env = deployment, goEnvironment(nil)
					output, err := process.CombinedOutput()
					if err == nil || !bytes.Contains(output, []byte("invalid private runtime baseline")) {
						t.Fatalf("invalid default startup = %v\n%s", err, output)
					}
					if bytes.Contains(output, []byte("private-default")) || bytes.Contains(output, []byte("private-key")) {
						t.Fatal("default leaked")
					}
				})
			}
		})
	}
}

func TestDormantValidationInventorySeparatesIntentDefaultsAndSchema(t *testing.T) {
	const module = "example.com/dormant-inventory"
	root := t.TempDir()
	writeApplicationModule(t, root, module)
	symbol := writeConstructorConfigurationOwner(t, root, module, false)
	implementation := filepath.Join(root, "configowner/implementation.go")
	source := strings.Replace(string(readAbsoluteFile(t, implementation)), "Label string", "Label string `plystra-default:\"private-first\"`", 1)
	writeFile(t, implementation, source)
	writeFile(t, filepath.Join(root, "plystra.yaml"), "{}\n")
	generate := func() (runtimebaseline.Document, applicationgen.ManifestProvenance) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := command.RunIn([]string{"generate"}, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
			t.Fatalf("generate = %d: %s\n%s", code, stdout.Bytes(), stderr.Bytes())
		}
		baseline, err := runtimebaseline.Decode(readFile(t, root, "dist/runtime-baseline.json"))
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		return baseline, manifest
	}
	baseline, manifest := generate()
	bootstrap := readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {use: {configuration.owner/v1: "+symbol+"}}\nconfig: {"+symbol+": {endpoint: private-endpoint}}\n")
	selected, selectedManifest := generate()
	if selected.ContractID != baseline.ContractID || selectedManifest.ApplicationModelDigest() != manifest.ApplicationModelDigest() || !bytes.Equal(bootstrap, readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")) {
		t.Fatal("dormant intent changed executable contract")
	}
	writeFile(t, implementation, strings.Replace(source, "private-first", "private-second", 1))
	changedDefault, _ := generate()
	if changedDefault.ContractID != baseline.ContractID || !bytes.Equal(bootstrap, readFile(t, root, "generated/go/bootstrap/bootstrap_gen.go")) || !bytes.Contains(changedDefault.Defaults[symbol], []byte("private-second")) {
		t.Fatal("private default refresh changed public contract or failed to refresh")
	}
	writeFile(t, implementation, strings.Replace(source, "Endpoint string", "Endpoint string\n\tCount int8", 1))
	changedSchema, schemaManifest := generate()
	if changedSchema.ContractID == baseline.ContractID || schemaManifest.ApplicationModelDigest() != manifest.ApplicationModelDigest() {
		t.Fatal("validation schema identity conflated with frozen executable identity")
	}
}
