package generation_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
)

func TestNormalizeOutputBuildsTypedImmutableGenerationNodes(t *testing.T) {
	t.Parallel()

	context := generatedNodeContext(t)
	output := generation.Output{Contributions: []generation.Contribution{validGeneratedNodeContribution(t)}}
	normalized, err := generation.NormalizeOutput(context, output)
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	contributions := normalized.Contributions()
	if len(contributions) != 1 {
		t.Fatalf("Contributions = %#v", contributions)
	}
	nodes := contributions[0].Nodes()
	wantKinds := []generation.GeneratedNodeKind{
		generation.GeneratedNodeKindContextDerivation,
		generation.GeneratedNodeKindCapabilityCall,
		generation.GeneratedNodeKindConditionalFailure,
		generation.GeneratedNodeKindContextDerivation,
		generation.GeneratedNodeKindMetadataAttachment,
		generation.GeneratedNodeKindAuditEventCall,
	}
	gotKinds := make([]generation.GeneratedNodeKind, len(nodes))
	for index, node := range nodes {
		gotKinds[index] = node.Kind()
	}
	if !slices.Equal(gotKinds, wantKinds) {
		t.Fatalf("node kinds = %v, want %v", gotKinds, wantKinds)
	}
	call, ok := nodes[1].CapabilityCall()
	if !ok || call.Capability.String() != "authn.session.verify/v1" || call.OnError != generation.GeneratedCallCapture || call.TimeoutMilliseconds != 1500 || len(call.Request) != 1 || call.Request[0].Field != "token" {
		t.Fatalf("capability call = %#v, %v", call, ok)
	}
	target, ok := call.Request[0].Target()
	if !ok || target.Type() != generation.GeneratedValueString || target.Items() != "" || !target.Required() || target.Enumerated() {
		t.Fatalf("capability call target = %#v, %v", target, ok)
	}
	if _, ok := output.Contributions[0].Nodes[1].CapabilityCall.Request[0].Target(); ok {
		t.Fatal("raw extension binding exposed a normalized target")
	}
	shape, ok := call.Request[0].Value.Shape()
	if !ok || shape.Type() != generation.GeneratedValueString || shape.Items() != "" || shape.Optional() || shape.Error() || !shape.Sensitive() || shape.Enumerated() {
		t.Fatalf("capability call value shape = %#v, %v", shape, ok)
	}
	if _, ok := output.Contributions[0].Nodes[1].CapabilityCall.Request[0].Value.Shape(); ok {
		t.Fatal("raw extension value exposed a normalized shape")
	}
	failure, ok := nodes[2].ConditionalFailure()
	if !ok || failure.Condition.Operator != generation.GeneratedConditionError || failure.ErrorCode != "invalid_state" || failure.Condition.Value.Node == nil || failure.Condition.Value.Node.ID != "verify-session" {
		t.Fatalf("conditional failure = %#v, %v", failure, ok)
	}
	errorShape, ok := failure.Condition.Value.Shape()
	if !ok || !errorShape.Error() || !errorShape.Optional() || errorShape.Type() != "" {
		t.Fatalf("conditional error shape = %#v, %v", errorShape, ok)
	}
	contextNode, ok := nodes[3].ContextDerivation()
	if !ok || contextNode.Key != "authn.verified-session" || contextNode.Type != generation.GeneratedValueObject || contextNode.Presence != generation.GeneratedContextRequired || !contextNode.Sensitive {
		t.Fatalf("context derivation = %#v, %v", contextNode, ok)
	}
	metadata, ok := nodes[4].MetadataAttachment()
	if !ok || metadata.Key != "authn.space-id" || metadata.MaximumBytes != 128 {
		t.Fatalf("metadata attachment = %#v, %v", metadata, ok)
	}
	audit, ok := nodes[5].AuditEventCall()
	if !ok || audit.Event != "authn.session-verified" || audit.Capability.String() != "audit.write/v1" || audit.OnError != generation.GeneratedCallContinue || len(audit.Request) != 2 || audit.Request[0].Field != "event" || audit.Request[1].Field != "space_id" {
		t.Fatalf("audit event call = %#v, %v", audit, ok)
	}
	if audit.Request[0].Value.Literal == nil || audit.Request[0].Value.Literal.String == nil || *audit.Request[0].Value.Literal.String != "authn.session-verified" {
		t.Fatalf("audit event literal = %#v", audit.Request[0].Value)
	}
	if _, ok := nodes[0].CapabilityCall(); ok {
		t.Fatal("ContextDerivation node exposed a CapabilityCall")
	}

	// Mutate every returned layer and the original construction form. None may
	// alter the normalized protocol state or digest.
	digest := normalized.Digest()
	nodes[0] = generation.NormalizedGeneratedNode{}
	call.Request[0].Field = "changed"
	audit.Request[0].Field = "changed"
	*audit.Request[0].Value.Literal.String = "changed"
	output.Contributions[0].Nodes[0].ContextDerivation.Key = "changed.key"
	output.Contributions[0].Nodes[1].CapabilityCall.Request[0].Value.Node.ID = "changed"
	*output.Contributions[0].Nodes[5].AuditEventCall.Request[1].Value.Literal.String = "changed"
	freshNodes := normalized.Contributions()[0].Nodes()
	freshCall, _ := freshNodes[1].CapabilityCall()
	freshAudit, _ := freshNodes[5].AuditEventCall()
	if freshNodes[0].ID() != "require-credential" || freshCall.Request[0].Field != "token" || freshCall.Request[0].Value.Node.ID != "require-credential" || freshAudit.Request[0].Field != "event" || *freshAudit.Request[0].Value.Literal.String != "authn.session-verified" || normalized.Digest() != digest {
		t.Fatal("normalized generated nodes exposed or retained mutable storage")
	}

	// Binding order is structural, while node order remains semantic.
	equivalent := generation.Output{Contributions: []generation.Contribution{validGeneratedNodeContribution(t)}}
	auditBindings := equivalent.Contributions[0].Nodes[5].AuditEventCall.Request
	equivalent.Contributions[0].Nodes[5].AuditEventCall.Request = []generation.GeneratedFieldBinding{auditBindings[1], auditBindings[0]}
	second, err := generation.NormalizeOutput(context, equivalent)
	if err != nil || !bytes.Equal(second.CanonicalJSON(), normalized.CanonicalJSON()) || second.Digest() != normalized.Digest() {
		t.Fatalf("equivalent node normalization = %s, %q, %v", second.CanonicalJSON(), second.Digest(), err)
	}

	payload, err := json.Marshal(equivalent)
	if err != nil {
		t.Fatalf("Marshal(Output): %v", err)
	}
	var decoded generation.Output
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("Unmarshal(Output): %v", err)
	}
	roundTrip, err := generation.NormalizeOutput(context, decoded)
	if err != nil || !bytes.Equal(roundTrip.CanonicalJSON(), normalized.CanonicalJSON()) || roundTrip.Digest() != normalized.Digest() {
		t.Fatalf("round-trip node normalization = %s, %q, %v", roundTrip.CanonicalJSON(), roundTrip.Digest(), err)
	}

	reordered := generation.Output{Contributions: []generation.Contribution{validGeneratedNodeContribution(t)}}
	nodesToReorder := reordered.Contributions[0].Nodes
	nodesToReorder[4], nodesToReorder[5] = nodesToReorder[5], nodesToReorder[4]
	third, err := generation.NormalizeOutput(context, reordered)
	if err != nil {
		t.Fatalf("NormalizeOutput(reordered independent sinks): %v", err)
	}
	if bytes.Equal(third.CanonicalJSON(), normalized.CanonicalJSON()) || third.Digest() == normalized.Digest() {
		t.Fatal("semantic node order did not participate in canonical output")
	}
}

