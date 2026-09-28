package applicationgenerate_test

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
)

func TestPublicGenerateValidatesAndIsolatesInterfaceValues(t *testing.T) {
	root := t.TempDir()
	writeApplicationModule(t, root, "example.com/acme/value-ownership")
	writeFile(t, filepath.Join(root, "plystra.yaml"), "interfaces: {require: [values.run/v1, bounded.run/v1]}\n")
	writeFile(t, filepath.Join(root, "interfaces/values/run/v1/interface.go"), strings.ReplaceAll(ownershipContract, "~", "`"))
	writeFile(t, filepath.Join(root, "interfaces/values/run/v1/interface.yaml"), ownershipMetadata)
	writeFile(t, filepath.Join(root, "interfaces/bounded/run/v1/interface.go"), strings.ReplaceAll(boundedContract, "~", "`"))
	writeFile(t, filepath.Join(root, "implementation/service.go"), ownershipImplementation)
	runCLI := func(args ...string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := command.RunIn(args, &stdout, &stderr, root, goEnvironment(nil)); code != 0 {
			t.Fatalf("plystra %v exited %d:\n%s\n%s", args, code, stdout.Bytes(), stderr.Bytes())
		}
	}
	runCLI("generate")
	writeFile(t, filepath.Join(root, "ownership_test.go"), ownershipRuntimeTests)
	compiled := exec.CommandContext(t.Context(), "go", "test", "-race", "./...", "-count=1")
	compiled.Dir = root
	compiled.Env = mergedEnvironment(map[string]string{"GOWORK": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=readonly"})
	if output, err := compiled.CombinedOutput(); err != nil {
		t.Fatalf("generated ownership runtime: %v\n%s", err, output)
	}
	runCLI("generate", "--check")
}

const ownershipContract = `package runv1
import ("context"; "time")
//plystra:interface values.run/v1
type Interface interface { Run(context.Context, Request) (Response, error) }
type Request struct { Value Payload ~plystra:"1" json:"value"~ }
type Response struct { Value Payload ~plystra:"1" json:"value"~ }
type Payload struct {
	Text string ~plystra:"1" json:"text"~
	Required *int32 ~plystra:"2,required" json:"required"~
	Nullable **string ~plystra:"3" json:"nullable"~
	Data []byte ~plystra:"4" json:"data"~
	Items []Leaf ~plystra:"5" json:"items"~
	Names map[string]Leaf ~plystra:"6" json:"names"~
	Flags map[bool][]byte ~plystra:"7"~
	I32 map[int32]Leaf ~plystra:"8"~
	I64 map[int64]Leaf ~plystra:"9"~
	U32 map[uint32]Leaf ~plystra:"10"~
	U64 map[uint64]Leaf ~plystra:"11"~
	PBytes *[]byte ~plystra:"12" json:"pbytes"~
	PItems *[]Leaf ~plystra:"13" json:"pitems"~
	PMap *map[string]Leaf ~plystra:"14" json:"pmap"~
	PLeaf **Leaf ~plystra:"15"~
	Number float64 ~plystra:"16" json:"number"~
	SmallFloat float32 ~plystra:"17" json:"small_float"~
	Signed int64 ~plystra:"18" json:"signed"~
	Unsigned uint64 ~plystra:"19" json:"unsigned"~
	Stamp time.Time ~plystra:"20"~
	Duration time.Duration ~plystra:"21"~
	Zero int32 ~plystra:"22,required"~
	RequiredNull **string ~plystra:"23,required" json:"required_null"~
	Reused *string ~plystra:"24"~
	Nested Leaf ~plystra:"25" json:"nested"~
	Other Leaf ~plystra:"26" json:"other"~
	ByteItems [][]byte ~plystra:"27"~
	Bool bool ~plystra:"28"~
}
type Leaf struct { Label string ~plystra:"1" json:"label"~; Data []byte ~plystra:"2"~ }
`

const boundedContract = `package boundedv1
import "context"
//plystra:interface bounded.run/v1
type Interface interface { Run(context.Context, Request) (Response, error) }
type Request struct {
	Items []int32 ~plystra:"1"~
	Tree []Node ~plystra:"2"~
	Map map[string]Node ~plystra:"3"~
}
type Response struct { Value Request ~plystra:"1"~ }
type Node struct { Children []Node ~plystra:"1"~; Lookup map[string]Node ~plystra:"2"~ }
`

const ownershipMetadata = `constraints:
  request.value.text: {min_length: 2, max_length: 8, pattern: '^\p{L}+$'}
  request.value.nullable: {min_length: 1}
  request.value.data: {max_length: 3}
  request.value.items: {max_items: 2}
  request.value.names: {max_items: 2}
  request.value.names.label: {min_length: 2}
  request.value.items.label: {min_length: 2}
  request.value.I32.label: {min_length: 2}
  request.value.I64.label: {min_length: 2}
  request.value.U32.label: {min_length: 2}
  request.value.U64.label: {min_length: 2}
  request.value.PLeaf.label: {min_length: 2}
  request.value.pbytes: {min_length: 1}
  request.value.pitems: {min_items: 1}
  request.value.pmap: {min_items: 1}
  request.value.number: {minimum: -1, maximum: 1}
  request.value.small_float: {minimum: -0.5, maximum: 0.5}
  request.value.signed: {minimum: -9223372036854775808, maximum: 9223372036854775807}
  request.value.unsigned: {minimum: 0, maximum: 18446744073709551615}
  request.value.nested.label: {min_length: 2}
  request.value.other.label: {max_length: 1}
  response.value.text: {min_length: 2}
`

const ownershipImplementation = `package implementation
import (
	"context"
	values "example.com/acme/value-ownership/interfaces/values/run/v1"
	bounded "example.com/acme/value-ownership/interfaces/bounded/run/v1"
)
var Calls int
var Retained values.Payload
var Invalid bool
var During func()
type Values struct{}
//plystra:implements values.run/v1
func NewValues() (*Values, error) { return &Values{}, nil }
func (*Values) Run(_ context.Context, request values.Request) (values.Response, error) {
	Calls++
	if During != nil { During() }
	Retained = request.Value
	if len(Retained.Data) > 0 { Retained.Data[0]++ }
	if Retained.Required != nil { *Retained.Required++ }
	if Invalid { Retained.Text = "x" }
	return values.Response{Value: Retained}, nil
}
type Bounded struct{}
//plystra:implements bounded.run/v1
func NewBounded() (*Bounded, error) { return &Bounded{}, nil }
func (*Bounded) Run(_ context.Context, request bounded.Request) (bounded.Response, error) {
	return bounded.Response{Value: request}, nil
}
`

const ownershipRuntimeTests = `package ownership_test
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	assembly "example.com/acme/value-ownership/generated/go/assembly"
	proxy "example.com/acme/value-ownership/generated/go/proxies/values/run/v1"
	boundedproxy "example.com/acme/value-ownership/generated/go/proxies/bounded/run/v1"
	adapter "example.com/acme/value-ownership/generated/go/adapters/implementations/values/run/v1"
	values "example.com/acme/value-ownership/interfaces/values/run/v1"
	bounded "example.com/acme/value-ownership/interfaces/bounded/run/v1"
	implementation "example.com/acme/value-ownership/implementation"
	"github.com/plystra/kernel/invocation"
)

func ptr[T any](value T) *T { return &value }
func valid() values.Payload {
	shared := ptr("ok")
	leaf := values.Leaf{Label: "ok", Data: []byte{1}}
	return values.Payload{
		Text: "ok", Required: ptr(int32(0)), Nullable: &shared, RequiredNull: ptr((*string)(nil)),
		Data: []byte{1}, Items: []values.Leaf{leaf}, Names: map[string]values.Leaf{"ok": leaf},
		Flags: map[bool][]byte{false: {1}, true: {2}}, I32: map[int32]values.Leaf{-1: leaf},
		I64: map[int64]values.Leaf{math.MinInt64: leaf}, U32: map[uint32]values.Leaf{math.MaxUint32: leaf},
		U64: map[uint64]values.Leaf{math.MaxUint64: leaf}, PBytes: ptr([]byte{1}),
		PItems: ptr([]values.Leaf{leaf}), PMap: ptr(map[string]values.Leaf{"ok": leaf}), PLeaf: ptr(&leaf),
		Signed: math.MinInt64, Unsigned: math.MaxUint64, Stamp: time.Now(), Duration: -time.Second,
		Reused: shared, Nested: leaf, Other: values.Leaf{Label: "x"}, ByteItems: [][]byte{{1}},
	}
}
func mutate(value values.Payload) {
	*value.Required = 99; **value.Nullable = "changed"; *value.Reused = "changed"
	value.Data[0] = 99; value.Items[0].Data[0] = 99; value.Items[0].Label = "changed"
	value.Names["ok"].Data[0] = 99; value.Names["new"] = values.Leaf{}
	value.Flags[false][0] = 99; value.I32[-1].Data[0] = 99; value.I64[math.MinInt64].Data[0] = 99
	value.U32[math.MaxUint32].Data[0] = 99; value.U64[math.MaxUint64].Data[0] = 99
	(*value.PBytes)[0] = 99; (*value.PItems)[0].Data[0] = 99; (*value.PMap)["ok"].Data[0] = 99
	(**value.PLeaf).Data[0] = 99; value.Nested.Data[0] = 99; value.ByteItems[0][0] = 99
}
func runtime(t *testing.T) assembly.InterfaceRuntime {
	t.Helper()
	runtime, err := assembly.NewInterfaceRuntime(assembly.ConstructorConfiguration{}, time.Second)
	if err != nil { t.Fatal(err) }
	t.Cleanup(func() { if err := runtime.Stop(context.Background()); err != nil { t.Error(err) } })
	return runtime
}
func TestOwnedGraphsThroughPublicGeneratedRuntime(t *testing.T) {
	runtime := runtime(t)
	request := values.Request{Value: valid()}
	baseline, err := proxy.CopyRequest(request)
	if err != nil { t.Fatal(err) }
	response, err := runtime.ValuesRunV1().Run(context.Background(), request)
	if err != nil { t.Fatal(err) }
	if request.Value.Data[0] != 1 || *request.Value.Required != 0 { t.Fatal("attempt mutated caller") }
	if response.Value.Data[0] != 2 || *response.Value.Required != 1 { t.Fatal("attempt mutation not returned") }
	resultBaseline, err := proxy.CopyResponse(response)
	if err != nil { t.Fatal(err) }
	mutate(implementation.Retained)
	if !reflect.DeepEqual(response, resultBaseline) { t.Fatal("result aliases target storage") }
	callerAfter, err := proxy.CopyRequest(request)
	if err != nil || !reflect.DeepEqual(callerAfter, baseline) { t.Fatal("retained attempt aliases caller") }
	mutate(response.Value)
	if request.Value.Data[0] != 1 { t.Fatal("caller result aliases caller request") }
}
func TestIndependentAttemptsAndConcurrentCallerMutation(t *testing.T) {
	runtime := runtime(t)
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{DefaultTimeout: time.Second})
	if err != nil { t.Fatal(err) }
	if err := dispatcher.Publish(runtime.Catalog()); err != nil { t.Fatal(err) }
	handle, err := invocation.NewHandle(dispatcher, adapter.Contract(), true)
	if err != nil { t.Fatal(err) }
	snapshot, err := proxy.CopyRequest(values.Request{Value: valid()})
	if err != nil { t.Fatal(err) }
	for i := 0; i < 2; i++ {
		response, err := handle.Invoke(context.Background(), snapshot)
		if err != nil || response.Value.Data[0] != 2 || *response.Value.Required != 1 { t.Fatalf("attempt %d: %v", i, err) }
	}
	if snapshot.Value.Data[0] != 1 || *snapshot.Value.Required != 0 { t.Fatal("adapter changed immutable snapshot") }
	request := values.Request{Value: valid()}
	implementation.During = func() {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); for i := 0; i < 100; i++ { mutate(request.Value) } }()
		for i := 0; i < 100; i++ { _, _ = proxy.CopyRequest(snapshot) }
		wg.Wait()
	}
	defer func() { implementation.During = nil }()
	response, err := runtime.ValuesRunV1().Run(context.Background(), request)
	if err != nil || response.Value.Data[0] != 2 { t.Fatalf("independent caller mutation: %v", err) }
}
func TestCanonicalPresenceAndValues(t *testing.T) {
	value := valid()
	value.Nullable = ptr((*string)(nil))
	value.Items = []values.Leaf{}; value.Names = map[string]values.Leaf{}; value.Data = []byte{}
	value.Flags = map[bool][]byte{false: {}}
	value.Stamp = time.Date(2026, 9, 28, 12, 30, 0, 0, time.FixedZone("private-zone", 3600))
	result, err := proxy.CopyRequest(values.Request{Value: value})
	if err != nil { t.Fatal(err) }
	got := result.Value
	if got.Nullable == nil || *got.Nullable != nil || got.RequiredNull == nil || *got.RequiredNull != nil { t.Fatal("explicit null lost") }
	if got.Items != nil || got.Names != nil || got.Data != nil || got.Flags[false] != nil { t.Fatal("empty collections not canonical") }
	if got.Stamp.Location() != time.UTC || !got.Stamp.Equal(value.Stamp) { t.Fatal("timestamp not canonical") }
	value.Nullable = nil; value.PBytes = nil; value.PItems = nil; value.PMap = nil; value.PLeaf = nil
	value.Text = "\u00e9\u4e2d"
	if _, err := proxy.CopyRequest(values.Request{Value: value}); err != nil { t.Fatal(err) }
	// Response has no collection constraints, so present empty pointers remain present.
	value.PBytes = ptr([]byte{}); value.PItems = ptr([]values.Leaf{}); value.PMap = ptr(map[string]values.Leaf{})
	response, err := proxy.CopyResponse(values.Response{Value: value})
	if err != nil { t.Fatal(err) }
	if response.Value.PBytes == nil || *response.Value.PBytes != nil || response.Value.PItems == nil || *response.Value.PItems != nil || response.Value.PMap == nil || *response.Value.PMap != nil { t.Fatal("present empties lost presence") }
	alias, err := proxy.CopyRequest(values.Request{Value: valid()})
	if err != nil { t.Fatal(err) }
	if *alias.Value.Nullable == alias.Value.Reused { t.Fatal("reference identity was preserved") }
}
func requireValueError(t *testing.T, err error, side, path, rule string) {
	t.Helper()
	var value *proxy.ValueError
	var boundary *invocation.Error
	if !errors.As(err, &value) || !errors.As(err, &boundary) || value.Interface() != "values.run/v1" || value.Side() != side || value.Path() != path || value.Rule() != rule { t.Fatalf("unexpected validation error: %T %v", err, err) }
	want := invocation.ErrorInvalidArgument
	if side == "response" { want = invocation.ErrorInternal }
	if boundary.Code() != want || boundary.DetailCode() != "contract."+side+"_invalid" { t.Fatalf("boundary: %v", boundary) }
	var log bytes.Buffer
	slog.New(slog.NewTextHandler(&log, nil)).Error("failure", "error", err)
	for _, text := range []string{fmt.Sprintf("%v %+v %#v", err, err, err), fmt.Sprint(errors.Unwrap(value)), log.String()} {
		if strings.Contains(text, "private") { t.Fatalf("leaked field or map key: %s", text) }
	}
}
func TestInvalidRequestsNeverEnterTarget(t *testing.T) {
	runtime := runtime(t)
	tests := []struct { name, path, rule string; change func(*values.Payload) }{
		{"required before constraints", "required", "required", func(p *values.Payload) { p.Text = ""; p.Required = nil }},
		{"required nullable", "required_null", "required", func(p *values.Payload) { p.RequiredNull = nil }},
		{"minimum length", "text", "min_length", func(p *values.Payload) { p.Text = "x" }},
		{"maximum length", "text", "max_length", func(p *values.Payload) { p.Text = "private-value" }},
		{"pattern", "text", "pattern", func(p *values.Payload) { p.Text = "01" }},
		{"utf8", "text", "utf8", func(p *values.Payload) { p.Text = string([]byte{0xff}) }},
		{"nullable present", "nullable", "min_length", func(p *values.Payload) { p.Nullable = ptr(ptr("")) }},
		{"bytes length", "data", "max_length", func(p *values.Payload) { p.Data = []byte("private") }},
		{"items", "items", "max_items", func(p *values.Payload) { p.Items = make([]values.Leaf, 3) }},
		{"map items", "names", "max_items", func(p *values.Payload) { p.Names = map[string]values.Leaf{"a": {}, "b": {}, "c": {}} }},
		{"map path", "names[0].label", "min_length", func(p *values.Payload) { p.Names = map[string]values.Leaf{"private-z": {}, "private-a": {}} }},
		{"invalid map key", "names[0]", "utf8", func(p *values.Payload) { p.Names = map[string]values.Leaf{string([]byte{0xff}): {Label: "ok"}} }},
		{"repeated path", "items[1].label", "min_length", func(p *values.Payload) { p.Items = []values.Leaf{{Label: "ok"}, {}} }},
		{"int32 lexical order", "I32[1].label", "min_length", func(p *values.Payload) { p.I32 = map[int32]values.Leaf{2: {}, 10: {Label: "ok"}} }},
		{"int64 lexical order", "I64[1].label", "min_length", func(p *values.Payload) { p.I64 = map[int64]values.Leaf{2: {}, 10: {Label: "ok"}} }},
		{"uint32 lexical order", "U32[1].label", "min_length", func(p *values.Payload) { p.U32 = map[uint32]values.Leaf{2: {}, 10: {Label: "ok"}} }},
		{"uint64 lexical order", "U64[1].label", "min_length", func(p *values.Payload) { p.U64 = map[uint64]values.Leaf{2: {}, 10: {Label: "ok"}} }},
		{"pointer message", "PLeaf.label", "min_length", func(p *values.Payload) { p.PLeaf = ptr(ptr(values.Leaf{})) }},
		{"pointer bytes", "pbytes", "min_length", func(p *values.Payload) { p.PBytes = ptr([]byte{}) }},
		{"pointer items", "pitems", "min_items", func(p *values.Payload) { p.PItems = ptr([]values.Leaf{}) }},
		{"pointer map", "pmap", "min_items", func(p *values.Payload) { p.PMap = ptr(map[string]values.Leaf{}) }},
		{"minimum", "number", "minimum", func(p *values.Payload) { p.Number = -2 }},
		{"maximum", "number", "maximum", func(p *values.Payload) { p.Number = 2 }},
		{"nan", "number", "finite", func(p *values.Payload) { p.Number = math.NaN() }},
		{"infinity", "small_float", "finite", func(p *values.Payload) { p.SmallFloat = float32(math.Inf(1)) }},
		{"float32", "small_float", "maximum", func(p *values.Payload) { p.SmallFloat = 0.75 }},
		{"nested", "nested.label", "min_length", func(p *values.Payload) { p.Nested.Label = "x" }},
		{"path specific", "other.label", "max_length", func(p *values.Payload) { p.Other.Label = "ok" }},
		{"timestamp", "Stamp", "timestamp", func(p *values.Payload) { p.Stamp = time.Date(1, 1, 1, 0, 0, 0, 0, time.FixedZone("private", 3600)) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := values.Request{Value: valid()}; test.change(&request.Value)
			before := implementation.Calls
			response, err := runtime.ValuesRunV1().Run(context.Background(), request)
			requireValueError(t, err, "request", "request.value."+test.path, test.rule)
			if implementation.Calls != before || !reflect.DeepEqual(response, values.Response{}) { t.Fatal("invalid request dispatched or returned value") }
		})
	}
	implementation.Invalid = true
	defer func() { implementation.Invalid = false }()
	response, err := runtime.ValuesRunV1().Run(context.Background(), values.Request{Value: valid()})
	requireValueError(t, err, "response", "response.value.text", "min_length")
	if !reflect.DeepEqual(response, values.Response{}) { t.Fatal("invalid response escaped") }
}
func TestTraversalBoundsAndCycles(t *testing.T) {
	for _, count := range []int{65529, 65530} {
		request := bounded.Request{Items: make([]int32, count)}
		_, err := boundedproxy.CopyRequest(request)
		if count == 65529 && err != nil { t.Fatalf("exact node boundary: %v", err) }
		if count == 65530 && err == nil { t.Fatal("node boundary not enforced") }
	}
	for _, count := range []int{31, 32} {
		node := bounded.Node{}
		for i := 1; i < count; i++ { node = bounded.Node{Children: []bounded.Node{node}} }
		_, err := boundedproxy.CopyRequest(bounded.Request{Tree: []bounded.Node{node}})
		if count == 31 && err != nil { t.Fatalf("exact depth boundary: %v", err) }
		if count == 32 && err == nil { t.Fatal("depth boundary not enforced") }
	}
	children := make([]bounded.Node, 1); children[0].Children = children
	lookup := map[string]bounded.Node{}; lookup["private-key"] = bounded.Node{Lookup: lookup}
	for _, request := range []bounded.Request{{Tree: children}, {Map: lookup}} {
		_, err := boundedproxy.CopyRequest(request)
		var value *boundedproxy.ValueError
		if !errors.As(err, &value) || value.Rule() != "maximum_depth" || strings.Contains(err.Error(), "private-key") { t.Fatalf("cycle error: %v", err) }
	}
	large := make(map[string]bounded.Node, 32768)
	for i := 0; i < 32768; i++ { large[strconv.Itoa(i)] = bounded.Node{} }
	_, err := boundedproxy.CopyRequest(bounded.Request{Map: large})
	var value *boundedproxy.ValueError
	if !errors.As(err, &value) || value.Rule() != "maximum_nodes" { t.Fatalf("map budget: %v", err) }
}
`
