package installedcapabilities_test

import (
	"bytes"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/installedcapabilities"
	"github.com/plystra/cli/internal/transporttoolchain"
	"github.com/plystra/cli/internal/version"
)

func TestCurrentReportsExactInstalledDistribution(t *testing.T) {
	t.Parallel()

	capabilities, err := installedcapabilities.Current()
	if err != nil || !capabilities.Valid() {
		t.Fatalf("Current = %#v, %v", capabilities, err)
	}
	if capabilities.CLIVersion() != version.Current ||
		capabilities.KernelVersion() != version.KernelVersion ||
		capabilities.SpecificationRevision() != version.SpecificationRevision ||
		capabilities.GoRequirement() != version.GoRequirement ||
		capabilities.GOOS() != runtime.GOOS || capabilities.GOARCH() != runtime.GOARCH ||
		capabilities.ProjectDocumentBytes() != applicationmeta.MaximumSize ||
		capabilities.StartupTimeout() != applicationmeta.DefaultStartupTimeout ||
		capabilities.InvocationTimeout() != applicationmeta.DefaultInvocationTimeout {
		t.Fatalf("installed facts = %#v", capabilities)
	}
	wantSupport := []string{
		"data|yes|no|no|no|no",
		"data.compiler|yes|no|no|no|no",
		"inspect.capabilities|yes|yes|not_applicable|yes|yes",
		"interfaces.policies.*.timeout|yes|yes|yes|no|no",
		"resource|yes|no|no|no|no",
		"transport.connect|yes|yes|yes|yes|yes",
	}
	gotSupport := make([]string, 0, len(capabilities.Support()))
	for _, support := range capabilities.Support() {
		gotSupport = append(gotSupport, strings.Join([]string{
			support.ID(),
			string(support.Specified()),
			string(support.Parsed()),
			string(support.Generated()),
			string(support.Executed()),
			string(support.Accepted()),
		}, "|"))
	}
	if !reflect.DeepEqual(gotSupport, wantSupport) {
		t.Fatalf("support = %#v, want %#v", gotSupport, wantSupport)
	}

	wantToolchain, err := transporttoolchain.Current()
	if err != nil {
		t.Fatalf("transporttoolchain.Current: %v", err)
	}
	gotToolchain := capabilities.TransportToolchain()
	if !gotToolchain.Valid() || len(gotToolchain.Components()) != 13 || gotToolchain.Digest() != wantToolchain.Digest() || !bytes.Equal(gotToolchain.RecordJSON(), wantToolchain.RecordJSON()) {
		t.Fatalf("transport toolchain = %#v", gotToolchain)
	}
}

func TestCurrentIsDeterministicAndProjectIndependent(t *testing.T) {
	t.Parallel()

	first, err := installedcapabilities.Current()
	if err != nil {
		t.Fatalf("first Current: %v", err)
	}
	second, err := installedcapabilities.Current()
	if err != nil {
		t.Fatalf("second Current: %v", err)
	}
	if !bytes.Equal(first.CanonicalJSON(), second.CanonicalJSON()) {
		t.Fatalf("Current is not deterministic:\nfirst  %s\nsecond %s", first.CanonicalJSON(), second.CanonicalJSON())
	}
	lower := strings.ToLower(string(first.CanonicalJSON()))
	for _, forbidden := range []string{"working_directory", "environment_name", "configuration_path", "timestamp", `c:\\`, `d:\\`} {
		if strings.Contains(lower, forbidden) {
			t.Fatalf("installed capabilities contain Project or machine state %q: %s", forbidden, first.CanonicalJSON())
		}
	}
}