func TestNormalizeOutputValidatesGeneratedNodeTypesReferencesBoundsAndFailures(t *testing.T) {
	t.Parallel()

	order := mustCapabilityID(t, "order.create/v1")
	verify := mustCapabilityID(t, "authn.session.verify/v1")
	audit := mustCapabilityID(t, "audit.write/v1")
	health := mustCapabilityID(t, "kernel.health/v1")
	profile := mustCapabilityID(t, "profile.get/v1")
	missing := mustCapabilityID(t, "missing.operation/v1")
	valid := validGeneratedNodeContribution(t)

	tests := []struct {
		name string
		edit func(*generation.Contribution)
		want string
	}{
		{name: "zero operation", edit: func(c *generation.Contribution) { c.Nodes = []generation.GeneratedNode{{ID: "empty"}} }, want: "exactly one generated operation"},
		{name: "multiple operations", edit: func(c *generation.Contribution) { c.Nodes[0].CapabilityCall = &generation.GeneratedCapabilityCall{} }, want: "exactly one generated operation"},
		{name: "duplicate node ID", edit: func(c *generation.Contribution) { c.Nodes[1].ID = c.Nodes[0].ID }, want: "duplicates earlier node"},
		{name: "invalid node ID", edit: func(c *generation.Contribution) { c.Nodes[0].ID = "Invalid_Node" }, want: "stable lower-kebab"},
		{name: "too many nodes", edit: func(c *generation.Contribution) { c.Nodes = make([]generation.GeneratedNode, 4097) }, want: "maximum is 4096"},
		{name: "unknown backward reference", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.Request[0].Value.Node.ID = "unknown" }, want: "must reference an earlier node"},
		{name: "forward reference", edit: func(c *generation.Contribution) {
			c.Nodes[0].ContextDerivation.Value = nodeGeneratedValue("verify-session", generation.GeneratedNodeResponse, "verified")
		}, want: "must reference an earlier node"},
		{name: "empty value union", edit: func(c *generation.Contribution) { c.Nodes[0].ContextDerivation.Value = generation.GeneratedValue{} }, want: "exactly one typed value source"},
		{name: "multiple value union", edit: func(c *generation.Contribution) {
			c.Nodes[0].ContextDerivation.Value.Literal = generation.StringValue("token").Literal
		}, want: "exactly one typed value source"},
		{name: "empty literal union", edit: func(c *generation.Contribution) {
			c.Nodes[5].AuditEventCall.Request[0].Value = generation.GeneratedValue{Literal: &generation.GeneratedLiteral{}}
		}, want: "exactly one scalar literal"},
		{name: "multiple literal union", edit: func(c *generation.Contribution) {
			value := int64(1)
			c.Nodes[5].AuditEventCall.Request[1].Value.Literal.Integer = &value
		}, want: "exactly one scalar literal"},
		{name: "long literal", edit: func(c *generation.Contribution) {
			c.Nodes[5].AuditEventCall.Request[0].Value = generation.StringValue(strings.Repeat("x", 4097))
		}, want: "at most 4096 bytes"},
		{name: "nul literal", edit: func(c *generation.Contribution) {
			c.Nodes[5].AuditEventCall.Request[0].Value = generation.StringValue("unsafe\x00value")
		}, want: "contain no NUL"},
		{name: "invalid UTF-8 literal", edit: func(c *generation.Contribution) {
			c.Nodes[5].AuditEventCall.Request[0].Value = generation.StringValue(string([]byte{0xff}))
		}, want: "valid UTF-8"},
		{name: "unknown call capability", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.Capability = missing }, want: "not a current required canonical Capability"},
		{name: "unrequired call capability", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.Capability = profile }, want: "not a current required canonical Capability"},
		{name: "missing required call field", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.Request = nil }, want: "omits required target request field"},
		{name: "unknown call field", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.Request[0].Field = "unknown" }, want: "not declared by the target"},
		{name: "duplicate call field", edit: func(c *generation.Contribution) {
			c.Nodes[1].CapabilityCall.Request = append(c.Nodes[1].CapabilityCall.Request, c.Nodes[1].CapabilityCall.Request[0])
		}, want: "duplicates binding"},
		{name: "too many call fields", edit: func(c *generation.Contribution) {
			c.Nodes[1].CapabilityCall.Request = make([]generation.GeneratedFieldBinding, 257)
		}, want: "maximum is 256"},
		{name: "call type mismatch", edit: func(c *generation.Contribution) {
			c.Nodes[1].CapabilityCall.Request[0].Value = generation.BooleanValue(true)
		}, want: "requires string"},
		{name: "optional call source", edit: func(c *generation.Contribution) {
			c.Nodes[1].CapabilityCall.Request[0].Value = invocationGeneratedValue(generation.GeneratedInvocationAdapterCredential, "authorization", "", "")
		}, want: "may be absent"},
		{name: "invalid call timeout", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.TimeoutMilliseconds = 0 }, want: "must be between 1"},
		{name: "oversized call timeout", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.TimeoutMilliseconds = 300001 }, want: "must be between 1"},
		{name: "invalid call failure mode", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.OnError = generation.GeneratedCallContinue }, want: "on_error"},
		{name: "unhandled captured failure", edit: func(c *generation.Contribution) { c.Nodes = slices.Delete(c.Nodes, 2, 4) }, want: "captures failure without"},
		{name: "captured response before failure", edit: func(c *generation.Contribution) { c.Nodes[2], c.Nodes[3] = c.Nodes[3], c.Nodes[2] }, want: "before an is-error"},
		{name: "fail-closed error reference", edit: func(c *generation.Contribution) {
			c.Nodes[1].CapabilityCall.OnError = generation.GeneratedCallFailClosed
		}, want: "references error from fail-closed"},
		{name: "invalid context key owner", edit: func(c *generation.Contribution) { c.Nodes[0].ContextDerivation.Key = "authz.credential" }, want: "extensions.authn ownership"},
		{name: "duplicate context key", edit: func(c *generation.Contribution) { c.Nodes[3].ContextDerivation.Key = c.Nodes[0].ContextDerivation.Key }, want: "duplicates context key"},
		{name: "invalid context type", edit: func(c *generation.Contribution) { c.Nodes[0].ContextDerivation.Type = "bytes" }, want: ".type \"bytes\" is not supported"},
		{name: "items on scalar context", edit: func(c *generation.Contribution) { c.Nodes[0].ContextDerivation.Items = generation.GeneratedValueString }, want: "items is valid only"},
		{name: "context type mismatch", edit: func(c *generation.Contribution) { c.Nodes[0].ContextDerivation.Type = generation.GeneratedValueBoolean }, want: "value has type sensitive optional string, want boolean"},
		{name: "invalid context presence", edit: func(c *generation.Contribution) { c.Nodes[0].ContextDerivation.Presence = "sometimes" }, want: ".presence"},
		{name: "zero context bound", edit: func(c *generation.Contribution) { c.Nodes[0].ContextDerivation.MaximumBytes = 0 }, want: ".maximum_bytes"},
		{name: "oversized context bound", edit: func(c *generation.Contribution) { c.Nodes[0].ContextDerivation.MaximumBytes = 65537 }, want: ".maximum_bytes"},
		{name: "literal exceeds context bound", edit: func(c *generation.Contribution) {
			c.Nodes[0].ContextDerivation.Value = generation.StringValue("credential")
			c.Nodes[0].ContextDerivation.MaximumBytes = 2
		}, want: "exceeding maximum_bytes"},
		{name: "wrong condition type", edit: func(c *generation.Contribution) {
			c.Nodes[2].ConditionalFailure.Condition.Operator = generation.GeneratedConditionFalse
			c.Nodes[2].ConditionalFailure.Condition.Value = generation.StringValue("false")
		}, want: "requires a present boolean"},
		{name: "missing condition on present value", edit: func(c *generation.Contribution) {
			c.Nodes[2].ConditionalFailure.Condition.Operator = generation.GeneratedConditionMissing
			c.Nodes[2].ConditionalFailure.Condition.Value = generation.StringValue("present")
		}, want: "requires an optional"},
		{name: "invalid condition operator", edit: func(c *generation.Contribution) { c.Nodes[2].ConditionalFailure.Condition.Operator = "equals" }, want: "is not supported"},
		{name: "undeclared failure error", edit: func(c *generation.Contribution) { c.Nodes[2].ConditionalFailure.ErrorCode = "permission_denied" }, want: "is not declared by source Capability"},
		{name: "invalid failure error", edit: func(c *generation.Contribution) { c.Nodes[2].ConditionalFailure.ErrorCode = "Invalid" }, want: "canonical lower snake"},
		{name: "empty failure message", edit: func(c *generation.Contribution) { c.Nodes[2].ConditionalFailure.Message = "" }, want: ".message must be non-empty"},
		{name: "nul failure message", edit: func(c *generation.Contribution) { c.Nodes[2].ConditionalFailure.Message = "unsafe\x00message" }, want: "contain no NUL"},
		{name: "invalid metadata key owner", edit: func(c *generation.Contribution) { c.Nodes[4].MetadataAttachment.Key = "audit.space-id" }, want: "extensions.authn ownership"},
		{name: "optional metadata value", edit: func(c *generation.Contribution) {
			c.Nodes[4].MetadataAttachment.Value = invocationGeneratedValue(generation.GeneratedInvocationAdapterCredential, "authorization", "", "")
		}, want: "present scalar"},
		{name: "derived credential metadata", edit: func(c *generation.Contribution) {
			c.Nodes[4].MetadataAttachment.Value = nodeGeneratedValue("require-credential", generation.GeneratedNodeDerived, "")
		}, want: "present scalar"},
		{name: "object metadata value", edit: func(c *generation.Contribution) {
			c.Nodes[4].MetadataAttachment.Value = invocationGeneratedValue(generation.GeneratedInvocationRequestField, "", "", "")
		}, want: "must identify one canonical field"},
		{name: "zero metadata bound", edit: func(c *generation.Contribution) { c.Nodes[4].MetadataAttachment.MaximumBytes = 0 }, want: ".maximum_bytes"},
		{name: "literal exceeds metadata bound", edit: func(c *generation.Contribution) {
			c.Nodes[4].MetadataAttachment.Value = generation.StringValue("space")
			c.Nodes[4].MetadataAttachment.MaximumBytes = 2
		}, want: "exceeding maximum_bytes"},
		{name: "duplicate metadata key", edit: func(c *generation.Contribution) {
			duplicate := *c.Nodes[4].MetadataAttachment
			c.Nodes = append(c.Nodes[:5], append([]generation.GeneratedNode{{ID: "duplicate-metadata", MetadataAttachment: &duplicate}}, c.Nodes[5:]...)...)
		}, want: "duplicates metadata key"},
		{name: "invalid audit event", edit: func(c *generation.Contribution) { c.Nodes[5].AuditEventCall.Event = "Invalid_Event" }, want: ".event"},
		{name: "intrinsic audit target", edit: func(c *generation.Contribution) { c.Nodes[5].AuditEventCall.Capability = health }, want: "explicit audit events must use an ordinary"},
		{name: "invalid audit failure mode", edit: func(c *generation.Contribution) { c.Nodes[5].AuditEventCall.OnError = generation.GeneratedCallCapture }, want: ".on_error"},
		{name: "invalid audit timeout", edit: func(c *generation.Contribution) { c.Nodes[5].AuditEventCall.TimeoutMilliseconds = 0 }, want: ".timeout_ms"},
		{name: "audit type mismatch", edit: func(c *generation.Contribution) {
			c.Nodes[5].AuditEventCall.Request[0].Value = generation.IntegerValue(1)
		}, want: "requires string"},
		{name: "sensitive audit field", edit: func(c *generation.Contribution) {
			c.Nodes[5].AuditEventCall.Request[0].Value = nodeGeneratedValue("require-credential", generation.GeneratedNodeDerived, "")
		}, want: "sensitive and cannot enter an audit-event request"},
		{name: "response before dispatch", edit: func(c *generation.Contribution) {
			c.Nodes[4].MetadataAttachment.Value = invocationGeneratedValue(generation.GeneratedInvocationResponseField, "order_id", "", "")
		}, want: "unavailable before canonical dispatch"},
		{name: "invocation error before dispatch", edit: func(c *generation.Contribution) {
			c.Nodes[2].ConditionalFailure.Condition.Value = invocationGeneratedValue(generation.GeneratedInvocationError, "", "", "")
		}, want: "unavailable before canonical dispatch"},
		{name: "explicit type on request field", edit: func(c *generation.Contribution) {
			c.Nodes[4].MetadataAttachment.Value = invocationGeneratedValue(generation.GeneratedInvocationRequestField, "space_id", generation.GeneratedValueString, "")
		}, want: "type is inferred"},
		{name: "explicit sensitivity on request field", edit: func(c *generation.Contribution) {
			c.Nodes[4].MetadataAttachment.Value = generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{Source: generation.GeneratedInvocationRequestField, Name: "space_id", Sensitive: true}}
		}, want: "type is inferred"},
		{name: "external sensitive context metadata", edit: func(c *generation.Contribution) {
			c.Nodes[4].MetadataAttachment.Value = generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{Source: generation.GeneratedInvocationContextValue, Name: "authn.external-secret", Type: generation.GeneratedValueString, Sensitive: true}}
		}, want: "present scalar"},
		{name: "invalid adapter credential name", edit: func(c *generation.Contribution) {
			c.Nodes[0].ContextDerivation.Value = invocationGeneratedValue(generation.GeneratedInvocationAdapterCredential, "Authorization", "", "")
		}, want: "canonical credential name"},
		{name: "invalid node output", edit: func(c *generation.Contribution) {
			c.Nodes[2].ConditionalFailure.Condition.Value.Node.Output = generation.GeneratedNodeDerived
		}, want: "not produced by capability-call"},
		{name: "field on error output", edit: func(c *generation.Contribution) { c.Nodes[2].ConditionalFailure.Condition.Value.Node.Field = "message" }, want: "must be empty for an error"},
		{name: "field on derived output", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.Request[0].Value.Node.Field = "value" }, want: "without a field"},
		{name: "ordinary call target remains valid", edit: func(c *generation.Contribution) { c.Nodes[1].CapabilityCall.Capability = verify }, want: ""},
		{name: "audit target remains valid", edit: func(c *generation.Contribution) { c.Nodes[5].AuditEventCall.Capability = audit }, want: ""},
		{name: "source remains valid", edit: func(c *generation.Contribution) { c.Source = order }, want: ""},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contribution := cloneContributionForTest(t, valid)
			test.edit(&contribution)
			_, err := generation.NormalizeOutput(generatedNodeContext(t), generation.Output{Contributions: []generation.Contribution{contribution}})
			if test.want == "" {
				if err != nil {
					t.Fatalf("NormalizeOutput: %v", err)
				}
				return
			}
			if !errors.Is(err, generation.ErrInvalidOutput) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NormalizeOutput error = %v, want ErrInvalidOutput containing %q", err, test.want)
			}
		})
	}
}

