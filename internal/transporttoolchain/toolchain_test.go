package transporttoolchain_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/apidocgen"
	"github.com/plystra/cli/internal/connectgen"
	"github.com/plystra/cli/internal/javascriptgen"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufmodel"
	"github.com/plystra/cli/internal/protobufwiremap"
	"github.com/plystra/cli/internal/transporttoolchain"
)

func TestCurrentRecordsExactEmbeddedGeneratorsAndGeneratedDependencies(t *testing.T) {
	t.Parallel()

	identity, err := transporttoolchain.Current()
	if err != nil || !identity.Valid() {
		t.Fatalf("Current = %#v, %v", identity, err)
	}
	got := componentSummaries(identity)
	want := []string{
		"embedded-runtime|go/format|" + runtime.Version(),
		"generator|api-documentation|" + apidocgen.GeneratorVersion,
		"generator|connect|" + connectgen.GeneratorVersion,
		"generator|javascript|" + javascriptgen.GeneratorVersion,
		"generator|protobuf-descriptor|" + protobufdescriptor.ProjectionSchema,
		"generator|protobuf-model|" + protobufmodel.GeneratorVersion,
		"generator|protobuf-wire-map|" + protobufwiremap.ProjectionSchema,
		"generated-go-dependency|connectrpc.com/connect|" + connectgen.ConnectModuleVersion,
		"generated-go-dependency|google.golang.org/protobuf|" + connectgen.ProtobufModuleVersion,
		"generated-npm-dependency|@bufbuild/protobuf|" + javascriptgen.ProtobufPackageVersion,
		"generated-npm-dependency|@connectrpc/connect|" + javascriptgen.ConnectPackageVersion,
		"generated-npm-dependency|@connectrpc/connect-web|" + javascriptgen.ConnectWebPackageVersion,
		"generated-npm-development-dependency|typescript|" + javascriptgen.TypeScriptPackageVersion,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Current components = %#v, want %#v", got, want)
	}
	if identity.Schema() != transporttoolchain.Schema ||
		!strings.HasPrefix(identity.Digest(), "sha256:") ||
		len(identity.Digest()) != len("sha256:")+64 {
		t.Fatalf("Current identity = schema %q digest %q", identity.Schema(), identity.Digest())
	}
}

func TestIdentityHasKnownCanonicalDigestAndDefensiveAccess(t *testing.T) {
	t.Parallel()

	inputs := knownInputs()
	reversed := append([]transporttoolchain.ComponentInput(nil), inputs...)
	for left, right := 0, len(reversed)-1; left < right; left, right = left+1, right-1 {
		reversed[left], reversed[right] = reversed[right], reversed[left]
	}
	identity, err := transporttoolchain.New(reversed)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const wantCanonical = `{"schema":"plystra.transport-toolchain/v2","components":[{"kind":"embedded-runtime","name":"go/format","version":"go1.99.1"},{"kind":"generator","name":"api-documentation","version":"generator-00"},{"kind":"generator","name":"connect","version":"generator-01"},{"kind":"generator","name":"javascript","version":"generator-02"},{"kind":"generator","name":"protobuf-descriptor","version":"generator-03"},{"kind":"generator","name":"protobuf-model","version":"generator-04"},{"kind":"generator","name":"protobuf-wire-map","version":"generator-05"},{"kind":"generated-go-dependency","name":"connectrpc.com/connect","version":"v9.1.0"},{"kind":"generated-go-dependency","name":"google.golang.org/protobuf","version":"v9.2.0"},{"kind":"generated-npm-dependency","name":"@bufbuild/protobuf","version":"9.3.0"},{"kind":"generated-npm-dependency","name":"@connectrpc/connect","version":"9.4.0"},{"kind":"generated-npm-dependency","name":"@connectrpc/connect-web","version":"9.5.0"},{"kind":"generated-npm-development-dependency","name":"typescript","version":"9.6.0"}]}`
	const wantDigest = "sha256:0969199584fbd923e21ba49e56ca6e7c25bb2405ccdb1336507c5a0e019f4b55"
	if string(identity.CanonicalJSON()) != wantCanonical || identity.Digest() != wantDigest {
		t.Fatalf("identity = digest %q canonical %s", identity.Digest(), identity.CanonicalJSON())
	}

	components := identity.Components()
	components[0] = transporttoolchain.Component{}
	canonical := identity.CanonicalJSON()
	canonical[0] = '!'
	record := identity.RecordJSON()
	record[0] = '!'
	if !identity.Valid() ||
		componentSummaries(identity)[0] != "embedded-runtime|go/format|go1.99.1" ||
		bytes.Equal(canonical, identity.CanonicalJSON()) ||
		bytes.Equal(record, identity.RecordJSON()) {
		t.Fatal("Identity exposed mutable internal storage")
	}
	decoded, err := transporttoolchain.Decode(identity.RecordJSON())
	if err != nil || !decoded.Valid() || decoded.Digest() != identity.Digest() ||
		!bytes.Equal(decoded.CanonicalJSON(), identity.CanonicalJSON()) {
		t.Fatalf("Decode = %#v, %v", decoded, err)
	}
}

