package command_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/changerequest"
)

func TestRunChangePlanInterfaceBatchActivatesChosenImplementation(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		for _, kind := range []string{"require", "expose"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				root, document := writeConfigurationPlanProject(t)
				document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
				path, args := "plystra.yaml", []string(nil)
				if mode == "environment" {
					writeCommandFile(t, filepath.Join(root, path), document)
					path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "# KEEP_OVERLAY\n{}\n"
				} else if mode == "replacement" {
					path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
				writeCommandFile(t, filepath.Join(root, path), document)
				request := configurationBatchRequest(t, rootConfigurationRequest(t, kind, "present"),
					implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner),
					configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)))
				first := runConfigurationPlan(t, root, request, true, args...)
				second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
				if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 || first.Payload.WriteSet[0].Path != path {
					t.Fatal("joint root and selection lost final-state scope or order independence")
				}
				if !bytes.Contains(first.Payload.ConfigurationImpact["newly_required_fields"], []byte(compoundImplementationOwner)) || !bytes.Contains(first.Payload.ConfigurationImpact["newly_unowned_paths"], []byte(configurationImplementationOwner)) || string(first.Payload.ConfigurationImpact["unresolved_decisions"]) != "[]" {
					t.Fatal("joint root and selection did not analyze the complete final ownership")
				}
			})
		}
	}
}

func TestRunChangePlanInterfaceBatchTransfersAndReleasesSharedOwnership(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprint(active), func(t *testing.T) {
			root, document := writeSharedInterfaceBatchProject(t)
			document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
			copyChoice := bytes.ReplaceAll(implementationConfigurationSelectionRequest(t, "set", configurationImplementationOwner), []byte("email.send/v1"), []byte("email.copy/v1"))
			request := configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner), copyChoice,
				configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)),
				configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_RETAINED"`)))
			if active {
				request = configurationBatchRequest(t, request, rootConfigurationRequest(t, "require", "present"),
					bytes.ReplaceAll(rootConfigurationRequest(t, "require", "present"), []byte("email.send/v1"), []byte("email.copy/v1")))
			}
			first := runConfigurationPlan(t, root, request, true)
			second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true)
			if !reflect.DeepEqual(first.Payload, second.Payload) || string(first.Payload.ConfigurationImpact["newly_unowned_paths"]) != "[]" || !bytes.Contains(first.Payload.ConfigurationImpact["retained_owners"], []byte(configurationImplementationOwner)) {
				t.Fatal("same-request choice did not acquire and retain old constructor ownership")
			}
			writeCommandFile(t, filepath.Join(root, "plystra.yaml"), string(previewImplementationConfiguration(t, root, request).Updated()))
			remove := implementationConfigurationSelectionRequest(t, "remove", "")
			release := configurationBatchRequest(t, remove, bytes.ReplaceAll(remove, []byte("email.send/v1"), []byte("email.copy/v1")), rootConfigurationRequest(t, "require", "absent"),
				bytes.ReplaceAll(rootConfigurationRequest(t, "require", "absent"), []byte("email.send/v1"), []byte("email.copy/v1")))
			result := runConfigurationPlan(t, root, release, true)
			derived := 0
			for _, operation := range result.Payload.Operations {
				if operation.Derived {
					derived++
				}
			}
			if derived != 2 {
				t.Fatal("jointly lost owners were not cleaned exactly once each")
			}
		})
	}
}

func TestRunChangePlanInterfaceBatchRemovesAmbiguousFallback(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeConfigurationPlanProject(t)
			document = strings.Replace(document, "resources:\n", "resources:\n  bind: {implementations: {"+configurationImplementationOwner+": {Obsolete: database.primary}}}\n", 1)
			path, args := "plystra.yaml", []string(nil)
			if mode == "environment" {
				writeCommandFile(t, filepath.Join(root, path), document)
				path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "# KEEP_OVERLAY\nconfig: {"+configurationImplementationOwner+": {value: [PRIVATE_INVALID]}}\n"
			} else {
				document = strings.Replace(document, "value: PRIVATE_IMPLEMENTATION", "value: [PRIVATE_INVALID]", 1)
				if mode == "replacement" {
					path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
			}
			writeCommandFile(t, filepath.Join(root, path), document)
			remove := implementationConfigurationSelectionRequest(t, "remove", "")
			runConfigurationPlan(t, root, remove, false, args...)
			request := configurationBatchRequest(t, remove, rootConfigurationRequest(t, "require", "absent"))
			first := runConfigurationPlan(t, root, request, true, args...)
			second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
			if !reflect.DeepEqual(first.Payload, second.Payload) || !bytes.Contains(first.Payload.ConfigurationImpact["derived_operations"], []byte("Obsolete")) {
				t.Fatal("joint removal failed final-only binding cleanup")
			}
			updated := previewImplementationConfiguration(t, root, request, args...).Updated()
			if mode == "environment" && (!bytes.Contains(updated, []byte("# KEEP_OVERLAY")) || bytes.Contains(updated, []byte("PRIVATE_")) || bytes.Count(updated, []byte("$remove: true")) != 3) {
				t.Fatal("joint removal did not preserve exact selected exclusions")
			}
			for _, action := range []string{"set", "remove"} {
				var value json.RawMessage
				if action == "set" {
					value = json.RawMessage(`"PRIVATE_ORPHAN"`)
				}
				runConfigurationPlan(t, root, configurationBatchRequest(t, request, configurationPlanRequest(t, configurationImplementationOwner, "value", action, value)), false, args...)
			}
		})
	}
}

