package applicationgenerate_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/protobufdescriptor"
	"github.com/plystra/cli/internal/protobufwiremap"
)

func TestGenerateProjectsExposedAuthoredInterfaceMessages(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/interface-protobuf"
	writeConnectApplicationModule(t, root, modulePath)
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
		"service RecordsListV1Service {",
		"rpc Invoke(.plystra.generated.records.list.v1.RecordsListV1Request) returns (.plystra.generated.records.list.v1.RecordsListV1Response);",
	} {
		if !bytes.Contains(source, []byte(fragment)) {
			t.Fatalf("%s omits %q:\n%s", protoPath, fragment, source)
		}
	}
	if bytes.Contains(source, []byte("Capability")) {
		t.Fatalf("%s contains an obsolete contract identity:\n%s", protoPath, source)
	}
	assertFileMissing(t, root, "generated/proto/plystra/generated/records/list/v1/capability.proto")
	if descriptor := readFile(t, root, protobufdescriptor.DescriptorSetPath); len(descriptor) == 0 {
		t.Fatal("combined descriptor set is empty")
	}
	initialWireHistory := decodeInterfaceWireHistory(t, readFile(t, root, protobufwiremap.Path), "records.list/v1", "RecordsListV1Request")
	if len(initialWireHistory.Fields) != 2 ||
		initialWireHistory.Fields["page_size"].Number != 7 ||
		len(initialWireHistory.ReservedNumbers) != 0 ||
		len(initialWireHistory.ReservedNames) != 0 {
		t.Fatalf("initial Interface wire history = %#v", initialWireHistory)
	}
	initialProcedure := decodeInterfaceProcedureHistory(t, readFile(t, root, protobufwiremap.Path), "records.list/v1")
	if initialProcedure.Service != "RecordsListV1Service" ||
		initialProcedure.Method != "Invoke" ||
		initialProcedure.Procedure != "/plystra.generated.records.list.v1.RecordsListV1Service/Invoke" {
		t.Fatalf("initial Interface procedure history = %#v", initialProcedure)
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
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, protobufwiremap.ErrHistory) ||
		!strings.Contains(err.Error(), `field "page_size" authored number changed from 7 to 9`) {
		t.Fatalf("Generate --check(renumbered) = %#v, %v", result, err)
	}
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatal("Generate --check mutated the renumbered Interface Project")
	}

	options.Check = false
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, protobufwiremap.ErrHistory) ||
		!reflect.DeepEqual(snapshotTree(t, root), beforeCheck) {
		t.Fatalf("Generate(renumbered) = %#v, %v", result, err)
	}

	writeFile(t, interfacePath, interfaceProtobufSource(0))
	beforeRemovalCheck := snapshotTree(t, root)
	options.Check = true
	drift, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil {
		t.Fatalf("Generate --check(removed): %v", err)
	}
	for _, changed := range []string{
		protoPath,
		protobufdescriptor.DescriptorSetPath,
		protobufwiremap.Path,
		"generated/manifest.json",
	} {
		if !slicesContains(drift.Report().Changed(), changed) {
			t.Fatalf("Interface field-removal drift omits %s: %#v", changed, drift.Report().Changes())
		}
	}
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeRemovalCheck) {
		t.Fatal("Generate --check mutated the Interface Project after a field removal")
	}

	options.Check = false
	updated, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !updated.Report().Clean() {
		t.Fatalf("Generate(updated) = changes %#v, %v", updated.Report().Changes(), err)
	}
	updatedSource := readFile(t, root, protoPath)
	for _, fragment := range []string{`reserved 7;`, `reserved "page_size";`} {
		if !bytes.Contains(updatedSource, []byte(fragment)) {
			t.Fatalf("updated Interface schema omits %q:\n%s", fragment, updatedSource)
		}
	}
	if bytes.Contains(updatedSource, []byte(`page_size =`)) {
		t.Fatalf("updated Interface schema retained the removed field:\n%s", updatedSource)
	}
	removedWireHistory := decodeInterfaceWireHistory(t, readFile(t, root, protobufwiremap.Path), "records.list/v1", "RecordsListV1Request")
	if len(removedWireHistory.Fields) != 1 ||
		!reflect.DeepEqual(removedWireHistory.ReservedNumbers, []int{7}) ||
		!reflect.DeepEqual(removedWireHistory.ReservedNames, []string{"page_size"}) {
		t.Fatalf("removed Interface wire history = %#v", removedWireHistory)
	}
	afterProvenance, err := applicationgen.DecodeManifestProvenance(readFile(t, root, "generated/manifest.json"))
	if err != nil {
		t.Fatalf("DecodeManifestProvenance(updated): %v", err)
	}
	if beforeProvenance.ApplicationModelDigest() == afterProvenance.ApplicationModelDigest() {
		t.Fatal("authored Interface field removal did not alter application_model_digest")
	}

	writeFile(t, interfacePath, interfaceProtobufSource(9))
	beforeReuse := snapshotTree(t, root)
	if result, err := applicationgenerate.Generate(t.Context(), options); !errors.Is(err, protobufwiremap.ErrHistory) ||
		!strings.Contains(err.Error(), `field "page_size" reuses a permanently reserved Protobuf name`) {
		t.Fatalf("Generate(reused field) = %#v, %v", result, err)
	}
	if afterReuse := snapshotTree(t, root); !reflect.DeepEqual(afterReuse, beforeReuse) {
		t.Fatal("failed reserved-name reuse mutated the Interface Project")
	}

	writeFile(t, interfacePath, interfaceProtobufSource(0))
	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() {
		t.Fatalf("Generate --check(clean) = changes %#v, %v", clean.Report().Changes(), err)
	}
}