func TestNormalizeOutputSupportsCompletionPointResponseAndErrorConditions(t *testing.T) {
	t.Parallel()

	order := mustCapabilityID(t, "order.create/v1")
	contribution := generation.Contribution{
		ID:        "audit.completion",
		Namespace: "audit",
		Source:    order,
		Point:     generation.GenerationPointInvocationComplete,
		Nodes: []generation.GeneratedNode{
			{
				ID: "fail-on-dispatch-error",
				ConditionalFailure: &generation.GeneratedConditionalFailure{
					Condition: generation.GeneratedCondition{Operator: generation.GeneratedConditionError, Value: invocationGeneratedValue(generation.GeneratedInvocationError, "", "", "")},
					ErrorCode: "invalid_state",
					Message:   "Canonical dispatch failed.",
				},
			},
			{
				ID: "attach-order",
				MetadataAttachment: &generation.GeneratedMetadataAttachment{
					Key:          "audit.order-id",
					Value:        invocationGeneratedValue(generation.GeneratedInvocationResponseField, "order_id", "", ""),
					MaximumBytes: 128,
				},
			},
		},
	}
	normalized, err := generation.NormalizeOutput(generatedNodeContext(t), generation.Output{Contributions: []generation.Contribution{contribution}})
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	if got := normalized.Contributions()[0].Nodes(); len(got) != 2 || got[0].Kind() != generation.GeneratedNodeKindConditionalFailure || got[1].Kind() != generation.GeneratedNodeKindMetadataAttachment {
		t.Fatalf("nodes = %#v", got)
	}
	unnamed := cloneContributionForTest(t, contribution)
	unnamed.Nodes[1].MetadataAttachment.Value = invocationGeneratedValue(generation.GeneratedInvocationResponseField, "", "", "")
	if _, err := generation.NormalizeOutput(generatedNodeContext(t), generation.Output{Contributions: []generation.Contribution{unnamed}}); !errors.Is(err, generation.ErrInvalidOutput) || !strings.Contains(err.Error(), "must identify one canonical field") {
		t.Fatalf("unnamed response field error = %v", err)
	}
	contribution.Nodes[0], contribution.Nodes[1] = contribution.Nodes[1], contribution.Nodes[0]
	if _, err := generation.NormalizeOutput(generatedNodeContext(t), generation.Output{Contributions: []generation.Contribution{contribution}}); !errors.Is(err, generation.ErrInvalidOutput) || !strings.Contains(err.Error(), "response before an is-error conditional-failure") {
		t.Fatalf("unhandled completion response error = %v", err)
	}
}

