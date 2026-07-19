package protobufidentity_test

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/capabilityid"
	"github.com/plystra/cli/internal/protobufidentity"
)

func TestBuildProjectsCanonicalAndAliasIdentitiesDeterministically(t *testing.T) {
	t.Parallel()

	surfaces := []protobufidentity.Surface{
		{PublicID: "customer.profile.get/v1", CanonicalID: "customer.profile.get/v1"},
		{PublicID: "billing.tax-rate/v12", CanonicalID: "billing.tax-rate/v12"},
		{PublicID: "account.enroll/v1", CanonicalID: "customer.profile.get/v1"},
	}
	first, err := protobufidentity.Build(surfaces)
	if err != nil || !first.Valid() {
		t.Fatalf("Build = %#v, %v", first, err)
	}
	reversed := append([]protobufidentity.Surface(nil), surfaces...)
	slices.Reverse(reversed)
	second, err := protobufidentity.Build(reversed)
	if err != nil || !second.Valid() {
		t.Fatalf("Build(reversed) = %#v, %v", second, err)
	}
	if first.Digest() != second.Digest() || !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) {
		t.Fatal("projection depends on input order")
	}

	identities := first.Identities()
	if got := identityPublicIDs(identities); !reflect.DeepEqual(got, []string{"account.enroll/v1", "billing.tax-rate/v12", "customer.profile.get/v1"}) {
		t.Fatalf("identity order = %v", got)
	}
	alias := identities[0]
	if alias.CanonicalID() != "customer.profile.get/v1" ||
		alias.Package() != "plystra.generated.account.enroll.v1" ||
		alias.Service() != "AccountEnrollV1Service" ||
		alias.Method() != "Invoke" ||
		alias.RequestType() != "plystra.generated.customer.profile.get.v1.CustomerProfileGetV1Request" ||
		alias.ResponseType() != "plystra.generated.customer.profile.get.v1.CustomerProfileGetV1Response" ||
		alias.Procedure() != "/plystra.generated.account.enroll.v1.AccountEnrollV1Service/Invoke" {
		t.Fatalf("Alias identity = %#v", alias)
	}
	hyphenated := identities[1]
	if hyphenated.Package() != "plystra.generated.billing.tax_h_rate.v12" || hyphenated.Service() != "BillingTaxRateV12Service" {
		t.Fatalf("hyphenated identity = %#v", hyphenated)
	}
	canonical := identities[2]
	if canonical.Service() != "CustomerProfileGetV1Service" || canonical.RequestType() != "plystra.generated.customer.profile.get.v1.CustomerProfileGetV1Request" || canonical.Procedure() != "/plystra.generated.customer.profile.get.v1.CustomerProfileGetV1Service/Invoke" {
		t.Fatalf("canonical identity = %#v", canonical)
	}

	identities[0] = protobufidentity.Identity{}
	if first.Identities()[0].PublicID() != "account.enroll/v1" {
		t.Fatal("Set exposed mutable identity storage")
	}
	canonicalJSON := first.CanonicalJSON()
	canonicalJSON[0] = 'x'
	if bytes.Equal(canonicalJSON, first.CanonicalJSON()) {
		t.Fatal("Set exposed mutable canonical JSON storage")
	}
	for _, forbidden := range []string{"module_path", "plugin", "provider", "secret"} {
		if strings.Contains(string(first.CanonicalJSON()), forbidden) {
			t.Fatalf("canonical projection contains forbidden %q: %s", forbidden, first.CanonicalJSON())
		}
	}
}

func TestPackageEncodingIsCanonicalAndReversible(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"customer.profile.get/v1",
		"billing.tax-rate/v12",
		"oauth2.device-code.exchange/v3",
		"oauth2.device--code.exchange/v3",
	} {
		identifier, err := capabilityid.Parse(value)
		if err != nil {
			t.Fatalf("Parse(%s): %v", value, err)
		}
		packageName := protobufidentity.Package(identifier)
		decoded, err := protobufidentity.DecodePackage(packageName)
		if err != nil || decoded != value {
			t.Fatalf("DecodePackage(%s) = %q, %v; want %q", packageName, decoded, err, value)
		}
	}

	for _, value := range []string{
		"generated.customer.profile.get.v1",
		"plystra.generated.customer.v1",
		"plystra.generated.customer.profile.v0",
		"plystra.generated.customer.profile.v01",
		"plystra.generated.customer.tax_rate.v1",
		"plystra.generated.customer.tax_x_rate.v1",
		"plystra.generated.Customer.profile.v1",
	} {
		if decoded, err := protobufidentity.DecodePackage(value); !errors.Is(err, protobufidentity.ErrBuild) || decoded != "" {
			t.Fatalf("DecodePackage(%q) = %q, %v", value, decoded, err)
		}
	}
}

func TestBuildRejectsInvalidDuplicateAndCrossVersionSurfaces(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		surfaces  []protobufidentity.Surface
		collision bool
		want      string
	}{
		{name: "invalid public", surfaces: []protobufidentity.Surface{{PublicID: "Customer.Profile/v1", CanonicalID: "customer.profile/v1"}}, want: "public_id"},
		{name: "invalid canonical", surfaces: []protobufidentity.Surface{{PublicID: "customer.profile/v1", CanonicalID: "Customer.Profile/v1"}}, want: "canonical_id"},
		{name: "cross version Alias", surfaces: []protobufidentity.Surface{{PublicID: "account.profile/v2", CanonicalID: "customer.profile/v1"}}, want: "same major version"},
		{name: "duplicate public", surfaces: []protobufidentity.Surface{{PublicID: "customer.profile/v1", CanonicalID: "customer.profile/v1"}, {PublicID: "customer.profile/v1", CanonicalID: "customer.profile/v1"}}, collision: true, want: "public Capability"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			set, err := protobufidentity.Build(test.surfaces)
			if !errors.Is(err, protobufidentity.ErrBuild) || set.Valid() || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Build = %#v, %v", set, err)
			}
			if test.collision && !errors.Is(err, protobufidentity.ErrCollision) {
				t.Fatalf("Build error %v does not wrap ErrCollision", err)
			}
		})
	}
}

func TestBuildEmptyProjectionIsStable(t *testing.T) {
	t.Parallel()

	set, err := protobufidentity.Build(nil)
	if err != nil || !set.Valid() || len(set.Identities()) != 0 || string(set.CanonicalJSON()) != `{"version":1,"surfaces":[]}` || !strings.HasPrefix(set.Digest(), "sha256:") {
		t.Fatalf("Build(nil) = %#v, %v", set, err)
	}
}

func identityPublicIDs(values []protobufidentity.Identity) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.PublicID()
	}
	return result
}
