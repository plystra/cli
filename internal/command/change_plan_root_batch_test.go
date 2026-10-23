package command_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/changerequest"
)

func TestRunChangePlanRootBatchMovesRootWithoutLosingOwner(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeAutomaticRootConfigurationProject(t)
			path, args := "plystra.yaml", []string(nil)
			if mode == "environment" {
				writeCommandFile(t, filepath.Join(root, path), document)
				path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "# KEEP_OVERLAY\n{}\n"
			} else if mode == "replacement" {
				path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
			}
			writeCommandFile(t, filepath.Join(root, path), document)
			move := configurationBatchRequest(t, rootConfigurationRequest(t, "require", "absent"), rootConfigurationRequest(t, "expose", "present"))
			for _, configured := range []bool{false, true} {
				request := move
				if configured {
					request = configurationBatchRequest(t, move, configurationPlanRequest(t, configurationImplementationOwner, "value", "set", json.RawMessage(`"PRIVATE_RETAINED"`)))
				}
				plan := previewRootConfiguration(t, root, request, args...)
				first := runConfigurationPlan(t, root, request, true, args...)
				second := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
				if !reflect.DeepEqual(first.Payload, second.Payload) || len(first.Payload.WriteSet) != 1 || first.Payload.WriteSet[0].Path != path || string(first.Payload.ConfigurationImpact["newly_unowned_paths"]) != "[]" || !bytes.Contains(first.Payload.ConfigurationImpact["retained_owners"], []byte(configurationImplementationOwner)) {
					t.Fatal("root move lost final ownership, selected scope, or order independence")
				}
				if mode != "environment" && !bytes.Contains(plan.Updated(), []byte("PRIVATE_IMPLEMENTATION_REQUIRED")) || mode == "environment" && !bytes.Contains(plan.Updated(), []byte("# KEEP_OVERLAY")) {
					t.Fatal("root move cleaned retained Config or lost selected comments")
				}
			}
		})
	}
}

func TestRunChangePlanRootBatchCleansOnlyFinalLostOwners(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root, document := writeAutomaticRootConfigurationProject(t)
			document += "http: {expose: {email.send/v1: {transport: connect}}}\n"
			document = strings.Replace(document, "resources:\n", "resources:\n  bind: {implementations: {"+configurationImplementationOwner+": {Obsolete: database.primary}}}\n", 1)
			path, args := "plystra.yaml", []string(nil)
			if mode == "environment" {
				writeCommandFile(t, filepath.Join(root, path), document)
				path, args, document = "plystra.production.yaml", []string{"--env", "production"}, "config: {"+configurationImplementationOwner+": {value: [PRIVATE_INVALID]}}\n"
			} else {
				document = strings.Replace(document, "value: PRIVATE_IMPLEMENTATION", "value: [PRIVATE_INVALID]", 1)
				if mode == "replacement" {
					path, args = "deploy/customer.yaml", []string{"--config", "deploy/customer.yaml"}
				}
			}
			writeCommandFile(t, filepath.Join(root, path), document)
			request := configurationBatchRequest(t, rootConfigurationRequest(t, "require", "absent"), rootConfigurationRequest(t, "expose", "absent"), configurationPlanRequest(t, "database.primary", "value", "set", json.RawMessage(`"PRIVATE_RETAINED"`)))
			result := runConfigurationPlan(t, root, request, true, args...)
			if !bytes.Contains(result.Payload.ConfigurationImpact["newly_unowned_paths"], []byte(configurationImplementationOwner)) || !bytes.Contains(result.Payload.ConfigurationImpact["derived_operations"], []byte("Obsolete")) {
				t.Fatal("final root removal omitted proven Config or binding cleanup")
			}
			reversed := runConfigurationPlan(t, root, reverseConfigurationBatchRequest(t, request), true, args...)
			if !reflect.DeepEqual(result.Payload, reversed.Payload) {
				t.Fatal("root removal cleanup depends on request order")
			}
			for _, action := range []string{"set", "remove"} {
				var value json.RawMessage
				if action == "set" {
					value = json.RawMessage(`"PRIVATE_LOST"`)
				}
				runConfigurationPlan(t, root, configurationBatchRequest(t, request, configurationPlanRequest(t, configurationImplementationOwner, "value", action, value)), false, args...)
			}
		})
	}
}