func TestIdentityRejectsInvalidClosedComponentSets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func([]transporttoolchain.ComponentInput) []transporttoolchain.ComponentInput
		want   string
	}{
		{
			name: "missing",
			mutate: func(inputs []transporttoolchain.ComponentInput) []transporttoolchain.ComponentInput {
				return inputs[1:]
			},
			want: "missing",
		},
		{
			name: "duplicate",
			mutate: func(inputs []transporttoolchain.ComponentInput) []transporttoolchain.ComponentInput {
				return append(inputs, inputs[0])
			},
			want: "duplicates",
		},
		{
			name: "unknown kind",
			mutate: func(inputs []transporttoolchain.ComponentInput) []transporttoolchain.ComponentInput {
				inputs[0].Kind = "executable"
				return inputs
			},
			want: "unknown component",
		},
		{
			name: "implicit protoc",
			mutate: func(inputs []transporttoolchain.ComponentInput) []transporttoolchain.ComponentInput {
				inputs[1].Name = "protoc"
				return inputs
			},
			want: "unknown component",
		},
		{
			name: "empty version",
			mutate: func(inputs []transporttoolchain.ComponentInput) []transporttoolchain.ComponentInput {
				inputs[1].Version = ""
				return inputs
			},
			want: "bounded safe",
		},
		{
			name: "control version",
			mutate: func(inputs []transporttoolchain.ComponentInput) []transporttoolchain.ComponentInput {
				inputs[1].Version = "v1\nsecret"
				return inputs
			},
			want: "bounded safe",
		},
		{
			name: "oversized version",
			mutate: func(inputs []transporttoolchain.ComponentInput) []transporttoolchain.ComponentInput {
				inputs[1].Version = strings.Repeat("v", 257)
				return inputs
			},
			want: "bounded safe",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			inputs := append([]transporttoolchain.ComponentInput(nil), knownInputs()...)
			identity, err := transporttoolchain.New(test.mutate(inputs))
			if !errors.Is(err, transporttoolchain.ErrInvalid) || !strings.Contains(err.Error(), test.want) || identity.Valid() {
				t.Fatalf("New = %#v, %v; want ErrInvalid containing %q", identity, err, test.want)
			}
		})
	}
}

