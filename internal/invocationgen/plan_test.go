package invocationgen_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/clientgen"
	"github.com/plystra/cli/internal/contractgen"
	"github.com/plystra/cli/internal/generationlowering"
	"github.com/plystra/cli/internal/invocationgen"
)

const (
	orderCreateSchema = `id: order.create/v1
request:
  order_id: {type: string, required: true}
  note: {type: string}
  reject: {type: boolean, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [policy_failed]
extensions:
  policy:
    permission: order.create
`
	auditOrderSchema = `id: order.create/v1
request:
  order_id: {type: string, required: true}
  note: {type: string}
  reject: {type: boolean, required: true}
response:
  accepted: {type: boolean, required: true}
errors: [policy_failed]
extensions:
  audit:
    event: order.create
`
	policyCheckSchema = `id: policy.check/v1
request:
  permission: {type: string, required: true}
  retry_count: {type: integer, required: true}
  enforce: {type: boolean, required: true}
  credential: {type: string}
  evidence: {type: object}
response:
  allowed: {type: boolean, required: true}
  reason: {type: string}
  details: {type: object}
errors: [unavailable]
`
	auditWriteSchema = `id: audit.write/v1
request:
  event: {type: string, required: true}
  note: {type: string}
  order_id: {type: string, required: true}
  rejected: {type: boolean, required: true}
response:
  recorded: {type: boolean, required: true}
errors: [write_failed]
`
	completionOrderSchema = `id: order.create/v1
request:
  order_id: {type: string, required: true}
  reject: {type: boolean, required: true}
response:
  accepted: {type: boolean, required: true}
  order_id: {type: string, required: true}
errors: [policy_failed]
extensions:
  audit:
    event: order.create
`
	typedOrderSchema = `id: order.typed-create/v1
request:
  name: {type: string, required: true}
  note: {type: string}
  attempts: {type: integer, required: true}
  active: {type: boolean, required: true}
  labels: {type: array, items: string, required: true}
  attributes: {type: object, required: true}
  mode: {type: string, enum: [fast, safe], required: true}
response:
  accepted: {type: boolean, required: true}
errors: [policy_failed]
extensions:
  policy:
    permission: order.typed-create
`
	typedPolicySchema = `id: policy.typed-check/v1
request:
  name: {type: string, required: true}
  optional_name: {type: string}
  optional_note: {type: string}
  ratio: {type: number, required: true}
  active: {type: boolean, required: true}
  labels: {type: array, items: string, required: true}
  attributes: {type: object, required: true}
  mode: {type: string, enum: [fast, safe], required: true}
  optional_literal: {type: boolean}
response:
  allowed: {type: boolean, required: true}
errors: [unavailable]
`
	contextOrderSchema = `id: order.context-create/v1
request:
  space_id: {type: string, required: true}
  note: {type: string}
response:
  accepted: {type: boolean, required: true}
errors: [policy_failed]
extensions:
  policy:
    permission: order.context-create
`
)

