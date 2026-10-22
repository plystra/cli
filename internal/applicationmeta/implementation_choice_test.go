package applicationmeta_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
)

func TestSetImplementationChoicePreservesCommentsAndUnrelatedSemantics(t *testing.T) {
	t.Parallel()

	input := []byte(`# Shared application choices.
interfaces:
  require: [email.send/v1]
  use:
    audit.write/v1: example.com/audit/file.New # keep this choice
  policies:
    email.send/v1: {timeout: 5s}
config:
  example.com/email/smtp.New:
    host: smtp.example.test
`)
	id := mustImplementationChoiceInterfaceID(t, "email.send/v1")
	constructor := mustImplementationChoiceConstructor(t, "example.com/email/smtp.New")
	updated, changed, err := applicationmeta.SetImplementationChoice(input, id, constructor)
	if err != nil || !changed {
		t.Fatalf("SetImplementationChoice = changed %t, %v", changed, err)
	}
	for _, fragment := range [][]byte{
		[]byte("# Shared application choices."),
		[]byte("# keep this choice"),
		[]byte("audit.write/v1: example.com/audit/file.New"),
		[]byte("email.send/v1: example.com/email/smtp.New"),
		[]byte("timeout: 5s"),
		[]byte("host: smtp.example.test"),
	} {
		if !bytes.Contains(updated, fragment) {
			t.Fatalf("updated document omits %q:\n%s", fragment, updated)
		}
	}
	manifest, err := applicationmeta.Parse(updated)
	if err != nil || !containsImplementationChoice(manifest, id, constructor) {
		t.Fatalf("Parse(updated) choices = %#v, %v", manifest.ImplementationChoices(), err)
	}

	idempotent, idempotentChanged, err := applicationmeta.SetImplementationChoice(updated, id, constructor)
	if err != nil || idempotentChanged || !bytes.Equal(idempotent, updated) {
		t.Fatalf("idempotent SetImplementationChoice = changed %t, data %q, %v", idempotentChanged, idempotent, err)
	}
}

func TestSetImplementationChoiceOverlayReplacesTombstoneAndPreservesSparseFields(t *testing.T) {
	t.Parallel()

	input := []byte(`# Production-only choices.
interfaces:
  require:
    remove: [debug.trace/v1]
  use:
    email.send/v1: {$remove: true} # replace this tombstone
`)
	id := mustImplementationChoiceInterfaceID(t, "email.send/v1")
	constructor := mustImplementationChoiceConstructor(t, "example.com/email/production.New")
	updated, changed, err := applicationmeta.SetImplementationChoiceOverlay(input, id, constructor)
	if err != nil || !changed {
		t.Fatalf("SetImplementationChoiceOverlay = changed %t, %v", changed, err)
	}
	for _, fragment := range [][]byte{
		[]byte("# Production-only choices."),
		[]byte("# replace this tombstone"),
		[]byte("remove: [debug.trace/v1]"),
		[]byte("email.send/v1: example.com/email/production.New"),
	} {
		if !bytes.Contains(updated, fragment) {
			t.Fatalf("updated overlay omits %q:\n%s", fragment, updated)
		}
	}
	manifest, err := applicationmeta.ParseOverlaySource("plystra.production.yaml", updated)
	if err != nil || !containsImplementationChoice(manifest, id, constructor) {
		t.Fatalf("ParseOverlaySource(updated) choices = %#v, %v", manifest.ImplementationChoices(), err)
	}
}

func TestSetImplementationChoicePreservesIntrinsicIntentForResolutionValidation(t *testing.T) {
	t.Parallel()

	id := mustImplementationChoiceInterfaceID(t, "kernel.health/v1")
	constructor := mustImplementationChoiceConstructor(t, "example.com/acme/health.New")
	updated, changed, err := applicationmeta.SetImplementationChoice([]byte("{}\n"), id, constructor)
	if err != nil || !changed {
		t.Fatalf("SetImplementationChoice = changed %t, %v", changed, err)
	}
	manifest, err := applicationmeta.Parse(updated)
	if err != nil || !containsImplementationChoice(manifest, id, constructor) {
		t.Fatalf("Parse(updated) choices = %#v, %v", manifest.ImplementationChoices(), err)
	}
	idempotent, idempotentChanged, err := applicationmeta.SetImplementationChoice(updated, id, constructor)
	if err != nil || idempotentChanged || !bytes.Equal(idempotent, updated) {
		t.Fatalf("idempotent SetImplementationChoice = changed %t, data %q, %v", idempotentChanged, idempotent, err)
	}
}

