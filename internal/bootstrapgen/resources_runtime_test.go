package bootstrapgen

const resourceRuntimeTests = `package runtimecheck
import (
 "bytes"
 "context"
 "encoding/json"
 "errors"
 "math/rand/v2"
 "os"
 "path/filepath"
 "reflect"
 "strings"
 "testing"
 "github.com/plystra/cli/internal/privatefile"
 "github.com/plystra/cli/internal/resourcename"
 "github.com/plystra/cli/internal/runtimebaseline"
 kernelconfiguration "github.com/plystra/kernel/configuration"
 "go.yaml.in/yaml/v3"
)
const rootRelationship = "template: example.com/near\n"

func fixture(t *testing.T) runtimebaseline.Document {
 t.Helper()
 d, err := runtimebaseline.Decode([]byte(initialBaseline)); if err != nil { t.Fatal(err) }; return d
}
func compose(t *testing.T, d runtimebaseline.Document, root, selected, overlay string) []byte {
 t.Helper()
 var replacement, environment []byte
 if selected != "" { replacement = []byte(selected) }; if overlay != "" { environment = []byte(overlay) }
 document, err := composeRuntimeTemplateDocument(d, []byte(root), replacement, environment)
 if err != nil { t.Fatal(err) }; return document
}
func bind(t *testing.T, document []byte) *runtimePreparedConfiguration {
 t.Helper()
 if err := validateRuntimeApplicationModel(document); err != nil { t.Fatal(err) }
 p, err := prepareRuntimeConstructorConfiguration(document); if err != nil { t.Fatal(err) }
 resolver, err := kernelconfiguration.NewResolver(kernelconfiguration.ResolverOptions{MaximumValueBytes:1024}); if err != nil { t.Fatal(err) }
 if err := p.resolve(context.Background(), resolver); err != nil { t.Fatal(err) }; return p
}
func secrets(t *testing.T) {
 t.Helper(); t.Setenv("RESOURCE_PRIMARY_SECRET", "primary-value"); t.Setenv("RESOURCE_SECONDARY_SECRET", "secondary-value")
}
func rawNode(t *testing.T, value string) *yaml.Node {
 t.Helper(); var node yaml.Node
 if err := yaml.Unmarshal([]byte(value), &node); err != nil || len(node.Content)!=1 { t.Fatal(err) }; return node.Content[0]
}

func TestIndependentTypedTargetsAndSelectors(t *testing.T) {
 secrets(t)
 root := t.TempDir()
 files := map[string]string{
  "plystra.yaml": rootRelationship+"resources: {instances: {database.primary: {config: {value: root}}}}\n",
  "plystra.prod.yaml": "resources: {instances: {database.primary: {config: {value: environment}}}}\n",
  "replacement.yaml": "resources: {instances: {database.primary: {config: {value: replacement}}}}\n",
 }
 for name, content := range files { if err := os.WriteFile(filepath.Join(root,name), []byte(content),0600); err != nil { t.Fatal(err) } }
 f, err := privatefile.Create(filepath.Join(root,"baseline.json")); if err != nil { t.Fatal(err) }
 if _, err := f.Write([]byte(initialBaseline)); err != nil { t.Fatal(err) }; if err := f.Close(); err != nil { t.Fatal(err) }
 for _, test := range []struct { args, env []string; expected string }{
  {nil,nil,"root"},
  {[]string{"--env","prod"},nil,"environment"},
  {[]string{"--config","replacement.yaml"},nil,"replacement"},
  {nil,[]string{"PLYSTRA_ENV=prod"},"environment"},
  {nil,[]string{"PLYSTRA_CONFIG=replacement.yaml"},"replacement"},
  {[]string{"--config","replacement.yaml"},[]string{"PLYSTRA_ENV=prod","PLYSTRA_CONFIG=missing"},"replacement"},
 } {
  args := append([]string{"--configuration-root",root,"--runtime-baseline","baseline.json"},test.args...)
  document, err := loadRuntimeDocument(RuntimeOptions{Arguments:args,Environment:test.env}); if err != nil { t.Fatal(err) }
  p := bind(t,document)
  primary, secondary := p.configuration.ResourceConfig1, p.configuration.ResourceConfig0
  if primary.Value != test.expected || secondary.Value != "inherited-secondary" || primary.Count != 7 || secondary.Count != 7 || primary.Private != "PRIVATE_DEFAULT" { t.Fatal("instance targets or defaults conflated") }
  if string(primary.Token.Bytes()) != "primary-value" || string(secondary.Token.Bytes()) != "secondary-value" { t.Fatal("Secret delivery conflated") }
  if bytes.Contains(p.legacyDocument,[]byte("resources:")) { t.Fatal("Resource config reached legacy loader") }
 }
 for name, content := range files { data, err := os.ReadFile(filepath.Join(root,name)); if err != nil || string(data)!=content { t.Fatal("startup modified authored input",err) } }
}

func TestResourceTypedCompositionAndAtomicBoundaries(t *testing.T) {
 secrets(t)
 const delta = "resources: {instances: {database.primary: {config: {nested: {right: current}, pointer: {right: current}, labels: {current: only}, items: [current]}}}}\n"
 for _, mode := range []string{"default","environment","replacement"} {
  t.Run(mode,func(t *testing.T) {
   root, selected, overlay := rootRelationship, "", ""
   switch mode { case "default": root += delta; case "environment": overlay=delta; case "replacement": selected=delta }
   p := bind(t,compose(t,fixture(t),root,selected,overlay)); c:=p.configuration.ResourceConfig1
   if c.Nested.Left!="inherited" || c.Nested.Right!="current" || c.Pointer==nil || c.Pointer.Left!="" || c.Pointer.Right!="current" || !reflect.DeepEqual(c.Labels,map[string]string{"current":"only"}) || !reflect.DeepEqual(c.Items,[]string{"current"}) { t.Fatal("typed merge became recursive YAML merge") }
  })
 }
 p := bind(t,compose(t,fixture(t),rootRelationship+"resources: {instances: {database.primary: {config: {pointer: null, labels: null, items: null, nested: {left: {$remove: true}}, private: {$remove: true}}}}}\n","",""))
 c := p.configuration.ResourceConfig1
 if c.Pointer!=nil || c.Labels!=nil || c.Items!=nil || c.Nested.Left!="" || c.Nested.Right!="inherited" || c.Private!="PRIVATE_DEFAULT" { t.Fatal("nullable values, tombstones, or final defaults incorrect") }
 // A lower required-field removal may be repaired by a later layer.
 p=bind(t,compose(t,fixture(t),rootRelationship+"resources: {instances: {database.primary: {config: {value: {$remove: true}}}}}\n","","resources: {instances: {database.primary: {config: {value: restored}}}}\n"))
 if p.configuration.ResourceConfig1.Value!="restored" { t.Fatal("requiredness evaluated before final overlay") }
}

func TestProviderReplacementDiscardsPriorConfiguration(t *testing.T) {
 document:=compose(t,fixture(t),rootRelationship+"resources: {instances: {database.primary: {use: example.com/alternate.New, config: {value: replaced}}}}\n","","")
 if !errors.Is(validateRuntimeApplicationModel(document),ErrRuntimeCompatibility) { t.Fatal("provider replacement did not change frozen identity") }
 root,err:=decodeRuntimeDocument(document,"effective"); if err!=nil { t.Fatal(err) }
 fields,_:=runtimeMapping(root,"",nil); resources,_:=runtimeResourceMapping(fields["resources"],nil); instances,_:=runtimeResourceMapping(resources["instances"],nil); primary,_:=runtimeResourceMapping(instances["database.primary"],nil); configuration,_:=runtimeResourceMapping(primary["config"],nil)
 nested,_:=runtimeResourceMapping(configuration["nested"],nil)
 if configuration["value"].Value!="replaced" || nested["left"].Value!="" || nested["right"].Value!="" || !runtimeNull(configuration["pointer"]) || !runtimeNull(configuration["labels"]) || configuration["token"]!=nil { t.Fatal("old provider configuration crossed replacement boundary") }
 if _,err:=composeRuntimeTemplateDocument(fixture(t),[]byte(rootRelationship+"resources: {instances: {database.primary: {use: example.com/alternate.New}}}\n"),nil,nil); err==nil { t.Fatal("replacement inherited required value from old provider") }
 document=compose(t,fixture(t),rootRelationship+"resources: {instances: {database.primary: {use: example.com/empty.New}}}\n","","")
 root,_=decodeRuntimeDocument(document,"effective"); fields,_=runtimeMapping(root,"",nil); resources,_=runtimeResourceMapping(fields["resources"],nil); instances,_=runtimeResourceMapping(resources["instances"],nil); primary,_=runtimeResourceMapping(instances["database.primary"],nil)
 if primary["config"]!=nil { t.Fatal("Config survived replacement by no-Config provider") }
}

func TestBindingResolutionAndFrozenMembership(t *testing.T) {
 for _, delta := range []string{
  "resources: {bind: {implementations: {example.com/consumer.New: {database: database.secondary}}}}\n",
  "resources: {bind: {instances: {cache: {database: database.secondary}}}}\n",
  "resources: {instances: {unused: {use: example.com/empty.New}}}\n",
  "resources: {instances: {database.secondary: {$remove: true}}}\n",
 } {
  document:=compose(t,fixture(t),rootRelationship+delta,"","")
  if !errors.Is(validateRuntimeApplicationModel(document),ErrRuntimeCompatibility) { t.Fatal("changed executable Resource graph accepted") }
 }
 d:=fixture(t)
 providers,err:=runtimeResourceInventory(d); if err!=nil { t.Fatal(err) }
 constructors,err:=runtimeConstructorInventory(d); if err!=nil { t.Fatal(err) }
 node,err:=composeRuntimeResources([]*yaml.Node{rawNode(t,"instances: {primary: {use: example.com/empty.New}, cache: {use: example.com/cached.New}}\n")},providers,constructors)
 if err!=nil { t.Fatal(err) }
 _, bindings,err:=runtimeApplicationModelResources(node)
 if err!=nil || len(bindings)!=2 || bindings[0].Target!="primary" || bindings[1].Target!="primary" { t.Fatal("implicit binding failed",err) }
 // Explicit-to-implicit spelling does not change the resolved frozen graph.
 node,err=composeRuntimeResources([]*yaml.Node{rawNode(t,"instances: {primary: {use: example.com/empty.New}, cache: {use: example.com/cached.New}}\nbind: {implementations: {example.com/consumer.New: {database: primary}}, instances: {cache: {database: primary}}}\n")},providers,constructors)
 if err!=nil { t.Fatal(err) }; _,explicit,err:=runtimeApplicationModelResources(node)
 if err!=nil || !reflect.DeepEqual(bindings,explicit) { t.Fatal("explicit binding differs from unique fallback",err) }
 for _, input:=range []string{
  "instances: {cache: {use: example.com/cached.New}}\n",
  "instances: {one: {use: example.com/empty.New}, two: {use: example.com/empty.New}, cache: {use: example.com/cached.New}}\n",
 } { if _,err:=composeRuntimeResources([]*yaml.Node{rawNode(t,input)},providers,constructors); err==nil { t.Fatal("missing or ambiguous implicit binding accepted") } }
 // Explicit dormant bindings validate but do not enter executable membership.
 document:=compose(t,fixture(t),rootRelationship+"resources: {bind: {implementations: {example.com/dormant.New: {database: database.secondary}}}}\n","","")
 if err:=validateRuntimeApplicationModel(document); err!=nil { t.Fatal("dormant edge changed executable membership",err) }
}

func TestResourceFailuresAreRedactedAndNonmutating(t *testing.T) {
 for name,delta:=range map[string]string{
  "bad name":"resources: {instances: {PRIVATE_SENTINEL: {use: example.com/provider.New}}}\n",
  "unknown instance field":"resources: {instances: {database.primary: {PRIVATE_SENTINEL: private}}}\n",
  "invalid provider":"resources: {instances: {database.primary: {use: PRIVATE_SENTINEL}}}\n",
  "unknown provider":"resources: {instances: {database.primary: {use: example.com/missing.New}}}\n",
  "missing use":"resources: {instances: {missing: {config: {value: PRIVATE_SENTINEL}}}}\n",
  "wrong scalar":"resources: {instances: {database.primary: {config: {count: PRIVATE_SENTINEL}}}}\n",
  "unknown field":"resources: {instances: {database.primary: {config: {PRIVATE_SENTINEL: value}}}}\n",
  "malformed null":"resources: {instances: {database.primary: {config: {pointer: !!null PRIVATE_SENTINEL}}}}\n",
  "malformed secret":"resources: {instances: {database.primary: {config: {token: {env: PRIVATE_SENTINEL, file: PRIVATE_SENTINEL}}}}}\n",
  "missing required":"resources: {instances: {database.primary: {config: {value: {$remove: true}}}}}\n",
  "null fixed":"resources: {instances: {database.primary: {config: {nested: null}}}}\n",
  "no schema":"resources: {instances: {database.primary: {use: example.com/empty.New, config: {value: PRIVATE_SENTINEL}}}}\n",
  "no schema removal":"resources: {instances: {database.primary: {use: example.com/empty.New, config: {$remove: true}}}}\n",
  "stale provider binding":"resources: {instances: {cache: {use: example.com/empty.New}}}\n",
  "bad removal":"resources: {instances: {database.primary: {$remove: true, PRIVATE_SENTINEL: value}}}\n",
  "container removal":"resources: {bind: {implementations: {example.com/consumer.New: {$remove: true}}}}\n",
  "old namespace":"resources: {bind: {example.com/consumer.New: {database: database.primary}}}\n",
  "dangling target":"resources: {bind: {instances: {cache: {database: missing}}}}\n",
  "removed target":"resources: {instances: {database.primary: {$remove: true}}}\n",
  "wrong contract":"resources: {bind: {implementations: {example.com/consumer.New: {database: cache}}}}\n",
  "wrong parameter":"resources: {bind: {implementations: {example.com/consumer.New: {Database: database.primary}}}}\n",
  "unknown consumer":"resources: {bind: {implementations: {example.com/missing.New: {database: database.primary}}}}\n",
  "unknown instance consumer":"resources: {bind: {instances: {missing: {database: database.primary}}}}\n",
  "dormant wrong parameter":"resources: {bind: {implementations: {example.com/dormant.New: {Database: database.primary}}}}\n",
  "removed ambiguous leaf":"resources: {bind: {instances: {cache: {database: {$remove: true}}}}}\n",
  "Data":"data: {}\n",
  "provider top-level config":"config: {example.com/provider.New: {value: PRIVATE_SENTINEL}}\n",
 } {
  for _,mode:=range []string{"default","environment","replacement"} {
   t.Run(name+"/"+mode,func(t *testing.T) {
    d:=fixture(t); before:=d.Templates[0].YAML
    root,replacement,overlay:=[]byte(rootRelationship),[]byte(nil),[]byte(nil)
    switch mode { case "default":root=append(root,delta...);case "environment":overlay=[]byte(delta);case "replacement":replacement=[]byte(delta) }
    rootBefore, replacementBefore, overlayBefore:=bytes.Clone(root),bytes.Clone(replacement),bytes.Clone(overlay)
    _,err:=composeRuntimeTemplateDocument(d,root,replacement,overlay)
    if err==nil || strings.Contains(err.Error(),"PRIVATE_SENTINEL") { t.Fatal("accepted invalid Resource or exposed private value",err) }
    if d.Templates[0].YAML!=before || !bytes.Equal(root,rootBefore) || !bytes.Equal(replacement,replacementBefore) || !bytes.Equal(overlay,overlayBefore) { t.Fatal("failed composition mutated input") }
   })
  }
 }
}

func TestResourceBuildVisibleAndPrivateDefaultGuards(t *testing.T) {
 document:=compose(t,fixture(t),rootRelationship+"resources: {instances: {database.primary: {config: {count: 8, token: {env: NEVER_RESOLVE}}}}}\n","","")
 if err:=validateRuntimeApplicationModel(document); err!=nil { t.Fatal(err) }
 if _,err:=prepareRuntimeConstructorConfiguration(document); !errors.Is(err,ErrRuntimeCompatibility) || !strings.Contains(err.Error(),"Resource database.primary") || strings.Contains(err.Error(),"NEVER_RESOLVE") { t.Fatal("build-visible drift not rejected before resolution",err) }
 for _,symbol:=range []string{"example.com/provider.New","example.com/alternate.New"} {
  d:=fixture(t); d.Defaults[symbol]=json.RawMessage("{\"/count\":\"PRIVATE_SENTINEL\"}")
  if err:=validateRuntimeBaseline(d); err==nil || strings.Contains(err.Error(),"PRIVATE_SENTINEL") { t.Fatal("malformed provider defaults accepted or leaked",err) }
 }
 d:=fixture(t);var defaults map[string]json.RawMessage
 if err:=json.Unmarshal(d.Defaults["example.com/provider.New"],&defaults);err!=nil { t.Fatal(err) }; defaults["/count"]=json.RawMessage("8"); d.Defaults["example.com/provider.New"],_=json.Marshal(defaults)
 if err:=validateRuntimeBaseline(d);!errors.Is(err,ErrRuntimeCompatibility) { t.Fatal("stale active compiled defaults accepted",err) }
 // Lower layers are typed even if later removal or provider replacement hides them.
 for _,upper:=range []string{"{$remove: true}","{use: example.com/alternate.New, config: {value: valid}}"} {
  d:=fixture(t);d.Templates[0].YAML=strings.Replace(d.Templates[0].YAML,"value: inherited-primary","value: [PRIVATE_SENTINEL]",1)
  if _,err:=composeRuntimeTemplateDocument(d,[]byte(rootRelationship+"resources: {instances: {database.primary: "+upper+"}}\n"),nil,nil);err==nil || strings.Contains(err.Error(),"PRIVATE_SENTINEL") { t.Fatal("suppressed malformed typed layer accepted or exposed",err) }
 }
}

func TestResourceProviderRequirednessWaitsForFinalLayer(t *testing.T) {
 for _, lower:=range []string{
  "{config: {PRIVATE_UNBOUND_KEY: PRIVATE_UNBOUND_VALUE}}",
  "{config: {$remove: true}}",
  "{}",
  "{use: example.com/missing.New}",
 } {
  for _, upper:=range []string{"{$remove: true}","{use: example.com/provider.New, config: {value: final}}"} {
   d:=fixture(t)
   root:=[]byte(rootRelationship+"resources: {instances: {pending: "+lower+"}}\n")
   overlay:=[]byte("resources: {instances: {pending: "+upper+"}}\n")
   if _,err:=composeRuntimeTemplateDocument(d,root,nil,overlay);err!=nil { t.Fatal("provider checked before final composition",err) }
   if _,err:=composeRuntimeTemplateDocument(d,root,nil,nil);err==nil { t.Fatal("missing final provider accepted") }
  }
 }
 for _, invalid:=range []string{
  "null", "[PRIVATE_SENTINEL]", "{value: !!null PRIVATE_SENTINEL}",
  "{$remove: false}", "{$remove: true, private: PRIVATE_SENTINEL}",
  "{value: 1, value: 2}", "{value: &PRIVATE_SENTINEL private}",
 } {
  if _,err:=composeRuntimeTemplateDocument(fixture(t),[]byte(rootRelationship+"resources: {instances: {pending: {config: "+invalid+"}}}\n"),nil,[]byte("resources: {instances: {pending: {$remove: true}}}\n"));err==nil || strings.Contains(err.Error(),"PRIVATE_SENTINEL") { t.Fatal("unbound raw shape bypassed or leaked",err) }
 }
}

func TestResourceNameGrammar(t *testing.T) {
 for _,name:=range []string{"a","primary","database.primary","a0-b1.c2-3",strings.Repeat("a",128)} { if !validRuntimeResourceName(name) { t.Fatal("valid name rejected") } }
 for _,name:=range []string{"","A","a-","a--b","a..b","a.0b","a/b","a_b","a.b-",strings.Repeat("a",129),"\u4e3b"} { if validRuntimeResourceName(name) { t.Fatal("invalid name accepted") } }
 random:=rand.New(rand.NewPCG(1,2))
 const alphabet="abcxyz019.-_AZ /\x00\xff"
 for range 50000 {
  value:=make([]byte,random.IntN(140))
  for i:=range value { value[i]=alphabet[random.IntN(len(alphabet))] }
  name:=string(value)
  if validRuntimeResourceName(name)!=(resourcename.Check(name)==nil) { t.Fatal("runtime name grammar differs from compiler") }
 }
}

func TestResourceCompositionMatchesCompiler(t *testing.T) {
 var cases []struct { Lower, Upper string; Accepted bool; Instances []struct { Name, Provider, Configuration string } }
 if err:=json.Unmarshal([]byte(compilerResourceParity),&cases);err!=nil { t.Fatal(err) }
 for index,item:=range cases {
  document,err:=composeRuntimeTemplateDocument(fixture(t),[]byte(rootRelationship+item.Lower),nil,[]byte(item.Upper))
  if (err==nil)!=item.Accepted { t.Fatalf("compiler/runtime acceptance differs in case %d: accepted=%v runtime=%v",index,item.Accepted,err) }
  if err!=nil { continue }
  root,_:=decodeRuntimeDocument(document,"effective");fields,_:=runtimeMapping(root,"",nil);resources,_:=runtimeResourceMapping(fields["resources"],nil);instances,_:=runtimeResourceMapping(resources["instances"],nil)
  if len(instances)!=len(item.Instances) { t.Fatalf("compiler/runtime instance membership differs in case %d",index) }
  for _,expected:=range item.Instances {
   actual,err:=runtimeResourceMapping(instances[expected.Name],nil);if err!=nil { t.Fatal(err) }
   provider,_:=runtimeString(actual["use"]);if provider!=expected.Provider { t.Fatalf("compiler/runtime provider differs in case %d",index) }
   var want,got any
   if expected.Configuration!="" { if err:=yaml.Unmarshal([]byte(expected.Configuration),&want);err!=nil { t.Fatal(err) } }
   if actual["config"]!=nil { if err:=actual["config"].Decode(&got);err!=nil { t.Fatal(err) } }
   if !reflect.DeepEqual(want,got) { t.Fatalf("compiler/runtime typed configuration differs in case %d, instance %s",index,expected.Name) }
  }
 }
}
`