func TestRenderPlanGoldenAndRuntimeOrder(t *testing.T) {
	t.Parallel()

	plan := prepareCallPlan(t, generation.Contribution{
		ID:        "policy.require-create",
		Namespace: "policy",
		Source:    planCapabilityID(t, "order.create/v1"),
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{{
			ID: "check",
			CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability: planCapabilityID(t, "policy.check/v1"),
				Request: []generation.GeneratedFieldBinding{
					{Field: "permission", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "order_id")},
					{Field: "retry_count", Value: generation.IntegerValue(2)},
					{Field: "enforce", Value: generation.BooleanValue(true)},
				},
				TimeoutMilliseconds: 75,
				OnError:             generation.GeneratedCallFailClosed,
			},
		}},
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	want, err := os.ReadFile("testdata/order.create.prepare.go")
	if err != nil {
		t.Fatalf("ReadFile(golden): %v", err)
	}
	if file.Path() != "generated/go/invocation/order/create/v1/invocation_gen.go" || file.PackageName() != "ordercreatev1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated plan = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	dependencies := file.Dependencies()
	if !slices.Equal(dependencies, []string{"policy.check/v1"}) {
		t.Fatalf("Dependencies = %q", dependencies)
	}
	dependencies[0] = "changed.invalid/v1"
	if !slices.Equal(file.Dependencies(), []string{"policy.check/v1"}) {
		t.Fatalf("Dependencies returned mutable state: %q", file.Dependencies())
	}
	assertGeneratedPrepareInvocationRuns(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil || repeated.Path() != file.Path() || repeated.PackageName() != file.PackageName() || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanPreservesLoweredSemanticOrder(t *testing.T) {
	t.Parallel()

	call := func(id, permission string) generation.Contribution {
		return generation.Contribution{
			ID: id, Namespace: "policy", Source: planCapabilityID(t, "order.create/v1"), Point: generation.GenerationPointInvocationPrepare,
			Nodes: []generation.GeneratedNode{{ID: "check", CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability: planCapabilityID(t, "policy.check/v1"),
				Request: []generation.GeneratedFieldBinding{
					{Field: "permission", Value: generation.StringValue(permission)},
					{Field: "retry_count", Value: generation.IntegerValue(1)},
					{Field: "enforce", Value: generation.BooleanValue(true)},
				},
				TimeoutMilliseconds: 50, OnError: generation.GeneratedCallFailClosed,
			}}},
		}
	}
	plan := prepareOrderedCallPlan(t, []generation.Contribution{
		call("policy.z-first", "first"),
		call("policy.a-second", "second"),
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	source := string(file.Data())
	first := strings.Index(source, "plystraPolicyZFirstCheckResponse")
	second := strings.Index(source, "plystraPolicyASecondCheckResponse")
	dispatch := strings.Index(source, "response, invocationError := h.target.Invoke(ctx, request)")
	if first < 0 || second <= first || dispatch <= second {
		t.Fatalf("semantic order was not preserved: first=%d second=%d dispatch=%d\n%s", first, second, dispatch, source)
	}
	if strings.Count(source, `"example.com/acme/project/generated/go/clients/policy/check/v1"`) != 1 || strings.Count(source, "policycheckv1Client policycheckv1.Client") != 2 {
		t.Fatalf("shared dependency was not deterministically deduplicated:\n%s", source)
	}
}

func TestRenderPlanSupportsEveryTypedRequestFieldShape(t *testing.T) {
	t.Parallel()

	sourceID := planCapabilityID(t, "order.typed-create/v1")
	targetID := planCapabilityID(t, "policy.typed-check/v1")
	plan := prepareCallPlanForSchemas(t, typedOrderSchema, typedPolicySchema, sourceID, targetID, generation.Contribution{
		ID:        "policy.typed",
		Namespace: "policy",
		Source:    sourceID,
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{{
			ID: "check",
			CapabilityCall: &generation.GeneratedCapabilityCall{
				Capability: targetID,
				Request: []generation.GeneratedFieldBinding{
					{Field: "name", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "name")},
					{Field: "optional_name", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "name")},
					{Field: "optional_note", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "note")},
					{Field: "ratio", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "attempts")},
					{Field: "active", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "active")},
					{Field: "labels", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "labels")},
					{Field: "attributes", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "attributes")},
					{Field: "mode", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "mode")},
					{Field: "optional_literal", Value: generation.BooleanValue(true)},
				},
				TimeoutMilliseconds: 80,
				OnError:             generation.GeneratedCallFailClosed,
			},
		}},
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(typedOrderSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	source := string(file.Data())
	for _, want := range []string{
		"Name:            string(request.Name)",
		"OptionalName:    plystraPointer(string(request.Name))",
		"OptionalNote:    plystraConvertOptional(request.Note",
		"Ratio:           float64(request.Attempts)",
		"Active:          bool(request.Active)",
		"Labels:          []string(request.Labels)",
		"Attributes:      map[string]any(request.Attributes)",
		"Mode:            policytypedcheckv1contract.RequestMode(request.Mode)",
		"OptionalLiteral: plystraPointer(bool(true))",
	} {
		if !strings.Contains(source, want) {
			t.Fatalf("generated typed binding source omits %q:\n%s", want, source)
		}
	}
	assertGeneratedTypedRequestBindingsRun(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(typedOrderSchema), plan)
	if err != nil || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanDerivesAndReusesBoundedContext(t *testing.T) {
	t.Parallel()

	sourceID := planCapabilityID(t, "order.context-create/v1")
	plan := prepareSourcePlan(t, contextOrderSchema, []generation.Contribution{{
		ID:        "policy.context",
		Namespace: "policy",
		Source:    sourceID,
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{
			{
				ID: "derive-space",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.space-id",
					Value:        planInvocationValue(generation.GeneratedInvocationRequestField, "space_id"),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 16,
				},
			},
			{
				ID: "derive-note",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.note",
					Value:        planInvocationValue(generation.GeneratedInvocationRequestField, "note"),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextOptional,
					MaximumBytes: 32,
				},
			},
			{
				ID: "derive-mode",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.mode",
					Value:        generation.StringValue("strict"),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 16,
				},
			},
			{
				ID: "derive-optional-literal",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.optional-literal",
					Value:        generation.StringValue("present"),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextOptional,
					MaximumBytes: 16,
				},
			},
			{
				ID: "reuse-caller",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key: "policy.caller-copy",
					Value: generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
						Source: generation.GeneratedInvocationContextValue,
						Name:   "policy.caller",
						Type:   generation.GeneratedValueString,
					}},
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 32,
				},
			},
			{
				ID: "reuse-optional",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key: "policy.optional-copy",
					Value: generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
						Source: generation.GeneratedInvocationContextValue,
						Name:   "policy.optional",
						Type:   generation.GeneratedValueString,
					}},
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextOptional,
					MaximumBytes: 32,
				},
			},
		},
	}})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(contextOrderSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	want, err := os.ReadFile("testdata/order.context-create.context.go")
	if err != nil {
		t.Fatalf("ReadFile(context golden): %v\n%s", err, file.Data())
	}
	if file.Path() != "generated/go/invocation/order/context-create/v1/invocation_gen.go" || file.PackageName() != "ordercontextcreatev1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated context plan = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	assertGeneratedContextDerivationRuns(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(contextOrderSchema), plan)
	if err != nil || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanGoldenAndRuntimeMetadataAttachment(t *testing.T) {
	t.Parallel()

	plan := prepareSourcePlan(t, orderCreateSchema, []generation.Contribution{{
		ID:        "policy.metadata",
		Namespace: "policy",
		Source:    planCapabilityID(t, "order.create/v1"),
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{
			{
				ID: "derive-code",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.code",
					Value:        planInvocationValue(generation.GeneratedInvocationRequestField, "order_id"),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 32,
				},
			},
			{
				ID: "attach-context-copy",
				MetadataAttachment: &generation.GeneratedMetadataAttachment{
					Key: "policy.code",
					Value: generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
						Source: generation.GeneratedInvocationContextValue,
						Name:   "policy.code",
						Type:   generation.GeneratedValueString,
					}},
					MaximumBytes: 16,
				},
			},
			{
				ID: "attach-request",
				MetadataAttachment: &generation.GeneratedMetadataAttachment{
					Key:          "policy.order-id",
					Value:        planInvocationValue(generation.GeneratedInvocationRequestField, "order_id"),
					MaximumBytes: 16,
				},
			},
			{
				ID: "attach-derived",
				MetadataAttachment: &generation.GeneratedMetadataAttachment{
					Key:          "policy.derived",
					Value:        planNodeValue("derive-code", generation.GeneratedNodeDerived),
					MaximumBytes: 16,
				},
			},
			{
				ID: "attach-rejected",
				MetadataAttachment: &generation.GeneratedMetadataAttachment{
					Key:          "policy.rejected",
					Value:        planInvocationValue(generation.GeneratedInvocationRequestField, "reject"),
					MaximumBytes: 5,
				},
			},
			{
				ID: "attach-literal",
				MetadataAttachment: &generation.GeneratedMetadataAttachment{
					Key:          "policy.literal",
					Value:        generation.BooleanValue(false),
					MaximumBytes: 5,
				},
			},
		},
	}})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	want, err := os.ReadFile("testdata/order.create.metadata.go")
	if err != nil {
		t.Fatalf("ReadFile(metadata golden): %v\n%s", err, file.Data())
	}
	if file.Path() != "generated/go/invocation/order/create/v1/invocation_gen.go" || file.PackageName() != "ordercreatev1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated metadata plan = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	assertGeneratedMetadataAttachmentRuns(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanGoldenAndRuntimeAuditEvents(t *testing.T) {
	t.Parallel()

	sourceID := planCapabilityID(t, "order.create/v1")
	auditID := planCapabilityID(t, "audit.write/v1")
	plan := prepareCallPlanForSchemas(t, auditOrderSchema, auditWriteSchema, sourceID, auditID, generation.Contribution{
		ID:        "audit.events",
		Namespace: "audit",
		Source:    sourceID,
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{
			{
				ID: "derive-order",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "audit.order-id",
					Value:        planInvocationValue(generation.GeneratedInvocationRequestField, "order_id"),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 32,
				},
			},
			{
				ID: "observe",
				AuditEventCall: &generation.GeneratedAuditEventCall{
					Event:      "order-observed",
					Capability: auditID,
					Request: []generation.GeneratedFieldBinding{
						{Field: "event", Value: generation.StringValue("order.observed")},
						{Field: "note", Value: generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
							Source: generation.GeneratedInvocationContextValue,
							Name:   "audit.note",
							Type:   generation.GeneratedValueString,
						}}},
						{Field: "order_id", Value: generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
							Source: generation.GeneratedInvocationContextValue,
							Name:   "audit.order-id",
							Type:   generation.GeneratedValueString,
						}}},
						{Field: "rejected", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "reject")},
					},
					TimeoutMilliseconds: 50,
					OnError:             generation.GeneratedCallContinue,
				},
			},
			{
				ID: "record-required",
				AuditEventCall: &generation.GeneratedAuditEventCall{
					Event:      "order-required",
					Capability: auditID,
					Request: []generation.GeneratedFieldBinding{
						{Field: "event", Value: generation.StringValue("order.required")},
						{Field: "order_id", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "order_id")},
						{Field: "rejected", Value: generation.BooleanValue(false)},
					},
					TimeoutMilliseconds: 50,
					OnError:             generation.GeneratedCallFailClosed,
				},
			},
		},
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(auditOrderSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	want, err := os.ReadFile("testdata/order.create.audit.go")
	if err != nil {
		t.Fatalf("ReadFile(audit golden): %v\n%s", err, file.Data())
	}
	if file.Path() != "generated/go/invocation/order/create/v1/invocation_gen.go" || file.PackageName() != "ordercreatev1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated audit plan = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	assertGeneratedAuditEventsRun(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanGoldenAndRuntimeCompletion(t *testing.T) {
	t.Parallel()

	sourceID := planCapabilityID(t, "order.create/v1")
	auditID := planCapabilityID(t, "audit.write/v1")
	plan := prepareOrderedCallPlanForSchemas(t, completionOrderSchema, auditWriteSchema, sourceID, auditID, []generation.Contribution{
		{
			ID:        "audit.preparation",
			Namespace: "audit",
			Source:    sourceID,
			Point:     generation.GenerationPointInvocationPrepare,
			Nodes: []generation.GeneratedNode{{
				ID: "derive-request-order",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "audit.request-order",
					Value:        planInvocationValue(generation.GeneratedInvocationRequestField, "order_id"),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 32,
				},
			}},
		},
		{
			ID:        "audit.completion",
			Namespace: "audit",
			Source:    sourceID,
			Point:     generation.GenerationPointInvocationComplete,
			Nodes: []generation.GeneratedNode{
				{
					ID: "handle-dispatch-error",
					ConditionalFailure: &generation.GeneratedConditionalFailure{
						Condition: generation.GeneratedCondition{
							Operator: generation.GeneratedConditionError,
							Value: generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
								Source: generation.GeneratedInvocationError,
							}},
						},
						ErrorCode: "policy_failed",
						Message:   "Canonical dispatch failed.",
					},
				},
				{
					ID: "derive-order",
					ContextDerivation: &generation.GeneratedContextDerivation{
						Key:          "audit.completed-order",
						Value:        planInvocationValue(generation.GeneratedInvocationResponseField, "order_id"),
						Type:         generation.GeneratedValueString,
						Presence:     generation.GeneratedContextRequired,
						MaximumBytes: 32,
					},
				},
				{
					ID: "attach-accepted",
					MetadataAttachment: &generation.GeneratedMetadataAttachment{
						Key:          "audit.accepted",
						Value:        planInvocationValue(generation.GeneratedInvocationResponseField, "accepted"),
						MaximumBytes: 5,
					},
				},
				{
					ID: "record",
					AuditEventCall: &generation.GeneratedAuditEventCall{
						Event:      "order-completed",
						Capability: auditID,
						Request: []generation.GeneratedFieldBinding{
							{Field: "event", Value: generation.StringValue("order.completed")},
							{Field: "order_id", Value: planInvocationValue(generation.GeneratedInvocationResponseField, "order_id")},
							{Field: "rejected", Value: planInvocationValue(generation.GeneratedInvocationRequestField, "reject")},
						},
						TimeoutMilliseconds: 50,
						OnError:             generation.GeneratedCallContinue,
					},
				},
			},
		},
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(completionOrderSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	want, err := os.ReadFile("testdata/order.create.completion.go")
	if err != nil {
		t.Fatalf("ReadFile(completion golden): %v\n%s", err, file.Data())
	}
	if file.Path() != "generated/go/invocation/order/create/v1/invocation_gen.go" || file.PackageName() != "ordercreatev1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated completion plan = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	assertGeneratedCompletionRuns(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(completionOrderSchema), plan)
	if err != nil || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanGoldenAndRuntimeConditionalFailures(t *testing.T) {
	t.Parallel()

	plan := prepareCallPlan(t, generation.Contribution{
		ID:        "policy.conditions",
		Namespace: "policy",
		Source:    planCapabilityID(t, "order.create/v1"),
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{
			{
				ID: "check",
				CapabilityCall: &generation.GeneratedCapabilityCall{
					Capability:          planCapabilityID(t, "policy.check/v1"),
					Request:             policyLiteralBindings(),
					TimeoutMilliseconds: 50,
					OnError:             generation.GeneratedCallCapture,
				},
			},
			{
				ID: "provider-failed",
				ConditionalFailure: &generation.GeneratedConditionalFailure{
					Condition: generation.GeneratedCondition{Operator: generation.GeneratedConditionError, Value: planNodeValue("check", generation.GeneratedNodeError)},
					ErrorCode: "policy_failed",
					Message:   "Policy provider failed.",
				},
			},
			{
				ID: "denied",
				ConditionalFailure: &generation.GeneratedConditionalFailure{
					Condition: generation.GeneratedCondition{Operator: generation.GeneratedConditionFalse, Value: generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: "check", Output: generation.GeneratedNodeResponse, Field: "allowed"}}},
					ErrorCode: "policy_failed",
					Message:   "Policy denied the request.",
				},
			},
			{
				ID: "note-required",
				ConditionalFailure: &generation.GeneratedConditionalFailure{
					Condition: generation.GeneratedCondition{Operator: generation.GeneratedConditionMissing, Value: planInvocationValue(generation.GeneratedInvocationRequestField, "note")},
					ErrorCode: "policy_failed",
					Message:   "Order note is required.",
				},
			},
			{
				ID: "request-rejected",
				ConditionalFailure: &generation.GeneratedConditionalFailure{
					Condition: generation.GeneratedCondition{Operator: generation.GeneratedConditionTrue, Value: planInvocationValue(generation.GeneratedInvocationRequestField, "reject")},
					ErrorCode: "policy_failed",
					Message:   "Order was explicitly rejected.",
				},
			},
			{
				ID: "context-blocked",
				ConditionalFailure: &generation.GeneratedConditionalFailure{
					Condition: generation.GeneratedCondition{Operator: generation.GeneratedConditionPresent, Value: generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{
						Source: generation.GeneratedInvocationContextValue,
						Name:   "policy.block",
						Type:   generation.GeneratedValueString,
					}}},
					ErrorCode: "policy_failed",
					Message:   "Policy context blocked the request.",
				},
			},
		},
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	want, err := os.ReadFile("testdata/order.create.conditions.go")
	if err != nil {
		t.Fatalf("ReadFile(conditions golden): %v\n%s", err, file.Data())
	}
	if file.Path() != "generated/go/invocation/order/create/v1/invocation_gen.go" || file.PackageName() != "ordercreatev1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated conditions plan = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	assertGeneratedConditionalFailuresRun(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanGoldenAndRuntimeNodeValues(t *testing.T) {
	t.Parallel()

	plan := prepareCallPlan(t, generation.Contribution{
		ID:        "policy.node-values",
		Namespace: "policy",
		Source:    planCapabilityID(t, "order.create/v1"),
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{
			{
				ID: "derive-permission",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.permission",
					Value:        planInvocationValue(generation.GeneratedInvocationRequestField, "order_id"),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 32,
				},
			},
			{
				ID: "check-source",
				CapabilityCall: &generation.GeneratedCapabilityCall{
					Capability: planCapabilityID(t, "policy.check/v1"),
					Request: []generation.GeneratedFieldBinding{
						{Field: "permission", Value: planNodeValue("derive-permission", generation.GeneratedNodeDerived)},
						{Field: "retry_count", Value: generation.IntegerValue(1)},
						{Field: "enforce", Value: generation.BooleanValue(true)},
					},
					TimeoutMilliseconds: 50,
					OnError:             generation.GeneratedCallFailClosed,
				},
			},
			{
				ID: "derive-allowed",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.allowed",
					Value:        generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: "check-source", Output: generation.GeneratedNodeResponse, Field: "allowed"}},
					Type:         generation.GeneratedValueBoolean,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 5,
				},
			},
			{
				ID: "derive-reason",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.reason",
					Value:        generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: "check-source", Output: generation.GeneratedNodeResponse, Field: "reason"}},
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextOptional,
					MaximumBytes: 32,
				},
			},
			{
				ID: "derive-evidence",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "policy.evidence",
					Value:        planNodeValue("check-source", generation.GeneratedNodeResponse),
					Type:         generation.GeneratedValueObject,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 256,
				},
			},
			{
				ID: "check-derived",
				CapabilityCall: &generation.GeneratedCapabilityCall{
					Capability: planCapabilityID(t, "policy.check/v1"),
					Request: []generation.GeneratedFieldBinding{
						{Field: "permission", Value: planNodeValue("derive-permission", generation.GeneratedNodeDerived)},
						{Field: "retry_count", Value: generation.IntegerValue(2)},
						{Field: "enforce", Value: planNodeValue("derive-allowed", generation.GeneratedNodeDerived)},
						{Field: "credential", Value: generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: "check-source", Output: generation.GeneratedNodeResponse, Field: "reason"}}},
						{Field: "evidence", Value: planNodeValue("check-source", generation.GeneratedNodeResponse)},
					},
					TimeoutMilliseconds: 50,
					OnError:             generation.GeneratedCallFailClosed,
				},
			},
		},
	})
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil {
		t.Fatalf("RenderPlan: %v", err)
	}
	want, err := os.ReadFile("testdata/order.create.node-values.go")
	if err != nil {
		t.Fatalf("ReadFile(node-values golden): %v\n%s", err, file.Data())
	}
	if file.Path() != "generated/go/invocation/order/create/v1/invocation_gen.go" || file.PackageName() != "ordercreatev1" || !bytes.Equal(file.Data(), want) {
		t.Fatalf("generated node-values plan = path %q, package %q\n%s\nwant:\n%s", file.Path(), file.PackageName(), file.Data(), want)
	}
	assertGeneratedNodeValuesRun(t, file)

	repeated, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if err != nil || !bytes.Equal(repeated.Data(), file.Data()) {
		t.Fatalf("repeated RenderPlan = %#v, %v", repeated, err)
	}
}