func TestNormalizeOutputPreservesFalseAndZeroGeneratedLiterals(t *testing.T) {
	t.Parallel()

	order := mustCapabilityID(t, "order.create/v1")
	output := generation.Output{Contributions: []generation.Contribution{{
		ID:        "authn.literals",
		Namespace: "authn",
		Source:    order,
		Point:     generation.GenerationPointInvocationPrepare,
		Nodes: []generation.GeneratedNode{
			{ID: "attach-false", MetadataAttachment: &generation.GeneratedMetadataAttachment{Key: "authn.boolean", Value: generation.BooleanValue(false), MaximumBytes: 5}},
			{ID: "attach-zero", MetadataAttachment: &generation.GeneratedMetadataAttachment{Key: "authn.integer", Value: generation.IntegerValue(0), MaximumBytes: 1}},
		},
	}}}
	normalized, err := generation.NormalizeOutput(generatedNodeContext(t), output)
	if err != nil {
		t.Fatalf("NormalizeOutput: %v", err)
	}
	canonical := normalized.CanonicalJSON()
	if !bytes.Contains(canonical, []byte(`"boolean":false`)) || !bytes.Contains(canonical, []byte(`"integer":0`)) {
		t.Fatalf("canonical literals = %s", canonical)
	}
	var decoded generation.Output
	if err := json.Unmarshal(canonical, &decoded); err != nil {
		t.Fatalf("Unmarshal canonical output: %v", err)
	}
	roundTrip, err := generation.NormalizeOutput(generatedNodeContext(t), decoded)
	if err != nil || !bytes.Equal(roundTrip.CanonicalJSON(), canonical) || roundTrip.Digest() != normalized.Digest() {
		t.Fatalf("literal round trip = %s, %q, %v", roundTrip.CanonicalJSON(), roundTrip.Digest(), err)
	}
}

