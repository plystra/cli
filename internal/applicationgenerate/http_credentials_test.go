package applicationgenerate_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
)

func TestGeneratedHTTPCredentialsThroughConnect(t *testing.T) {
	root := t.TempDir()
	writeConnectApplicationModule(t, root, "example.com/http-credentials")
	writeFile(t, filepath.Join(root, "plystra.yaml"), `http:
  expose: {records.echo/v1: {transport: connect}}
`)
	writeFile(t, filepath.Join(root, "interfaces", "records", "echo", "v1", "interface.go"), `package echov1
import "context"
//plystra:interface records.echo/v1
type Interface interface { Echo(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct {
  Present bool `+"`json:\"present\" plystra:\"1\"`"+`
  Expected bool `+"`json:\"expected\" plystra:\"2\"`"+`
}
`)
	writeFile(t, filepath.Join(root, "records", "service.go"), `package records
import (
  "context"
  "encoding/json"
  "errors"
  echov1 "example.com/http-credentials/interfaces/records/echo/v1"
  "github.com/plystra/kernel/contextmetadata"
  "github.com/plystra/kernel/invocation"
)
type Service struct{}
//plystra:implements records.echo/v1
func New() (*Service, error) { return &Service{}, nil }
func (*Service) Echo(ctx context.Context, request echov1.Request) (echov1.Response, error) {
  if _, ok := invocation.Current(ctx); !ok { return echov1.Response{}, errors.New("ungoverned invocation") }
  data, present, err := contextmetadata.Bytes(ctx, "plystra.http-credential-metadata/v1")
  if err != nil { return echov1.Response{}, err }
  if !present { return echov1.Response{}, nil }
  var input struct { Transport string; InterfaceID string `+"`json:\"interface_id\"`"+`; Bearer string; Cookies []struct{Name, Value string} }
  if err := json.Unmarshal(data, &input); err != nil { return echov1.Response{}, err }
  expected := input.Transport == "connect" && input.InterfaceID == "records.echo/v1" && input.Bearer == "fixture-canary" &&
    len(input.Cookies) == 2 && input.Cookies[0].Name == "a" && input.Cookies[0].Value == "first" && input.Cookies[1].Name == "z"
  return echov1.Response{Present: true, Expected: expected}, nil
}
`)
	_, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: root, Environment: goEnvironment(nil),
		Validate: func(ctx context.Context, start string) error {
			cmd := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "./...")
			cmd.Dir, cmd.Env = start, goEnvironment(nil)
			if output, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("build generated credential Project: %w\n%s", err, output)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertGeneratedArtifactEvidence(t, readGeneratedArtifact(t, root, "generated/go/internal/connectschema/credentials_gen.go"), "plystra.connect-generator/v1", []string{"interface:records.echo/v1"}, nil)
	runtimeTestPath := filepath.Join(root, "generated", "go", "adapters", "connect", "records", "echo", "v1", "credentials_test.go")
	writeFile(t, runtimeTestPath, generatedHTTPCredentialsTests)
	for _, race := range []bool{false, true} {
		args := []string{"test", "-mod=readonly", "./generated/go/adapters/connect/records/echo/v1", "-run", "^TestHTTPCredentials", "-count=1", "-timeout", "60s"}
		if race {
			args = append(args, "-race")
		}
		cmd := exec.CommandContext(t.Context(), "go", args...)
		cmd.Dir, cmd.Env = root, goEnvironment(nil)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("targeted generated credential tests (race=%v): %v\n%s", race, err, output)
		}
	}
	if err := os.Remove(runtimeTestPath); err != nil {
		t.Fatal(err)
	}
	checked, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{Start: root, Check: true, Environment: goEnvironment(nil)})
	if err != nil || !checked.Report().Clean() {
		t.Fatalf("credential generation drift: %v, %v", checked.Report().Changes(), err)
	}
}