func TestRenderPlanRejectsUnsupportedOrUnsafeCalls(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		contribution generation.Contribution
		want         []string
	}{
		{
			name: "self call",
			contribution: generation.Contribution{
				ID: "policy.self", Namespace: "policy", Source: planCapabilityID(t, "order.create/v1"), Point: generation.GenerationPointInvocationPrepare,
				Nodes: []generation.GeneratedNode{{ID: "check", CapabilityCall: &generation.GeneratedCapabilityCall{
					Capability: planCapabilityID(t, "order.create/v1"),
					Request: []generation.GeneratedFieldBinding{
						{Field: "order_id", Value: generation.StringValue("recursive")},
						{Field: "reject", Value: generation.BooleanValue(false)},
					},
					TimeoutMilliseconds: 50, OnError: generation.GeneratedCallFailClosed,
				}}},
			},
			want: []string{"policy.self", "recursively calls", "order.create/v1"},
		},
		{
			name: "self audit",
			contribution: generation.Contribution{
				ID: "policy.audit-self", Namespace: "policy", Source: planCapabilityID(t, "order.create/v1"), Point: generation.GenerationPointInvocationPrepare,
				Nodes: []generation.GeneratedNode{{ID: "record", AuditEventCall: &generation.GeneratedAuditEventCall{
					Event:      "order-recursive",
					Capability: planCapabilityID(t, "order.create/v1"),
					Request: []generation.GeneratedFieldBinding{
						{Field: "order_id", Value: generation.StringValue("recursive")},
						{Field: "reject", Value: generation.BooleanValue(false)},
					},
					TimeoutMilliseconds: 50, OnError: generation.GeneratedCallFailClosed,
				}}},
			},
			want: []string{"policy.audit-self", "recursively audits", "order.create/v1"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			plan := prepareCallPlan(t, test.contribution)
			file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
			if !errors.Is(err, invocationgen.ErrRender) || !errors.Is(err, invocationgen.ErrContribution) || file.Path() != "" || file.Data() != nil {
				t.Fatalf("RenderPlan = %#v, %v", file, err)
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("RenderPlan error %q omits %q", err, want)
				}
			}
		})
	}
}

func TestRenderPlanRejectsModuleDrift(t *testing.T) {
	t.Parallel()

	plan, err := generationlowering.Lower("example.com/other", []generation.NormalizedContribution{})
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	file, err := invocationgen.RenderPlan(testModulePath, []byte(orderCreateSchema), plan)
	if !errors.Is(err, invocationgen.ErrRender) || !errors.Is(err, invocationgen.ErrContribution) || !strings.Contains(err.Error(), "example.com/other") || file.Data() != nil {
		t.Fatalf("RenderPlan(module drift) = %#v, %v", file, err)
	}
}

func prepareCallPlan(t *testing.T, contribution generation.Contribution) generationlowering.Plan {
	t.Helper()
	return prepareOrderedCallPlan(t, []generation.Contribution{contribution})
}