func TestRunChangePlanInterfaceBatchRepairsTransitiveOwners(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	writeCommandInterface(t, root, "reports/read/v1", "read", "reports.read/v1", "Read")
	writeCommandFile(t, filepath.Join(root, "reports/implementation.go"), `package reports
import (
 "context"
 email "example.com/acme/implementation-use/interfaces/email/send/v1"
 contract "example.com/acme/implementation-use/interfaces/reports/read/v1"
)
type Config struct { Endpoint string `+"`plystra:\"required\"`"+` }
type Service struct{}
//plystra:implements reports.read/v1
func New(cfg Config,sender email.Interface)(*Service,error){panic("must not execute")}
func (*Service) Read(context.Context,contract.Request)(contract.Response,error){return contract.Response{},nil}
`)
	document = strings.Replace(document, "require: [email.send/v1]", "require: []", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	reportsRoot := bytes.ReplaceAll(rootConfigurationRequest(t, "require", "present"), []byte("email.send/v1"), []byte("reports.read/v1"))
	reportsOwner := "example.com/acme/implementation-use/reports.New"
	reportsChoice := bytes.ReplaceAll(implementationConfigurationSelectionRequest(t, "set", reportsOwner), []byte("email.send/v1"), []byte("reports.read/v1"))
	request := configurationBatchRequest(t, reportsRoot, reportsChoice, implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner),
		configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_EMAIL"`)))
	runConfigurationPlan(t, root, request, false)
	request = configurationBatchRequest(t, request, configurationPlanRequest(t, reportsOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_REPORTS"`)))
	first := runConfigurationPlan(t, root, request, true)
	second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true)
	if !reflect.DeepEqual(first.Payload, second.Payload) {
		t.Fatal("joint transitive ownership depends on operation order")
	}
	for _, owner := range []string{reportsOwner, compoundImplementationOwner} {
		if !bytes.Contains(first.Payload.ConfigurationImpact["newly_required_fields"], []byte(owner)) {
			t.Fatal("jointly reachable constructor fields were omitted")
		}
	}
}

func TestRunChangePlanInterfaceBatchRequiresCompleteFinalState(t *testing.T) {
	root, _ := writeSharedInterfaceBatchProject(t)
	copyChoice := bytes.ReplaceAll(implementationConfigurationSelectionRequest(t, "set", configurationImplementationOwner), []byte("email.send/v1"), []byte("email.copy/v1"))
	runConfigurationPlan(t, root, configurationBatchRequest(t, implementationConfigurationSelectionRequest(t, "set", configurationImplementationOwner), copyChoice), true)
	selection := implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner)
	// A singleton can enumerate required fields; a satisfied second operation
	// cannot turn an invalid complete compound into an applicable partial plan.
	runConfigurationPlan(t, root, selection, true)
	for _, other := range [][]byte{rootConfigurationRequest(t, "require", "present"), copyChoice} {
		request := configurationBatchRequest(t, selection, other)
		runConfigurationPlan(t, root, request, false)
		request = configurationBatchRequest(t, request, configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`)))
		runConfigurationPlan(t, root, request, true)
	}
}

func TestRunChangePlanInterfaceBatchPrivateIdentityAndNoOps(t *testing.T) {
	root, _ := writeConfigurationPlanProject(t)
	base := configurationBatchRequest(t, rootConfigurationRequest(t, "require", "present"), implementationConfigurationSelectionRequest(t, "set", configurationImplementationOwner))
	if result := runConfigurationPlan(t, root, base, true); len(result.Payload.WriteSet) != 0 || len(result.Effects.Planned) != 0 {
		t.Fatal("satisfied root/choice batch declared a write")
	}
	request := configurationBatchRequest(t, base, configurationPlanRequest(t, configurationImplementationOwner, "password", "set", json.RawMessage(`{"env":"PRIVATE_SECRET"}`)))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), string(previewImplementationConfiguration(t, root, request).Updated()))
	plan := previewImplementationConfiguration(t, root, request)
	if !plan.Changed() || !bytes.Equal(plan.Original(), plan.Updated()) {
		t.Fatal("identical-byte configuration set lost write intent")
	}
	first := runConfigurationPlan(t, root, request, true)
	second := runConfigurationPlan(t, root, bytes.ReplaceAll(request, []byte("PRIVATE_SECRET"), []byte("PRIVATE_OTHER")), true)
	if !reflect.DeepEqual(first.Payload, second.Payload) {
		t.Fatal("joint preview exposed unchanged Secret-reference equality")
	}
	for _, field := range []string{"value", "public"} {
		first := runConfigurationPlan(t, root, configurationBatchRequest(t, base, configurationPlanRequest(t, configurationImplementationOwner, field, "set", json.RawMessage(`"PRIVATE_IMPLEMENTATION"`))), true)
		second := runConfigurationPlan(t, root, configurationBatchRequest(t, base, configurationPlanRequest(t, configurationImplementationOwner, field, "set", json.RawMessage(`"PRIVATE_SECOND"`))), true)
		if first.Payload.RequestDigest != second.Payload.RequestDigest || (first.Payload.PlanDigest == second.Payload.PlanDigest) != (field == "value") {
			t.Fatal("joint preview confused private and build-visible final identity")
		}
	}
}

func TestRunChangePlanInterfaceBatchFinalBoundsAndMaximumRequests(t *testing.T) {
	root, document := writeConfigurationPlanProject(t)
	padding := applicationmeta.MaximumSize - len(document) + len("PRIVATE_IMPLEMENTATION") - 32
	document = strings.Replace(document, "value: PRIVATE_IMPLEMENTATION,", "value: "+strings.Repeat("x", padding)+",", 1)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	requests := [][]byte{rootConfigurationRequest(t, "expose", "present"), implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner), configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`))}
	for index := range changerequest.MaximumOperations - len(requests) {
		requests = append(requests, bytes.ReplaceAll(rootConfigurationRequest(t, "require", "absent"), []byte("email.send/v1"), []byte(fmt.Sprintf("unused.n%04d/v1", index))))
	}
	request := configurationBatchRequest(t, requests...)
	first := runConfigurationPlan(t, root, request, true)
	second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true)
	requested, derived := 0, 0
	for _, operation := range first.Payload.Operations {
		if operation.Derived {
			derived++
		} else {
			requested++
		}
	}
	if !reflect.DeepEqual(first.Payload, second.Payload) || requested != changerequest.MaximumOperations || derived == 0 {
		t.Fatal("joint preview lost maximum requests, separate cleanup, or order independence")
	}
}