func TestNormalizeOutputGeneratedNodeDiagnosticsAreDeterministic(t *testing.T) {
	t.Parallel()

	order := mustCapabilityID(t, "order.create/v1")
	health := mustCapabilityID(t, "kernel.health/v1")
	audit := mustCapabilityID(t, "audit.write/v1")
	tests := []generation.Contribution{
		{
			ID:        "authn.captured",
			Namespace: "authn",
			Source:    order,
			Point:     generation.GenerationPointInvocationPrepare,
			Nodes: []generation.GeneratedNode{
				{ID: "z-call", CapabilityCall: &generation.GeneratedCapabilityCall{Capability: health, TimeoutMilliseconds: 10, OnError: generation.GeneratedCallCapture}},
				{ID: "a-call", CapabilityCall: &generation.GeneratedCapabilityCall{Capability: health, TimeoutMilliseconds: 10, OnError: generation.GeneratedCallCapture}},
			},
		},
		{
			ID:        "authn.audit",
			Namespace: "authn",
			Source:    order,
			Point:     generation.GenerationPointInvocationPrepare,
			Nodes: []generation.GeneratedNode{{
				ID:             "audit",
				AuditEventCall: &generation.GeneratedAuditEventCall{Event: "authn.test", Capability: audit, TimeoutMilliseconds: 10, OnError: generation.GeneratedCallFailClosed},
			}},
		},
	}
	wants := []string{`node "a-call" captures failure`, `omits required target request field "event"`}
	for index, contribution := range tests {
		var first string
		for iteration := 0; iteration < 100; iteration++ {
			_, err := generation.NormalizeOutput(generatedNodeContext(t), generation.Output{Contributions: []generation.Contribution{contribution}})
			if !errors.Is(err, generation.ErrInvalidOutput) || !strings.Contains(err.Error(), wants[index]) {
				t.Fatalf("case %d iteration %d error = %v, want %q", index, iteration, err, wants[index])
			}
			if first == "" {
				first = err.Error()
			} else if err.Error() != first {
				t.Fatalf("case %d diagnostic changed: %q then %q", index, first, err)
			}
		}
	}
}

