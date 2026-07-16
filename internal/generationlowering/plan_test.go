package generationlowering_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/generationresolution"
)

var (
	_ generationlowering.ContributionView = generation.NormalizedContribution{}
	_ generationlowering.ContributionView = generationresolution.ResolvedContribution{}
)

func TestLowerBuildsImmutableRenderReadyPlan(t *testing.T) {
	t.Parallel()

	context := loweringContext(t)
	order := mustCapabilityID(t, "order.create/v1")
	authz := mustCapabilityID(t, "authz.check/v1")
	audit := mustCapabilityID(t, "audit.write/v1")
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: []generation.Contribution{
		{
			ID:        "authz.authorize",
			Namespace: "authz",
			Source:    order,
			Point:     generation.GenerationPointInvocationPrepare,
			Nodes: []generation.GeneratedNode{
				{
					ID: "derive-space",
					ContextDerivation: &generation.GeneratedContextDerivation{
						Key:          "authz.space-id",
						Value:        invocationValue(generation.GeneratedInvocationRequestField, "space_id"),
						Type:         generation.GeneratedValueString,
						Presence:     generation.GeneratedContextRequired,
						MaximumBytes: 128,
					},
				},
				{
					ID: "check-permission",
					CapabilityCall: &generation.GeneratedCapabilityCall{
						Capability: authz,
						Request: []generation.GeneratedFieldBinding{
							{Field: "permission", Value: generation.StringValue("order.create")},
							{Field: "space_id", Value: nodeValue("derive-space", generation.GeneratedNodeDerived, "")},
						},
						TimeoutMilliseconds: 1000,
						OnError:             generation.GeneratedCallFailClosed,
					},
				},
				{
					ID: "reject-denial",
					ConditionalFailure: &generation.GeneratedConditionalFailure{
						Condition: generation.GeneratedCondition{
							Operator: generation.GeneratedConditionFalse,
							Value:    nodeValue("check-permission", generation.GeneratedNodeResponse, "allowed"),
						},
						ErrorCode: "forbidden",
						Message:   "Permission denied.",
					},
				},
			},
		},
		{
			ID:        "audit.record",
			Namespace: "audit",
			Source:    order,
			Point:     generation.GenerationPointInvocationComplete,
			Nodes: []generation.GeneratedNode{
				{
					ID: "reject-dispatch-error",
					ConditionalFailure: &generation.GeneratedConditionalFailure{
						Condition: generation.GeneratedCondition{
							Operator: generation.GeneratedConditionError,
							Value:    invocationValue(generation.GeneratedInvocationError, ""),
						},
						ErrorCode: "dispatch_failed",
						Message:   "Order creation failed.",
					},
				},
				{
					ID: "attach-order-id",
					MetadataAttachment: &generation.GeneratedMetadataAttachment{
						Key:          "audit.order-id",
						Value:        invocationValue(generation.GeneratedInvocationResponseField, "order_id"),
						MaximumBytes: 128,
					},
				},
				{
					ID: "write-event",
					AuditEventCall: &generation.GeneratedAuditEventCall{
						Event:      "order.created",
						Capability: audit,
						Request: []generation.GeneratedFieldBinding{
							{Field: "event", Value: generation.StringValue("order.created")},
							{Field: "order_id", Value: invocationValue(generation.GeneratedInvocationResponseField, "order_id")},
						},
						TimeoutMilliseconds: 500,
						OnError:             generation.GeneratedCallContinue,
					},
				},
			},
		},
	}})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	byID := normalizedContributionsByID(output.Contributions())
	inputs := []pluginContribution{
		{NormalizedContribution: byID["authz.authorize"], pluginID: "plystra.authz.default"},
		{NormalizedContribution: byID["audit.record"], pluginID: "acme.audit"},
	}
	plan, err := generationlowering.Lower("example.com/acme/app/v2", inputs)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if plan.ModulePath() != "example.com/acme/app/v2" {
		t.Fatalf("ModulePath = %q", plan.ModulePath())
	}
	imports := plan.Scope().Imports()
	if got := importStrings(imports); !slices.Equal(got, []string{
		"auditwritev1=example.com/acme/app/v2/generated/go/clients/audit/write/v1",
		"authzcheckv1=example.com/acme/app/v2/generated/go/clients/authz/check/v1",
	}) {
		t.Fatalf("imports = %v", got)
	}
	contributions := plan.Contributions()
	if len(contributions) != 2 || contributions[0].ID() != "authz.authorize" || contributions[1].ID() != "audit.record" {
		t.Fatalf("contribution order = %#v", contributions)
	}
	if contributions[0].PluginID() != "plystra.authz.default" || contributions[1].PluginID() != "acme.audit" {
		t.Fatalf("plugin provenance = %q, %q", contributions[0].PluginID(), contributions[1].PluginID())
	}
	authzNodes := contributions[0].Nodes()
	if len(authzNodes) != 3 || authzNodes[0].ID() != "derive-space" || authzNodes[1].ID() != "check-permission" || authzNodes[2].ID() != "reject-denial" {
		t.Fatalf("authz nodes = %#v", authzNodes)
	}
	if got := nodeIdentifierStrings(authzNodes); !slices.Equal(got, []string{
		"derive-space:error=plystraAuthzAuthorizeDeriveSpaceError",
		"derive-space:derived-value=plystraAuthzAuthorizeDeriveSpaceDerived",
		"check-permission:response=plystraAuthzAuthorizeCheckPermissionResponse",
		"check-permission:error=plystraAuthzAuthorizeCheckPermissionError",
	}) {
		t.Fatalf("authz identifiers = %v", got)
	}
	target, ok := authzNodes[1].Target()
	if !ok || target.Capability() != authz || target.ImportName() != "authzcheckv1" || target.Operation() != "Check" {
		t.Fatalf("authz target = %#v, %v", target, ok)
	}
	auditNodes := contributions[1].Nodes()
	if got := nodeIdentifierStrings(auditNodes); !slices.Equal(got, []string{
		"attach-order-id:error=plystraAuditRecordAttachOrderIDError",
		"write-event:error=plystraAuditRecordWriteEventError",
	}) {
		t.Fatalf("audit identifiers = %v", got)
	}
	auditTarget, ok := auditNodes[2].Target()
	if !ok || auditTarget.Capability() != audit || auditTarget.ImportName() != "auditwritev1" || auditTarget.Operation() != "Write" {
		t.Fatalf("audit target = %#v, %v", auditTarget, ok)
	}

	contributions[0] = generationlowering.Contribution{}
	authzNodes[0] = generationlowering.Node{}
	imports[0] = generationlowering.Import{}
	if fresh := plan.Contributions(); fresh[0].ID() != "authz.authorize" || fresh[0].Nodes()[0].ID() != "derive-space" {
		t.Fatal("Plan exposed mutable contribution storage")
	}
	if fresh := plan.Scope().Imports(); fresh[0].Name() != "auditwritev1" {
		t.Fatal("Plan exposed mutable scope storage")
	}
}