func TestBuiltCLIChangePlanInterfaceBatchFromStdin(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "plystra.exe")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/plystra")
	build.Dir, build.Env = commandRepositoryRoot(t), commandGoEnvironmentWith(nil)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	root, _ := writeConfigurationPlanProject(t)
	before := commandTree(t, root)
	command := exec.CommandContext(t.Context(), binary, "change", "plan", "--request", "-", "--format", "json", "--env", "production")
	command.Dir, command.Env = filepath.Join(root, "original"), implementationSelectionCommandEnvironment(nil)
	command.Stdin = bytes.NewReader(configurationBatchRequest(t, rootConfigurationRequest(t, "expose", "present"), implementationConfigurationSelectionRequest(t, "set", compoundImplementationOwner), configurationPlanRequest(t, compoundImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_NEW"`))))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	var result configurationChangePlanResult
	if decodeErr := json.Unmarshal(output, &result); err != nil || decodeErr != nil || stderr.Len() != 0 || result.Status != "success" || bytes.Contains(output, []byte("PRIVATE_")) || !reflect.DeepEqual(before, commandTree(t, root)) {
		t.Fatalf("compiled joint preview: %v\n%s\n%s", err, output, &stderr)
	}
}

func writeSharedInterfaceBatchProject(t *testing.T) (string, string) {
	t.Helper()
	root, document := writeConfigurationPlanProject(t)
	writeCommandInterface(t, root, "email/copy/v1", "copy", "email.copy/v1", "Copy")
	path := filepath.Join(root, "smtp/implementation.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(string(data), "//plystra:implements email.send/v1", "//plystra:implements email.send/v1\n//plystra:implements email.copy/v1", 1)
	source = strings.Replace(source, `contract "example.com/acme/implementation-use/interfaces/email/send/v1"`, "copyapi \"example.com/acme/implementation-use/interfaces/email/copy/v1\"\n contract \"example.com/acme/implementation-use/interfaces/email/send/v1\"", 1)
	writeCommandFile(t, path, source+"\nfunc (*Service) Copy(context.Context,copyapi.Request)(copyapi.Response,error){return copyapi.Response{},nil}\n")
	return root, document
}