func FuzzNormalizeGeneratedNodesJSON(f *testing.F) {
	valid := generation.Output{Contributions: []generation.Contribution{validGeneratedNodeContributionForSeed()}}
	encoded, err := json.Marshal(valid)
	if err != nil {
		f.Fatalf("Marshal seed: %v", err)
	}
	for _, seed := range []string{
		string(encoded),
		`{"contributions":[{"id":"authn.node","namespace":"authn","source":"order.create/v1","point":"invocation.prepare","nodes":[{"id":"empty"}]}]}`,
		`{"contributions":[{"id":"authn.node","namespace":"authn","source":"order.create/v1","point":"invocation.prepare","nodes":[{"id":"value","metadata_attachment":{"key":"authn.value","value":{"literal":{"boolean":false}},"maximum_bytes":8}}]}]}`,
		`{"contributions":[{"id":"authn.node","namespace":"authn","source":"order.create/v1","point":"invocation.prepare","nodes":[{"id":"value","metadata_attachment":{"key":"authn.value","value":{"literal":{"integer":0}},"maximum_bytes":8}}]}]}`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload string) {
		if len(payload) > 1<<20 {
			return
		}
		var output generation.Output
		if err := json.Unmarshal([]byte(payload), &output); err != nil {
			return
		}
		context := generatedNodeContext(t)
		first, firstErr := generation.NormalizeOutput(context, output)
		second, secondErr := generation.NormalizeOutput(context, output)
		if (firstErr == nil) != (secondErr == nil) {
			t.Fatalf("NormalizeOutput changed result: %v then %v", firstErr, secondErr)
		}
		if firstErr != nil {
			if !errors.Is(firstErr, generation.ErrInvalidOutput) || !errors.Is(secondErr, generation.ErrInvalidOutput) {
				t.Fatalf("NormalizeOutput errors = %v and %v", firstErr, secondErr)
			}
			return
		}
		if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) || first.Digest() != second.Digest() {
			t.Fatal("generated node output is nondeterministic")
		}
	})
}

