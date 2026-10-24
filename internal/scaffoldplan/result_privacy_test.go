package scaffoldplan

import (
	"bytes"
	"context"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/configurationimpact"
	"go.yaml.in/yaml/v3"
)

func TestPrivateResultNestedFormatting(t *testing.T) {
	private := []byte("PRIVATE_RESULT_CONTENT")
	inputs := privateResultInputs(t, private)
	decimal := strings.Trim(fmt.Sprint(private), "[]")
	_, hexadecimal, _ := strings.Cut(fmt.Sprintf("%#v", private), "{")
	hexadecimal = strings.TrimSuffix(hexadecimal, "}")
	for _, state := range []*resultState{
		{updated: private},
		{inputs: inputs},
		{model: string(private), rootDigest: string(private), selectedDigest: string(private)},
	} {
		result := Result{state: &state}
		values := []any{
			result, &result,
			struct{ private Result }{result},
			struct{ private *Result }{&result},
			struct{ private any }{result},
			struct{ private any }{&result},
			struct{ private []Result }{[]Result{result}},
			struct{ private [1]Result }{[1]Result{result}},
			struct{ private map[string]Result }{map[string]Result{"result": result}},
		}
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X", "%f", "%t", "%c", "%U", "%b", "%o", "%O"} {
			for _, value := range values {
				output := fmt.Sprintf(format, value)
				for _, forbidden := range []string{"PRIVATE_", decimal, hexadecimal, fmt.Sprintf("%x", private), fmt.Sprintf("%X", private), inputs.Module().Path()} {
					if strings.Contains(output, forbidden) {
						t.Fatalf("nested private formatting bypass for %T with %s", value, format)
					}
				}
			}
		}
	}
}

func TestPrivateResultRejectsSerializationAndRedactsLogs(t *testing.T) {
	state := &resultState{updated: []byte("PRIVATE_RESULT_CONTENT")}
	result := Result{state: &state}
	for _, value := range []Result{{}, result} {
		for _, marshaler := range []encoding.TextMarshaler{value, &value} {
			if data, err := marshaler.MarshalText(); !errors.Is(err, ErrPrivateResult) || data != nil {
				t.Fatal("private result accepted text serialization")
			}
		}
		for _, carrier := range []any{value, &value, struct{ Result Result }{value}, []Result{value}, map[string]Result{"result": value}} {
			if data, err := json.Marshal(carrier); !errors.Is(err, ErrPrivateResult) || data != nil {
				t.Fatal("private result accepted JSON serialization")
			}
			if data, err := yaml.Marshal(carrier); !errors.Is(err, ErrPrivateResult) || data != nil {
				t.Fatal("private result accepted YAML serialization")
			}
		}
		if logged := value.LogValue(); logged.Kind() != slog.KindString || logged.String() != "<private-scaffold-plan>" {
			t.Fatal("private result has an unsafe structured-log value")
		}
		var output bytes.Buffer
		for _, handler := range []slog.Handler{slog.NewTextHandler(&output, nil), slog.NewJSONHandler(&output, nil)} {
			logger := slog.New(handler)
			logger.Info("preview", "value", value, "pointer", &value)
			if !strings.Contains(output.String(), "<private-scaffold-plan>") || strings.Contains(output.String(), "PRIVATE_") {
				t.Fatal("private result leaked through structured logging")
			}
			output.Reset()
			logger.Info("nested preview", "value", struct{ private Result }{value})
			if strings.Contains(output.String(), "PRIVATE_") || strings.Contains(output.String(), "80 82 73 86") {
				t.Fatal("nested private result leaked through structured logging")
			}
			output.Reset()
		}
	}
}

func TestPrivateResultZeroSafeAccessors(t *testing.T) {
	var state *resultState
	for _, result := range []Result{{}, {state: &state}} {
		if result.Inputs().Module().Path() != "" || result.Inputs().SelectedSnapshot().Data() != nil || result.Updated() != nil || result.ReadSet() != nil || len(result.WriteSet()) != 0 {
			t.Fatal("zero result returned populated inputs or paths")
		}
		if result.ConfigurationImpact().Valid() || result.Capture().Valid() || result.Effects().Valid() || result.FinalModelDigest() != "" {
			t.Fatal("zero result returned valid candidate evidence")
		}
		if root, selected := result.OriginalConfigurationDigests(); root != "" || selected != "" {
			t.Fatal("zero result returned configuration identities")
		}
	}
}

