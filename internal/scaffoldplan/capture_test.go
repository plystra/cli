package scaffoldplan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/projectstage"
)

func TestPrivateCaptureNestedFormatting(t *testing.T) {
	private := []byte("PRIVATE_CAPTURE_CONTENT")
	writeState := &capturedWrite{path: "private.yaml", after: private, afterExists: true}
	write := CapturedWrite{state: &writeState}
	state := &captureState{request: private, writes: []CapturedWrite{write}, model: "model"}
	captured := Capture{state: &state}
	values := []any{
		captured, write,
		struct{ private Capture }{captured},
		struct{ private CapturedWrite }{write},
		struct{ private any }{captured},
		struct{ private []Capture }{[]Capture{captured}},
		struct{ private [1]CapturedWrite }{[1]CapturedWrite{write}},
		struct{ private map[string]Capture }{map[string]Capture{"capture": captured}},
	}
	decimal := strings.Trim(fmt.Sprint(private), "[]")
	_, hexadecimal, _ := strings.Cut(fmt.Sprintf("%#v", private), "{")
	hexadecimal = strings.TrimSuffix(hexadecimal, "}")
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X", "%f", "%t", "%c", "%U", "%b", "%o", "%O"} {
		for _, value := range values {
			output := fmt.Sprintf(format, value)
			if strings.Contains(output, "PRIVATE_") || strings.Contains(output, decimal) || strings.Contains(output, hexadecimal) {
				t.Fatalf("nested private formatting bypass for %T with %s", value, format)
			}
		}
	}
}

func TestCandidateCaptureRejectsUnauthenticatedSources(t *testing.T) {
	root, source := t.TempDir(), t.TempDir()
	for name, data := range map[string]string{"go.mod": "module example.com/app\ngo 1.26\n", "plystra.yaml": "{}\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var environment []string
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !strings.EqualFold(key, "GOWORK") && !strings.EqualFold(key, "GOENV") {
			environment = append(environment, value)
		}
	}
	environment = append(environment, "GOWORK=off", "GOENV=off")
	stage, err := projectstage.Create(t.Context(), projectstage.Options{Start: root, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stage.Close() })
	if err := stage.RetainSourceTrees(t.Context(), []string{source}); err != nil {
		t.Fatal(err)
	}
	request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"records.list","semantics":"query"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	captured, err := captureCandidate(t.Context(), stage, request, nil, nil, applicationresolve.ConfigurationSelection{}, false, "model")
	if !errors.Is(err, projectstage.ErrSourceIntegrity) || captured.Valid() {
		t.Fatalf("pending source evidence became a candidate: %v", err)
	}
}
