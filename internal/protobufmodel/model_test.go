package protobufmodel_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	generation "github.com/plystra/cli/generation/v1"
	"github.com/plystra/cli/internal/capabilitymeta"
	"github.com/plystra/cli/internal/protobufidentity"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/sdkmodel"
)

const projectionContract = `id: customer.profile.get/v1
request:
  include_history: {type: boolean}
  profile_id: {type: string, required: true}
  status: {type: string, enum: [active, suspended]}
  tags: {type: array, items: string}
response:
  found: {type: boolean, required: true}
  revision: {type: integer}
errors: [profile_unavailable, profile_missing]
extensions:
  authn: {authenticated: true}
`

const projectionCommandSemantics = `semantics:
  kind: command
  effects: external-write
  idempotency: {mode: none}
  retry: {safety: never}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`

func TestBuildNormalizesExactConnectProjectionDeterministically(t *testing.T) {
	t.Parallel()

	target := projectionTarget(t, projectionContract, generation.Exposure{Go: true, HTTP: true})
	hiddenTarget := projectionTarget(t, "id: internal.refresh/v1\n", generation.Exposure{Go: true})
	alias := projectionAlias(t, "account.profile/v1", target, generation.Exposure{HTTP: true}, "Use customer.profile.get/v1.")
	hiddenAlias := projectionAlias(t, "internal.profile/v1", target, generation.Exposure{Go: true}, "")

	model, err := protobufmodel.Build(true,
		[]protobufmodel.CanonicalTargetView{hiddenTarget, target},
		[]protobufmodel.AliasView{hiddenAlias, alias},
	)
	if err != nil || !model.Valid() || !model.Enabled() {
		t.Fatalf("Build = %#v, %v", model, err)
	}
	operations := model.Operations()
	if len(operations) != 1 || operations[0].ID().String() != "customer.profile.get/v1" || operations[0].Kind() != capabilitymeta.CapabilityKindQuery || operations[0].ContractDigest() != target.digest {
		t.Fatalf("Operations = %#v", operations)
	}
	identity := operations[0].Identity()
	if identity.Package() != "plystra.generated.customer.profile.get.v1" || identity.Service() != "CustomerProfileGetV1Service" || identity.RequestType() != "plystra.generated.customer.profile.get.v1.CustomerProfileGetV1Request" || identity.Procedure() != "/plystra.generated.customer.profile.get.v1.CustomerProfileGetV1Service/Invoke" {
		t.Fatalf("canonical identity = %#v", identity)
	}
	request := operations[0].Request()
	if got := projectionFieldNames(request); !slices.Equal(got, []string{"include_history", "profile_id", "status", "tags"}) {
		t.Fatalf("request fields = %v", got)
	}
	if request[1].Kind() != sdkmodel.KindString || !request[1].Required() || request[3].Kind() != sdkmodel.KindArray || request[3].Items() != sdkmodel.KindString {
		t.Fatalf("request projection = %#v", request)
	}
	if got := projectionRawStrings(request[2].EnumJSON()); !slices.Equal(got, []string{`"active"`, `"suspended"`}) {
		t.Fatalf("status enum = %v", got)
	}
	if got := projectionFieldNames(operations[0].Response()); !slices.Equal(got, []string{"found", "revision"}) {
		t.Fatalf("response fields = %v", got)
	}
	if got := operations[0].Errors(); !slices.Equal(got, []string{"profile_missing", "profile_unavailable"}) {
		t.Fatalf("semantic errors = %v", got)
	}
	if got := operations[0].Sources(); !slices.Equal(got, target.sources) {
		t.Fatalf("contract sources = %v", got)
	}

	aliases := model.Aliases()
	if len(aliases) != 1 || aliases[0].ID().String() != "account.profile/v1" || aliases[0].Target() != target.id || aliases[0].TargetContractDigest() != target.digest || aliases[0].Deprecated() != "Use customer.profile.get/v1." {
		t.Fatalf("Aliases = %#v", aliases)
	}
	aliasIdentity := aliases[0].Identity()
	if aliasIdentity.Package() != "plystra.generated.account.profile.v1" || aliasIdentity.RequestType() != identity.RequestType() || aliasIdentity.ResponseType() != identity.ResponseType() || aliasIdentity.Procedure() != "/plystra.generated.account.profile.v1.AccountProfileV1Service/Invoke" {
		t.Fatalf("Alias identity = %#v", aliasIdentity)
	}

	canonical := model.CanonicalJSON()
	for _, required := range []string{
		`{"version":2,"enabled":true,"operations":[{"public_id":"customer.profile.get/v1","canonical_id":"customer.profile.get/v1","kind":"query"`,
		`"contract_digest":"` + target.digest + `"`,
		`"sources":["` + target.sources[0] + `"]`,
		`"name":"profile_id","kind":"string","required":true`,
		`"name":"status","kind":"string","required":false,"enum":["active","suspended"]`,
		`"aliases":[{"public_id":"account.profile/v1","canonical_id":"customer.profile.get/v1"`,
		`"deprecated":"Use customer.profile.get/v1."`,
	} {
		if !bytes.Contains(canonical, []byte(required)) {
			t.Fatalf("CanonicalJSON %s omits %s", canonical, required)
		}
	}
	for _, forbidden := range []string{"internal.refresh", "internal.profile", "provider", "plugin", "module_path", "secret"} {
		if bytes.Contains(canonical, []byte(forbidden)) {
			t.Fatalf("CanonicalJSON contains forbidden %q: %s", forbidden, canonical)
		}
	}
	if model.Digest() != projectionDigest(canonical) {
		t.Fatalf("Digest = %q, want %q", model.Digest(), projectionDigest(canonical))
	}

	repeated, err := protobufmodel.Build(true,
		[]protobufmodel.CanonicalTargetView{target, hiddenTarget},
		[]protobufmodel.AliasView{alias, hiddenAlias},
	)
	if err != nil || !bytes.Equal(repeated.CanonicalJSON(), canonical) || repeated.Digest() != model.Digest() {
		t.Fatalf("reordered Build = %s, %q, %v", repeated.CanonicalJSON(), repeated.Digest(), err)
	}
	operations[0] = protobufmodel.Operation{}
	aliases[0] = protobufmodel.Alias{}
	request[0] = sdkmodel.Field{}
	operationSources := model.Operations()[0].Sources()
	operationSources[0] = "changed"
	enumCopy := model.Operations()[0].Request()[2].EnumJSON()
	enumCopy[0][0] = 'x'
	canonical[0] = 'x'
	if model.Operations()[0].ID().String() != "customer.profile.get/v1" || model.Operations()[0].Sources()[0] != target.sources[0] || model.Aliases()[0].ID().String() != "account.profile/v1" || string(model.Operations()[0].Request()[2].EnumJSON()[0]) != `"active"` || model.CanonicalJSON()[0] != '{' {
		t.Fatal("Model exposed mutable projection storage")
	}
}