func TestLowerRejectsGeneratedIdentifierCollisionsDeterministically(t *testing.T) {
	t.Parallel()

	context := loweringContext(t)
	order := mustCapabilityID(t, "order.create/v1")
	verify := mustCapabilityID(t, "authn.session.verify/v1")
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: []generation.Contribution{
		capabilityCallContribution("authn.verify-session", "check", "authn", order, verify),
		capabilityCallContribution("authn.verify", "session-check", "authn", order, verify),
	}})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	inputs := output.Contributions()
	var first string
	for iteration := 0; iteration < 2; iteration++ {
		_, err := generationlowering.Lower("example.com/app", inputs)
		if !errors.Is(err, generationlowering.ErrLower) || !errors.Is(err, generationlowering.ErrIdentifierCollision) {
			t.Fatalf("Lower error = %v, want ErrLower and ErrIdentifierCollision", err)
		}
		for _, want := range []string{"plystraAuthnVerifySessionCheckError", "authn.verify-session", "authn.verify", "session-check"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("Lower error %q omits %q", err, want)
			}
		}
		if first == "" {
			first = err.Error()
		} else if err.Error() != first {
			t.Fatalf("collision changed after reversal: %q then %q", first, err)
		}
		slices.Reverse(inputs)
	}
}

func TestLowerRejectsGeneratedClientImportCollisions(t *testing.T) {
	t.Parallel()

	context := loweringContext(t)
	order := mustCapabilityID(t, "order.create/v1")
	createItem := mustCapabilityID(t, "order.create-item/v1")
	createitem := mustCapabilityID(t, "order.createitem/v1")
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: []generation.Contribution{
		capabilityCallContribution("authn.first", "call-first", "authn", order, createItem),
		capabilityCallContribution("authn.second", "call-second", "authn", order, createitem),
	}})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	_, err = generationlowering.Lower("example.com/app", output.Contributions())
	if !errors.Is(err, generationlowering.ErrLower) || !errors.Is(err, generationlowering.ErrIdentifierCollision) {
		t.Fatalf("Lower error = %v, want ErrLower and ErrIdentifierCollision", err)
	}
	for _, want := range []string{"ordercreateitemv1", "order/create-item/v1", "order/createitem/v1", "authn.first", "authn.second"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Lower error %q omits %q", err, want)
		}
	}
}

