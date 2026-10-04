package command_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"golang.org/x/mod/modfile"
)

// writeCommandTemplate installs a local module into the consumer's effective
// graph without copying its declarations into the consumer's authored delta.
func writeCommandTemplate(t testing.TB, root, name, configuration string) (string, string) {
	t.Helper()
	module := "example.com/templates/" + name
	directory := t.TempDir()
	writeCommandFile(t, filepath.Join(directory, "go.mod"), "module "+module+"\n\ngo 1.26\n")
	writeCommandFile(t, filepath.Join(directory, "plystra.yaml"), configuration)
	parsed, err := modfile.Parse("go.mod", readCommandFile(t, root, "go.mod"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := parsed.AddRequire(module, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := parsed.AddReplace(module, "", filepath.ToSlash(directory), ""); err != nil {
		t.Fatal(err)
	}
	data, err := parsed.Format()
	if err != nil {
		t.Fatal(err)
	}
	writeCommandFile(t, filepath.Join(root, "go.mod"), string(data))
	return "template: " + module + "\n", directory
}

func TestPublicTemplateErrorsRetainRootSourceInEverySelector(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		for _, module := range []string{"example.com/acme/policy", "example.com/absent", "example.com/templates/nonproject"} {
			t.Run(mode+"/"+module, func(t *testing.T) {
				root := writeCommandPolicyProject(t)
				if module == "example.com/templates/nonproject" {
					_, dependency := writeCommandTemplate(t, root, "nonproject", "{}\n")
					if err := os.Remove(filepath.Join(dependency, "plystra.yaml")); err != nil {
						t.Fatal(err)
					}
				}
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "template: "+module+"\n")
				var selector []string
				switch mode {
				case "environment":
					writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
					selector = []string{"--env", "production"}
				case "replacement":
					writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), "{}\n")
					selector = []string{"--config", "deploy/customer.yaml"}
				}
				before := commandTree(t, root)
				for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}, {"inspect", "configuration"}} {
					args := append(append([]string(nil), invocation...), selector...)
					code, _, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code != 1 || !strings.Contains(stderr, "PLYSTRA_TEMPLATE_INVALID") || !strings.Contains(stderr, "example.com/acme/policy:plystra.yaml:") || !strings.Contains(stderr, module) {
						t.Fatalf("%v = %d, %q", args, code, stderr)
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatalf("%v changed a rejected Project", args)
					}
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}

func TestPublicSelectedDocumentCannotReplaceTemplateRelationship(t *testing.T) {
	for _, mode := range []string{"environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeCommandPolicyProject(t)
			relationship, dependency := writeCommandTemplate(t, root, "base", "interfaces: {require: [email.send/v1]}\n")
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), relationship)
			path, selector := "plystra.production.yaml", []string{"--env", "production"}
			if mode == "replacement" {
				path, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			writeCommandFile(t, filepath.Join(root, path), "# forbidden relationship\n\ntemplate: example.com/private-replacement\n")
			before, inherited := commandTree(t, root), commandTree(t, dependency)
			for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
				args := append(append([]string(nil), invocation...), selector...)
				code, _, stderr := runCommand(t, args, root, commandGoEnvironment())
				if code != 1 || !strings.Contains(stderr, "PLYSTRA_TEMPLATE_INVALID") || !strings.Contains(stderr, "example.com/acme/policy:"+path+":3:1") {
					t.Fatalf("%v = %d, %q", args, code, stderr)
				}
				if !reflect.DeepEqual(commandTree(t, root), before) || !reflect.DeepEqual(commandTree(t, dependency), inherited) {
					t.Fatal("rejected relationship mutation changed authored state")
				}
			}
			assertNoCommandTransactions(t, root)
		})
	}
}

