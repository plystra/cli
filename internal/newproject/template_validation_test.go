package newproject_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/command"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/diagnosticcode"
	"github.com/plystra/cli/internal/diagnosticjson"
	"github.com/plystra/cli/internal/gocommand"
)

func TestPublicNewTemplateValidationDiagnosticsAndRollback(t *testing.T) {
	const (
		templatePath  = "example.com/acme/validation-platform"
		basePath      = "example.com/acme/validation-base"
		version       = "v1.0.0"
		constructor   = basePath + "/mailer.New"
		privateKey    = "PRIVATE_TEMPLATE_CONFIG_KEY"
		privateValue  = "PRIVATE_TEMPLATE_CONFIG_VALUE"
		privateOrigin = "https://private-template-origin.example"
	)
	for _, test := range []struct {
		name           string
		base           string
		templateDelta  string
		implementation bool
		code           string
		detail         string
		owner          string
		kind           string
	}{
		{
			name:          "cors-final-invariant",
			base:          "http: {cors: {allowed_origins: [" + privateOrigin + "], allow_credentials: true}}\n",
			templateDelta: "http: {cors: {allowed_origins: ['*']}}\n",
			code:          diagnosticcode.ConfigurationInvalid,
			detail:        "http.cors cannot combine wildcard origin",
			owner:         templatePath,
			kind:          "configuration-declaration",
		},
		{
			name: "constructor-configuration",
			base: "interfaces: {require: [email.send/v1], use: {email.send/v1: " + constructor + "}}\n" +
				"config: {" + constructor + ": {endpoint: {" + privateKey + ": " + privateValue + "}}}\n",
			implementation: true,
			code:           diagnosticcode.ConstructorConfigurationValuesInvalid,
			detail:         `config["` + constructor + `"]["endpoint"]`,
			owner:          basePath,
			kind:           "configuration-declaration",
		},
		{
			name:   "missing-implementation",
			base:   "interfaces: {require: [email.send/v1]}\n",
			code:   diagnosticcode.ResolveMissingImplementation,
			detail: "email.send/v1",
			owner:  basePath,
			kind:   "declaration",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			proxy := createKernelProxy(t)
			baseFiles := map[string][]byte{
				"plystra.yaml": []byte(test.base),
				"interfaces/email/send/v1/interface.go": []byte(`package sendv1

import "context"

//plystra:interface email.send/v1
type Interface interface { Send(context.Context, Request) (Response, error) }
type Request struct{}
type Response struct{}
`),
			}
			if test.implementation {
				baseFiles["mailer/mailer.go"] = []byte(`package mailer

import (
	"context"
	contract "` + basePath + `/interfaces/email/send/v1"
)

type Config struct { Endpoint string }
type Service struct{}

//plystra:implements email.send/v1
func New(Config) (*Service, error) { return &Service{}, nil }
func (*Service) Send(context.Context, contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`)
			}
			writeProxyModule(t, proxy, basePath, version, baseFiles)
			writeProxyModule(t, proxy, templatePath, version, map[string][]byte{
				"go.mod":       []byte("module " + templatePath + "\n\ngo 1.26\n\nrequire " + basePath + " " + version + "\n"),
				"template.go":  []byte("package platform\n"),
				"plystra.yaml": []byte("template: " + basePath + "\n" + test.templateDelta),
			})
			environment := isolatedGoEnvironment(t, proxy)
			for _, modulePath := range []string{basePath, templatePath} {
				if err := gocommand.Run(t.Context(), gocommand.Options{
					Directory: t.TempDir(), Environment: environment,
				}, "mod", "download", modulePath+"@"+version); err != nil {
					t.Fatalf("pre-download template: %v", err)
				}
			}
			baseCache := moduleCacheRoot(t, environment, basePath, version)
			templateCache := moduleCacheRoot(t, environment, templatePath, version)
			baseBefore := snapshotTree(t, baseCache)
			templateBefore := snapshotTree(t, templateCache)
			proxyBefore := snapshotTree(t, proxy)
			wantSource := diagnosticjson.Source{Module: test.owner, Path: "plystra.yaml", Kind: test.kind, Line: 1, Column: 1}

			for _, format := range []string{"human", "json"} {
				t.Run(format, func(t *testing.T) {
					parent := t.TempDir()
					writeTestFile(t, filepath.Join(parent, "keep.txt"), []byte("existing parent content\n"))
					parentBefore := snapshotTree(t, parent)
					var stdout, stderr bytes.Buffer
					exitCode := command.RunIn([]string{
						"new", "my-app", "--module", "example.com/acme/my-app",
						"--template", templatePath + "@" + version, "--format", format,
						"--no-agent-guidance",
					}, &stdout, &stderr, parent, environment)

					assertPathPresence(t, filepath.Join(parent, "my-app"), false)
					assertNoTransactionFiles(t, parent)
					if entries, err := os.ReadDir(parent); err != nil || len(entries) != 1 || entries[0].Name() != "keep.txt" {
						t.Errorf("creation left target or residue: %v, %v", entries, err)
					}
					for _, snapshot := range []struct {
						name, root string
						before     map[string][]byte
					}{
						{"parent", parent, parentBefore},
						{"oldest template", baseCache, baseBefore},
						{"nearest template", templateCache, templateBefore},
						{"module proxy", proxy, proxyBefore},
					} {
						if !reflect.DeepEqual(snapshot.before, snapshotTree(t, snapshot.root)) {
							t.Errorf("rejected creation mutated %s", snapshot.name)
						}
					}
					output := stdout.String() + stderr.String()
					for _, private := range []string{privateKey, privateValue, privateOrigin, parent, proxy, baseCache, templateCache, environmentValue(t, environment, "GOMODCACHE")} {
						for _, representation := range []string{private, filepath.ToSlash(private), strings.ReplaceAll(private, `\`, `\\`)} {
							if strings.Contains(output, representation) {
								t.Errorf("creation diagnostic leaked private input %q", representation)
							}
						}
					}
					if exitCode != 3 {
						t.Errorf("invalid template exit = %d, want 3; stdout %q, stderr %q", exitCode, stdout.String(), stderr.String())
					}
					if strings.Contains(output, diagnosticcode.ProjectCreateFailed) {
						t.Error("creation wrapper masked the template validation diagnostic")
					}
					if format == "human" {
						want := fmt.Sprintf("Source: %s:%s:%d:%d (%s)", wantSource.Module, wantSource.Path, wantSource.Line, wantSource.Column, wantSource.Kind)
						if stdout.Len() != 0 || !strings.Contains(stderr.String(), want) || !strings.Contains(stderr.String(), test.detail) ||
							!strings.Contains(stderr.String(), "Diagnostic: "+test.code) || strings.Count(stderr.String(), "Source: ") != 1 ||
							strings.Count(stderr.String(), "Recovery:") != 1 || strings.Count(stderr.String(), "Diagnostic:") != 1 {
							t.Fatalf("human template diagnostic = stdout %q, stderr %q", stdout.String(), stderr.String())
						}
						return
					}
					if stderr.Len() != 0 {
						t.Errorf("JSON stderr = %q", stderr.String())
					}
					var result struct {
						Schema      string `json:"schema"`
						Operation   string `json:"operation"`
						Status      string `json:"status"`
						ExitClass   int    `json:"exit_class"`
						Diagnostics []struct {
							Code      string                  `json:"code"`
							Severity  string                  `json:"severity"`
							Locations []diagnosticjson.Source `json:"locations"`
						} `json:"diagnostics"`
						Changes  []json.RawMessage `json:"changes"`
						Recovery []struct {
							Schema string `json:"schema"`
							Kind   string `json:"kind"`
							Target struct {
								Kind string `json:"kind"`
								ID   string `json:"id"`
							} `json:"target"`
						} `json:"recovery"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
						t.Fatalf("decode public creation result: %v", err)
					}
					if result.Schema != commandschema.ResultSchemaV1 || result.Operation != "new" || result.Status != "validation_failed" || result.ExitClass != 3 ||
						len(result.Changes) != 0 || len(result.Recovery) != 1 || len(result.Diagnostics) != 1 {
						t.Fatalf("JSON template validation result = %#v", result)
					}
					if diagnostic := result.Diagnostics[0]; diagnostic.Code != test.code || diagnostic.Severity != "error" ||
						!reflect.DeepEqual(diagnostic.Locations, []diagnosticjson.Source{wantSource}) {
						t.Fatalf("JSON template validation diagnostic = %#v", diagnostic)
					}
					if recovery := result.Recovery[0]; recovery.Schema != commandschema.RecoverySchemaV1 || recovery.Kind != "manual" ||
						recovery.Target.Kind != "argument" || recovery.Target.ID != "template" {
						t.Fatalf("JSON template validation recovery = %#v", recovery)
					}
				})
			}
		})
	}
}
