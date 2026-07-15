package pluginid_test

import (
	"errors"
	"testing"

	"github.com/plystra/cli/internal/pluginid"
)

func TestValidate(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"acme.email.smtp", "plystra.authz.rbac.default", "example.document-processing.extractor2"} {
		if err := pluginid.Validate(value); err != nil {
			t.Errorf("Validate(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "plystra", "Plystra.plugin", "plystra_plugin", "plystra..plugin", "plystra.-plugin", "plystra.plugin-", "plystra.plugin--name", " plystra.plugin", "普莱斯特拉.plugin"} {
		if err := pluginid.Validate(value); !errors.Is(err, pluginid.ErrInvalid) {
			t.Errorf("Validate(%q) error = %v, want ErrInvalid", value, err)
		}
	}
}

func TestValidSegment(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"account", "email-smtp", "extractor2"} {
		if !pluginid.ValidSegment(value) {
			t.Errorf("ValidSegment(%q) = false", value)
		}
	}
	for _, value := range []string{"", "Account", "account_2", "account-", "account--profile", "2account"} {
		if pluginid.ValidSegment(value) {
			t.Errorf("ValidSegment(%q) = true", value)
		}
	}
}

func FuzzValidate(f *testing.F) {
	for _, seed := range []string{"acme.email.smtp", "bad", "普莱斯特拉.plugin"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		err := pluginid.Validate(value)
		if err != nil && !errors.Is(err, pluginid.ErrInvalid) {
			t.Fatalf("Validate(%q) returned unexpected error: %v", value, err)
		}
	})
}
