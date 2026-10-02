package assemblygen_test

const generatedBootstrapRootTest = `package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	localservice "example.com/assemblyapp/local-service"
	remotestore "example.com/assemblydependency/remote-store"
	kernelconfiguration "github.com/plystra/kernel/configuration"
)

func TestApplicationRequiresExplicitConfigurationRoot(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	writeRuntimeDocument(t, validRuntimeDocument)
	for _, arguments := range [][]string{
		nil,
		{"--configuration-root"},
		{"--configuration-root", ""},
		{"--configuration-root", ".", "--configuration-root", "."},
		{"--configuration-root", "runtime-private-missing-root"},
		{"--configuration-root", "plystra.yaml"},
		{"--configuration-root", "runtime-private\x00root"},
		{"--configuration-root", ".", "--runtime-private-option", "value"},
	} {
		localservice.Reset()
		remotestore.Reset()
		application, err := New(context.Background(), RuntimeOptions{Arguments: arguments})
		if application != nil || !errors.Is(err, ErrRuntimeSelector) {
			t.Fatalf("invalid configuration root accepted: %v", err)
		}
		assertNoBootstrapConstructorCalls(t)
		if strings.Contains(err.Error(), "runtime-private") {
			t.Fatalf("root diagnostic exposed private input: %v", err)
		}
	}
}

func TestApplicationLoadsOnlyExplicitRootFromUnrelatedDirectory(t *testing.T) {
	t.Setenv("PLYSTRA_ASSEMBLY_PRIVATE_SECRET", "runtime-private-secret-value")
	root := t.TempDir()
	write := func(name, contents string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0600); err != nil { t.Fatal(err) }
	}
	write("plystra.yaml", validRuntimeDocument)
	write("plystra.production.yaml", "config:\n  acme.local-service: {label: selected-overlay}\n")
	write("replacement.yaml", "config:\n  acme.local-service: {label: selected-replacement}\n" + bootstrapRemoteConfiguration)
	write("plystra.ignored.yaml", "invalid: [\n")
	t.Chdir(t.TempDir())
	if err := os.WriteFile("plystra.yaml", []byte("invalid: [\n"), 0600); err != nil { t.Fatal(err) }
	tests := []struct {
		arguments []string
		environment []string
		label string
	}{
		{[]string{"--configuration-root", root}, nil, "public-default-label"},
		{[]string{"--configuration-root", root, "--env", "production"}, []string{"PLYSTRA_CONFIG=missing", "PLYSTRA_ENV=missing"}, "selected-overlay"},
		{[]string{"--env", "production", "--configuration-root", root}, nil, "selected-overlay"},
		{[]string{"--configuration-root", root}, []string{"PLYSTRA_ENV=production"}, "selected-overlay"},
		{[]string{"--configuration-root", root, "--config", "replacement.yaml"}, nil, "selected-replacement"},
		{[]string{"--config", filepath.Join(root, "replacement.yaml"), "--configuration-root", root}, nil, "selected-replacement"},
		{[]string{"--configuration-root", root}, []string{"PLYSTRA_CONFIG=replacement.yaml"}, "selected-replacement"},
	}
	for _, test := range tests {
		localservice.Reset()
		remotestore.Reset()
		application, err := New(context.Background(), RuntimeOptions{Arguments: test.arguments, Environment: test.environment})
		if err != nil { t.Fatal(err) }
		_, configuration := localservice.Snapshot()
		if configuration.Label != test.label { t.Fatalf("label = %q, want %q", configuration.Label, test.label) }
		if err := application.Stop(context.Background()); err != nil { t.Fatal(err) }
	}
}

func TestApplicationConfinesEveryConfigurationDocumentBeforeConstruction(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		problems := []string{"missing", "directory", "oversized", "symbolic file"}
		if mode == "replacement" { problems = append(problems, "symbolic directory", "outside") }
		for _, problem := range problems {
			t.Run(mode+"/"+problem, func(t *testing.T) {
				root := t.TempDir()
				documentPath := "plystra.yaml"
				arguments := []string{"--configuration-root", root}
				switch mode {
				case "environment": documentPath = "plystra.production.yaml"; arguments = append(arguments, "--env", "production")
				case "replacement": documentPath = "replacement.yaml"; arguments = append(arguments, "--config", documentPath)
				}
				if mode != "default" {
					if err := os.WriteFile(filepath.Join(root, "plystra.yaml"), []byte(validRuntimeDocument), 0600); err != nil { t.Fatal(err) }
				}
				target := filepath.Join(root, documentPath)
				reason := kernelconfiguration.ErrDocumentUnavailable
				switch problem {
				case "directory":
					if err := os.Mkdir(target, 0700); err != nil { t.Fatal(err) }
				case "oversized":
					if err := os.WriteFile(target, []byte(strings.Repeat("x", runtimeMaximumDocumentSize+1)), 0600); err != nil { t.Fatal(err) }
					reason = kernelconfiguration.ErrDocumentTooLarge
				case "symbolic file", "symbolic directory":
					outside := t.TempDir()
					if err := os.WriteFile(filepath.Join(outside, documentPath), []byte(validRuntimeDocument), 0600); err != nil { t.Fatal(err) }
					link, destination := target, filepath.Join(outside, documentPath)
					if problem == "symbolic directory" {
						link, destination = filepath.Join(root, "linked"), outside
						arguments[len(arguments)-1] = filepath.Join("linked", documentPath)
					}
					if err := os.Symlink(destination, link); err != nil { t.Skipf("symbolic links unavailable: %v", err) }
				case "outside":
					arguments[len(arguments)-1] = filepath.Join(t.TempDir(), documentPath)
					reason = ErrRuntimeSelector
				}
				localservice.Reset()
				remotestore.Reset()
				application, err := New(context.Background(), RuntimeOptions{Arguments: arguments})
				if application != nil || !errors.Is(err, ErrRuntimeSelector) || !errors.Is(err, reason) {
					t.Fatalf("unsafe configuration accepted or misclassified: %v", err)
				}
				assertNoBootstrapConstructorCalls(t)
				if strings.Contains(err.Error(), root) { t.Fatalf("private root disclosed: %v", err) }
			})
		}
	}
}
`