func TestPublicMalformedTemplateRelationshipReportsExactOwningKey(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		for _, owner := range []string{"current", "template"} {
			t.Run(mode+"/"+owner, func(t *testing.T) {
				root := writeCommandPolicyProject(t)
				malformed := "# relationship follows\n\ntemplate: [private-value]\n"
				module, dependency := "example.com/acme/policy", ""
				if owner == "template" {
					var relationship string
					relationship, dependency = writeCommandTemplate(t, root, "malformed", malformed)
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), relationship)
					module = "example.com/templates/malformed"
				} else {
					writeCommandFile(t, filepath.Join(root, "plystra.yaml"), malformed)
				}
				var selector []string
				switch mode {
				case "environment":
					writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
					selector = []string{"--env", "production"}
				case "replacement":
					writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), "{}\n")
					selector = []string{"--config", "deploy/customer.yaml"}
				}
				before := commandTree(t, root)
				var inherited map[string][]byte
				if dependency != "" {
					inherited = commandTree(t, dependency)
				}
				for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}, {"inspect", "configuration"}} {
					args := append(append([]string(nil), invocation...), selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					wantStdout := ""
					if invocation[0] == "inspect" {
						wantStdout = inspectProgress
					}
					if code != 1 || stdout != wantStdout || !strings.Contains(stderr, "PLYSTRA_TEMPLATE_INVALID") || !strings.Contains(stderr, module+":plystra.yaml:3:1") || strings.Contains(stderr, "private-value") {
						t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
					}
					if !reflect.DeepEqual(commandTree(t, root), before) || dependency != "" && !reflect.DeepEqual(commandTree(t, dependency), inherited) {
						t.Fatal("malformed relationship rejection changed authored inputs")
					}
				}
				assertNoCommandTransactions(t, root)
			})
		}
	}
}

func TestPublicTemplateOrderRetainsEqualPrivateContributions(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	const fieldPath = `config["` + constructor + `"]["settings"]`
	root := writeImplementationSelectionCommandProject(t)
	writeCommandPointerConfigurationImplementation(t, root)
	configuration := "config: {" + constructor + ": {settings: {first: PRIVATE_EQUAL}}}\n"
	ancestor, oldest := writeCommandTemplate(t, root, "oldest", configuration)
	relationship, nearest := writeCommandTemplate(t, root, "nearest", ancestor+configuration)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), relationship+"interfaces: {use: {email.send/v1: "+constructor+"}}\n")
	before, oldestBefore, nearestBefore := commandTree(t, root), commandTree(t, oldest), commandTree(t, nearest)
	for _, invocation := range [][]string{{"inspect", "configuration"}, {"explain", "config", fieldPath}} {
		for _, format := range []string{"human", "json"} {
			args := append(append([]string(nil), invocation...), "--format", format)
			code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
			if code != 0 || strings.Contains(stdout+stderr, "PRIVATE_EQUAL") || strings.Contains(stdout+stderr, root) {
				t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
			}
			if format == "human" {
				if invocation[0] == "inspect" {
					if !strings.Contains(stdout, "Template order: 1 (oldest to nearest)") || !strings.Contains(stdout, "Template order: 2 (oldest to nearest)") {
						t.Fatalf("inspection omits template ordering: %s", stdout)
					}
				} else if !strings.Contains(stdout, "template order 2, oldest to nearest") {
					t.Fatalf("explanation omits winning template order: %s", stdout)
				}
				continue
			}
			var evidence json.RawMessage
			if invocation[0] == "inspect" {
				evidence = decodeInspectGraphCommandEnvelope(t, stdout).Result.ResolutionEvidence
			} else {
				evidence = decodeExplainCommandEnvelope(t, stdout).Result.ResolutionEvidence
			}
			var document struct {
				Fields []struct {
					Path         string `json:"path"`
					Contributors []struct {
						Owner         string `json:"owner"`
						Precedence    int    `json:"precedence"`
						TemplateOrder int    `json:"template_order"`
						Effective     bool   `json:"effective"`
						Sources       []struct {
							Module string `json:"module"`
						} `json:"sources"`
					} `json:"contributors"`
				} `json:"configuration_fields"`
			}
			if err := json.Unmarshal(evidence, &document); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, field := range document.Fields {
				if field.Path != fieldPath {
					continue
				}
				found = true
				if len(field.Contributors) != 2 {
					t.Fatalf("equal private layers were grouped: %#v", field)
				}
				for index, contribution := range field.Contributors {
					module := []string{"example.com/templates/oldest", "example.com/templates/nearest"}[index]
					if contribution.Owner != "template" || contribution.Precedence != 1 || contribution.TemplateOrder != index+1 || contribution.Effective != (index == 1) || len(contribution.Sources) != 1 || contribution.Sources[0].Module != module {
						t.Fatalf("template contribution %d = %#v", index, contribution)
					}
				}
			}
			if !found {
				t.Fatalf("evidence omits %s", fieldPath)
			}
		}
	}
	if !reflect.DeepEqual(commandTree(t, root), before) || !reflect.DeepEqual(commandTree(t, oldest), oldestBefore) || !reflect.DeepEqual(commandTree(t, nearest), nearestBefore) {
		t.Fatal("inspection changed authored template inputs")
	}
}