func TestBuildDisabledProjectionIsExplicitAndIgnoresUnselectedInput(t *testing.T) {
	t.Parallel()

	model, err := protobufmodel.Build(false, []protobufmodel.CanonicalTargetView{nil}, []protobufmodel.AliasView{nil})
	if err != nil || !model.Valid() || model.Enabled() || len(model.Operations()) != 0 || len(model.Aliases()) != 0 || string(model.CanonicalJSON()) != `{"version":2,"enabled":false,"operations":[],"aliases":[]}` || model.Digest() != projectionDigest(model.CanonicalJSON()) {
		t.Fatalf("Build(disabled) = %#v, %v", model, err)
	}
}

func TestBuildNormalizesUnaryCommandAndAlias(t *testing.T) {
	t.Parallel()

	target := projectionTarget(t, `id: customer.profile.update/v1
request: {profile_id: {type: string, required: true}}
response: {updated: {type: boolean, required: true}}
errors: [update_failed]
`+projectionCommandSemantics, generation.Exposure{HTTP: true})
	alias := projectionAlias(t, "account.profile.update/v1", target, generation.Exposure{HTTP: true}, "")
	model, err := protobufmodel.Build(true, []protobufmodel.CanonicalTargetView{target}, []protobufmodel.AliasView{alias})
	if err != nil || !model.Valid() || !model.Enabled() {
		t.Fatalf("Build(command) = %#v, %v", model, err)
	}
	operations := model.Operations()
	aliases := model.Aliases()
	if len(operations) != 1 || operations[0].ID().String() != "customer.profile.update/v1" || operations[0].Kind() != capabilitymeta.CapabilityKindCommand {
		t.Fatalf("command operations = %#v", operations)
	}
	if len(aliases) != 1 || aliases[0].ID().String() != "account.profile.update/v1" || aliases[0].Target() != operations[0].ID() {
		t.Fatalf("command aliases = %#v", aliases)
	}
	if !bytes.Contains(model.CanonicalJSON(), []byte(`"kind":"command"`)) {
		t.Fatalf("command canonical projection omits typed kind: %s", model.CanonicalJSON())
	}
}