func validGeneratedNodeContribution(t *testing.T) generation.Contribution {
	t.Helper()
	return validGeneratedNodeContributionIDs(
		mustCapabilityID(t, "order.create/v1"),
		mustCapabilityID(t, "authn.session.verify/v1"),
		mustCapabilityID(t, "audit.write/v1"),
	)
}

func validGeneratedNodeContributionForSeed() generation.Contribution {
	order, _ := generation.ParseCapabilityID("order.create/v1")
	verify, _ := generation.ParseCapabilityID("authn.session.verify/v1")
	audit, _ := generation.ParseCapabilityID("audit.write/v1")
	return validGeneratedNodeContributionIDs(order, verify, audit)
}

func validGeneratedNodeContributionIDs(order, verify, audit generation.CapabilityID) generation.Contribution {
	return generation.Contribution{
		ID:        "authn.verify",
		Namespace: "authn",
		Source:    order,
		Point:     generation.GenerationPointInvocationPrepare,
		Provides:  []generation.ContributionToken{"verified-authn-context"},
		Nodes: []generation.GeneratedNode{
			{
				ID: "require-credential",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "authn.credential",
					Value:        invocationGeneratedValue(generation.GeneratedInvocationAdapterCredential, "authorization", "", ""),
					Type:         generation.GeneratedValueString,
					Presence:     generation.GeneratedContextRequired,
					MaximumBytes: 4096,
				},
			},
			{
				ID: "verify-session",
				CapabilityCall: &generation.GeneratedCapabilityCall{
					Capability:          verify,
					Request:             []generation.GeneratedFieldBinding{{Field: "token", Value: nodeGeneratedValue("require-credential", generation.GeneratedNodeDerived, "")}},
					TimeoutMilliseconds: 1500,
					OnError:             generation.GeneratedCallCapture,
				},
			},
			{
				ID: "reject-verification-error",
				ConditionalFailure: &generation.GeneratedConditionalFailure{
					Condition: generation.GeneratedCondition{Operator: generation.GeneratedConditionError, Value: nodeGeneratedValue("verify-session", generation.GeneratedNodeError, "")},
					ErrorCode: "invalid_state",
					Message:   "Session verification failed.",
				},
			},
			{
				ID: "store-verified-session",
				ContextDerivation: &generation.GeneratedContextDerivation{
					Key:          "authn.verified-session",
					Value:        nodeGeneratedValue("verify-session", generation.GeneratedNodeResponse, "verified"),
					Type:         generation.GeneratedValueObject,
					Presence:     generation.GeneratedContextRequired,
					Sensitive:    true,
					MaximumBytes: 65536,
				},
			},
			{
				ID: "attach-space",
				MetadataAttachment: &generation.GeneratedMetadataAttachment{
					Key:          "authn.space-id",
					Value:        invocationGeneratedValue(generation.GeneratedInvocationRequestField, "space_id", "", ""),
					MaximumBytes: 128,
				},
			},
			{
				ID: "record-verification",
				AuditEventCall: &generation.GeneratedAuditEventCall{
					Event:      "authn.session-verified",
					Capability: audit,
					Request: []generation.GeneratedFieldBinding{
						{Field: "space_id", Value: invocationGeneratedValue(generation.GeneratedInvocationRequestField, "space_id", "", "")},
						{Field: "event", Value: generation.StringValue("authn.session-verified")},
					},
					TimeoutMilliseconds: 500,
					OnError:             generation.GeneratedCallContinue,
				},
			},
		},
	}
}

