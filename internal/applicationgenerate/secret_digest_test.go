package applicationgenerate_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationresolve"
)

func TestResolvedSecretEnvironmentValueDoesNotAffectAnyDigest(t *testing.T) {
	t.Parallel()

	const modulePath = "example.com/acme/secret-free-digests"
	root := t.TempDir()
	writeApplicationModule(t, root, modulePath)
	writePlugin(t, root, "mailer", "id: acme.mailer\nprovides: [email.send/v1]\n")
	writeCapability(t, root, "mailer", "email.send/v1", "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeFile(t, filepath.Join(root, "interfaces", "order", "create", "v1", "interface.go"), `package createv1

import "context"

//plystra:interface order.create/v1
type Interface interface {
	Create(context.Context, Request) (Response, error)
}

type Request struct {
	OrderID string `+"`json:\"order_id\" plystra:\"1,required\"`"+`
}

type Response struct {
	Accepted bool `+"`json:\"accepted\" plystra:\"1\"`"+`
}
`)
	writeFile(t, filepath.Join(root, "interfaces", "order", "create", "v1", "interface.yaml"), `description: Creates an order.
semantics: {kind: command}
errors:
  - code: order_rejected
    description: The order was rejected.
constraints:
  request.order_id: {min_length: 1}
examples:
  - name: accepted
    request: {order_id: ord_123}
    response: {accepted: true}
`)
	writeFile(t, filepath.Join(root, "configowner", "implementation.go"), `package configowner

import (
	"context"

	createv1 "example.com/acme/secret-free-digests/interfaces/order/create/v1"
	"github.com/plystra/kernel/configuration"
)

type Config struct {
	Password configuration.Secret
}

type Service struct{}

//plystra:implements order.create/v1
func New(Config) (*Service, error) { return &Service{}, nil }

func (*Service) Create(context.Context, createv1.Request) (createv1.Response, error) {
	return createv1.Response{}, nil
}
`)
	writeFile(t, filepath.Join(root, "plystra.yaml"), `capabilities: {require: [email.send/v1]}
config:
  example.com/acme/secret-free-digests/configowner.New:
    password: {env: PLYSTRA_DIGEST_SECRET}
`)

	firstEnvironment := goEnvironment(map[string]string{"PLYSTRA_DIGEST_SECRET": "resolved-secret-one"})
	secondEnvironment := goEnvironment(map[string]string{"PLYSTRA_DIGEST_SECRET": "resolved-secret-two"})
	generate := func(environment []string) {
		t.Helper()
		result, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
			Start:       root,
			Environment: environment,
			Validate:    func(_ context.Context, _ string) error { return nil },
		})
		if err != nil || !result.Report().Clean() {
			t.Fatalf("Generate = changes %#v, %v", result.Report().Changes(), err)
		}
	}
	resolve := func(environment []string) applicationresolve.Result {
		t.Helper()
		result, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
			Start:       root,
			Environment: environment,
		})
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		return result
	}

	generate(firstEnvironment)
	firstResolution := resolve(firstEnvironment)
	firstEvidence := collectResolutionDigests(t, firstResolution)
	firstManifest := readFile(t, root, "generated/manifest.json")
	firstManifestDigests := collectJSONDigests(t, firstManifest)
	firstGenerated := snapshotGenerated(t, root)

	secondResolution := resolve(secondEnvironment)
	secondEvidence := collectResolutionDigests(t, secondResolution)
	if !reflect.DeepEqual(secondEvidence, firstEvidence) {
		t.Fatalf("resolved Secret changed resolution digests:\nfirst:  %#v\nsecond: %#v", firstEvidence, secondEvidence)
	}

	generate(secondEnvironment)
	secondManifest := readFile(t, root, "generated/manifest.json")
	secondManifestDigests := collectJSONDigests(t, secondManifest)
	if !reflect.DeepEqual(secondManifestDigests, firstManifestDigests) {
		t.Fatalf("resolved Secret changed generated manifest digests:\nfirst:  %#v\nsecond: %#v", firstManifestDigests, secondManifestDigests)
	}
	if !bytes.Equal(secondManifest, firstManifest) {
		t.Fatal("resolved Secret changed generated manifest bytes without changing a recorded digest")
	}
	if secondGenerated := snapshotGenerated(t, root); !reflect.DeepEqual(secondGenerated, firstGenerated) {
		t.Fatal("resolved Secret changed generated output")
	}

	for _, secret := range []string{"resolved-secret-one", "resolved-secret-two"} {
		if bytes.Contains(secondManifest, []byte(secret)) {
			t.Fatalf("generated manifest leaked resolved Secret %q", secret)
		}
		for _, entry := range firstGenerated {
			if bytes.Contains(entry.data, []byte(secret)) {
				t.Fatalf("generated file %s leaked resolved Secret %q", entry.path, secret)
			}
		}
	}
}

func collectResolutionDigests(t testing.TB, result applicationresolve.Result) map[string]string {
	t.Helper()
	interfaces := result.Interfaces().Interfaces()
	if len(interfaces) != 1 {
		t.Fatalf("discovered Interfaces = %#v", interfaces)
	}
	provenance := result.PreviousManifestProvenance()
	evidence := result.ResolutionEvidence()
	digests := map[string]string{
		"application_model":       provenance.ApplicationModelDigest(),
		"configuration_selection": result.ConfigurationSelection().Digest(),
		"dependency_composition":  result.Composition().DependencyDigest(),
		"interface_contract":      interfaces[0].ContractDigest(),
		"interface_documentation": interfaces[0].DocumentationDigest(),
		"interface_examples":      interfaces[0].ExampleDigest(),
		"private_configuration":   result.Configurations().Digest(),
		"protobuf_wire_map":       provenance.ProtobufWireMapDigest(),
		"resolution_aliases":      result.Resolution().AliasResolution().Digest(),
		"resolution_build_model":  result.Resolution().Context().BuildModelDigest(),
		"resolution_context":      result.Resolution().Context().Digest(),
		"resolution_evidence":     evidence.Digest(),
		"root_configuration":      provenance.RootDigest(),
		"selected_configuration":  provenance.SelectedDigest(),
	}
	for name, digest := range digests {
		if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
			t.Fatalf("%s digest = %q", name, digest)
		}
	}
	return digests
}

func collectJSONDigests(t testing.TB, data []byte) map[string]string {
	t.Helper()
	var document any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode generated manifest: %v", err)
	}
	digests := make(map[string]string)
	var visit func(string, any)
	visit = func(path string, value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				childPath := path + "/" + key
				if strings.Contains(strings.ToLower(key), "digest") {
					digest, ok := child.(string)
					if !ok || !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
						t.Fatalf("manifest digest %s = %#v", childPath, child)
					}
					digests[childPath] = digest
				}
				visit(childPath, child)
			}
		case []any:
			for index, child := range typed {
				visit(fmt.Sprintf("%s/%d", path, index), child)
			}
		}
	}
	visit("", document)
	if len(digests) == 0 {
		t.Fatal("generated manifest contains no digests")
	}
	return digests
}