func TestBuildRejectsEventAndStreamKindsAtTheUnaryConnectBoundary(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		kind      capabilitymeta.CapabilityKind
		semantics string
	}{
		{
			kind: capabilitymeta.CapabilityKindEvent,
			semantics: `semantics:
  kind: event
  effects: external-write
  idempotency: {mode: none}
  retry: {safety: never}
  cancellation: {mode: best-effort}
  completion: {mode: accepted-for-processing}
  ordering: {mode: none}
  data: {request: public, response: public}
`,
		},
		{
			kind: capabilitymeta.CapabilityKindStream,
			semantics: `semantics:
  kind: stream
  effects: none
  idempotency: {mode: none}
  retry: {safety: never}
  cancellation: {mode: best-effort}
  completion: {mode: accepted-for-processing}
  ordering: {mode: none}
  data: {request: public, response: public}
`,
		},
	} {
		test := test
		t.Run(string(test.kind), func(t *testing.T) {
			t.Parallel()

			target := projectionTarget(t, "id: customer.profile.get/v1\n"+test.semantics, generation.Exposure{HTTP: true})
			model, err := protobufmodel.Build(true, []protobufmodel.CanonicalTargetView{target}, nil)
			if model.Valid() || !errors.Is(err, protobufmodel.ErrBuild) || !errors.Is(err, protobufmodel.ErrTarget) || !errors.Is(err, protobufmodel.ErrOperationKind) {
				t.Fatalf("Build = %#v, %v", model, err)
			}
			for _, want := range []string{
				"Capability customer.profile.get/v1",
				`semantics.kind "` + string(test.kind) + `"`,
				"requested Connect surface",
				"unary boundary",
				`semantics.kind "query"`,
				`"command"`,
				"remove customer.profile.get/v1 from http.expose",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("Build error %q omits %q", err, want)
				}
			}
		})
	}
}

func TestBuildRejectsInvalidSelectedTargetsAndAliases(t *testing.T) {
	t.Parallel()

	target := projectionTarget(t, projectionContract, generation.Exposure{HTTP: true})
	alias := projectionAlias(t, "account.profile/v1", target, generation.Exposure{HTTP: true}, "")
	missingSources := target
	missingSources.sources = nil
	for _, test := range []struct {
		name    string
		targets []protobufmodel.CanonicalTargetView
		aliases []protobufmodel.AliasView
		kind    error
		want    string
	}{
		{name: "absent target", targets: []protobufmodel.CanonicalTargetView{nil}, kind: protobufmodel.ErrTarget, want: "view is absent"},
		{name: "absent Alias", targets: []protobufmodel.CanonicalTargetView{target}, aliases: []protobufmodel.AliasView{nil}, kind: protobufmodel.ErrAlias, want: "view is absent"},
		{name: "duplicate target", targets: []protobufmodel.CanonicalTargetView{target, target}, kind: sdkmodel.ErrTarget, want: "duplicates canonical Capability"},
		{name: "duplicate Alias", targets: []protobufmodel.CanonicalTargetView{target}, aliases: []protobufmodel.AliasView{alias, alias}, kind: sdkmodel.ErrAlias, want: "duplicates Capability Alias"},
		{name: "missing contract provenance", targets: []protobufmodel.CanonicalTargetView{missingSources}, kind: protobufmodel.ErrTarget, want: "at least one source"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			model, err := protobufmodel.Build(true, test.targets, test.aliases)
			if !errors.Is(err, protobufmodel.ErrBuild) || !errors.Is(err, test.kind) || model.Valid() || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Build = %#v, %v", model, err)
			}
		})
	}
}

func TestBuildRejectsProtobufFieldIdentityCollisions(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		contract  string
		direction string
		left      string
		right     string
		kind      string
		identity  string
	}{
		{
			name: "request JSON name",
			contract: `id: naming.collision/v1
request:
  foo_1: {type: string}
  foo1: {type: string}
response: {}
errors: []
`,
			direction: "request", left: "foo1", right: "foo_1", kind: "Protobuf JSON name", identity: "foo1",
		},
		{
			name: "response enum type",
			contract: `id: naming.collision/v1
request: {}
response:
  http_status: {type: string, enum: [ready]}
  h_t_t_p_status: {type: string, enum: [waiting]}
errors: []
`,
			direction: "response", left: "h_t_t_p_status", right: "http_status", kind: "generated enum identity", identity: "plystra.generated.naming.collision.v1.NamingCollisionV1ResponseHTTPStatusEnum",
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			target := projectionTarget(t, test.contract, generation.Exposure{HTTP: true})
			model, err := protobufmodel.Build(true, []protobufmodel.CanonicalTargetView{target}, nil)
			if model.Valid() || !errors.Is(err, protobufmodel.ErrBuild) || !errors.Is(err, protobufmodel.ErrTarget) || !errors.Is(err, protobufidentity.ErrCollision) {
				t.Fatalf("Build = %#v, %v", model, err)
			}
			for _, want := range []string{"Capability naming.collision/v1", test.direction, test.left, test.right, test.kind, test.identity} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("Build error %q omits %q", err, want)
				}
			}
		})
	}
}