func TestGenerateProjectsExposedIntrinsicKernelInterfaceMessages(t *testing.T) {
	root := t.TempDir()
	const modulePath = "example.com/intrinsic-interface-protobuf"
	writeConnectApplicationModule(t, root, modulePath)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose:
    - kernel.info/v1
    - kernel.health/v1
`)

	options := applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
	}
	initial, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !initial.Report().Clean() {
		t.Fatalf("Generate = changes %#v, %v", initial.Report().Changes(), err)
	}

	tests := []struct {
		path      string
		fragments []string
	}{
		{
			path: "generated/proto/plystra/generated/kernel/health/v1/interface.proto",
			fragments: []string{
				"message KernelHealthV1Request {",
				"message KernelHealthV1Response {",
				`optional string status = 1 [json_name = "status"];`,
				"service KernelHealthV1Service {",
				"rpc Invoke(.plystra.generated.kernel.health.v1.KernelHealthV1Request) returns (.plystra.generated.kernel.health.v1.KernelHealthV1Response);",
			},
		},
		{
			path: "generated/proto/plystra/generated/kernel/info/v1/interface.proto",
			fragments: []string{
				`optional string assembly_api = 1 [json_name = "assembly_api"];`,
				`optional string kernel_module = 2 [json_name = "kernel_module"];`,
				`optional string kernel_version = 3 [json_name = "kernel_version"];`,
				"service KernelInfoV1Service {",
				"rpc Invoke(.plystra.generated.kernel.info.v1.KernelInfoV1Request) returns (.plystra.generated.kernel.info.v1.KernelInfoV1Response);",
			},
		},
	}
	for _, test := range tests {
		source := readFile(t, root, test.path)
		for _, fragment := range test.fragments {
			if !bytes.Contains(source, []byte(fragment)) {
				t.Fatalf("%s omits %q:\n%s", test.path, fragment, source)
			}
		}
	}
	for _, identifier := range []string{"health", "info"} {
		path := "generated/proto/plystra/generated/kernel/" + identifier + "/v1/capability.proto"
		source := readFile(t, root, path)
		if !bytes.Contains(source, []byte(`import "plystra/generated/kernel/`+identifier+`/v1/interface.proto";`)) ||
			bytes.Contains(source, []byte("service ")) ||
			bytes.Contains(source, []byte("rpc ")) ||
			bytes.Contains(source, []byte("message ")) ||
			bytes.Contains(source, []byte("enum ")) {
			t.Fatalf("%s retained a competing legacy message contract:\n%s", path, source)
		}
	}
	assemblySource := readFile(t, root, "generated/go/assembly/interfaces_gen.go")
	for _, fragment := range []string{
		"func (runtime InterfaceRuntime) KernelHealthV1()",
		"func (runtime InterfaceRuntime) KernelInfoV1()",
		"kernelintrinsic.HealthContract()",
		"kernelintrinsic.InfoContract()",
	} {
		if !bytes.Contains(assemblySource, []byte(fragment)) {
			t.Fatalf("intrinsic Interface assembly omits %q:\n%s", fragment, assemblySource)
		}
	}
	writeFile(t, filepath.Join(root, "intrinsic_connect_test.go"), `package intrinsicinterfaceprotobuf_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	healthadapter "example.com/intrinsic-interface-protobuf/generated/go/adapters/connect/kernel/health/v1"
	infoadapter "example.com/intrinsic-interface-protobuf/generated/go/adapters/connect/kernel/info/v1"
	bootstrap "example.com/intrinsic-interface-protobuf/generated/go/bootstrap"
	healthv1 "github.com/plystra/kernel/interfaces/kernel/health/v1"
)

