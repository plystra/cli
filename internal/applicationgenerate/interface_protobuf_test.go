package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/protobufdescriptor"
)

func TestGenerateProjectsExposedAuthoredInterfaceMessages(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/interface-protobuf"
	writeApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose:
    - records.list/v1
`)
	interfacePath := filepath.Join(root, "interfaces", "records", "list", "v1", "interface.go")
	writeFile(t, interfacePath, interfaceProtobufSource(7))
	writeFile(t, filepath.Join(root, "records", "service.go"), `package records

import (
	"context"

	listv1 "example.com/interface-protobuf/interfaces/records/list/v1"
)

type Service struct{}

//plystra:implements records.list/v1
func New() (*Service, error) { return &Service{}, nil }

func (*Service) List(context.Context, listv1.Request) (listv1.Response, error) {
	return listv1.Response{}, nil
}
`)

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
	}
	initial, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !initial.Report().Clean() {
		t.Fatalf("Generate = changes %#v, %v", initial.Report().Changes(), err)
	}

	protoPath := "generated/proto/plystra/generated/records/list/v1/interface.proto"
	source := readFile(t, root, protoPath)
	for _, fragment := range []string{
		"message RecordsListV1Request {",
		`map<string, sint64> labels = 2 [json_name = "labels"];`,
		`optional sint32 page_size = 7 [json_name = "page_size"];`,
		"message RecordsListV1Response {",
	} {
		if !bytes.Contains(source, []byte(fragment)) {
			t.Fatalf("%s omits %q:\n%s", protoPath, fragment, source)
		}
	}
	if bytes.Contains(source, []byte("service ")) || bytes.Contains(source, []byte("Capability")) {
		t.Fatalf("%s contains a premature procedure or obsolete contract identity:\n%s", protoPath, source)
	}
	assertFileMissing(t, root, "generated/proto/plystra/generated/records/list/v1/capability.proto")
	if descriptor := readFile(t, root, protobufdescriptor.DescriptorSetPath); len(descriptor) == 0 {
		t.Fatal("combined descriptor set is empty")
	}
	beforeProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(initial): %v", err)
	}

	command := exec.CommandContext(t.Context(), "go", "test", "./...", "-count=1")
	command.Dir = root
	command.Env = mergedEnvironment(map[string]string{
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go test generated Interface Project: %v\n%s", err, output)
	}

	writeFile(t, interfacePath, interfaceProtobufSource(9))
	beforeCheck := snapshotTree(t, root)
	options.Check = true
	drift, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil {
		t.Fatalf("Generate --check: %v", err)
	}
	if !slicesContains(drift.Report().Changed(), protoPath) ||
		!slicesContains(drift.Report().Changed(), protobufdescriptor.DescriptorSetPath) ||
		!slicesContains(drift.Report().Changed(), "generated/manifest.json") {
		t.Fatalf("Interface field-number drift = %#v", drift.Report().Changes())
	}
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatal("Generate --check mutated the authored Interface Project")
	}

	options.Check = false
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !updated.Report().Clean() {
		t.Fatalf("Generate(updated) = changes %#v, %v", updated.Report().Changes(), err)
	}
	updatedSource := readFile(t, root, protoPath)
	if !bytes.Contains(updatedSource, []byte(`optional sint32 page_size = 9 [json_name = "page_size"];`)) ||
		bytes.Contains(updatedSource, []byte(`page_size = 7`)) {
		t.Fatalf("updated Interface schema did not preserve the authored field number:\n%s", updatedSource)
	}
	afterProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(updated): %v", err)
	}
	if beforeProvenance.ApplicationModelDigest() == afterProvenance.ApplicationModelDigest() {
		t.Fatal("authored Interface field-number change did not alter application_model_digest")
	}

	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() {
		t.Fatalf("Generate --check(clean) = changes %#v, %v", clean.Report().Changes(), err)
	}
}

func interfaceProtobufSource(pageSizeNumber int) string {
	return strings.ReplaceAll(`package listv1

import "context"

//plystra:interface records.list/v1
type Interface interface {
	List(context.Context, Request) (Response, error)
}

type Request struct {
	PageSize int32            `+"`json:\"page_size\" plystra:\"FIELD_NUMBER,required\"`"+`
	Labels   map[string]int64 `+"`json:\"labels\" plystra:\"2\"`"+`
}

type Response struct {
	Records []Record `+"`json:\"records\" plystra:\"11,required\"`"+`
}

type Record struct {
	ID string `+"`json:\"id\" plystra:\"1,required\"`"+`
}
`, "FIELD_NUMBER", strconv.Itoa(pageSizeNumber))
}