func TestBuildScopesProtobufFieldIdentitiesToRequestAndResponse(t *testing.T) {
	t.Parallel()

	target := projectionTarget(t, `id: naming.scoped/v1
request:
  foo1: {type: string}
  http_status: {type: string, enum: [ready]}
response:
  foo_1: {type: string}
  h_t_t_p_status: {type: string, enum: [waiting]}
errors: []
`, generation.Exposure{HTTP: true})
	model, err := protobufmodel.Build(true, []protobufmodel.CanonicalTargetView{target}, nil)
	if err != nil || !model.Valid() {
		t.Fatalf("Build = %#v, %v", model, err)
	}
}

type projectionTargetView struct {
	id       generation.CapabilityID
	contract []byte
	digest   string
	sources  []string
	exposure generation.Exposure
}

func (v projectionTargetView) ID() generation.CapabilityID   { return v.id }
func (v projectionTargetView) ContractJSON() []byte          { return append([]byte(nil), v.contract...) }
func (v projectionTargetView) ContractDigest() string        { return v.digest }
func (v projectionTargetView) Sources() []string             { return append([]string(nil), v.sources...) }
func (v projectionTargetView) Exposure() generation.Exposure { return v.exposure }

type projectionAliasView struct {
	id         generation.CapabilityID
	target     generation.CapabilityID
	digest     string
	exposure   generation.Exposure
	deprecated string
}

func (v projectionAliasView) ID() generation.CapabilityID     { return v.id }
func (v projectionAliasView) Target() generation.CapabilityID { return v.target }
func (v projectionAliasView) TargetContractDigest() string    { return v.digest }
func (v projectionAliasView) Exposure() generation.Exposure   { return v.exposure }
func (v projectionAliasView) Deprecated() string              { return v.deprecated }

func projectionTarget(t testing.TB, source string, exposure generation.Exposure) projectionTargetView {
	t.Helper()
	source = withProjectionQuerySemantics(source)
	canonical, err := capabilitymeta.NormalizeSchema([]byte(source))
	if err != nil {
		t.Fatalf("NormalizeSchema: %v", err)
	}
	var idSource struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(canonical, &idSource); err != nil {
		t.Fatalf("decode normalized ID: %v", err)
	}
	id, err := generation.ParseCapabilityID(idSource.ID)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%s): %v", idSource.ID, err)
	}
	return projectionTargetView{id: id, contract: canonical, digest: projectionDigest(canonical), sources: []string{"example.com/contracts@v1/" + id.String() + "/capability.yaml"}, exposure: exposure}
}

func withProjectionQuerySemantics(source string) string {
	if strings.Contains(source, "\nsemantics:") {
		return source
	}
	if !strings.HasSuffix(source, "\n") {
		source += "\n"
	}
	return source + `semantics:
  kind: query
  effects: none
  idempotency: {mode: inherent}
  retry: {safety: safe}
  cancellation: {mode: best-effort}
  completion: {mode: completed-before-return}
  ordering: {mode: none}
  data: {request: public, response: public}
`
}

func projectionAlias(t testing.TB, id string, target projectionTargetView, exposure generation.Exposure, deprecated string) projectionAliasView {
	t.Helper()
	aliasID, err := generation.ParseCapabilityID(id)
	if err != nil {
		t.Fatalf("ParseCapabilityID(%s): %v", id, err)
	}
	return projectionAliasView{id: aliasID, target: target.id, digest: target.digest, exposure: exposure, deprecated: deprecated}
}

func projectionFieldNames(fields []sdkmodel.Field) []string {
	result := make([]string, len(fields))
	for index, field := range fields {
		result[index] = field.Name()
	}
	return result
}

func projectionRawStrings(values []json.RawMessage) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}

func projectionDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