func TestLowerRejectsInvalidModuleAndDuplicateContribution(t *testing.T) {
	t.Parallel()

	if _, err := generationlowering.Lower("../app", []generation.NormalizedContribution{}); !errors.Is(err, generationlowering.ErrLower) || !strings.Contains(err.Error(), "Go Module path") {
		t.Fatalf("invalid module error = %v", err)
	}

	context := loweringContext(t)
	order := mustCapabilityID(t, "order.create/v1")
	verify := mustCapabilityID(t, "authn.session.verify/v1")
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: []generation.Contribution{
		capabilityCallContribution("authn.verify", "verify", "authn", order, verify),
	}})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	duplicate := []generation.NormalizedContribution{output.Contributions()[0], output.Contributions()[0]}
	if _, err := generationlowering.Lower("example.com/app", duplicate); !errors.Is(err, generationlowering.ErrLower) || !errors.Is(err, generationlowering.ErrInvalidContribution) || !strings.Contains(err.Error(), "repeats ID") {
		t.Fatalf("duplicate contribution error = %v", err)
	}
}

func TestLowerAllowsEmptyContributionPlan(t *testing.T) {
	t.Parallel()

	plan, err := generationlowering.Lower("example.com/app", []generation.NormalizedContribution{})
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if plan.ModulePath() != "example.com/app" || len(plan.Contributions()) != 0 || len(plan.Scope().Imports()) != 0 || len(plan.Scope().Identifiers()) != 0 {
		t.Fatalf("empty plan = %#v", plan)
	}
}

type pluginContribution struct {
	generation.NormalizedContribution
	pluginID string
}

func (c pluginContribution) PluginID() string { return c.pluginID }