func TestPublicTemplateRemovalHistorySurvivesObjectRevival(t *testing.T) {
	const constructor = "example.com/acme/implementation-use/smtp.New"
	const objectPath = `config["` + constructor + `"]`
	const childPath = objectPath + `["settings"]["first"]`
	for _, mode := range []string{"default", "environment", "replacement"} {
		for _, boundary := range []string{"constructor", "settings"} {
			t.Run(mode+"/"+boundary, func(t *testing.T) {
				root := writeImplementationSelectionCommandProject(t)
				writeCommandPointerConfigurationImplementation(t, root)
				implementation := string(readCommandFile(t, root, "smtp/implementation.go"))
				writeCommandFile(t, filepath.Join(root, "smtp/implementation.go"), strings.Replace(implementation, "Settings *struct", "Settings struct", 1))
				ancestor, oldest := writeCommandTemplate(t, root, "oldest", "config: {"+constructor+": {settings: {first: PRIVATE_OLD}}}\n")
				removed, removedPath := "{$remove: true}", objectPath
				if boundary == "settings" {
					removed, removedPath = "{settings: {$remove: true}}", objectPath+`["settings"]`
				}
				relationship, nearest := writeCommandTemplate(t, root, "nearest", ancestor+"config: {"+constructor+": "+removed+"}\n")
				delta := "interfaces: {use: {email.send/v1: " + constructor + "}}\nconfig: {" + constructor + ": {settings: {second: PRIVATE_NEW}}}\n"
				selected := "plystra.yaml"
				var selector []string
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), relationship)
				switch mode {
				case "default":
					delta = relationship + delta
				case "environment":
					selected, selector = "plystra.production.yaml", []string{"--env", "production"}
				case "replacement":
					selected, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
				writeCommandFile(t, filepath.Join(root, selected), delta)
				before, oldestBefore, nearestBefore := commandTree(t, root), commandTree(t, oldest), commandTree(t, nearest)
				for _, invocation := range [][]string{{"inspect", "configuration"}, {"explain", "config", childPath}} {
					for _, format := range []string{"human", "json"} {
						args := append(append(append([]string(nil), invocation...), "--format", format), selector...)
						code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
						if code != 0 || strings.Contains(stdout+stderr, "PRIVATE_") || strings.Contains(stdout+stderr, root) {
							t.Fatalf("%v = %d, %q, %q", args, code, stdout, stderr)
						}
						if format == "human" {
							if !strings.Contains(stdout, "suppressed by template at "+removedPath) {
								t.Fatalf("revived object lost historical template suppression: %s", stdout)
							}
							if invocation[0] == "explain" && !strings.Contains(stdout, "template order 2, oldest to nearest") {
								t.Fatalf("explanation lost removal layer: %s", stdout)
							}
						} else if invocation[0] == "explain" {
							document := decodeExplainCommandEnvelope(t, stdout)
							sources := document.Result.Reason.Sources
							if document.Result.Decision.Outcome != "suppressed" || document.Result.Reason.Code != "ancestor-removal" || len(sources) != 1 || sources[0].Module != "example.com/templates/nearest" || sources[0].Kind != "configuration-removal" || document.Result.Change.Path != selected || document.Result.Change.Field != removedPath {
								t.Fatalf("revived-object explanation = %#v", document.Result)
							}
						} else {
							document := decodeInspectGraphCommandEnvelope(t, stdout)
							assertInspectGraphEdge(t, document.Result.Edges, "suppresses-configuration", "configuration-field:"+removedPath, "configuration-field:"+childPath, "ancestor-removal", "example.com/templates/nearest", "plystra.yaml", "configuration-removal")
						}
					}
				}
				args := append([]string{"explain", "config", removedPath, "--format", "json"}, selector...)
				code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
				if code != 0 {
					t.Fatalf("revived parent %v = %d, %q, %q", args, code, stdout, stderr)
				}
				parent := decodeExplainCommandEnvelope(t, stdout).Result
				if parent.Decision.Outcome != "effective" || len(parent.Reason.Sources) != 1 || parent.Reason.Sources[0].Module != "example.com/acme/implementation-use" || parent.Reason.Sources[0].Path != selected || parent.Reason.Sources[0].Kind != "configuration-value" {
					t.Fatalf("revived parent was incorrectly suppressed by historical removal: %#v", parent)
				}
				if !reflect.DeepEqual(commandTree(t, root), before) || !reflect.DeepEqual(commandTree(t, oldest), oldestBefore) || !reflect.DeepEqual(commandTree(t, nearest), nearestBefore) {
					t.Fatal("historical suppression inspection changed authored inputs")
				}
			})
		}
	}
}

