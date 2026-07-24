package command_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeProviderCommandProject remains only for pre-Gate-14 explain-command
// coverage while that obsolete public model is being removed.
func writeProviderCommandProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cliRoot := commandRepositoryRoot(t)
	kernelRoot := filepath.Clean(filepath.Join(cliRoot, "..", "kernel"))
	writeCommandFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf(`module example.com/acme/provider-use

go 1.26

require github.com/plystra/kernel v0.0.0

replace github.com/plystra/kernel => %s
`, filepath.ToSlash(kernelRoot)))
	goSum, err := os.ReadFile(filepath.Join(cliRoot, "go.sum"))
	if err != nil {
		t.Fatalf("read CLI go.sum: %v", err)
	}
	writeCommandFile(t, filepath.Join(root, "go.sum"), string(goSum))
	writeCommandFile(t, filepath.Join(root, "plystra.yaml"), "# Shared Provider choices.\ncapabilities:\n  require: [email.send/v1]\n")
	contract := "id: email.send/v1\nrequest: {}\nresponse: {}\nerrors: []\n"
	for _, provider := range []struct {
		directory string
		packageID string
		pluginID  string
		config    string
	}{
		{directory: "smtp", packageID: "smtp", pluginID: "acme.email.smtp", config: "SMTPConfig"},
		{directory: "local", packageID: "local", pluginID: "acme.email.local", config: "LocalConfig"},
	} {
		writeCommandFile(t, filepath.Join(root, provider.directory, "plugin.yaml"), "id: "+provider.pluginID+"\nprovides: [email.send/v1]\n")
		writeCommandFile(t, filepath.Join(root, provider.directory, "capabilities", "email.send", "v1", "capability.yaml"), contract)
		writeCommandFile(t, filepath.Join(root, provider.directory, "plugin.go"), fmt.Sprintf(`package %s

import (
	"context"

	configuration "example.com/acme/provider-use/generated/go/configuration"
	contract "example.com/acme/provider-use/generated/go/contracts/email/send/v1"
)

type Config = configuration.%s
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Send(_ context.Context, _ contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`, provider.packageID, provider.config))
	}
	writeCommandFile(t, filepath.Join(root, "other", "plugin.yaml"), "id: acme.other\nprovides: [reports.read/v1]\n")
	writeCommandFile(t, filepath.Join(root, "other", "capabilities", "reports.read", "v1", "capability.yaml"), "id: reports.read/v1\nrequest: {}\nresponse: {}\nerrors: []\n")
	writeCommandFile(t, filepath.Join(root, "other", "plugin.go"), `package other

import (
	"context"

	configuration "example.com/acme/provider-use/generated/go/configuration"
	contract "example.com/acme/provider-use/generated/go/contracts/reports/read/v1"
)

type Config = configuration.OtherConfig
type Plugin struct{}

func New(_ Config) *Plugin { return &Plugin{} }

func (*Plugin) Read(_ context.Context, _ contract.Request) (contract.Response, error) {
	return contract.Response{}, nil
}
`)
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize Provider command Project: %v", err)
	}
	return canonical
}
