package applicationmeta_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestProcessSettingContentsStayOutOfPublicIdentity(t *testing.T) {
	t.Parallel()
	lookup := composeSchemaLookup(nil)
	var original []applicationmeta.ConfigurationDecision
	var originalDigest string
	for _, values := range []struct{ address, startup string }{
		{"PRIVATE_FIRST:19080", "1s"},
		{"PRIVATE_SECOND:29080", "2m"},
		{"[::1]:39080", "1ns"},
	} {
		manifest, err := applicationmeta.ParseSource("deploy/customer.yaml", []byte(fmt.Sprintf("http: {address: '%s'}\ntimeouts: {startup: %s}\n", values.address, values.startup)))
		if err != nil {
			t.Fatal(err)
		}
		decisions, err := applicationmeta.ConfigurationDecisions(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		if originalDigest == "" {
			original, originalDigest = decisions, digest
		}
		if !reflect.DeepEqual(decisions, original) || digest != originalDigest {
			t.Fatal("runtime-only process contents changed public identity")
		}
		composed, err := applicationmeta.Compose(nil, manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		address, present := composed.Manifest().HTTPAddress()
		duration, _ := time.ParseDuration(values.startup)
		if !present || address != values.address || composed.Manifest().StartupTimeout() != duration {
			t.Fatal("public redaction changed private process settings")
		}
		for _, decision := range decisions {
			if decision.DependencyComposable() || decision.Removed() || decision.Source() != "deploy/customer.yaml" {
				t.Fatal("process declaration ownership changed")
			}
		}
	}
	for _, data := range []string{"{}", "http: {address: null}\ntimeouts: {startup: null}", "http: {address: ':19080', cors: {allowed_origins: ['https://example.test']}}\ntimeouts: {startup: 1s}"} {
		manifest, err := applicationmeta.ParseOverlaySource("deploy/customer.yaml", []byte(data))
		if err != nil {
			t.Fatal(err)
		}
		digest, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil || digest == originalDigest {
			t.Fatalf("absence, removal, or build-affecting CORS lost public identity: %v", err)
		}
	}
}

func TestProcessPrivacyDoesNotBypassValidation(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		"http: {address: ''}", "http: {address: 42}", "http: {address: ' PRIVATE_ADDRESS '}",
		"http: {address: '" + strings.Repeat("x", 4097) + "'}",
		"timeouts: {startup: 0s}", "timeouts: {startup: -1s}", "timeouts: {startup: PRIVATE_INVALID}",
		"timeouts: {startup: 123}",
	} {
		if _, err := applicationmeta.Parse([]byte(data)); err == nil || strings.Contains(err.Error(), "PRIVATE_") {
			t.Fatalf("invalid process settings accepted or disclosed: %v", err)
		}
	}
}

func FuzzProcessSettingPublicIdentity(f *testing.F) {
	f.Add([]byte("first"), uint32(1))
	f.Add([]byte("second"), uint32(0))
	lookup := composeSchemaLookup(nil)
	digest := func(t testing.TB, address string, duration time.Duration) string {
		t.Helper()
		manifest := composeManifest(t, fmt.Sprintf("http: {address: '%s'}\ntimeouts: {startup: %s}", address, duration))
		value, err := applicationmeta.ConfigurationLayerDigest(manifest, lookup)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	want := digest(f, "PRIVATE_BASELINE:19080", time.Second)
	f.Fuzz(func(t *testing.T, address []byte, nanos uint32) {
		if len(address) > 1024 {
			t.Skip()
		}
		if digest(t, fmt.Sprintf("PRIVATE_%x:19080", address), time.Duration(nanos)+time.Nanosecond) != want {
			t.Fatal("runtime process contents entered public identity")
		}
	})
}