func TestRunChangePlanRootBatchRepairsJointlyRequiredOwners(t *testing.T) {
	root, document := writeAutomaticRootConfigurationProject(t)
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
	document = document[:strings.Index(document, "\nconfig:")] + "\nconfig: {}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	reports := bytes.ReplaceAll(rootConfigurationRequest(t, "require", "present"), []byte("email.send/v1"), []byte("reports.read/v1"))
	request := configurationBatchRequest(t, reports, rootConfigurationRequest(t, "require", "present"), configurationPlanRequest(t, configurationImplementationOwner, "endpoint", "set", json.RawMessage(`"PRIVATE_EMAIL"`)))
	runConfigurationPlan(t, root, request, false)
	owner := "example.com/acme/implementation-use/reports.New"
	request = configurationBatchRequest(t, request, configurationPlanRequest(t, owner, "endpoint", "set", json.RawMessage(`"PRIVATE_REPORTS"`)))
	result := runConfigurationPlan(t, root, request, true)
	for _, required := range []string{owner, configurationImplementationOwner} {
		if !bytes.Contains(result.Payload.ConfigurationImpact["newly_required_fields"], []byte(required)) {
			t.Fatal("a final newly required owner was omitted")
		}
	}
	invalid := bytes.ReplaceAll(reports, []byte("reports.read/v1"), []byte("unknown.run/v1"))
	runConfigurationPlan(t, root, configurationBatchRequest(t, request, invalid), false)
}

func TestRunChangePlanRootBatchChecksSatisfiedRequestsAndMaximumCleanup(t *testing.T) {
	root, document := writeAutomaticRootConfigurationProject(t)
	document += "http: {expose: {email.send/v1: {transport: connect}}}\n"
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	satisfied := configurationBatchRequest(t, rootConfigurationRequest(t, "require", "present"), rootConfigurationRequest(t, "expose", "present"))
	result := runConfigurationPlan(t, root, satisfied, true, "--env", "production")
	if len(result.Payload.WriteSet) != 0 || len(result.Effects.Planned) != 0 {
		t.Fatal("inherited satisfied roots materialized selected intent")
	}
	writeCommandFile(t, filepath.Join(root, "plystra.production.yaml"), "resources: {instances: {database.retained: {config: {endpoint: [PRIVATE_INVALID]}}}}\n")
	runConfigurationPlan(t, root, satisfied, false, "--env", "production")
	requests := [][]byte{rootConfigurationRequest(t, "require", "absent"), rootConfigurationRequest(t, "expose", "absent")}
	for index := range changerequest.MaximumOperations - 2 {
		request := bytes.ReplaceAll(rootConfigurationRequest(t, "require", "absent"), []byte("email.send/v1"), []byte(fmt.Sprintf("unused.n%04d/v1", index)))
		requests = append(requests, request)
	}
	result = runConfigurationPlan(t, root, configurationBatchRequest(t, requests...), true)
	requested, derived := 0, 0
	for _, operation := range result.Payload.Operations {
		if operation.Derived {
			derived++
		} else {
			requested++
		}
	}
	if requested != changerequest.MaximumOperations || derived == 0 {
		t.Fatal("root batch cleanup consumed the independent requested-operation allowance")
	}
}

func TestRunChangePlanRootBatchPreservesPrivateAndBuildVisibleIdentity(t *testing.T) {
	root, document := writeAutomaticRootConfigurationProject(t)
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), document)
	roots := configurationBatchRequest(t, rootConfigurationRequest(t, "require", "absent"), rootConfigurationRequest(t, "expose", "present"))
	for _, field := range []string{"value", "password", "public"} {
		values := []string{"PRIVATE_PRIMARY", "PRIVATE_DIFFERENT"}
		if field == "password" {
			values[0] = "PRIVATE_SECRET"
		} else if field == "public" {
			values = []string{"PUBLIC_A", "PUBLIC_B"}
		}
		var first configurationChangePlanResult
		for index, private := range values {
			value := json.RawMessage(fmt.Sprintf("%q", private))
			if field == "password" {
				value = json.RawMessage(fmt.Sprintf(`{"env":%q}`, private))
			}
			result := runConfigurationPlan(t, root, configurationBatchRequest(t, roots, configurationPlanRequest(t, "database.primary", field, "set", value)), true)
			if index == 0 {
				first = result
			} else if field == "public" {
				if first.Payload.PlanDigest == result.Payload.PlanDigest || first.Payload.RequestDigest != result.Payload.RequestDigest {
					t.Fatal("root batch lost build-visible final identity")
				}
			} else if !reflect.DeepEqual(first.Payload, result.Payload) {
				t.Fatal("root batch exposed private value or unchanged-Secret-reference equality")
			}
		}
	}
	first := runConfigurationPlan(t, root, roots, true)
	later := bytes.ReplaceAll(rootConfigurationRequest(t, "require", "absent"), []byte("email.send/v1"), []byte("unused.run/v1"))
	second := runConfigurationPlan(t, root, configurationBatchRequest(t, roots, later), true)
	if first.Payload.RequestDigest == second.Payload.RequestDigest || first.Payload.PlanDigest == second.Payload.PlanDigest {
		t.Fatal("a later satisfied root operation was not digest-bound")
	}
}