func generatedNodeContext(t *testing.T) generation.Context {
	t.Helper()
	input := generation.Input{
		Plugins: []generation.PluginInput{
			{ID: "acme.orders", ModulePath: "github.com/acme/app", Provides: []string{"order.create/v1"}},
			{ID: "plystra.authn.default", ModulePath: "github.com/plystra/authn", Provides: []string{"authn.session.verify/v1"}},
			{ID: "plystra.authz.default", ModulePath: "github.com/plystra/authz", Provides: []string{"authz.check/v1"}},
			{ID: "acme.audit", ModulePath: "github.com/acme/audit", Provides: []string{"audit.write/v1"}},
		},
		Capabilities: []generation.CapabilityInput{
			{ContractJSON: json.RawMessage(`{"id":"order.create/v1","request":{"dry_run":{"type":"boolean"},"space_id":{"type":"string","required":true}},"response":{"order_id":{"type":"string","required":true}},"errors":["invalid_state"],"semantics":` + querySemanticsJSON + `,"extensions":{"audit":{"event":"order.create"},"authn":{"authenticated":true},"authz":{"permission":"order.create","space":"request.space_id"}}}`), Exposure: generation.Exposure{Go: true, HTTP: true}},
			{ContractJSON: json.RawMessage(`{"id":"authn.session.verify/v1","request":{"token":{"type":"string","required":true}},"response":{"verified":{"type":"object","required":true}},"errors":["invalid_credentials"],"semantics":` + querySemanticsJSON + `}`), Exposure: generation.Exposure{Go: true}},
			{ContractJSON: json.RawMessage(`{"id":"authz.check/v1","request":{"permission":{"type":"string","required":true},"space_id":{"type":"string","required":true}},"response":{"allowed":{"type":"boolean","required":true}},"errors":["decision_failed"],"semantics":` + querySemanticsJSON + `}`), Exposure: generation.Exposure{Go: true}},
			{ContractJSON: json.RawMessage(`{"id":"audit.write/v1","request":{"event":{"type":"string","required":true},"space_id":{"type":"string","required":true}},"response":{},"errors":["write_failed"],"semantics":` + querySemanticsJSON + `}`), Exposure: generation.Exposure{Go: true}},
			{ContractJSON: canonicalContract("kernel.health/v1", nil), Intrinsic: true, Exposure: generation.Exposure{Go: true}},
			{ContractJSON: canonicalContract("profile.get/v1", nil), Exposure: generation.Exposure{Go: true}},
		},
		Requirements: []string{"order.create/v1", "authn.session.verify/v1", "authz.check/v1", "audit.write/v1", "kernel.health/v1"},
		Providers: []generation.ProviderInput{
			{Capability: "order.create/v1", Plugin: "acme.orders"},
			{Capability: "authn.session.verify/v1", Plugin: "plystra.authn.default"},
			{Capability: "authz.check/v1", Plugin: "plystra.authz.default"},
			{Capability: "audit.write/v1", Plugin: "acme.audit"},
		},
	}
	context, err := generation.NewContext(input)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	return context
}

func invocationGeneratedValue(source generation.GeneratedInvocationValueSource, name string, typeName, items generation.GeneratedValueType) generation.GeneratedValue {
	return generation.GeneratedValue{Invocation: &generation.GeneratedInvocationValue{Source: source, Name: name, Type: typeName, Items: items}}
}

func nodeGeneratedValue(id string, output generation.GeneratedNodeOutput, field string) generation.GeneratedValue {
	return generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: id, Output: output, Field: field}}
}

func cloneContributionForTest(t *testing.T, input generation.Contribution) generation.Contribution {
	t.Helper()
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("Marshal contribution: %v", err)
	}
	var result generation.Contribution
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("Unmarshal contribution: %v", err)
	}
	return result
}

func ExampleGeneratedNode() {
	verify, _ := generation.ParseCapabilityID("authn.session.verify/v1")
	node := generation.GeneratedNode{
		ID: "verify-session",
		CapabilityCall: &generation.GeneratedCapabilityCall{
			Capability: verify,
			Request: []generation.GeneratedFieldBinding{{
				Field: "token",
				Value: generation.GeneratedValue{Node: &generation.GeneratedNodeValue{ID: "require-credential", Output: generation.GeneratedNodeDerived}},
			}},
			TimeoutMilliseconds: 1500,
			OnError:             generation.GeneratedCallCapture,
		},
	}
	fmt.Println(node.ID, node.CapabilityCall.Capability, node.CapabilityCall.OnError)
	// Output: verify-session authn.session.verify/v1 capture
}