func loweringContext(t *testing.T) generation.Context {
	t.Helper()
	capabilities := []generation.CapabilityInput{
		{ContractJSON: json.RawMessage(`{"id":"order.create/v1","request":{"space_id":{"type":"string","required":true}},"response":{"order_id":{"type":"string","required":true}},"errors":["dispatch_failed","forbidden"],"extensions":{"audit":{"event":"order.created"},"authn":{"authenticated":true},"authz":{"permission":"order.create","space":"request.space_id"}}}`)},
		{ContractJSON: json.RawMessage(`{"id":"authn.session.verify/v1","request":{"token":{"type":"string","required":true}},"response":{"verified":{"type":"object","required":true}},"errors":["invalid_credentials"]}`)},
		{ContractJSON: json.RawMessage(`{"id":"authz.check/v1","request":{"permission":{"type":"string","required":true},"space_id":{"type":"string","required":true}},"response":{"allowed":{"type":"boolean","required":true}},"errors":["decision_failed"]}`)},
		{ContractJSON: json.RawMessage(`{"id":"audit.write/v1","request":{"event":{"type":"string","required":true},"order_id":{"type":"string","required":true}},"response":{},"errors":["write_failed"]}`)},
		{ContractJSON: json.RawMessage(`{"id":"order.create-item/v1","request":{},"response":{},"errors":[]}`)},
		{ContractJSON: json.RawMessage(`{"id":"order.createitem/v1","request":{},"response":{},"errors":[]}`)},
	}
	plugins := []generation.PluginInput{
		{ID: "acme.orders", ModulePath: "example.com/app", Provides: []string{"order.create/v1", "order.create-item/v1", "order.createitem/v1"}},
		{ID: "plystra.authn.default", ModulePath: "github.com/plystra/authn", Provides: []string{"authn.session.verify/v1"}},
		{ID: "plystra.authz.default", ModulePath: "github.com/plystra/authz", Provides: []string{"authz.check/v1"}},
		{ID: "acme.audit", ModulePath: "example.com/audit", Provides: []string{"audit.write/v1"}},
	}
	providers := []generation.ProviderInput{
		{Capability: "order.create/v1", Plugin: "acme.orders"},
		{Capability: "order.create-item/v1", Plugin: "acme.orders"},
		{Capability: "order.createitem/v1", Plugin: "acme.orders"},
		{Capability: "authn.session.verify/v1", Plugin: "plystra.authn.default"},
		{Capability: "authz.check/v1", Plugin: "plystra.authz.default"},
		{Capability: "audit.write/v1", Plugin: "acme.audit"},
	}
	context, err := generation.NewContext(generation.Input{
		Plugins:      plugins,
		Capabilities: capabilities,
		Requirements: []string{"order.create/v1", "order.create-item/v1", "order.createitem/v1", "authn.session.verify/v1", "authz.check/v1", "audit.write/v1"},
		Providers:    providers,
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return context
}

func capabilityCallContribution(id, nodeID, namespace string, source, target generation.CapabilityID) generation.Contribution {
	request := []generation.GeneratedFieldBinding{}
	if target.String() == "authn.session.verify/v1" {
		request = append(request, generation.GeneratedFieldBinding{Field: "token", Value: generation.StringValue("token")})
	}
	return generation.Contribution{
		ID:        id,
		Namespace: namespace,
		Source:    source,
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{{
			ID: nodeID,
			CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability:          target,
				Request:             request,
				TimeoutMilliseconds: 100,
				OnError:             generation.GeneratedCallFailClosed,
			},
		}},
	}
}

func normalizedContributionsByID(values []generation.NormalizedContribution) map[string]generation.NormalizedContribution {
	result := make(map[string]generation.NormalizedContribution, len(values))
	for _, value := range values {
		result[value.ID()] = value
	}
	return result
}

func importStrings(values []generationlowering.Import) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.Name() + "=" + value.Path()
	}
	return result
}

func nodeIdentifierStrings(nodes []generationlowering.Node) []string {
	result := make([]string, 0)
	for _, node := range nodes {
		for _, output := range []generation.GeneratedNodeOutput{
			generation.GeneratedNodeResponse,
			generation.GeneratedNodeError,
			generation.GeneratedNodeDerived,
		} {
			if identifier, ok := node.Identifier(output); ok {
				result = append(result, node.ID()+":"+string(output)+"="+identifier)
			}
		}
	}
	return result
}

func invocationValue(source generation.GeneratedInvocationValueSource, name string) generation.GeneratedValue {
	return generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{Source: source, Name: name}}
}

func nodeValue(id string, output generation.GeneratedNodeOutput, field string) generation.GeneratedValue {
	return generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: id, Output: output, Field: field}}
}

func mustCapabilityID(t *testing.T, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%q): %v", value, err)
	}
	return id
}