func TestPrivateResultAccessorsOwnReturnedData(t *testing.T) {
	private := []byte("PRIVATE_RESULT_CONTENT")
	inputs := privateResultInputs(t, private)
	impact, err := configurationimpact.New(configurationimpact.Input{
		RetainedOwners: []configurationimpact.RetainedOwner{{Owner: "reader.New", Path: "config", Reason: "still_owned"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	effect, err := commandschema.NewEffect(commandschema.EffectInput{
		ID: "candidate-stage", Class: commandschema.EffectTemporaryFile, Phase: "plan", Target: ".",
		Owner: commandschema.Owner{Module: "example.com/private-result", Path: "."}, Reason: "private_candidate_stage", Reversibility: "none", Verification: []string{"go", "env"},
	})
	if err != nil {
		t.Fatal(err)
	}
	effects, err := commandschema.NewEffects(commandschema.EffectsInput{Observed: []commandschema.Effect{effect}})
	if err != nil {
		t.Fatal(err)
	}
	writeState := &capturedWrite{path: "private.yaml", after: bytes.Clone(private), afterExists: true, afterMode: 0o600}
	capturedState := &captureState{request: bytes.Clone(private), writes: []CapturedWrite{{state: &writeState}}, model: "model"}
	state := &resultState{
		inputs: inputs, updated: bytes.Clone(private), impact: impact, capture: Capture{state: &capturedState},
		reads: []commandschema.ChangePlanPathInput{{Path: "go.mod", BeforeExists: true}}, effects: effects,
		model: "model", rootDigest: "root", selectedDigest: "selected",
	}
	result := Result{state: &state}
	result.Updated()[0] ^= 1
	result.ReadSet()[0].Path = "changed"
	result.WriteSet()[0].Path = "changed"
	result.Capture().CanonicalRequest()[0] ^= 1
	result.Capture().Writes()[0].After()[0] ^= 1
	result.Capture().Writes()[0] = CapturedWrite{}
	result.Inputs().SelectedSnapshot().Data()[0] ^= 1
	result.ConfigurationImpact().RetainedOwners()[0].Owner = "changed"
	result.Effects().Observed()[0] = commandschema.Effect{}
	if !bytes.Equal(result.Updated(), private) || result.ReadSet()[0].Path != "go.mod" || result.WriteSet()[0].Path != "private.yaml" || !bytes.Equal(result.Capture().CanonicalRequest(), private) || !bytes.Equal(result.Capture().Writes()[0].After(), private) {
		t.Fatal("result exposes mutable private bytes or paths")
	}
	if !bytes.Contains(result.Inputs().SelectedSnapshot().Data(), private) || !result.ConfigurationImpact().Valid() || result.ConfigurationImpact().RetainedOwners()[0].Owner != "reader.New" || !result.Effects().Valid() || result.Effects().Observed()[0].ID() != "candidate-stage" {
		t.Fatal("result exposes mutable nested inputs or projections")
	}
	if root, selected := result.OriginalConfigurationDigests(); root != "root" || selected != "selected" || result.FinalModelDigest() != "model" {
		t.Fatal("result lost model or configuration identities")
	}
}

func TestPreviewResultKeepsFailureEffects(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":                              "module example.com/private-result\n\ngo 1.26\n",
		"plystra.yaml":                        "{}\n",
		"interfaces/records/list/v1/keep.txt": "USER_SOURCE\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	request, err := changerequest.Parse([]byte(`{"schema":"plystra.change/v1","operations":[{"kind":"interface_create","name":"records.list","semantics":"query"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	result, err := Preview(t.Context(), Options{Start: root, TemporaryParent: parent, Environment: append(os.Environ(), "GOWORK=off")}, request)
	if !errors.Is(err, ErrPreview) || result.Capture().Valid() || result.FinalModelDigest() != "" || !result.Effects().Valid() {
		t.Fatal("failed preview lost its failure or effect accounting")
	}
	observed := result.Effects().Observed()
	seen := make(map[string]bool)
	for _, effect := range observed {
		seen[effect.ID()] = true
	}
	if len(observed) != 2 || !seen["candidate-go-environment"] || !seen["candidate-stage"] || len(result.Effects().Unverified()) != 3 {
		t.Fatal("failed preview lost observed or unverified effects")
	}
	if children, err := os.ReadDir(parent); err != nil || len(children) != 0 {
		t.Fatal("failed preview retained its private working tree")
	}
	if data, err := os.ReadFile(filepath.Join(root, "interfaces/records/list/v1/keep.txt")); err != nil || string(data) != "USER_SOURCE\n" {
		t.Fatal("failed preview changed authored source")
	}
	if _, err := os.Stat(filepath.Join(root, "interfaces/records/list/v1/interface.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed preview installed a scaffold")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if canceled, err := Preview(ctx, Options{Start: root}, request); !errors.Is(err, context.Canceled) || canceled.Effects().Valid() || canceled.Capture().Valid() {
		t.Fatal("early failure changed zero-result semantics")
	}
}

func privateResultInputs(t *testing.T, private []byte) applicationresolve.SelectionInputs {
	t.Helper()
	root := t.TempDir()
	for name, data := range map[string]string{
		"go.mod":       "module example.com/private-result\n\ngo 1.26\n",
		"source.go":    "package example\n",
		"plystra.yaml": "config:\n  example.com/private-result/reader.New: {endpoint: " + string(private) + "}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inputs, err := applicationresolve.DiscoverSelectionInputs(t.Context(), applicationresolve.Options{
		Start: root, Environment: append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(inputs.SelectedSnapshot().Data(), private) {
		t.Fatal("fixture lost private selected input")
	}
	return inputs
}