func prepareOrderedCallPlan(t *testing.T, contributions []generation.Contribution) generationlowering.Plan {
	t.Helper()
	return prepareOrderedCallPlanForSchemas(
		t,
		orderCreateSchema,
		policyCheckSchema,
		planCapabilityID(t, "order.create/v1"),
		planCapabilityID(t, "policy.check/v1"),
		contributions,
	)
}

func prepareCallPlanForSchemas(
	t *testing.T,
	sourceSchema string,
	targetSchema string,
	sourceID generation.CapabilityID,
	targetID generation.CapabilityID,
	contribution generation.Contribution,
) generationlowering.Plan {
	t.Helper()
	return prepareOrderedCallPlanForSchemas(t, sourceSchema, targetSchema, sourceID, targetID, []generation.Contribution{contribution})
}

func prepareOrderedCallPlanForSchemas(
	t *testing.T,
	sourceSchema string,
	targetSchema string,
	sourceID generation.CapabilityID,
	targetID generation.CapabilityID,
	contributions []generation.Contribution,
) generationlowering.Plan {
	t.Helper()
	sourceContract := planCanonicalContract(t, sourceSchema)
	targetContract := planCanonicalContract(t, targetSchema)
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{
			{ID: "example.source", ModulePath: testModulePath, Provides: []string{sourceID.String()}, BuildMetadataJSON: []byte("{}")},
			{ID: "example.target", ModulePath: testModulePath, Provides: []string{targetID.String()}, BuildMetadataJSON: []byte("{}")},
		},
		Capabilities: []generation.CapabilityInput{{ContractJSON: sourceContract}, {ContractJSON: targetContract}},
		Requirements: []string{sourceID.String(), targetID.String()},
		Providers: []generation.ProviderInput{
			{Capability: sourceID.String(), Plugin: "example.source"},
			{Capability: targetID.String(), Plugin: "example.target"},
		},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: contributions})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	byID := make(map[string]generation.NormalizedContribution, len(contributions))
	for _, contribution := range output.Contributions() {
		byID[contribution.ID()] = contribution
	}
	ordered := make([]generation.NormalizedContribution, len(contributions))
	for index, contribution := range contributions {
		ordered[index] = byID[contribution.ID]
	}
	plan, err := generationlowering.Lower(testModulePath, ordered)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	return plan
}

func prepareSourcePlan(t *testing.T, sourceSchema string, contributions []generation.Contribution) generationlowering.Plan {
	t.Helper()
	if len(contributions) == 0 {
		t.Fatal("prepareSourcePlan requires at least one contribution")
	}
	sourceID := contributions[0].Source
	context, err := generation.NewContext(generation.Input{
		Plugins: []generation.PluginInput{{
			ID:                "example.source",
			ModulePath:        testModulePath,
			Provides:          []string{sourceID.String()},
			BuildMetadataJSON: []byte("{}"),
		}},
		Capabilities: []generation.CapabilityInput{{ContractJSON: planCanonicalContract(t, sourceSchema)}},
		Requirements: []string{sourceID.String()},
		Providers:    []generation.ProviderInput{{Capability: sourceID.String(), Plugin: "example.source"}},
	})
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	output, err := generation.NormalizeOutput(context, generation.Output{Contributions: contributions})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	byID := make(map[string]generation.NormalizedContribution, len(contributions))
	for _, contribution := range output.Contributions() {
		byID[contribution.ID()] = contribution
	}
	ordered := make([]generation.NormalizedContribution, len(contributions))
	for index, contribution := range contributions {
		ordered[index] = byID[contribution.ID]
	}
	plan, err := generationlowering.Lower(testModulePath, ordered)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	return plan
}

func policyLiteralBindings() []generation.GeneratedFieldBinding {
	return []generation.GeneratedFieldBinding{
		{Field: "permission", Value: generation.StringValue("order.create")},
		{Field: "retry_count", Value: generation.IntegerValue(2)},
		{Field: "enforce", Value: generation.BooleanValue(true)},
	}
}

func planCanonicalContract(t *testing.T, schema string) []byte {
	t.Helper()
	canonical, err := capabilitymeta.NormalizeSchema([]byte(schema))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	return canonical
}

func planCapabilityID(t *testing.T, value string) generation.CapabilityID {
	t.Helper()
	id, err := generation.ParseCapabilityID(value)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%s): %v", value, err)
	}
	return id
}

func planNodeValue(id string, output generation.GeneratedNodeOutput) generation.GeneratedValue {
	return generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: id, Output: output}}
}

func planInvocationValue(source generation.GeneratedInvocationValueSource, name string) generation.GeneratedValue {
	return generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{Source: source, Name: name}}
}

