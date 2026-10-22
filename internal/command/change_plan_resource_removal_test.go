package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/implementationselect"
	"go.yaml.in/yaml/v3"
)

type resourceRemovalPlanResult struct {
	Status  string `json:"status"`
	Payload struct {
		PlanDigest   string                  `json:"plan_digest"`
		Completeness string                  `json:"completeness"`
		WriteSet     []struct{ Path string } `json:"write_set"`
		Operations   []struct {
			Kind, Action, Instance, Owner, Path string
			Derived                             bool
		} `json:"operations"`
		ConfigurationImpact map[string]json.RawMessage          `json:"configuration_impact"`
		Effects             struct{ Planned []json.RawMessage } `json:"effects"`
	} `json:"payload"`
	Effects struct{ Planned []json.RawMessage } `json:"effects"`
}

func TestRunChangePlanResourceRemovalReportsExactOwnership(t *testing.T) {
	for _, selector := range []struct {
		name, path string
		args       []string
	}{
		{"default", "plystra.yaml", nil},
		{"environment", "plystra.production.yaml", []string{"--env", "production"}},
		{"replacement", "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}},
	} {
		for _, old := range []string{"valid", "invalid config", "unknown provider", "schema-less config"} {
			t.Run(selector.name+"/"+old, func(t *testing.T) {
				root := writeResourceSelectionCommandProject(t)
				writeRemovalWrapper(t, root)
				document := `resources:
  instances:
    database.primary:
      use: example.com/acme/implementation-use/wrapper.New
      config: {value: PRIVATE_PRIMARY}
    database.retained:
      use: example.com/acme/implementation-use/wrapper.New
      config: {value: PRIVATE_RETAINED}
    database.upstream:
      use: example.com/acme/implementation-use/original.New
      config: {value: PRIVATE_UPSTREAM}
  bind:
    instances:
      database.primary: {db: database.upstream}
      database.retained: {db: database.upstream}
`
				rootDocument, selectedDocument := document, document
				overlay := "{}\n"
				if old == "invalid config" {
					selectedDocument = strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1)
					overlay = "resources: {instances: {database.primary: {config: {value: [PRIVATE_INVALID]}}}}\n"
				} else if old == "unknown provider" || old == "schema-less config" {
					provider := "example.com/acme/implementation-use/missing.New"
					if old == "schema-less config" {
						provider = "example.com/acme/implementation-use/schemaless.New"
						writeCommandFile(t, filepath.Join(root, "schemaless/provider.go"), `package schemaless
import "example.com/acme/implementation-use/database"
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(db database.Resource) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
					}
					selectedDocument = strings.Replace(document, "example.com/acme/implementation-use/wrapper.New", provider, 1)
					overlay = fmt.Sprintf("resources: {instances: {database.primary: {use: %s, config: {value: PRIVATE_PRIMARY}}}}\n", provider)
				}
				if selector.name != "environment" {
					rootDocument = selectedDocument
				}
				writeCommandFile(t, filepath.Join(root, "plystra.yaml"), rootDocument)
				writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), selectedDocument)
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), overlay)
				result := runResourceRemovalPlan(t, root, "database.primary", selector.args...)
				if len(result.Payload.WriteSet) != 1 || result.Payload.WriteSet[0].Path != selector.path {
					t.Fatalf("wrong write scope: %#v", result.Payload.WriteSet)
				}
				var unowned []struct{ Owner, Path, Source string }
				if err := json.Unmarshal(result.Payload.ConfigurationImpact["newly_unowned_paths"], &unowned); err != nil {
					t.Fatal(err)
				}
				binding := `resources.bind.instances["database.primary"]["db"]`
				if len(unowned) < 2 {
					t.Fatalf("missing ownership impact: %#v", unowned)
				}
				for _, path := range unowned {
					if path.Owner != "database.primary" || path.Path != binding && path.Path != `resources.instances["database.primary"].config` || path.Source == "" {
						t.Fatalf("removed unrelated owner: %#v", path)
					}
				}
				var retained []struct{ Owner string }
				if err := json.Unmarshal(result.Payload.ConfigurationImpact["retained_owners"], &retained); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"database.retained", "database.upstream"} {
					if !slices.ContainsFunc(retained, func(value struct{ Owner string }) bool { return value.Owner == name }) {
						t.Fatalf("lost retained instance: %#v", retained)
					}
				}
				requested, outgoing := false, false
				for _, operation := range result.Payload.Operations {
					requested = requested || operation.Kind == "resource_selection" && operation.Action == "remove" && operation.Instance == "database.primary" && !operation.Derived
					outgoing = outgoing || operation.Kind == "resource_binding" && operation.Action == "remove" && operation.Path == binding && operation.Derived
					if operation.Derived && (operation.Owner != "database.primary" || operation.Path != binding && operation.Path != `resources.instances["database.primary"].config`) {
						t.Fatalf("unrelated derived edit: %#v", operation)
					}
				}
				if !requested || !outgoing {
					t.Fatalf("missing exact operations: %#v", result.Payload.Operations)
				}
			})
		}
	}
}

func TestRunChangePlanResourceRemovalValidatesSurvivingConsumers(t *testing.T) {
	for _, mode := range []string{"explicit inbound Resource", "explicit inbound Implementation", "implicit missing", "implicit unique", "implicit ambiguous", "unrelated invalid config", "unrelated required config", "dormant required config", "non-enumerable required config", "removed cycle"} {
		t.Run(mode, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeRemovalWrapper(t, root)
			document := `resources:
  instances:
    database.primary: {use: example.com/acme/implementation-use/original.New}
    database.retained: {use: example.com/acme/implementation-use/original.New}
`
			switch mode {
			case "explicit inbound Resource":
				document = strings.Replace(document, "database.retained: {use: example.com/acme/implementation-use/original.New}", "database.retained: {use: example.com/acme/implementation-use/wrapper.New}", 1)
				document += "    database.alternative: {use: example.com/acme/implementation-use/original.New}\n"
				document += "  bind: {instances: {database.retained: {db: database.primary}}}\n"
			case "explicit inbound Implementation", "implicit missing", "implicit unique", "implicit ambiguous":
				writeCommandFile(t, filepath.Join(root, "reports/implementation.go"), `package reports
import (
 "context"
 "example.com/acme/implementation-use/database"
 contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Service struct{}
//plystra:implements reports.read/v1
func New(db database.Resource) (*Service, error) { return &Service{}, nil }
func (*Service) Read(context.Context, contract.Request) (contract.Response, error) { return contract.Response{}, nil }
`)
				if mode == "explicit inbound Implementation" {
					document += "  bind: {implementations: {example.com/acme/implementation-use/reports.New: {db: database.primary}}}\n"
				}
				if mode == "implicit missing" {
					document = strings.ReplaceAll(document, "    database.retained: {use: example.com/acme/implementation-use/original.New}\n", "")
				}
				if mode == "implicit ambiguous" {
					document += "    database.alternative: {use: example.com/acme/implementation-use/original.New}\n"
				}
				document += "interfaces: {require: [reports.read/v1]}\n"
			case "unrelated invalid config":
				document = strings.Replace(document, "database.retained: {use: example.com/acme/implementation-use/original.New}", "database.retained: {use: example.com/acme/implementation-use/original.New, config: {value: [PRIVATE_INVALID]}}", 1)
			case "unrelated required config", "dormant required config", "non-enumerable required config":
				writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
				if mode == "non-enumerable required config" {
					path := filepath.Join(root, "smtp/implementation.go")
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					writeCommandFile(t, path, strings.Replace(string(data), "Endpoint string", "Endpoint map[string]string", 1))
				}
				document += "interfaces:\n  use: {email.send/v1: example.com/acme/implementation-use/smtp.New}\n"
				if mode != "dormant required config" {
					document += "  require: [email.send/v1]\n"
				} else {
					document += "config: {example.com/acme/implementation-use/smtp.New: {}}\n"
				}
			case "removed cycle":
				document = strings.Replace(document, "database.primary: {use: example.com/acme/implementation-use/original.New}", "database.primary: {use: example.com/acme/implementation-use/wrapper.New}", 1)
				document += "  bind: {instances: {database.primary: {db: database.primary}}}\n"
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeResourceRemovalRequest(t, root, "database.primary")
			before := commandTree(t, root)
			exit, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json"}, root, implementationSelectionCommandEnvironment(nil))
			var result resourceRemovalPlanResult
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("unsafe output: %s\n%s", stdout, stderr)
			}
			if mode == "implicit unique" || mode == "removed cycle" {
				if exit != 0 || result.Status != "success" || len(result.Payload.Operations) == 0 {
					t.Fatalf("ordinary implicit resolution failed: %s", stdout)
				}
			} else if exit == 0 || result.Status != "validation_failed" || result.Payload.PlanDigest != "" {
				t.Fatalf("surviving consumer silently repaired: %s", stdout)
			}
			if !reflect.DeepEqual(commandTree(t, root), before) {
				t.Fatal("preview changed Project files")
			}
		})
	}
}

func TestRunChangePlanResourceRemovalRetainsLowerLayerValidation(t *testing.T) {
	for _, invalid := range []string{"unknown provider", "invalid config"} {
		t.Run(invalid, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			document := "resources: {instances: {database.primary: {use: example.com/acme/implementation-use/original.New, config: {value: PRIVATE_PRIMARY}}}}\n"
			if invalid == "unknown provider" {
				document = strings.Replace(document, "/original.New", "/missing.New", 1)
			} else {
				document = strings.Replace(document, "value: PRIVATE_PRIMARY", "value: [PRIVATE_INVALID]", 1)
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
			writeResourceRemovalRequest(t, root, "database.primary")
			before := commandTree(t, root)
			exit, stdout, stderr := runCommand(t, []string{"change", "plan", "--request", "change.json", "--format", "json", "--env", "production"}, root, implementationSelectionCommandEnvironment(nil))
			var result resourceRemovalPlanResult
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if exit == 0 || result.Status != "validation_failed" || result.Payload.PlanDigest != "" || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
				t.Fatalf("invalid lower layer bypassed: %s\n%s", stdout, stderr)
			}
			if !reflect.DeepEqual(commandTree(t, root), before) {
				t.Fatal("failed removal changed Project files")
			}
		})
	}
}

func TestResourceRemovalPreviewEditsOnlyExactConsumerLeaves(t *testing.T) {
	for _, mode := range []string{"complete", "inherited", "local", "override"} {
		t.Run(mode, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeRemovalWrapper(t, root)
			document := `# keep root
resources:
  instances:
    database.primary: {use: example.com/acme/implementation-use/wrapper.New, config: {value: PRIVATE_PRIMARY}}
    database.retained: {use: example.com/acme/implementation-use/wrapper.New, config: {value: PRIVATE_RETAINED}}
    database.upstream: {use: example.com/acme/implementation-use/original.New}
  bind:
    instances:
      database.retained: {db: database.upstream}
`
			if mode != "local" {
				document += "      database.primary: {db: database.upstream}\n"
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			selected := "plystra.yaml"
			options := implementationselect.Options{Start: filepath.Join(root, "original"), Target: "database.primary", Environment: implementationSelectionCommandEnvironment(nil)}
			if mode != "complete" {
				selected, options.EnvironmentName = "plystra.production.yaml", "production"
				overlay := "# keep overlay\nresources:\n  bind:\n    instances:\n      database.unrelated: {untouched: {$remove: true}}\n      database.primary: {previous: {$remove: true}}\n"
				if mode != "inherited" {
					overlay = strings.Replace(overlay, "previous: {$remove: true}", "previous: {$remove: true}, db: database.upstream, local: database.upstream", 1)
				}
				writeCommandFile(t, filepath.Join(root, selected), overlay)
			}
			before := commandTree(t, root)
			plan, err := implementationselect.PreviewResourceRemoval(t.Context(), options)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Changed() || plan.Target() != options.Target || plan.Kind() != "resource" || plan.Constructor().String() != "" || plan.ManifestPath() != filepath.Join(root, selected) {
				t.Fatal("incorrect removal plan identity")
			}
			var updated map[string]any
			if err := yaml.Unmarshal(plan.Updated(), &updated); err != nil {
				t.Fatal(err)
			}
			resources := updated["resources"].(map[string]any)
			instances := resources["instances"].(map[string]any)
			bindings := resources["bind"].(map[string]any)["instances"].(map[string]any)
			if mode == "complete" {
				if _, exists := instances["database.primary"]; exists {
					t.Fatal("whole instance survived complete removal")
				}
				if leaves, exists := bindings["database.primary"]; exists && len(leaves.(map[string]any)) != 0 {
					t.Fatal("outgoing binding survived complete removal")
				}
				if !reflect.DeepEqual(bindings["database.retained"], map[string]any{"db": "database.upstream"}) {
					t.Fatal("surviving binding changed")
				}
			} else {
				tombstone := map[string]any{"$remove": true}
				if !reflect.DeepEqual(instances, map[string]any{"database.primary": tombstone}) {
					t.Fatal("overlay copied lower instances or retained Config")
				}
				wantLeaves := map[string]any{"previous": tombstone}
				if mode != "local" {
					wantLeaves["db"] = tombstone
				}
				if !reflect.DeepEqual(bindings["database.primary"], wantLeaves) || !reflect.DeepEqual(bindings["database.unrelated"], map[string]any{"untouched": tombstone}) {
					t.Fatal("cleanup widened consumer tombstones or lost existing exclusions")
				}
				if !bytes.Contains(plan.Updated(), []byte("# keep overlay")) {
					t.Fatal("lost unrelated overlay comment")
				}
			}
			candidate, err := plan.Inputs().ComposeCandidate(plan.Updated())
			if err != nil || plan.Inputs().ValidateCandidate(candidate) != nil {
				t.Fatalf("invalid removal candidate: %v", err)
			}
			if got := candidate.Manifest().ResourceInstances(); len(got) != 2 {
				t.Fatalf("lost sibling Resource: %d", len(got))
			}
			updatedBytes := plan.Updated()
			updatedBytes[0] = '!'
			if bytes.Equal(updatedBytes, plan.Updated()) || !reflect.DeepEqual(commandTree(t, root), before) {
				t.Fatal("preview exposed mutable bytes or wrote Project files")
			}
		})
	}
}

func TestResourceRemovalPreviewRejectsInvalidRequestBeforeDiscovery(t *testing.T) {
	for _, test := range []struct {
		ctx     context.Context
		options implementationselect.Options
	}{
		{nil, implementationselect.Options{Target: "database.primary"}},
		{t.Context(), implementationselect.Options{Target: "PRIVATE_INVALID!"}},
		{t.Context(), implementationselect.Options{Target: "database.primary", Constructor: "PRIVATE_PROVIDER"}},
	} {
		result, err := implementationselect.PreviewResourceRemoval(test.ctx, test.options)
		if !errors.Is(err, implementationselect.ErrSelect) || strings.Contains(err.Error(), "PRIVATE_") || result.Updated() != nil {
			t.Fatalf("invalid request accepted or leaked: %v", err)
		}
	}
}

func TestRunChangePlanResourceRemovalRejectsOrphansAndInvalidNoOps(t *testing.T) {
	for _, mode := range []string{"absent orphan", "excluded orphan", "excluded lower orphan", "absent invalid graph", "absent required config"} {
		t.Run(mode, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			writeRemovalWrapper(t, root)
			document := "resources:\n  instances:\n    database.retained: {use: example.com/acme/implementation-use/original.New}\n"
			switch mode {
			case "absent orphan", "excluded orphan", "excluded lower orphan":
				if mode == "excluded lower orphan" {
					document += "    database.primary: {use: example.com/acme/implementation-use/wrapper.New}\n"
				}
				document += "  bind: {instances: {database.primary: {db: database.retained}}}\n"
			case "absent invalid graph":
				document = strings.Replace(document, "/original.New", "/wrapper.New", 1)
			case "absent required config":
				writeCommandConfigurableImplementation(t, root, "smtp", "email.send/v1", "email/send/v1", "Send")
				document += "interfaces: {require: [email.send/v1], use: {email.send/v1: example.com/acme/implementation-use/smtp.New}}\n"
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeResourceRemovalRequest(t, root, "database.primary")
			args := []string{"change", "plan", "--request", "change.json", "--format", "json"}
			if mode == "excluded orphan" || mode == "excluded lower orphan" {
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {$remove: true}}}\n")
				args = append(args, "--env", "production")
			}
			before := commandTree(t, root)
			exit, stdout, stderr := runCommand(t, args, root, implementationSelectionCommandEnvironment(nil))
			var result resourceRemovalPlanResult
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatal(err)
			}
			if exit == 0 || result.Status != "validation_failed" || result.Payload.PlanDigest != "" || stderr != "" {
				t.Fatalf("invalid no-op silently repaired: %s\n%s", stdout, stderr)
			}
			if !reflect.DeepEqual(commandTree(t, root), before) {
				t.Fatal("invalid no-op changed Project files")
			}
		})
	}
}

func TestRunChangePlanResourceRemovalSatisfiedAndSparseExclusion(t *testing.T) {
	for _, mode := range []string{"absent default", "absent replacement", "existing overlay exclusion", "new overlay exclusion"} {
		t.Run(mode, func(t *testing.T) {
			root := writeResourceSelectionCommandProject(t)
			document := "resources: {instances: {database.retained: {use: example.com/acme/implementation-use/original.New}}}\n"
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			writeCommandFile(t, filepath.Join(root, "deploy/customer.yaml"), document)
			writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "{}\n")
			var selector []string
			switch mode {
			case "absent replacement":
				selector = []string{"--config", "deploy/customer.yaml"}
			case "existing overlay exclusion":
				writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.primary: {$remove: true}}}\n")
				selector = []string{"--env", "production"}
			case "new overlay exclusion":
				selector = []string{"--env", "production"}
			}
			result := runResourceRemovalPlan(t, root, "database.primary", selector...)
			want := 0
			if mode == "new overlay exclusion" {
				want = 1
			}
			if len(result.Payload.WriteSet) != want || len(result.Payload.Effects.Planned) != want || len(result.Effects.Planned) != want || len(result.Payload.Operations) != 1 {
				t.Fatalf("wrong satisfied/exclusion effects: %#v", result)
			}
		})
	}
}

func TestBuiltCLIChangePlanResourceRemovalFromStdin(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(map[string]string{"GOFLAGS": os.Getenv("GOFLAGS")})
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	root := writeResourceSelectionCommandProject(t)
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json", "--env", "production")
	command.Dir, command.Env = filepath.Join(root, "original"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = strings.NewReader(`{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"remove","instance":"database.primary"}]}`)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil || stderr.Len() != 0 || bytes.Contains(output, []byte("PRIVATE_")) {
		t.Fatalf("compiled removal: %v\n%s\n%s", err, output, &stderr)
	}
	var result resourceRemovalPlanResult
	if err := json.Unmarshal(output, &result); err != nil || result.Status != "success" || len(result.Payload.WriteSet) != 1 || result.Payload.WriteSet[0].Path != "plystra.production.yaml" {
		t.Fatalf("compiled result = %s, %v", output, err)
	}
	if !reflect.DeepEqual(commandTree(t, root), before) {
		t.Fatal("compiled preview changed Project files")
	}
}

func writeRemovalWrapper(t testing.TB, root string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "wrapper/provider.go"), `package wrapper
import "example.com/acme/implementation-use/database"
type Config struct { Value string }
type Value struct{}
//plystra:implements-resource storage.database/v1
func New(cfg Config, db database.Resource) (*Value, error) { return &Value{}, nil }
func (*Value) Read() {}
`)
}

func writeResourceRemovalRequest(t testing.TB, root, target string) {
	t.Helper()
	writeCommandFile(t, filepath.Join(root, "change.json"), fmt.Sprintf(`{"schema":"plystra.change/v1","operations":[{"kind":"resource_selection","action":"remove","instance":%q}]}`, target))
}

func runResourceRemovalPlan(t *testing.T, root, target string, selector ...string) resourceRemovalPlanResult {
	t.Helper()
	writeResourceRemovalRequest(t, root, target)
	before := commandTree(t, root)
	args := append([]string{"change", "plan", "--request", "change.json", "--format", "json"}, selector...)
	var first resourceRemovalPlanResult
	for attempt := 0; attempt < 2; attempt++ {
		exit, stdout, stderr := runCommand(t, args, root, implementationSelectionCommandEnvironment(nil))
		if exit != 0 || stderr != "" || strings.Contains(stdout, "PRIVATE_") {
			t.Fatalf("removal preview = %d\n%s\n%s", exit, stdout, stderr)
		}
		var result resourceRemovalPlanResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != "success" || result.Payload.Completeness != "non_exact" || !strings.HasPrefix(result.Payload.PlanDigest, "sha256:") {
			t.Fatalf("wrong removal result: %s", stdout)
		}
		for _, name := range []string{"newly_required_fields", "retained_owners", "newly_unowned_paths", "derived_operations", "unresolved_decisions"} {
			if value := result.Payload.ConfigurationImpact[name]; len(value) == 0 || string(value) == "null" {
				t.Fatalf("missing impact %s: %s", name, stdout)
			}
		}
		if attempt == 0 {
			first = result
		} else if first.Payload.PlanDigest != result.Payload.PlanDigest {
			t.Fatal("unstable removal plan digest")
		}
	}
	if !reflect.DeepEqual(commandTree(t, root), before) {
		t.Fatal("removal preview changed Project files")
	}
	assertNoCommandTransactions(t, root)
	return first
}