func TestIntrinsicConnectHandlersUseGovernedInterfaceAccessors(t *testing.T) {
	application, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{})
	if err != nil || !application.Valid() {
		t.Fatalf("bootstrap.New = %#v, %v", application, err)
	}
	health, err := application.Interfaces().KernelHealthV1().Health(context.Background(), healthv1.Request{})
	if err != nil || health.Status != healthv1.StatusHealthy {
		t.Fatalf("internal health = %#v, %v", health, err)
	}
	root := func(parent context.Context, _ http.Header) (context.Context, error) { return parent, nil }
	healthHandler, err := healthadapter.New(root, application.Interfaces().KernelHealthV1())
	if err != nil || !healthadapter.Available(healthHandler) {
		t.Fatalf("healthadapter.New = %#v, %v", healthHandler, err)
	}
	infoHandler, err := infoadapter.New(root, application.Interfaces().KernelInfoV1())
	if err != nil || !infoadapter.Available(infoHandler) {
		t.Fatalf("infoadapter.New = %#v, %v", infoHandler, err)
	}
	server := httptest.NewServer(healthHandler)
	defer server.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+healthadapter.Procedure, bytes.NewBufferString("{}"))
	if err != nil {
		t.Fatalf("http.NewRequestWithContext: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connect-Protocol-Version", "1")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("Connect health: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`+"`"+`"status":"healthy"`+"`"+`)) {
		t.Fatalf("Connect health = status %d body %s, %v", response.StatusCode, body, err)
	}
}
`)

	command := exec.CommandContext(t.Context(), "go", "test", "./...", "-count=1")
	command.Dir = root
	command.Env = mergedEnvironment(map[string]string{
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go test generated intrinsic Interface Project: %v\n%s", err, output)
	}

	beforeCheck := snapshotTree(t, root)
	options.Check = true
	clean, err := applicationgenerate.Generate(t.Context(), options)
	if err != nil || !clean.Report().Clean() {
		t.Fatalf("Generate --check = changes %#v, %v", clean.Report().Changes(), err)
	}
	if afterCheck := snapshotTree(t, root); !reflect.DeepEqual(afterCheck, beforeCheck) {
		t.Fatal("Generate --check mutated the intrinsic Interface Project")
	}
}

func TestGenerateTransitionalAliasAgainstIntrinsicInterfaceCompiles(t *testing.T) {
	root := t.TempDir()
	writeConnectApplicationModule(t, root, "example.com/intrinsic-interface-alias")
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose: [kernel.health/v1]
capabilities:
  aliases:
    health.status/v1: kernel.health/v1
`)

	generated, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start:       root,
		Environment: goEnvironment(nil),
	})
	if err != nil || !generated.Report().Clean() {
		t.Fatalf("Generate = changes %#v, %v", generated.Report().Changes(), err)
	}
	aliasSource := readFile(t, root, "generated/go/adapters/connect/health/status/v1/handler_gen.go")
	canonicalSource := readFile(t, root, "generated/go/adapters/connect/kernel/health/v1/handler_gen.go")
	if !bytes.Contains(aliasSource, []byte("target.InvokeRequested")) ||
		!bytes.Contains(canonicalSource, []byte(`case "health.status/v1":`)) ||
		bytes.Contains(canonicalSource, []byte("applicationinvocation")) {
		t.Fatalf("transitional Alias does not remain on the canonical governed Interface handler:\nAlias:\n%s\nCanonical:\n%s", aliasSource, canonicalSource)
	}
}

func interfaceProtobufSource(pageSizeNumber int) string {
	pageSizeField := ""
	if pageSizeNumber > 0 {
		pageSizeField = "\tPageSize int32            `json:\"page_size\" plystra:\"" + strconv.Itoa(pageSizeNumber) + ",required\"`\n"
	}
	return strings.ReplaceAll(`package listv1

import "context"

//plystra:interface records.list/v1
type Interface interface {
	List(context.Context, Request) (Response, error)
}

type Request struct {
PAGE_SIZE_FIELD
	Labels   map[string]int64 `+"`json:\"labels\" plystra:\"2\"`"+`
}

type Response struct {
	Records []Record `+"`json:\"records\" plystra:\"11,required\"`"+`
}

type Record struct {
	ID string `+"`json:\"id\" plystra:\"1,required\"`"+`
}
`, "PAGE_SIZE_FIELD\n", pageSizeField)
}

type interfaceWireHistory struct {
	Fields map[string]struct {
		Number int `json:"number"`
	} `json:"fields"`
	ReservedNumbers []int    `json:"reserved_numbers"`
	ReservedNames   []string `json:"reserved_names"`
}

type interfaceProcedureHistory struct {
	Service   string `json:"service"`
	Method    string `json:"method"`
	Procedure string `json:"procedure"`
}

func decodeInterfaceProcedureHistory(t testing.TB, data []byte, identifier string) interfaceProcedureHistory {
	t.Helper()
	var document struct {
		Interfaces map[string]interfaceProcedureHistory `json:"interfaces"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode Interface procedure history: %v", err)
	}
	history, exists := document.Interfaces[identifier]
	if !exists {
		t.Fatalf("Interface %s is absent from procedure history", identifier)
	}
	return history
}

func decodeInterfaceWireHistory(t testing.TB, data []byte, identifier, message string) interfaceWireHistory {
	t.Helper()
	var document struct {
		Interfaces map[string]struct {
			Messages map[string]interfaceWireHistory `json:"messages"`
		} `json:"interfaces"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode Interface wire history: %v", err)
	}
	record, exists := document.Interfaces[identifier]
	if !exists {
		t.Fatalf("Interface %s is absent from wire history", identifier)
	}
	history, exists := record.Messages[message]
	if !exists {
		t.Fatalf("message %s is absent from Interface %s wire history", message, identifier)
	}
	return history
}