func TestDecodeRejectsNoncanonicalOrTamperedRecords(t *testing.T) {
	t.Parallel()

	identity, err := transporttoolchain.New(knownInputs())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var record struct {
		Schema     string                              `json:"schema"`
		Components []transporttoolchainRecordComponent `json:"components"`
		Digest     string                              `json:"digest"`
	}
	if err := json.Unmarshal(identity.RecordJSON(), &record); err != nil {
		t.Fatalf("Unmarshal record: %v", err)
	}

	unsorted := record
	unsorted.Components = append([]transporttoolchainRecordComponent(nil), record.Components...)
	unsorted.Components[1], unsorted.Components[2] = unsorted.Components[2], unsorted.Components[1]
	unsortedJSON, err := json.Marshal(unsorted)
	if err != nil {
		t.Fatalf("Marshal unsorted record: %v", err)
	}
	if _, err := transporttoolchain.Decode(unsortedJSON); !errors.Is(err, transporttoolchain.ErrInvalid) || !strings.Contains(err.Error(), "canonically ordered") {
		t.Fatalf("Decode(unsorted) error = %v", err)
	}

	tampered := record
	tampered.Digest = strings.TrimSuffix(record.Digest, record.Digest[len(record.Digest)-1:]) + "0"
	if tampered.Digest == record.Digest {
		tampered.Digest = strings.TrimSuffix(record.Digest, "0") + "1"
	}
	tamperedJSON, err := json.Marshal(tampered)
	if err != nil {
		t.Fatalf("Marshal tampered record: %v", err)
	}
	if _, err := transporttoolchain.Decode(tamperedJSON); !errors.Is(err, transporttoolchain.ErrInvalid) || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("Decode(tampered) error = %v", err)
	}

	legacyV1 := bytes.Replace(
		identity.RecordJSON(),
		[]byte(transporttoolchain.Schema),
		[]byte("plystra.transport-toolchain/v1"),
		1,
	)
	if _, err := transporttoolchain.Decode(legacyV1); !errors.Is(
		err,
		transporttoolchain.ErrInvalid,
	) || !strings.Contains(err.Error(), `schema must equal "plystra.transport-toolchain/v2"`) {
		t.Fatalf("Decode(legacy v1) error = %v", err)
	}

	for name, data := range map[string][]byte{
		"unknown top-level field": bytes.Replace(identity.RecordJSON(), []byte(`"schema":`), []byte(`"unknown":true,"schema":`), 1),
		"unknown component field": bytes.Replace(identity.RecordJSON(), []byte(`"kind":`), []byte(`"unknown":true,"kind":`), 1),
		"unknown schema":          bytes.Replace(identity.RecordJSON(), []byte(transporttoolchain.Schema), []byte("plystra.transport-toolchain/v3"), 1),
		"missing components":      []byte(`{"schema":"plystra.transport-toolchain/v2","digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}`),
		"trailing JSON":           append(identity.RecordJSON(), []byte(` {}`)...),
		"malformed JSON":          []byte(`{"schema":`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := transporttoolchain.Decode(data); !errors.Is(err, transporttoolchain.ErrInvalid) {
				t.Fatalf("Decode(%s) error = %v", name, err)
			}
		})
	}
}

func TestIdentityContainsNoImplicitOrMachineSpecificState(t *testing.T) {
	t.Parallel()

	identity, err := transporttoolchain.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	record := strings.ToLower(string(identity.RecordJSON()))
	for _, forbidden := range []string{
		`"protoc"`,
		`"environment"`,
		`"secret"`,
		`"timestamp"`,
		`"vcs"`,
		`"dirty"`,
		`"path"`,
		`"hosted"`,
		`"executable"`,
		`c:\\`,
		`d:\\`,
	} {
		if strings.Contains(record, forbidden) {
			t.Fatalf("transport toolchain contains forbidden state %q: %s", forbidden, record)
		}
	}
}

type transporttoolchainRecordComponent struct {
	Kind    transporttoolchain.Kind `json:"kind"`
	Name    string                  `json:"name"`
	Version string                  `json:"version"`
}

func knownInputs() []transporttoolchain.ComponentInput {
	return []transporttoolchain.ComponentInput{
		{Kind: transporttoolchain.KindEmbeddedRuntime, Name: "go/format", Version: "go1.99.1"},
		{Kind: transporttoolchain.KindGenerator, Name: "api-documentation", Version: "generator-00"},
		{Kind: transporttoolchain.KindGenerator, Name: "connect", Version: "generator-01"},
		{Kind: transporttoolchain.KindGenerator, Name: "javascript", Version: "generator-02"},
		{Kind: transporttoolchain.KindGenerator, Name: "protobuf-descriptor", Version: "generator-03"},
		{Kind: transporttoolchain.KindGenerator, Name: "protobuf-model", Version: "generator-04"},
		{Kind: transporttoolchain.KindGenerator, Name: "protobuf-wire-map", Version: "generator-05"},
		{Kind: transporttoolchain.KindGeneratedGoDependency, Name: "connectrpc.com/connect", Version: "v9.1.0"},
		{Kind: transporttoolchain.KindGeneratedGoDependency, Name: "google.golang.org/protobuf", Version: "v9.2.0"},
		{Kind: transporttoolchain.KindGeneratedNPMDependency, Name: "@bufbuild/protobuf", Version: "9.3.0"},
		{Kind: transporttoolchain.KindGeneratedNPMDependency, Name: "@connectrpc/connect", Version: "9.4.0"},
		{Kind: transporttoolchain.KindGeneratedNPMDependency, Name: "@connectrpc/connect-web", Version: "9.5.0"},
		{Kind: transporttoolchain.KindGeneratedNPMDev, Name: "typescript", Version: "9.6.0"},
	}
}

func componentSummaries(identity transporttoolchain.Identity) []string {
	components := identity.Components()
	result := make([]string, len(components))
	for index, component := range components {
		result[index] = string(component.Kind()) + "|" + component.Name() + "|" + component.Version()
	}
	return result
}