func assertGeneratedPrepareInvocationRuns(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(orderCreateSchema))
	if err != nil {
		t.Fatalf("Render(source contract): %v", err)
	}
	targetContract, err := contractgen.Render([]byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(target contract): %v", err)
	}
	targetInvocation, err := invocationgen.Render(testModulePath, []byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(target invocation): %v", err)
	}
	targetClient, err := clientgen.Render(testModulePath, []byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(target client): %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{targetContract.Path(), targetContract.Data()},
		{targetInvocation.Path(), targetInvocation.Data()},
		{targetClient.Path(), targetClient.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/create/v1/invocation_gen_test.go", []byte(`package ordercreatev1_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	policyclient "example.com/acme/project/generated/go/clients/policy/check/v1"
	ordercontract "example.com/acme/project/generated/go/contracts/order/create/v1"
	policycontract "example.com/acme/project/generated/go/contracts/policy/check/v1"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/create/v1"
	policyinvocation "example.com/acme/project/generated/go/invocation/policy/check/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestPrepareCallRunsBeforeCanonicalDispatchAndFailsClosed(t *testing.T) {
	sequence := []string{}
	failPolicy := false
	policyTarget := kernelinvocation.NewTestHandle(true, func(ctx context.Context, request policycontract.Request) (policycontract.Response, error) {
		sequence = append(sequence, "policy:"+request.Permission)
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 0 || remaining > 100*time.Millisecond {
			t.Fatalf("policy deadline = %v, %t, remaining %v", deadline, ok, remaining)
		}
		if request.Permission == "" || request.RetryCount != 2 || !request.Enforce {
			t.Fatalf("policy request = %#v", request)
		}
		if failPolicy {
			return policycontract.Response{}, errors.New("policy unavailable")
		}
		return policycontract.Response{Allowed: true}, nil
	})
	orderTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, request ordercontract.Request) (ordercontract.Response, error) {
		sequence = append(sequence, "order:"+request.OrderID)
		return ordercontract.Response{Accepted: true}, nil
	})
	policy := policyclient.New(policyinvocation.New(policyTarget))
	handle := orderinvocation.New(orderTarget, policy)
	response, err := handle.Invoke(context.Background(), ordercontract.Request{OrderID: "one"})
	if err != nil || !response.Accepted || !slices.Equal(sequence, []string{"policy:one", "order:one"}) {
		t.Fatalf("Invoke = %#v, %v, sequence %v", response, err, sequence)
	}

	failPolicy = true
	sequence = nil
	response, err = handle.Invoke(context.Background(), ordercontract.Request{OrderID: "two"})
	if err == nil || response.Accepted || !slices.Equal(sequence, []string{"policy:two"}) {
		t.Fatalf("failed Invoke = %#v, %v, sequence %v", response, err, sequence)
	}
	sequence = nil
	if _, err := handle.Invoke(nil, ordercontract.Request{OrderID: "three"}); err == nil || len(sequence) != 0 {
		t.Fatalf("nil-context Invoke = %v, sequence %v", err, sequence)
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func assertGeneratedTypedRequestBindingsRun(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(typedOrderSchema))
	if err != nil {
		t.Fatalf("Render(typed source contract): %v", err)
	}
	targetContract, err := contractgen.Render([]byte(typedPolicySchema))
	if err != nil {
		t.Fatalf("Render(typed target contract): %v", err)
	}
	targetInvocation, err := invocationgen.Render(testModulePath, []byte(typedPolicySchema))
	if err != nil {
		t.Fatalf("Render(typed target invocation): %v", err)
	}
	targetClient, err := clientgen.Render(testModulePath, []byte(typedPolicySchema))
	if err != nil {
		t.Fatalf("Render(typed target client): %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{targetContract.Path(), targetContract.Data()},
		{targetInvocation.Path(), targetInvocation.Data()},
		{targetClient.Path(), targetClient.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/typed-create/v1/invocation_gen_test.go", []byte(`package ordertypedcreatev1_test

import (
	"context"
	"reflect"
	"slices"
	"testing"

	policyclient "example.com/acme/project/generated/go/clients/policy/typed-check/v1"
	ordercontract "example.com/acme/project/generated/go/contracts/order/typed-create/v1"
	policycontract "example.com/acme/project/generated/go/contracts/policy/typed-check/v1"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/typed-create/v1"
	policyinvocation "example.com/acme/project/generated/go/invocation/policy/typed-check/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestTypedBindingsPreserveValuesAndAbsence(t *testing.T) {
	policyCalls := 0
	sawPresentNote := false
	sawAbsentNote := false
	policyTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, request policycontract.Request) (policycontract.Response, error) {
		policyCalls++
		if request.Name != "alpha" || request.OptionalName == nil || *request.OptionalName != "alpha" || request.Ratio != 3 || !request.Active {
			t.Fatalf("scalar request = %#v", request)
		}
		if !slices.Equal(request.Labels, []string{"one", "two"}) || !reflect.DeepEqual(request.Attributes, map[string]any{"source": "test"}) {
			t.Fatalf("collection request = %#v", request)
		}
		if string(request.Mode) != "fast" || request.OptionalLiteral == nil || !*request.OptionalLiteral {
			t.Fatalf("enum or literal request = %#v", request)
		}
		if request.OptionalNote == nil {
			sawAbsentNote = true
		} else if *request.OptionalNote == "memo" {
			sawPresentNote = true
		} else {
			t.Fatalf("optional note = %#v", request.OptionalNote)
		}
		return policycontract.Response{Allowed: true}, nil
	})
	dispatches := 0
	orderTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, request ordercontract.Request) (ordercontract.Response, error) {
		dispatches++
		return ordercontract.Response{Accepted: request.Name == "alpha"}, nil
	})
	policy := policyclient.New(policyinvocation.New(policyTarget))
	handle := orderinvocation.New(orderTarget, policy)
	note := "memo"
	request := ordercontract.Request{
		Name:       "alpha",
		Note:       &note,
		Attempts:   3,
		Active:     true,
		Labels:     []string{"one", "two"},
		Attributes: map[string]any{"source": "test"},
		Mode:       ordercontract.RequestModeFast,
	}
	response, err := handle.Invoke(context.Background(), request)
	if err != nil || !response.Accepted {
		t.Fatalf("Invoke(present) = %#v, %v", response, err)
	}
	request.Note = nil
	response, err = handle.Invoke(context.Background(), request)
	if err != nil || !response.Accepted {
		t.Fatalf("Invoke(absent) = %#v, %v", response, err)
	}
	if policyCalls != 2 || dispatches != 2 || !sawPresentNote || !sawAbsentNote {
		t.Fatalf("calls = policy %d dispatch %d, notes present=%t absent=%t", policyCalls, dispatches, sawPresentNote, sawAbsentNote)
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func assertGeneratedContextDerivationRuns(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(contextOrderSchema))
	if err != nil {
		t.Fatalf("Render(context source contract): %v", err)
	}
	contextFile, err := invocationgen.RenderContext()
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{contextFile.Path(), contextFile.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/context-create/v1/invocation_gen_test.go", []byte(`package ordercontextcreatev1_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	ordercontract "example.com/acme/project/generated/go/contracts/order/context-create/v1"
	invocationcontext "example.com/acme/project/generated/go/internal/invocationcontext"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/context-create/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestContextDerivationIsBoundedOptionalAndReusable(t *testing.T) {
	dispatches := 0
	notePresence := []bool{}
	optionalCopyPresence := []bool{}
	target := kernelinvocation.NewTestHandle(true, func(ctx context.Context, _ ordercontract.Request) (ordercontract.Response, error) {
		dispatches++
		space, spaceOK := invocationcontext.Value[string](ctx, "policy.space-id")
		mode, modeOK := invocationcontext.Value[string](ctx, "policy.mode")
		optionalLiteral, optionalLiteralOK := invocationcontext.Value[string](ctx, "policy.optional-literal")
		caller, callerOK := invocationcontext.Value[string](ctx, "policy.caller")
		copy, copyOK := invocationcontext.Value[string](ctx, "policy.caller-copy")
		_, noteOK := invocationcontext.Value[string](ctx, "policy.note")
		_, optionalCopyOK := invocationcontext.Value[string](ctx, "policy.optional-copy")
		notePresence = append(notePresence, noteOK)
		optionalCopyPresence = append(optionalCopyPresence, optionalCopyOK)
		if !spaceOK || space != "space-1" || !modeOK || mode != "strict" || !optionalLiteralOK || optionalLiteral != "present" || !callerOK || caller != "caller-1" || !copyOK || copy != caller {
			t.Fatalf("derived context = space %q/%t mode %q/%t optional %q/%t caller %q/%t copy %q/%t", space, spaceOK, mode, modeOK, optionalLiteral, optionalLiteralOK, caller, callerOK, copy, copyOK)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("derived context lost its parent deadline")
		}
		return ordercontract.Response{Accepted: true}, nil
	})
	handle := orderinvocation.New(target)
	deadlineContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	parent, err := invocationcontext.WithValue(deadlineContext, "policy.caller", "caller-1", 32)
	if err != nil {
		t.Fatalf("WithValue(parent): %v", err)
	}
	parentWithOptional, err := invocationcontext.WithValue(parent, "policy.optional", "optional-1", 32)
	if err != nil {
		t.Fatalf("WithValue(optional): %v", err)
	}
	note := "memo"
	response, err := handle.Invoke(parentWithOptional, ordercontract.Request{SpaceID: "space-1", Note: &note})
	if err != nil || !response.Accepted {
		t.Fatalf("Invoke(present): %#v, %v", response, err)
	}
	response, err = handle.Invoke(parent, ordercontract.Request{SpaceID: "space-1"})
	if err != nil || !response.Accepted || !slices.Equal(notePresence, []bool{true, false}) || !slices.Equal(optionalCopyPresence, []bool{true, false}) {
		t.Fatalf("Invoke(absent): %#v, %v, notes %v, optional copies %v", response, err, notePresence, optionalCopyPresence)
	}

	before := dispatches
	oversizedNote := strings.Repeat("x", 32)
	for _, test := range []struct {
		name string
		ctx context.Context
		request ordercontract.Request
	}{
		{name: "missing reusable value", ctx: context.Background(), request: ordercontract.Request{SpaceID: "space-1"}},
		{name: "oversized required value", ctx: parent, request: ordercontract.Request{SpaceID: strings.Repeat("x", 16)}},
		{name: "oversized optional value", ctx: parent, request: ordercontract.Request{SpaceID: "space-1", Note: &oversizedNote}},
		{name: "nil context", request: ordercontract.Request{SpaceID: "space-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := handle.Invoke(test.ctx, test.request)
			if !errors.Is(err, invocationcontext.ErrInvalidValue) || response.Accepted {
				t.Fatalf("Invoke = %#v, %v", response, err)
			}
		})
	}
	if dispatches != before {
		t.Fatalf("failed derivation dispatched: before %d after %d", before, dispatches)
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func assertGeneratedMetadataAttachmentRuns(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(orderCreateSchema))
	if err != nil {
		t.Fatalf("Render(metadata source contract): %v", err)
	}
	contextFile, err := invocationgen.RenderContext()
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{contextFile.Path(), contextFile.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/create/v1/invocation_gen_test.go", []byte(`package ordercreatev1_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	ordercontract "example.com/acme/project/generated/go/contracts/order/create/v1"
	invocationcontext "example.com/acme/project/generated/go/internal/invocationcontext"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/create/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestMetadataIsBoundedPropagatedAndSeparateFromTypedValues(t *testing.T) {
	dispatches := 0
	target := kernelinvocation.NewTestHandle(true, func(ctx context.Context, request ordercontract.Request) (ordercontract.Response, error) {
		dispatches++
		codeValue, codeValueOK := invocationcontext.Value[string](ctx, "policy.code")
		codeMetadata, codeMetadataOK := invocationcontext.Metadata[string](ctx, "policy.code")
		orderID, orderIDOK := invocationcontext.Metadata[string](ctx, "policy.order-id")
		derived, derivedOK := invocationcontext.Metadata[string](ctx, "policy.derived")
		rejected, rejectedOK := invocationcontext.Metadata[bool](ctx, "policy.rejected")
		literal, literalOK := invocationcontext.Metadata[bool](ctx, "policy.literal")
		if !codeValueOK || codeValue != request.OrderID || !codeMetadataOK || codeMetadata != request.OrderID || !orderIDOK || orderID != request.OrderID || !derivedOK || derived != request.OrderID {
			t.Fatalf("string metadata = value %q/%t code %q/%t order %q/%t derived %q/%t", codeValue, codeValueOK, codeMetadata, codeMetadataOK, orderID, orderIDOK, derived, derivedOK)
		}
		if !rejectedOK || !rejected || !literalOK || literal {
			t.Fatalf("boolean metadata = rejected %t/%t literal %t/%t", rejected, rejectedOK, literal, literalOK)
		}
		return ordercontract.Response{Accepted: true}, nil
	})
	handle := orderinvocation.New(target)
	response, err := handle.Invoke(context.Background(), ordercontract.Request{OrderID: "order-1", Reject: true})
	if err != nil || !response.Accepted || dispatches != 1 {
		t.Fatalf("Invoke = %#v, %v, dispatches %d", response, err, dispatches)
	}

	response, err = handle.Invoke(context.Background(), ordercontract.Request{OrderID: strings.Repeat("x", 16)})
	if !errors.Is(err, invocationcontext.ErrInvalidMetadata) || response.Accepted || dispatches != 1 {
		t.Fatalf("Invoke(oversized) = %#v, %v, dispatches %d", response, err, dispatches)
	}
	response, err = handle.Invoke(nil, ordercontract.Request{OrderID: "order-1"})
	if !errors.Is(err, invocationcontext.ErrInvalidValue) || response.Accepted || dispatches != 1 {
		t.Fatalf("Invoke(nil context) = %#v, %v, dispatches %d", response, err, dispatches)
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func assertGeneratedAuditEventsRun(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(auditOrderSchema))
	if err != nil {
		t.Fatalf("Render(audit source contract): %v", err)
	}
	auditContract, err := contractgen.Render([]byte(auditWriteSchema))
	if err != nil {
		t.Fatalf("Render(audit target contract): %v", err)
	}
	auditInvocation, err := invocationgen.Render(testModulePath, []byte(auditWriteSchema))
	if err != nil {
		t.Fatalf("Render(audit target invocation): %v", err)
	}
	auditClient, err := clientgen.Render(testModulePath, []byte(auditWriteSchema))
	if err != nil {
		t.Fatalf("Render(audit target client): %v", err)
	}
	contextFile, err := invocationgen.RenderContext()
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{auditContract.Path(), auditContract.Data()},
		{auditInvocation.Path(), auditInvocation.Data()},
		{auditClient.Path(), auditClient.Data()},
		{contextFile.Path(), contextFile.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/create/v1/invocation_gen_test.go", []byte(`package ordercreatev1_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	auditclient "example.com/acme/project/generated/go/clients/audit/write/v1"
	auditcontract "example.com/acme/project/generated/go/contracts/audit/write/v1"
	ordercontract "example.com/acme/project/generated/go/contracts/order/create/v1"
	invocationcontext "example.com/acme/project/generated/go/internal/invocationcontext"
	auditinvocation "example.com/acme/project/generated/go/invocation/audit/write/v1"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/create/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestAuditEventsUseCanonicalClientAndDeclaredFailureModes(t *testing.T) {
	sequence := []string{}
	failEvent := ""
	wantObservedNote := (*string)(nil)
	auditTarget := kernelinvocation.NewTestHandle(true, func(ctx context.Context, request auditcontract.Request) (auditcontract.Response, error) {
		sequence = append(sequence, "audit:"+request.Event)
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 0 || remaining > 100*time.Millisecond {
			t.Fatalf("audit deadline = %v, %t, remaining %v", deadline, ok, remaining)
		}
		if request.OrderID != "order-1" {
			t.Fatalf("audit request = %#v", request)
		}
		switch request.Event {
		case "order.observed":
			if !request.Rejected {
				t.Fatalf("observed request = %#v", request)
			}
			if wantObservedNote == nil && request.Note != nil || wantObservedNote != nil && (request.Note == nil || *request.Note != *wantObservedNote) {
				t.Fatalf("observed note = %#v, want %#v", request.Note, wantObservedNote)
			}
		case "order.required":
			if request.Rejected || request.Note != nil {
				t.Fatalf("required request = %#v", request)
			}
		default:
			t.Fatalf("unexpected audit event %q", request.Event)
		}
		if request.Event == failEvent {
			return auditcontract.Response{}, errors.New("audit unavailable")
		}
		return auditcontract.Response{Recorded: true}, nil
	})
	dispatches := 0
	orderTarget := kernelinvocation.NewTestHandle(true, func(ctx context.Context, request ordercontract.Request) (ordercontract.Response, error) {
		sequence = append(sequence, "order:"+request.OrderID)
		dispatches++
		orderID, ok := invocationcontext.Value[string](ctx, "audit.order-id")
		if !ok || orderID != request.OrderID {
			t.Fatalf("audit context = %q, %t", orderID, ok)
		}
		return ordercontract.Response{Accepted: true}, nil
	})
	audit := auditclient.New(auditinvocation.New(auditTarget))
	handle := orderinvocation.New(orderTarget, audit)
	request := ordercontract.Request{OrderID: "order-1", Reject: true}

	note := "caller-note"
	wantObservedNote = &note
	ctx, err := invocationcontext.WithValue(context.Background(), "audit.note", note, 32)
	if err != nil {
		t.Fatalf("WithValue(audit.note): %v", err)
	}
	response, err := handle.Invoke(ctx, request)
	if err != nil || !response.Accepted || dispatches != 1 || !slices.Equal(sequence, []string{"audit:order.observed", "audit:order.required", "order:order-1"}) {
		t.Fatalf("Invoke(happy) = %#v, %v, dispatches %d, sequence %v", response, err, dispatches, sequence)
	}
	failEvent = "order.observed"
	wantObservedNote = nil
	sequence = nil
	response, err = handle.Invoke(context.Background(), request)
	if err != nil || !response.Accepted || dispatches != 2 || !slices.Equal(sequence, []string{"audit:order.observed", "audit:order.required", "order:order-1"}) {
		t.Fatalf("Invoke(continue) = %#v, %v, dispatches %d, sequence %v", response, err, dispatches, sequence)
	}
	failEvent = "order.required"
	sequence = nil
	response, err = handle.Invoke(context.Background(), request)
	if err == nil || response.Accepted || dispatches != 2 || !slices.Equal(sequence, []string{"audit:order.observed", "audit:order.required"}) {
		t.Fatalf("Invoke(fail closed) = %#v, %v, dispatches %d, sequence %v", response, err, dispatches, sequence)
	}
	sequence = nil
	response, err = handle.Invoke(nil, request)
	if !errors.Is(err, invocationcontext.ErrInvalidValue) || response.Accepted || dispatches != 2 || len(sequence) != 0 {
		t.Fatalf("Invoke(nil context) = %#v, %v, dispatches %d, sequence %v", response, err, dispatches, sequence)
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func assertGeneratedCompletionRuns(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(completionOrderSchema))
	if err != nil {
		t.Fatalf("Render(completion source contract): %v", err)
	}
	auditContract, err := contractgen.Render([]byte(auditWriteSchema))
	if err != nil {
		t.Fatalf("Render(completion audit contract): %v", err)
	}
	auditInvocation, err := invocationgen.Render(testModulePath, []byte(auditWriteSchema))
	if err != nil {
		t.Fatalf("Render(completion audit invocation): %v", err)
	}
	auditClient, err := clientgen.Render(testModulePath, []byte(auditWriteSchema))
	if err != nil {
		t.Fatalf("Render(completion audit client): %v", err)
	}
	contextFile, err := invocationgen.RenderContext()
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{auditContract.Path(), auditContract.Data()},
		{auditInvocation.Path(), auditInvocation.Data()},
		{auditClient.Path(), auditClient.Data()},
		{contextFile.Path(), contextFile.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/create/v1/invocation_gen_test.go", []byte(`package ordercreatev1_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	auditclient "example.com/acme/project/generated/go/clients/audit/write/v1"
	auditcontract "example.com/acme/project/generated/go/contracts/audit/write/v1"
	ordercontract "example.com/acme/project/generated/go/contracts/order/create/v1"
	invocationcontext "example.com/acme/project/generated/go/internal/invocationcontext"
	auditinvocation "example.com/acme/project/generated/go/invocation/audit/write/v1"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/create/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestCompletionRunsAfterCanonicalDispatchAndHandlesItsOutcome(t *testing.T) {
	sequence := []string{}
	failOrder := false
	failAudit := false
	dispatches := 0
	audits := 0
	orderTarget := kernelinvocation.NewTestHandle(true, func(ctx context.Context, request ordercontract.Request) (ordercontract.Response, error) {
		sequence = append(sequence, "order:"+request.OrderID)
		dispatches++
		requestOrder, ok := invocationcontext.Value[string](ctx, "audit.request-order")
		if !ok || requestOrder != request.OrderID {
			t.Fatalf("prepared order context = %q, %t", requestOrder, ok)
		}
		if failOrder {
			return ordercontract.Response{}, errors.New("provider failed")
		}
		return ordercontract.Response{Accepted: true, OrderID: request.OrderID}, nil
	})
	auditTarget := kernelinvocation.NewTestHandle(true, func(ctx context.Context, request auditcontract.Request) (auditcontract.Response, error) {
		sequence = append(sequence, "audit:"+request.Event)
		audits++
		deadline, ok := ctx.Deadline()
		remaining := time.Until(deadline)
		if !ok || remaining <= 0 || remaining > 100*time.Millisecond {
			t.Fatalf("audit deadline = %v, %t, remaining %v", deadline, ok, remaining)
		}
		orderID, orderIDOK := invocationcontext.Value[string](ctx, "audit.completed-order")
		accepted, acceptedOK := invocationcontext.Metadata[bool](ctx, "audit.accepted")
		if request.Event != "order.completed" || request.OrderID != "order-1" || request.Rejected || request.Note != nil || !orderIDOK || orderID != request.OrderID || !acceptedOK || !accepted {
			t.Fatalf("audit request/context = %#v, order %q/%t, accepted %t/%t", request, orderID, orderIDOK, accepted, acceptedOK)
		}
		if failAudit {
			return auditcontract.Response{}, errors.New("audit unavailable")
		}
		return auditcontract.Response{Recorded: true}, nil
	})
	audit := auditclient.New(auditinvocation.New(auditTarget))
	handle := orderinvocation.New(orderTarget, audit)
	request := ordercontract.Request{OrderID: "order-1"}

	response, err := handle.Invoke(context.Background(), request)
	if err != nil || !response.Accepted || response.OrderID != request.OrderID || dispatches != 1 || audits != 1 || !slices.Equal(sequence, []string{"order:order-1", "audit:order.completed"}) {
		t.Fatalf("Invoke(happy) = %#v, %v, dispatches %d, audits %d, sequence %v", response, err, dispatches, audits, sequence)
	}
	failAudit = true
	sequence = nil
	response, err = handle.Invoke(context.Background(), request)
	if err != nil || !response.Accepted || response.OrderID != request.OrderID || dispatches != 2 || audits != 2 || !slices.Equal(sequence, []string{"order:order-1", "audit:order.completed"}) {
		t.Fatalf("Invoke(audit continue) = %#v, %v, dispatches %d, audits %d, sequence %v", response, err, dispatches, audits, sequence)
	}
	failOrder = true
	sequence = nil
	response, err = handle.Invoke(context.Background(), request)
	if !errors.Is(err, ordercontract.ErrPolicyFailed) || err.Error() != "Canonical dispatch failed." || response.Accepted || response.OrderID != "" || dispatches != 3 || audits != 2 || !slices.Equal(sequence, []string{"order:order-1"}) {
		t.Fatalf("Invoke(dispatch failure) = %#v, %v, dispatches %d, audits %d, sequence %v", response, err, dispatches, audits, sequence)
	}
	failOrder = false
	sequence = nil
	response, err = handle.Invoke(nil, request)
	if !errors.Is(err, invocationcontext.ErrInvalidValue) || response.Accepted || response.OrderID != "" || dispatches != 3 || audits != 2 || len(sequence) != 0 {
		t.Fatalf("Invoke(nil context) = %#v, %v, dispatches %d, audits %d, sequence %v", response, err, dispatches, audits, sequence)
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func assertGeneratedConditionalFailuresRun(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(orderCreateSchema))
	if err != nil {
		t.Fatalf("Render(conditional source contract): %v", err)
	}
	targetContract, err := contractgen.Render([]byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(conditional target contract): %v", err)
	}
	targetInvocation, err := invocationgen.Render(testModulePath, []byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(conditional target invocation): %v", err)
	}
	targetClient, err := clientgen.Render(testModulePath, []byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(conditional target client): %v", err)
	}
	contextFile, err := invocationgen.RenderContext()
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{targetContract.Path(), targetContract.Data()},
		{targetInvocation.Path(), targetInvocation.Data()},
		{targetClient.Path(), targetClient.Data()},
		{contextFile.Path(), contextFile.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/create/v1/invocation_gen_test.go", []byte(`package ordercreatev1_test

import (
	"context"
	"errors"
	"testing"

	policyclient "example.com/acme/project/generated/go/clients/policy/check/v1"
	ordercontract "example.com/acme/project/generated/go/contracts/order/create/v1"
	policycontract "example.com/acme/project/generated/go/contracts/policy/check/v1"
	invocationcontext "example.com/acme/project/generated/go/internal/invocationcontext"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/create/v1"
	policyinvocation "example.com/acme/project/generated/go/invocation/policy/check/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestConditionalFailuresPreventCanonicalDispatch(t *testing.T) {
	policyAllowed := true
	var policyError error
	policyCalls := 0
	policyTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, request policycontract.Request) (policycontract.Response, error) {
		policyCalls++
		if request.Permission != "order.create" || request.RetryCount != 2 || !request.Enforce {
			t.Fatalf("policy request = %#v", request)
		}
		return policycontract.Response{Allowed: policyAllowed}, policyError
	})
	dispatches := 0
	orderTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, _ ordercontract.Request) (ordercontract.Response, error) {
		dispatches++
		return ordercontract.Response{Accepted: true}, nil
	})
	policy := policyclient.New(policyinvocation.New(policyTarget))
	handle := orderinvocation.New(orderTarget, policy)
	note := "memo"
	request := ordercontract.Request{OrderID: "order-1", Note: &note}
	response, err := handle.Invoke(context.Background(), request)
	if err != nil || !response.Accepted || policyCalls != 1 || dispatches != 1 {
		t.Fatalf("Invoke(happy) = %#v, %v, policy calls %d, dispatches %d", response, err, policyCalls, dispatches)
	}

	blockedContext, err := invocationcontext.WithValue(context.Background(), "policy.block", "manual", 32)
	if err != nil {
		t.Fatalf("WithValue(block): %v", err)
	}
	tests := []struct {
		name             string
		ctx              context.Context
		request          ordercontract.Request
		allowed          bool
		providerError    error
		wantMessage      string
		wantPolicyCalls int
	}{
		{name: "provider error", ctx: context.Background(), request: request, allowed: true, providerError: errors.New("unavailable"), wantMessage: "Policy provider failed.", wantPolicyCalls: 1},
		{name: "denial", ctx: context.Background(), request: request, wantMessage: "Policy denied the request.", wantPolicyCalls: 1},
		{name: "missing note", ctx: context.Background(), request: ordercontract.Request{OrderID: "order-1"}, allowed: true, wantMessage: "Order note is required.", wantPolicyCalls: 1},
		{name: "request rejection", ctx: context.Background(), request: ordercontract.Request{OrderID: "order-1", Note: &note, Reject: true}, allowed: true, wantMessage: "Order was explicitly rejected.", wantPolicyCalls: 1},
		{name: "context block", ctx: blockedContext, request: request, allowed: true, wantMessage: "Policy context blocked the request.", wantPolicyCalls: 1},
		{name: "nil context", request: request, allowed: true, wantMessage: "Policy provider failed."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policyAllowed = test.allowed
			policyError = test.providerError
			beforePolicyCalls := policyCalls
			beforeDispatches := dispatches
			response, err := handle.Invoke(test.ctx, test.request)
			if !errors.Is(err, ordercontract.ErrPolicyFailed) || err.Error() != test.wantMessage || response.Accepted {
				t.Fatalf("Invoke = %#v, %v", response, err)
			}
			if policyCalls-beforePolicyCalls != test.wantPolicyCalls || dispatches != beforeDispatches {
				t.Fatalf("calls = policy %d, dispatches before %d after %d", policyCalls-beforePolicyCalls, beforeDispatches, dispatches)
			}
		})
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func assertGeneratedNodeValuesRun(t testing.TB, sourceInvocation invocationgen.File) {
	t.Helper()
	root := t.TempDir()
	sourceContract, err := contractgen.Render([]byte(orderCreateSchema))
	if err != nil {
		t.Fatalf("Render(node source contract): %v", err)
	}
	targetContract, err := contractgen.Render([]byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(node target contract): %v", err)
	}
	targetInvocation, err := invocationgen.Render(testModulePath, []byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(node target invocation): %v", err)
	}
	targetClient, err := clientgen.Render(testModulePath, []byte(policyCheckSchema))
	if err != nil {
		t.Fatalf("Render(node target client): %v", err)
	}
	contextFile, err := invocationgen.RenderContext()
	if err != nil {
		t.Fatalf("RenderContext: %v", err)
	}
	for _, file := range []struct {
		path string
		data []byte
	}{
		{sourceContract.Path(), sourceContract.Data()},
		{targetContract.Path(), targetContract.Data()},
		{targetInvocation.Path(), targetInvocation.Data()},
		{targetClient.Path(), targetClient.Data()},
		{contextFile.Path(), contextFile.Data()},
		{sourceInvocation.Path(), sourceInvocation.Data()},
	} {
		writeGeneratedFile(t, root, file.path, file.data)
	}
	writeInvocationTestKernel(t, root)
	writeGeneratedFile(t, root, "generated/go/invocation/order/create/v1/invocation_gen_test.go", []byte(`package ordercreatev1_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	policyclient "example.com/acme/project/generated/go/clients/policy/check/v1"
	ordercontract "example.com/acme/project/generated/go/contracts/order/create/v1"
	policycontract "example.com/acme/project/generated/go/contracts/policy/check/v1"
	invocationcontext "example.com/acme/project/generated/go/internal/invocationcontext"
	orderinvocation "example.com/acme/project/generated/go/invocation/order/create/v1"
	policyinvocation "example.com/acme/project/generated/go/invocation/policy/check/v1"
	kernelinvocation "github.com/plystra/kernel/invocation"
)

func TestEarlierNodeValuesFlowIntoContextAndCalls(t *testing.T) {
	var sourceReason *string
	var sourceDetails *map[string]any
	failCall := 0
	callIndex := 0
	wantEvidence := func() map[string]any {
		result := map[string]any{"allowed": true}
		if sourceReason != nil {
			result["reason"] = *sourceReason
		}
		if sourceDetails != nil {
			result["details"] = *sourceDetails
		}
		return result
	}
	policyTarget := kernelinvocation.NewTestHandle(true, func(_ context.Context, request policycontract.Request) (policycontract.Response, error) {
		callIndex++
		switch callIndex {
		case 1:
			if request.Permission != "order-1" || request.RetryCount != 1 || !request.Enforce || request.Credential != nil || request.Evidence != nil {
				t.Fatalf("source policy request = %#v", request)
			}
			if failCall == 1 {
				return policycontract.Response{}, errors.New("source policy failed")
			}
			return policycontract.Response{Allowed: true, Reason: sourceReason, Details: sourceDetails}, nil
		case 2:
			if request.Permission != "order-1" || request.RetryCount != 2 || !request.Enforce {
				t.Fatalf("derived policy request = %#v", request)
			}
			if sourceReason == nil && request.Credential != nil || sourceReason != nil && (request.Credential == nil || *request.Credential != *sourceReason) {
				t.Fatalf("derived credential = %#v, source %#v", request.Credential, sourceReason)
			}
			if request.Evidence == nil || !reflect.DeepEqual(*request.Evidence, wantEvidence()) {
				t.Fatalf("derived evidence = %#v, want %#v", request.Evidence, wantEvidence())
			}
			if failCall == 2 {
				return policycontract.Response{}, errors.New("derived policy failed")
			}
			return policycontract.Response{Allowed: true}, nil
		default:
			t.Fatalf("unexpected policy call %d", callIndex)
			return policycontract.Response{}, errors.New("unexpected call")
		}
	})
	dispatches := 0
	orderTarget := kernelinvocation.NewTestHandle(true, func(ctx context.Context, _ ordercontract.Request) (ordercontract.Response, error) {
		dispatches++
		permission, permissionOK := invocationcontext.Value[string](ctx, "policy.permission")
		allowed, allowedOK := invocationcontext.Value[bool](ctx, "policy.allowed")
		reason, reasonOK := invocationcontext.Value[string](ctx, "policy.reason")
		evidence, evidenceOK := invocationcontext.Value[map[string]any](ctx, "policy.evidence")
		if !permissionOK || permission != "order-1" || !allowedOK || !allowed || !evidenceOK || !reflect.DeepEqual(evidence, wantEvidence()) {
			t.Fatalf("derived context = permission %q/%t allowed %t/%t evidence %#v/%t", permission, permissionOK, allowed, allowedOK, evidence, evidenceOK)
		}
		if sourceReason == nil && reasonOK || sourceReason != nil && (!reasonOK || reason != *sourceReason) {
			t.Fatalf("derived reason = %q/%t, source %#v", reason, reasonOK, sourceReason)
		}
		return ordercontract.Response{Accepted: true}, nil
	})
	policy := policyclient.New(policyinvocation.New(policyTarget))
	handle := orderinvocation.New(orderTarget, policy)
	invoke := func(ctx context.Context, reason *string, details *map[string]any, failure int) (ordercontract.Response, error, int, int) {
		sourceReason = reason
		sourceDetails = details
		failCall = failure
		callIndex = 0
		beforeDispatches := dispatches
		response, err := handle.Invoke(ctx, ordercontract.Request{OrderID: "order-1"})
		return response, err, callIndex, dispatches - beforeDispatches
	}

	reason := "verified"
	response, err, calls, dispatched := invoke(context.Background(), &reason, nil, 0)
	if err != nil || !response.Accepted || calls != 2 || dispatched != 1 {
		t.Fatalf("Invoke(present) = %#v, %v, calls %d, dispatched %d", response, err, calls, dispatched)
	}
	response, err, calls, dispatched = invoke(context.Background(), nil, nil, 0)
	if err != nil || !response.Accepted || calls != 2 || dispatched != 1 {
		t.Fatalf("Invoke(absent) = %#v, %v, calls %d, dispatched %d", response, err, calls, dispatched)
	}
	response, err, calls, dispatched = invoke(context.Background(), &reason, nil, 1)
	if err == nil || response.Accepted || calls != 1 || dispatched != 0 {
		t.Fatalf("Invoke(first failure) = %#v, %v, calls %d, dispatched %d", response, err, calls, dispatched)
	}
	response, err, calls, dispatched = invoke(context.Background(), &reason, nil, 2)
	if err == nil || response.Accepted || calls != 2 || dispatched != 0 {
		t.Fatalf("Invoke(second failure) = %#v, %v, calls %d, dispatched %d", response, err, calls, dispatched)
	}
	invalidDetails := map[string]any{"invalid": func() {}}
	response, err, calls, dispatched = invoke(context.Background(), &reason, &invalidDetails, 0)
	if err == nil || err.Error() != "invalid canonical Provider response" || response.Accepted || calls != 1 || dispatched != 0 {
		t.Fatalf("Invoke(conversion failure) = %#v, %v, calls %d, dispatched %d", response, err, calls, dispatched)
	}
	response, err, calls, dispatched = invoke(nil, &reason, nil, 0)
	if !errors.Is(err, invocationcontext.ErrInvalidValue) || response.Accepted || calls != 0 || dispatched != 0 {
		t.Fatalf("Invoke(nil context) = %#v, %v, calls %d, dispatched %d", response, err, calls, dispatched)
	}
}
`))
	writeGeneratedFile(t, root, "go.mod", []byte("module "+testModulePath+"\n\ngo 1.26\n\nrequire github.com/plystra/kernel v0.0.0\n\nreplace github.com/plystra/kernel => ./kernel\n"))
	runGeneratedGoTests(t, root)
}

func writeInvocationTestKernel(t testing.TB, root string) {
	t.Helper()
	writeGeneratedFile(t, root, "kernel/go.mod", []byte("module github.com/plystra/kernel\n\ngo 1.26\n"))
	writeGeneratedFile(t, root, "kernel/invocation/error.go", []byte(`package invocation

import "strings"

type ErrorCode string

const (
	ErrorInvalidArgument ErrorCode = "invalid_argument"
	ErrorNotFound ErrorCode = "not_found"
	ErrorConflict ErrorCode = "conflict"
	ErrorDenied ErrorCode = "denied"
	ErrorUnauthenticated ErrorCode = "unauthenticated"
	ErrorUnavailable ErrorCode = "unavailable"
	ErrorTimeout ErrorCode = "timeout"
	ErrorCancelled ErrorCode = "cancelled"
	ErrorResultUnknown ErrorCode = "result_unknown"
	ErrorInternal ErrorCode = "internal"
	ErrorVersionIncompatible ErrorCode = "version_incompatible"
)

func (c ErrorCode) String() string { return string(c) }
func (c ErrorCode) Valid() bool {
	switch c {
	case ErrorInvalidArgument, ErrorNotFound, ErrorConflict, ErrorDenied, ErrorUnauthenticated, ErrorUnavailable, ErrorTimeout, ErrorCancelled, ErrorResultUnknown, ErrorInternal, ErrorVersionIncompatible:
		return true
	default:
		return false
	}
}

func ValidDetailCode(value string) bool {
	return value == "" || len(value) <= 128 && !strings.ContainsAny(value, " \r\n\x00")
}

type Error struct {
	code ErrorCode
	detail string
}

func NewTestError(code ErrorCode, detail string) *Error { return &Error{code: code, detail: detail} }
func (e *Error) Error() string { return "classified Provider secret" }
func (e *Error) Code() ErrorCode { return e.code }
func (e *Error) DetailCode() string { return e.detail }

type SemanticError struct { code string }
func NewTestSemanticError(code string) *SemanticError { return &SemanticError{code: code} }
func (e *SemanticError) Error() string { return "semantic Provider secret" }
func (e *SemanticError) SemanticErrorCode() string { return e.code }

type PanickingSemanticError struct{}
func NewTestPanickingSemanticError() *PanickingSemanticError { return &PanickingSemanticError{} }
func (*PanickingSemanticError) Error() string { return "panicking semantic Provider secret" }
func (*PanickingSemanticError) SemanticErrorCode() string { panic("semantic Provider secret") }
`))
	writeGeneratedFile(t, root, "kernel/invocation/handle.go", []byte(`package invocation

import "context"

type Handle[Request, Response any] struct {
	available bool
	invoke func(context.Context, Request) (Response, error)
}

func NewTestHandle[Request, Response any](available bool, invoke func(context.Context, Request) (Response, error)) Handle[Request, Response] {
	return Handle[Request, Response]{available: available, invoke: invoke}
}

func (h Handle[Request, Response]) Available() bool { return h.available }

func (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {
	if h.invoke == nil {
		var response Response
		return response, context.Canceled
	}
	return h.invoke(ctx, request)
}
`))
}

func runGeneratedGoTests(t testing.TB, root string) {
	t.Helper()
	command := exec.CommandContext(t.Context(), "go", "test", "-count=1", "./...")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated invocation tests: %v\n%s", err, output)
	}
}