func TestSetImplementationChoiceRejectsInvalidInputWithoutSecretDisclosure(t *testing.T) {
	t.Parallel()

	const secret = "do-not-disclose-secret-target"
	validID := mustImplementationChoiceInterfaceID(t, "email.send/v1")
	validConstructor := mustImplementationChoiceConstructor(t, "example.com/email/smtp.New")
	tests := []struct {
		name        string
		data        []byte
		id          interfaceid.Identifier
		constructor constructorsymbol.Symbol
	}{
		{name: "empty Interface", data: []byte("{}\n"), constructor: validConstructor},
		{name: "empty constructor", data: []byte("{}\n"), id: validID},
		{name: "invalid document", data: []byte("config:\n  example.com/email/smtp.New:\n    password: {env: " + secret + "}\nunknown: true\n"), id: validID, constructor: validConstructor},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updated, changed, err := applicationmeta.SetImplementationChoice(test.data, test.id, test.constructor)
			if !errors.Is(err, applicationmeta.ErrSetImplementationChoice) || changed || updated != nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("SetImplementationChoice = changed %t, data %q, error %v", changed, updated, err)
			}
		})
	}
}

func TestRemoveImplementationChoicePreservesOtherIntentAndIsIdempotent(t *testing.T) {
	t.Parallel()
	id := mustImplementationChoiceInterfaceID(t, "email.send/v1")
	for _, overlay := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "overlay"}[overlay], func(t *testing.T) {
			input := []byte(`# Selected choices.
interfaces:
  require: [audit.write/v1]
  use:
    audit.write/v1: example.com/audit/file.New # retain this choice
    email.send/v1: example.com/email/smtp.New
config:
  example.com/email/smtp.New: {host: PRIVATE_HOST}
`)
			remove := applicationmeta.RemoveImplementationChoice
			parse := applicationmeta.Parse
			if overlay {
				remove = applicationmeta.RemoveImplementationChoiceOverlay
				parse = func(data []byte) (applicationmeta.Manifest, error) {
					return applicationmeta.ParseOverlaySource("plystra.production.yaml", data)
				}
			}
			updated, changed, err := remove(input, id)
			if err != nil || !changed {
				t.Fatalf("remove = changed %t, %v", changed, err)
			}
			for _, fragment := range []string{"# Selected choices.", "# retain this choice", "audit.write/v1: example.com/audit/file.New", "require: [audit.write/v1]", "PRIVATE_HOST"} {
				if !bytes.Contains(updated, []byte(fragment)) {
					t.Fatalf("removal lost %q: %s", fragment, updated)
				}
			}
			manifest, err := parse(updated)
			if err != nil || len(manifest.ImplementationChoices()) != 1 || manifest.ImplementationChoices()[0].InterfaceID().String() != "audit.write/v1" {
				t.Fatalf("remaining choices = %#v, %v", manifest.ImplementationChoices(), err)
			}
			if bytes.Contains(updated, []byte("email.send/v1: {$remove: true}")) != overlay {
				t.Fatalf("unexpected removal marker: %s", updated)
			}
			repeated, repeatedChanged, err := remove(updated, id)
			if err != nil || repeatedChanged || !bytes.Equal(repeated, updated) {
				t.Fatalf("repeat remove = changed %t, data %q, %v", repeatedChanged, repeated, err)
			}
		})
	}
	for _, input := range []string{"{}\n", "interfaces: {use: {audit.write/v1: example.com/audit/file.New}}\n"} {
		unchanged, changed, err := applicationmeta.RemoveImplementationChoice([]byte(input), id)
		if err != nil || changed || string(unchanged) != input {
			t.Fatalf("absent local choice = changed %t, data %q, %v", changed, unchanged, err)
		}
		updated, changed, err := applicationmeta.RemoveImplementationChoiceOverlay([]byte(input), id)
		if err != nil || !changed || !bytes.Contains(updated, []byte("email.send/v1: {$remove: true}")) {
			t.Fatalf("absent overlay choice = changed %t, data %q, %v", changed, updated, err)
		}
	}
}

func TestRemoveImplementationChoiceRejectsInvalidInputWithoutDisclosure(t *testing.T) {
	t.Parallel()
	for _, remove := range []func([]byte, interfaceid.Identifier) ([]byte, bool, error){applicationmeta.RemoveImplementationChoice, applicationmeta.RemoveImplementationChoiceOverlay} {
		for _, test := range []struct {
			data string
			id   interfaceid.Identifier
		}{
			{data: "{}\n"},
			{data: "unknown: PRIVATE_VALUE\n", id: mustImplementationChoiceInterfaceID(t, "email.send/v1")},
		} {
			updated, changed, err := remove([]byte(test.data), test.id)
			if !errors.Is(err, applicationmeta.ErrRemoveImplementationChoice) || changed || updated != nil || strings.Contains(err.Error(), "PRIVATE_VALUE") {
				t.Fatalf("invalid removal = changed %t, data %q, %v", changed, updated, err)
			}
		}
	}
}

func mustImplementationChoiceInterfaceID(t testing.TB, value string) interfaceid.Identifier {
	t.Helper()
	id, err := interfaceid.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return id
}

func mustImplementationChoiceConstructor(t testing.TB, value string) constructorsymbol.Symbol {
	t.Helper()
	constructor, err := constructorsymbol.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q): %v", value, err)
	}
	return constructor
}

func containsImplementationChoice(manifest applicationmeta.Manifest, id interfaceid.Identifier, constructor constructorsymbol.Symbol) bool {
	for _, choice := range manifest.ImplementationChoices() {
		if choice.InterfaceID() == id && choice.Constructor() == constructor {
			return true
		}
	}
	return false
}