const generatedHTTPCredentialsTests = `package recordsechov1_test
import (
  "context"
  "encoding/json"
  "fmt"
  "io"
  "net/http"
  "net/http/httptest"
  "os"
  "path/filepath"
  "strings"
  "sync"
  "sync/atomic"
  "testing"

  "connectrpc.com/connect"
  adapter "example.com/http-credentials/generated/go/adapters/connect/records/echo/v1"
  "example.com/http-credentials/generated/go/bootstrap"
  schema "example.com/http-credentials/generated/go/internal/connectschema"
  contract "example.com/http-credentials/interfaces/records/echo/v1"
  "github.com/plystra/kernel/contextmetadata"
  "google.golang.org/protobuf/encoding/protojson"
  "google.golang.org/protobuf/types/dynamicpb"
)
const metadataKey = "plystra.http-credential-metadata/v1"

func TestHTTPCredentialsExtraction(t *testing.T) {
  for _, transport := range []string{"connect", "rest"} {
    headers := http.Header{"authorization": {"bEaReR fixture-canary"}, "Cookie": {"z=last; a=\"first\""}, "Unrelated": {"private-unused-header"}}
    frozen, err := schema.ParseHTTPCredentials(context.Background(), transport, "records.echo/v1", headers)
    if err != nil { t.Fatal(err) }
    headers.Set("authorization", "Bearer changed")
    ctx, err := frozen.WithContext(context.Background())
    if err != nil { t.Fatal(err) }
    raw, present, err := contextmetadata.Bytes(ctx, metadataKey)
    if err != nil || !present { t.Fatal("missing candidates") }
    want := "{\"transport\":\""+transport+"\",\"interface_id\":\"records.echo/v1\",\"bearer\":\"fixture-canary\",\"cookies\":[{\"name\":\"a\",\"value\":\"first\"},{\"name\":\"z\",\"value\":\"last\"}]}"
    if string(raw) != want { t.Fatal("candidate schema, ordering or frozen copy differs") }
    raw[0] = '!'
    again, _, _ := contextmetadata.Bytes(ctx, metadataKey)
    if string(again) != want { t.Fatal("metadata copy mutated stored candidates") }
    for _, value := range []string{fmt.Sprint(ctx), fmt.Sprintf("%+v", frozen), fmt.Sprintf("%#v", frozen)} {
      if strings.Contains(value, "fixture-canary") { t.Fatal("formatted context exposed candidates") }
    }
    if _, err := frozen.WithContext(ctx); err == nil { t.Fatal("accepted root metadata conflict") }
    if _, err := schema.ParseHTTPCredentials(ctx, transport, "records.echo/v1", nil); err == nil { t.Fatal("accepted ingress metadata conflict") }
    cancelled, cancel := context.WithCancel(context.Background())
    attached, err := frozen.WithContext(cancelled)
    cancel()
    if err != nil || attached.Err() != context.Canceled { t.Fatal("lost cancellation") }
    var wg sync.WaitGroup
    for range 16 {
      wg.Go(func() {
        if _, err := frozen.WithContext(context.Background()); err != nil { t.Error(err) }
      })
    }
    wg.Wait()
  }
  empty, err := schema.ParseHTTPCredentials(context.Background(), "connect", "records.echo/v1", nil)
  if err != nil { t.Fatal(err) }
  ctx, _ := empty.WithContext(context.Background())
  raw, _, _ := contextmetadata.Bytes(ctx, metadataKey)
  if string(raw) != "{\"transport\":\"connect\",\"interface_id\":\"records.echo/v1\",\"cookies\":[]}" { t.Fatal("anonymous schema differs") }
  if _, err := (schema.HTTPCredentials{}).WithContext(context.Background()); err == nil { t.Fatal("accepted zero candidate") }
}

func TestHTTPCredentialsRejectMalformedInput(t *testing.T) {
  cases := []struct{name string; headers http.Header}{
    {"duplicate authorization", http.Header{"Authorization": {"Bearer one", "Bearer two"}}},
    {"mixed case authorization", http.Header{"Authorization": {"Bearer one"}, "authorization": {"Bearer two"}}},
    {"empty authorization list", http.Header{"Authorization": {}}},
    {"scheme", http.Header{"Authorization": {"Basic fixture-canary"}}},
    {"empty bearer", http.Header{"Authorization": {"Bearer "}}},
    {"tab separator", http.Header{"Authorization": {"Bearer\tfixture-canary"}}},
    {"space bearer", http.Header{"Authorization": {"Bearer fixture-canary extra"}}},
    {"control bearer", http.Header{"Authorization": {"Bearer fixture-canary\r\n"}}},
    {"unicode bearer", http.Header{"Authorization": {"Bearer fixture-canary\u00e9"}}},
    {"oversized bearer", http.Header{"Authorization": {"Bearer "+strings.Repeat("x",65537)}}},
    {"cookie pair", http.Header{"Cookie": {"fixture-canary"}}},
    {"cookie trailing separator", http.Header{"Cookie": {"a=fixture-canary;"}}},
    {"duplicate cookie", http.Header{"Cookie": {"a=fixture-canary; a=other"}}},
    {"cross field duplicate cookie", http.Header{"Cookie": {"a=fixture-canary"}, "cookie": {"a=other"}}},
    {"cookie quote", http.Header{"Cookie": {"a=\"fixture-canary"}}},
    {"cookie control", http.Header{"Cookie": {"a=fixture-canary\r\n"}}},
    {"cookie invalid name", http.Header{"Cookie": {"a b=fixture-canary"}}},
    {"cookie oversized name", http.Header{"Cookie": {strings.Repeat("n",257)+"=fixture-canary"}}},
    {"cookie oversized value", http.Header{"Cookie": {"a="+strings.Repeat("x",4097)}}},
    {"cookie count", http.Header{"Cookie": {strings.Repeat("a=x;",32)+"z=x"}}},
    {"empty cookie list", http.Header{"Cookie": {}}},
  }
  for _, tc := range cases {
    t.Run(tc.name, func(t *testing.T) {
      _, err := schema.ParseHTTPCredentials(context.Background(), "connect", "records.echo/v1", tc.headers)
      if err == nil || strings.Contains(err.Error(), "fixture-canary") { t.Fatal("expected redacted rejection") }
    })
  }
  for _, invalid := range []struct{ctx context.Context; transport, id string}{
    {nil, "connect", "records.echo/v1"},
    {context.Background(), "grpc", "records.echo/v1"},
    {context.Background(), "connect", "records.echo/v01"},
    {context.Background(), "connect", "records.echo/v+1"},
    {context.Background(), "connect", "records/echo/v1"},
  } {
    if _, err := schema.ParseHTTPCredentials(invalid.ctx, invalid.transport, invalid.id, nil); err == nil { t.Fatal("accepted invalid metadata identity") }
  }
}

func TestHTTPCredentialsBounds(t *testing.T) {
  // Exact aggregate: 16 names of 256 bytes plus 16 values of 3840 bytes.
  pairs := make([]string, 16)
  for i := range pairs { pairs[i] = fmt.Sprintf("%03d", i)+strings.Repeat("n",253)+"="+strings.Repeat("x",3840) }
  headers := http.Header{"Authorization": {"Bearer "+strings.Repeat("x",65536)}, "Cookie": {strings.Join(pairs,"; ")}}
  if _, err := schema.ParseHTTPCredentials(context.Background(),"connect","records.echo/v1",headers); err != nil { t.Fatal("rejected exact aggregate bound", err) }
  pairs[0] += "x"
  headers.Set("Cookie", strings.Join(pairs,"; "))
  if _, err := schema.ParseHTTPCredentials(context.Background(),"connect","records.echo/v1",headers); err == nil { t.Fatal("accepted oversized aggregate") }
  pairs = make([]string,32)
  for i := range pairs { pairs[i] = fmt.Sprintf("n%d=",i) }
  headers = http.Header{"Cookie": {strings.Join(pairs,"; ")}}
  if _, err := schema.ParseHTTPCredentials(context.Background(),"connect","records.echo/v1",headers); err != nil { t.Fatal("rejected 32 empty values",err) }
  headers = http.Header{"Cookie": {"a="+strings.Repeat("x",4096)}}
  if _, err := schema.ParseHTTPCredentials(context.Background(),"connect","records.echo/v1",headers); err != nil { t.Fatal("rejected maximum value",err) }
  oversized, err := contextmetadata.WithBytes(context.Background(), "other.metadata/v1", []byte("x"))
  if err != nil { t.Fatal(err) }
  for i := range 31 {
    oversized, err = contextmetadata.WithBytes(oversized, fmt.Sprintf("other.metadata-%d/v1",i), []byte("x"))
    if err != nil { t.Fatal(err) }
  }
  frozen, err := schema.ParseHTTPCredentials(oversized,"connect","records.echo/v1",nil)
  if err != nil { t.Fatal(err) }
  if _, err := frozen.WithContext(oversized); err == nil { t.Fatal("accepted generic metadata entry overflow") }
}

func TestHTTPCredentialsHTTPAndGovernedCall(t *testing.T) {
  original, err := os.Getwd()
  if err != nil { t.Fatal(err) }
  if err := os.Chdir(filepath.Join("..","..","..","..","..","..","..")); err != nil { t.Fatal(err) }
  t.Cleanup(func(){ if err := os.Chdir(original); err != nil { t.Error(err) } })
  app, err := bootstrap.New(context.Background(), bootstrap.RuntimeOptions{Arguments: []string{"--configuration-root", ".", "--runtime-baseline", "dist/runtime-baseline.json"}})
  if err != nil || !app.Valid() { t.Fatal("bootstrap",err) }
  if err := app.Start(context.Background()); err != nil { t.Fatal(err) }
  t.Cleanup(func(){ if err := app.Stop(context.Background()); err != nil { t.Error(err) } })
  internal, err := app.Interfaces().RecordsEchoV1().Echo(context.Background(),contract.Request{})
  if err != nil || internal.Present { t.Fatal("internal call invented transport candidates",err) }
  var roots atomic.Int32
  handler, err := adapter.New(func(ctx context.Context, headers http.Header)(context.Context,error){
    roots.Add(1)
    headers.Set("Authorization","Bearer modified-by-root")
    headers.Set("Cookie","modified=true")
    return context.Background(),nil
  },app.Interfaces().RecordsEchoV1())
  if err != nil { t.Fatal(err) }
  server := httptest.NewServer(handler)
  defer server.Close()
  send := func(headers http.Header) (int, []byte) {
    req, err := http.NewRequestWithContext(t.Context(), "POST",server.URL+adapter.Procedure,strings.NewReader("{}"))
    if err != nil { t.Fatal(err) }
    req.Header = headers.Clone()
    req.Header.Set("Content-Type","application/json")
    req.Header.Set("Connect-Protocol-Version","1")
    response, err := server.Client().Do(req)
    if err != nil { t.Fatal(err) }
    defer response.Body.Close()
    body, err := io.ReadAll(response.Body)
    if err != nil { t.Fatal(err) }
    if strings.Contains(string(body),"fixture-canary") { t.Fatal("response exposed candidates") }
    return response.StatusCode, body
  }
  status, body := send(http.Header{"Authorization":{"Bearer fixture-canary"},"Cookie":{"z=last; a=first"}})
  var result struct{Present, Expected bool}
  if status != 200 || json.Unmarshal(body,&result) != nil || !result.Present || !result.Expected { t.Fatalf("credential did not reach governed Implementation: status=%d",status) }
  before := roots.Load()
  for _, bad := range []http.Header{
    {"Authorization":{"Basic fixture-canary"}},
    {"Authorization":{"Bearer fixture-canary","Bearer other"}},
    {"Cookie":{"a=fixture-canary; a=other"}},
  } {
    status, body := send(bad)
    if status != 400 || !strings.Contains(string(body),"not_started") { t.Fatalf("unsafe malformed request outcome: %d",status) }
  }
  if roots.Load() != before { t.Fatal("malformed candidates reached root callback") }

  method, err := schema.Method("plystra.generated.records.echo.v1.RecordsEchoV1Service.Invoke")
  if err != nil { t.Fatal(err) }
  message := dynamicpb.NewMessage(method.Input())
  if err := protojson.Unmarshal([]byte("{}"), message); err != nil { t.Fatal(err) }
  conflicting, _ := contextmetadata.WithBytes(context.Background(),metadataKey,[]byte("private-conflict"))
  _, err = handler.Invoke(conflicting, connect.NewRequest(message))
  if connect.CodeOf(err) != connect.CodeInvalidArgument || roots.Load() != before { t.Fatal("ingress metadata conflict was ignored") }
  rootConflict, err := adapter.New(func(context.Context,http.Header)(context.Context,error){ return conflicting,nil },app.Interfaces().RecordsEchoV1())
  if err != nil { t.Fatal(err) }
  _, err = rootConflict.Invoke(context.Background(),connect.NewRequest(message))
  if connect.CodeOf(err) != connect.CodeInvalidArgument { t.Fatal("detached root conflict was ignored") }
  if strings.Contains(err.Error(),"private-conflict") { t.Fatal("unsafe conflict error") }
  cancelled, cancel := context.WithCancel(context.Background())
  cancel()
  _, err = handler.Invoke(cancelled,connect.NewRequest(message))
  if connect.CodeOf(err) != connect.CodeCanceled { t.Fatal("detached credential root lost caller cancellation") }
}
`
