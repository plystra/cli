package applicationmeta_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/capabilityid"
)

func TestSetProviderChoicePreservesCommentsAndUnrelatedSemantics(t *testing.T) {
	t.Parallel()

	input := []byte(`# Shared choices.
http:
  expose: [email.send/v1]
capabilities:
  require: [email.send/v1]
  use:
    email.send/v1: acme.email.old # replace this Provider
    records.read/v1: acme.records # preserve this Provider
  aliases:
    mail.send/v1: email.send/v1
config:
  example.com/acme/email/old.New:
    token: {env: SMTP_TOKEN}
`)
	id := mustProviderChoiceID(t, "email.send/v1")
	updated, changed, err := applicationmeta.SetProviderChoice(input, id, "acme.email.smtp")
	if err != nil || !changed {
		t.Fatalf("SetProviderChoice = changed %t, %v", changed, err)
	}
	for _, retained := range [][]byte{
		[]byte("# Shared choices."),
		[]byte("email.send/v1: acme.email.smtp # replace this Provider"),
		[]byte("records.read/v1: acme.records # preserve this Provider"),
		[]byte("mail.send/v1:"),
		[]byte("env: SMTP_TOKEN"),
	} {
		if !bytes.Contains(updated, retained) {
			t.Fatalf("updated manifest omits %q:\n%s", retained, updated)
		}
	}
	manifest, err := applicationmeta.Parse(updated)
	if err != nil || !containsProviderChoice(manifest, id, "acme.email.smtp") {
		t.Fatalf("Parse(updated) Provider choices = %#v, %v", manifest.ProviderChoices(), err)
	}
	idempotent, idempotentChanged, err := applicationmeta.SetProviderChoice(updated, id, "acme.email.smtp")
	if err != nil || idempotentChanged || !bytes.Equal(idempotent, updated) {
		t.Fatalf("idempotent SetProviderChoice = changed %t, data %q, %v", idempotentChanged, idempotent, err)
	}
}

func TestSetProviderChoiceOverlayReplacesTombstoneAndPreservesSparseFields(t *testing.T) {
	t.Parallel()

	input := []byte(`# Production choices.
http:
  cors:
    allow_credentials: true # inherit root origins
capabilities:
  use:
    email.send/v1: null # restore an explicit Provider
    records.read/v1: acme.records
`)
	id := mustProviderChoiceID(t, "email.send/v1")
	updated, changed, err := applicationmeta.SetProviderChoiceOverlay(input, id, "acme.email.production")
	if err != nil || !changed {
		t.Fatalf("SetProviderChoiceOverlay = changed %t, %v", changed, err)
	}
	for _, retained := range [][]byte{
		[]byte("# Production choices."),
		[]byte("allow_credentials: true # inherit root origins"),
		[]byte("email.send/v1: acme.email.production # restore an explicit Provider"),
		[]byte("records.read/v1: acme.records"),
	} {
		if !bytes.Contains(updated, retained) {
			t.Fatalf("updated overlay omits %q:\n%s", retained, updated)
		}
	}
	manifest, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", updated)
	if err != nil || !containsProviderChoice(manifest, id, "acme.email.production") {
		t.Fatalf("ParseOverlaySource(updated) Provider choices = %#v, %v", manifest.ProviderChoices(), err)
	}
}

func TestSetProviderChoiceRejectsInvalidInputWithoutSecretDisclosure(t *testing.T) {
	t.Parallel()

	secret := "unique-provider-secret"
	tests := []struct {
		name     string
		data     []byte
		id       capabilityid.Identifier
		pluginID string
	}{
		{name: "empty Capability", data: []byte("{}\n"), pluginID: "acme.email"},
		{name: "intrinsic Capability", data: []byte("{}\n"), id: mustProviderChoiceID(t, "kernel.health/v1"), pluginID: "acme.email"},
		{name: "invalid Plugin", data: []byte("{}\n"), id: mustProviderChoiceID(t, "email.send/v1"), pluginID: "Acme.Email"},
		{name: "invalid manifest", data: []byte("config:\n  acme.email:\n    token: " + secret + "\nunknown: true\n"), id: mustProviderChoiceID(t, "email.send/v1"), pluginID: "acme.email"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updated, changed, err := applicationmeta.SetProviderChoice(test.data, test.id, test.pluginID)
			if !errors.Is(err, applicationmeta.ErrSetProviderChoice) || changed || updated != nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("SetProviderChoice = changed %t, data %q, error %v", changed, updated, err)
			}
		})
	}
}

func containsProviderChoice(manifest applicationmeta.Manifest, capability capabilityid.Identifier, pluginID string) bool {
	for _, choice := range manifest.ProviderChoices() {
		if choice.Capability() == capability && choice.PluginID() == pluginID {
			return true
		}
	}
	return false
}

func mustProviderChoiceID(t *testing.T, value string) capabilityid.Identifier {
	t.Helper()
	id, err := capabilityid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}