func TestPublicTemplateCORSTombstonesAndNullRejection(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeCommandPolicyProject(t)
			relationship, dependency := writeCommandTemplate(t, root, "cors", "http: {cors: {allowed_origins: [https://template.example], allow_credentials: true}}\n")
			selectedPath := "plystra.yaml"
			options := applicationresolve.Options{Start: root, Environment: commandGoEnvironment()}
			var selector []string
			switch mode {
			case "environment":
				selectedPath, selector = "plystra.production.yaml", []string{"--env", "production"}
				options.EnvironmentName = "production"
			case "replacement":
				selectedPath, selector = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				options.ConfigurationPath = selectedPath
			}
			write := func(value string) {
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), relationship)
				if selectedPath == "plystra.yaml" {
					value = relationship + value
				}
				writeCommandFile(t, filepath.Join(root, selectedPath), value)
			}
			inherited := commandTree(t, dependency)
			for _, value := range []string{"http: {cors: {$remove: true}}\n", "http: {cors: {allow_credentials: {$remove: true}}}\n"} {
				write(value)
				resolved, err := applicationresolve.Resolve(t.Context(), options)
				if err != nil {
					t.Fatal(err)
				}
				cors, exists := resolved.Manifest().HTTPCORS()
				if strings.Contains(value, "allow_credentials") {
					if !exists || cors.AllowCredentials || !reflect.DeepEqual(cors.AllowedOrigins, []string{"https://template.example"}) {
						t.Fatalf("credential removal = %#v, %t", cors, exists)
					}
				} else if exists {
					t.Fatal("whole CORS tombstone retained inherited policy")
				}
				for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
					args := append(append([]string(nil), invocation...), selector...)
					if code, _, stderr := runCommand(t, args, root, commandGoEnvironment()); code != 0 {
						t.Fatalf("%v = %d, %s", args, code, stderr)
					}
				}
			}
			for _, value := range []string{"null", "{allow_credentials: null}", "{allowed_origins: null}"} {
				write("http: {cors: " + value + "}\n")
				before := commandTree(t, root)
				for _, invocation := range [][]string{{"generate"}, {"generate", "--check"}, {"check"}} {
					args := append(append([]string(nil), invocation...), selector...)
					code, stdout, stderr := runCommand(t, args, root, commandGoEnvironment())
					if code != 1 || stdout != "" || !strings.Contains(stderr, "Diagnostic: PLYSTRA_") {
						t.Fatalf("null %s: %v = %d, %q, %q", value, args, code, stdout, stderr)
					}
					if !reflect.DeepEqual(commandTree(t, root), before) {
						t.Fatal("invalid CORS mutated Project")
					}
				}
			}
			if !reflect.DeepEqual(commandTree(t, dependency), inherited) {
				t.Fatal("CORS commands changed template source")
			}
			assertNoCommandTransactions(t, root)
		})
	}
}
