package implementationselect_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/implementationselect"
	"go.yaml.in/yaml/v3"
)

const selectionModule = "example.com/acme/implementation-rollback"

func TestResourceSelectionPreservesInstanceOwnershipAcrossSelectors(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			options, selected := resourceSelectionLayer(t, root, mode, resourceSelectionDocument)
			primaryName := "PRIVATE_PRIMARY"
			if mode == "environment" {
				primaryName = "PRIVATE_OVERLAY"
				writeImplementationFile(t, filepath.Join(root, selected), "# Preserve selected overlay comment.\nresources: {instances: {database.primary: {config: {name: PRIVATE_OVERLAY}}}}\n")
			}
			options.Target, options.Constructor = "database.primary", selectionModule+"/resourceold.New"
			before := implementationProjectTree(t, root)
			result, err := implementationselect.Select(t.Context(), options)
			if err != nil {
				t.Fatalf("same-provider selection: %v", err)
			}
			assertUnifiedSelectionResult(t, result, root, selected, options.Target, "resource", options.Constructor)
			manifest := resolveSelectionManifest(t, options)
			assertSelectionValue(t, manifest, primaryName, "resources", "instances", "database.primary", "config", "name")
			assertSelectionValue(t, manifest, "PRIVATE_PRIMARY_SECRET", "resources", "instances", "database.primary", "config", "password", "env")
			assertSelectionValue(t, manifest, "PRIVATE_REPLICA", "resources", "instances", "database.replica", "config", "name")
			assertUnselectedDocuments(t, root, before, selected)
			assertSelectionUnchanged(t, options)

			options.Constructor = selectionModule + "/resourcenew.New"
			result, err = implementationselect.Select(t.Context(), options)
			if err != nil || !result.Changed() {
				t.Fatalf("changed-provider selection: changed %t, %v", result.Changed(), err)
			}
			assertUnifiedSelectionResult(t, result, root, selected, options.Target, "resource", options.Constructor)
			document := readSelectionYAML(t, root, selected)
			assertSelectionValue(t, document, options.Constructor, "resources", "instances", options.Target, "use")
			if _, exists := selectionValue(document, "resources", "instances", options.Target, "config"); exists {
				t.Fatal("provider replacement retained old instance-owned Config")
			}
			if !bytes.Contains(readSelectionFile(t, root, selected), []byte("# Preserve")) {
				t.Fatal("selection discarded unrelated selected-layer comments")
			}
			manifest = resolveSelectionManifest(t, options)
			assertSelectionValue(t, manifest, "PRIVATE_REPLICA", "resources", "instances", "database.replica", "config", "name")
			assertSelectionValue(t, manifest, selectionModule+"/resourceold.New", "resources", "instances", "database.replica", "use")
			assertUnselectedDocuments(t, root, before, selected)
			assertSelectionUnchanged(t, options)
			assertSelectionGeneratedCurrent(t, options)
			if mode == "default" {
				runSelectedResourceRuntime(t, root)
			}
		})
	}
}

func TestResourceSelectionCleansOnlyObsoleteExactConsumerParameters(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			options, selected := resourceSelectionLayer(t, root, mode, resourceConsumerDocument)
			options.Target, options.Constructor = "database.wrapper", selectionModule+"/resourceone.New"
			before := implementationProjectTree(t, root)
			if _, err := implementationselect.Select(t.Context(), options); err != nil {
				t.Fatalf("replace Resource consumer: %v", err)
			}
			manifest := resolveSelectionManifest(t, options)
			if _, exists := selectionValue(manifest, "resources", "bind", "instances", "database.wrapper", "upstream"); exists {
				t.Fatal("obsolete exact upstream parameter binding survived")
			}
			assertSelectionValue(t, manifest, "database.replica", "resources", "bind", "instances", "database.wrapper", "Replica")
			assertSelectionValue(t, manifest, "database.primary", "resources", "bind", "instances", "database.still-wrapper", "upstream")
			assertSelectionValue(t, manifest, "database.replica", "resources", "bind", "instances", "database.still-wrapper", "Replica")
			assertSelectionValue(t, manifest, "PRIVATE_STILL_OWNED", "resources", "instances", "database.still-wrapper", "config", "name")
			assertSelectionValue(t, manifest, "PRIVATE_PRIMARY", "resources", "instances", "database.primary", "config", "name")
			if mode == "environment" {
				assertSelectionValue(t, readSelectionYAML(t, root, selected), true, "resources", "bind", "instances", "database.wrapper", "upstream", "$remove")
			}
			assertUnselectedDocuments(t, root, before, selected)
			assertSelectionGeneratedCurrent(t, options)
		})
	}
}

func TestResourceSelectionAssignsFirstProviderWithOnlyLocallyOwnedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, mode, local string
		withConfig        bool
	}{
		{name: "empty local instance", mode: "default", local: "database.pending: {}\n"},
		{name: "root local unbound Config", mode: "default", local: "database.pending: {config: {name: PRIVATE_FIRST}}\n", withConfig: true},
		{name: "overlay local unbound Config", mode: "environment", local: "database.pending: {config: {name: PRIVATE_FIRST}}\n", withConfig: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			options, selected := resourceSelectionLayer(t, root, test.mode, resourceSelectionDocument)
			if test.mode == "environment" {
				writeImplementationFile(t, filepath.Join(root, selected), "# Preserve first provider intent.\nresources:\n  instances:\n    "+test.local)
			} else {
				writeImplementationFile(t, filepath.Join(root, selected), resourceSelectionDocument+"    "+test.local)
			}
			options.Target, options.Constructor = "database.pending", selectionModule+"/resourcenew.New"
			if test.withConfig {
				options.Constructor = selectionModule + "/resourcerequired.New"
			}
			before := implementationProjectTree(t, root)
			result, err := implementationselect.Select(t.Context(), options)
			if err != nil || !result.Changed() {
				t.Fatalf("first provider selection: changed %t, %v", result.Changed(), err)
			}
			assertUnifiedSelectionResult(t, result, root, selected, options.Target, "resource", options.Constructor)
			manifest := resolveSelectionManifest(t, options)
			assertSelectionValue(t, manifest, options.Constructor, "resources", "instances", options.Target, "use")
			if test.withConfig {
				assertSelectionValue(t, manifest, "PRIVATE_FIRST", "resources", "instances", options.Target, "config", "name")
			}
			assertSelectionValue(t, manifest, "PRIVATE_REPLICA", "resources", "instances", "database.replica", "config", "name")
			assertUnselectedDocuments(t, root, before, selected)
			assertSelectionGeneratedCurrent(t, options)
		})
	}
}

func TestResourceSelectionRejectsTombstonedTargetAndInheritedUnboundConfiguration(t *testing.T) {
	for _, test := range []struct {
		name, base, overlay, target, constructor string
		want                                     error
	}{
		{
			name: "tombstoned instance", base: resourceSelectionDocument,
			overlay: "resources: {instances: {database.primary: {$remove: true}}}\n",
			target:  "database.primary", constructor: "resourcenew.New", want: implementationselect.ErrTargetNotFound,
		},
		{
			name: "lower unbound Config is not migrated", base: resourceSelectionDocument + "    database.pending: {config: {name: PRIVATE_LOWER}}\n",
			overlay: "{}\n", target: "database.pending", constructor: "resourcerequired.New", want: applicationmeta.ErrConfigurationRequired,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			options, selected := resourceSelectionLayer(t, root, "environment", test.base)
			writeImplementationFile(t, filepath.Join(root, selected), test.overlay)
			options.Target, options.Constructor = test.target, selectionModule+"/"+test.constructor
			before := implementationProjectTree(t, root)
			_, err := implementationselect.Select(t.Context(), options)
			if !errors.Is(err, implementationselect.ErrSelect) || !errors.Is(err, test.want) {
				t.Fatalf("Select = %v, want ErrSelect and %v", err, test.want)
			}
			assertSelectionTree(t, root, before)
		})
	}
}

func TestResourceSelectionRejectsUnresolvedFinalStatesWithoutMutation(t *testing.T) {
	tests := []struct {
		name, target, constructor, document string
		want                                error
	}{
		{"malformed target", "database/../primary", "resourcenew.New", resourceSelectionDocument, implementationselect.ErrInvalidTarget},
		{"missing instance", "database.missing", "resourcenew.New", resourceSelectionDocument, implementationselect.ErrTargetNotFound},
		{"missing Interface", "missing.contract/v1", "smtp.New", resourceSelectionDocument, implementationselect.ErrTargetNotFound},
		{"wrong exact Resource contract", "database.primary", "resourceother.New", resourceSelectionDocument, implementationselect.ErrProviderIncompatible},
		{"Implementation is not a provider", "database.primary", "smtp.New", resourceSelectionDocument, implementationselect.ErrProviderIncompatible},
		{"new required Config", "database.primary", "resourcerequired.New", resourceSelectionDocument, applicationmeta.ErrConfigurationRequired},
		{"same provider missing required Config", "database.primary", "resourceold.New", strings.Replace(resourceSelectionDocument, "name: PRIVATE_PRIMARY, ", "", 1), applicationmeta.ErrConfigurationRequired},
		{"surviving binding changed contract", "database.wrapper", "resourcechanged.New", resourceConsumerDocument, constructorgraph.ErrInvalidResourceBinding},
		{"renamed parameter cannot choose among instances", "database.wrapper", "resourcerenamed.New", resourceConsumerDocument, constructorgraph.ErrAmbiguousResourceBinding},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			options, _ := resourceSelectionLayer(t, root, "default", test.document)
			options.Target, options.Constructor = test.target, selectionModule+"/"+test.constructor
			before := implementationProjectTree(t, root)
			_, err := implementationselect.Select(t.Context(), options)
			if !errors.Is(err, implementationselect.ErrSelect) || !errors.Is(err, test.want) {
				t.Fatalf("Select = %v, want ErrSelect and %v", err, test.want)
			}
			if strings.Contains(err.Error(), "PRIVATE_") {
				t.Fatalf("selection error exposed private configuration: %v", err)
			}
			if errors.Is(err, constructorgraph.ErrInvalidResourceBinding) || errors.Is(err, constructorgraph.ErrAmbiguousResourceBinding) {
				var detail *constructorgraph.ResourceBindingError
				parameter := "Replica"
				if test.constructor == "resourcerenamed.New" {
					parameter = "replica"
				}
				if !errors.As(err, &detail) || detail.Consumer() != "database.wrapper" || detail.ParameterName() != parameter {
					t.Fatalf("binding error lost exact consumer/parameter: %v", err)
				}
			}
			assertSelectionTree(t, root, before)
		})
	}
}

func TestResourceSelectionRepairsInvalidStartingGraphAndRequiredFields(t *testing.T) {
	for _, test := range []struct {
		name, document, target string
		initialFailure         error
	}{
		{"missing old required value", strings.Replace(resourceSelectionDocument, "name: PRIVATE_PRIMARY, ", "", 1), "database.primary", applicationmeta.ErrConfigurationRequired},
		{"malformed old typed value", strings.Replace(resourceSelectionDocument, "name: PRIVATE_PRIMARY", "name: [PRIVATE_INVALID]", 1), "database.primary", applicationmeta.ErrConfigurationInvalidValue},
		{"cyclic old consumer", strings.Replace(resourceConsumerDocument, "upstream: database.primary", "upstream: database.wrapper", 1), "database.wrapper", constructorgraph.ErrCycle},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			options, _ := resourceSelectionLayer(t, root, "default", test.document)
			if _, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: options.Environment}); !errors.Is(err, test.initialFailure) {
				t.Fatalf("repair fixture initial error = %v, want %v", err, test.initialFailure)
			}
			options.Target, options.Constructor = test.target, selectionModule+"/resourcenew.New"
			if _, err := implementationselect.Select(t.Context(), options); err != nil {
				t.Fatalf("valid final selection cannot repair invalid starting state: %v", err)
			}
			manifest := resolveSelectionManifest(t, options)
			assertSelectionValue(t, manifest, options.Constructor, "resources", "instances", options.Target, "use")
			assertSelectionValue(t, manifest, "PRIVATE_REPLICA", "resources", "instances", "database.replica", "config", "name")
			assertSelectionGeneratedCurrent(t, options)
		})
	}
}

func TestResourceSelectionPreservesSelectedLayerAndSparseOverlay(t *testing.T) {
	for _, mode := range []string{"default", "environment", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			options, selected := resourceSelectionLayer(t, root, mode, resourceConsumerDocument)
			options.Target, options.Constructor = "database.wrapper", selectionModule+"/resourceone.New"
			before := implementationProjectTree(t, root)
			if _, err := implementationselect.Select(t.Context(), options); err != nil {
				t.Fatalf("lower-layer provider selection: %v", err)
			}
			document := readSelectionYAML(t, root, selected)
			assertSelectionValue(t, document, options.Constructor, "resources", "instances", options.Target, "use")
			if mode == "environment" {
				assertSelectionValue(t, document, true, "resources", "bind", "instances", options.Target, "upstream", "$remove")
			} else if _, exists := selectionValue(document, "resources", "bind", "instances", options.Target, "upstream"); exists {
				t.Fatal("root or replacement selection retained an overlay-only binding tombstone")
			}
			if mode == "environment" {
				if _, exists := selectionValue(document, "resources", "bind", "instances", options.Target, "Replica"); exists {
					t.Fatal("overlay materialized an unchanged lower-layer binding")
				}
				if _, exists := selectionValue(document, "resources", "instances", "database.replica"); exists {
					t.Fatal("overlay copied an unchanged lower-layer instance")
				}
			}
			manifest := resolveSelectionManifest(t, options)
			assertSelectionValue(t, manifest, "database.replica", "resources", "bind", "instances", options.Target, "Replica")
			assertSelectionValue(t, manifest, "PRIVATE_STILL_OWNED", "resources", "instances", "database.still-wrapper", "config", "name")
			assertUnselectedDocuments(t, root, before, selected)
			assertSelectionGeneratedCurrent(t, options)
		})
	}
}

func TestUnifiedInterfaceSelectionCleansOnlyLastOwnedConstructorConfiguration(t *testing.T) {
	tests := []struct {
		name, mode, extra string
		retained          bool
	}{
		{name: "last explicit owner", mode: "default"},
		{name: "last owner through overlay", mode: "environment"},
		{name: "last owner through replacement", mode: "replacement"},
		{name: "other dormant explicit owner", mode: "default", extra: "    email.copy/v1: " + selectionModule + "/shared.New\n", retained: true},
		{name: "other active automatic owner", mode: "default", extra: "  require: [email.copy/v1]\n", retained: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			writeSelectionInterfaceOwners(t, root)
			document := "# Preserve unrelated intent.\ninterfaces:\n  use:\n    email.send/v1: " + selectionModule + "/shared.New\n" + test.extra + "config:\n  " + selectionModule + "/shared.New: {name: PRIVATE_SHARED}\n"
			options, selected := resourceSelectionLayer(t, root, test.mode, document)
			options.Target, options.Constructor = "email.send/v1", selectionModule+"/local.New"
			before := implementationProjectTree(t, root)
			result, err := implementationselect.Select(t.Context(), options)
			if err != nil {
				t.Fatalf("Interface selection ownership cleanup: %v", err)
			}
			assertUnifiedSelectionResult(t, result, root, selected, options.Target, "interface", options.Constructor)
			manifest := resolveSelectionManifest(t, options)
			value, exists := selectionValue(manifest, "config", selectionModule+"/shared.New", "name")
			if exists != test.retained || exists && value != "PRIVATE_SHARED" {
				t.Fatalf("shared configuration ownership: exists %t, want %t", exists, test.retained)
			}
			if test.mode == "environment" {
				assertSelectionValue(t, readSelectionYAML(t, root, selected), true, "config", selectionModule+"/shared.New", "$remove")
			}
			assembly := readSelectionFile(t, root, "generated/go/assembly/interfaces_gen.go")
			if bytes.Contains(assembly, []byte(selectionModule+"/local.New")) {
				t.Fatal("dormant Interface selection created an application root")
			}
			assertUnselectedDocuments(t, root, before, selected)
			assertSelectionGeneratedCurrent(t, options)
		})
	}
}

func TestUnifiedInterfaceSelectionCleansOrRetainsReachableConfiguration(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("dependency-still-required-%t", retained), func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			writeSelectionInterfaceOwners(t, root)
			require := "email.send/v1"
			if retained {
				require += ", email.copy/v1"
			}
			document := "interfaces:\n  require: [" + require + "]\n  use: {email.send/v1: " + selectionModule + "/dependent.New}\nconfig:\n  " + selectionModule + "/dependent.New: {name: PRIVATE_DEPENDENT}\n  " + selectionModule + "/shared.New: {name: PRIVATE_TRANSITIVE}\n"
			options, _ := resourceSelectionLayer(t, root, "default", document)
			options.Target, options.Constructor = "email.send/v1", selectionModule+"/local.New"
			if _, err := implementationselect.Select(t.Context(), options); err != nil {
				t.Fatalf("transitive ownership cleanup: %v", err)
			}
			manifest := resolveSelectionManifest(t, options)
			if _, exists := selectionValue(manifest, "config", selectionModule+"/dependent.New"); exists {
				t.Fatal("orphan old selected constructor configuration survived")
			}
			value, exists := selectionValue(manifest, "config", selectionModule+"/shared.New", "name")
			if exists != retained || exists && value != "PRIVATE_TRANSITIVE" {
				t.Fatalf("transitive configuration ownership: exists %t, want %t", exists, retained)
			}
			assertSelectionGeneratedCurrent(t, options)
		})
	}
}

func TestUnifiedInterfaceSelectionRepairsMissingOldRequiredConfiguration(t *testing.T) {
	root := writeResourceSelectionProject(t)
	writeSelectionInterfaceOwners(t, root)
	options, _ := resourceSelectionLayer(t, root, "default", "interfaces: {require: [email.send/v1], use: {email.send/v1: "+selectionModule+"/shared.New}}\n")
	if _, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: options.Environment}); !errors.Is(err, applicationmeta.ErrConfigurationRequired) {
		t.Fatalf("old selected constructor required Config error = %v", err)
	}
	options.Target, options.Constructor = "email.send/v1", selectionModule+"/local.New"
	if _, err := implementationselect.Select(t.Context(), options); err != nil {
		t.Fatalf("Interface selection could not repair missing old Config: %v", err)
	}
	assertSelectionGeneratedCurrent(t, options)
}

func TestUnifiedInterfaceSelectionRepairsOnlyProvablyObsoleteInvalidConfiguration(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid-config-still-owned-%t", retained), func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			writeSelectionInterfaceOwners(t, root)
			document := "interfaces:\n  use:\n    email.send/v1: " + selectionModule + "/shared.New\n"
			if retained {
				document += "    email.copy/v1: " + selectionModule + "/shared.New\n"
			}
			document += "config:\n  " + selectionModule + "/shared.New: {unknown: PRIVATE_INVALID}\n"
			options, _ := resourceSelectionLayer(t, root, "default", document)
			options.Target, options.Constructor = "email.send/v1", selectionModule+"/local.New"
			before := implementationProjectTree(t, root)
			_, err := implementationselect.Select(t.Context(), options)
			if retained {
				if !errors.Is(err, implementationselect.ErrSelect) || !errors.Is(err, applicationmeta.ErrConfigurationValues) {
					t.Fatalf("still-owned invalid Config was not rejected: %v", err)
				}
				if strings.Contains(err.Error(), "PRIVATE_INVALID") {
					t.Fatal("invalid Config diagnostic exposed its value")
				}
				assertSelectionTree(t, root, before)
				return
			}
			if err != nil {
				t.Fatalf("obsolete invalid Config prevented valid final replacement: %v", err)
			}
			if _, exists := selectionValue(readSelectionYAML(t, root, "plystra.yaml"), "config", selectionModule+"/shared.New"); exists {
				t.Fatal("obsolete invalid Config survived replacement")
			}
			assertSelectionGeneratedCurrent(t, options)
		})
	}
}

func TestResourceSelectionRollsBackCleanupAndGeneratedStateOnValidationFailure(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(fmt.Sprintf("concurrent-edit-%t", concurrent), func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			options, selected := resourceSelectionLayer(t, root, "default", resourceConsumerDocument)
			options.Target, options.Constructor = "database.wrapper", selectionModule+"/resourcewrapper.New"
			if _, err := implementationselect.Select(t.Context(), options); err != nil {
				t.Fatalf("establish generated state: %v", err)
			}
			writeImplementationFile(t, filepath.Join(root, "go.mod"), string(readSelectionFile(t, root, "go.mod"))+"\n\n")
			before := implementationProjectTree(t, root)
			original := before[selected]
			failure := errors.New("injected unified-selection validation failure")
			observed := false
			options.Constructor = selectionModule + "/resourceone.New"
			options.Validate = func(_ context.Context, updatedRoot string) error {
				observed = true
				document := readSelectionYAML(t, updatedRoot, selected)
				assertSelectionValue(t, document, options.Constructor, "resources", "instances", options.Target, "use")
				if _, exists := selectionValue(document, "resources", "bind", "instances", options.Target, "upstream"); exists {
					t.Error("validator saw obsolete consumer binding")
				}
				if !bytes.Contains(readSelectionFile(t, updatedRoot, "generated/go/assembly/interfaces_gen.go"), []byte(options.Constructor)) {
					t.Error("validator did not see regenerated Resource assembly")
				}
				if concurrent {
					data := append(readSelectionFile(t, updatedRoot, selected), []byte("# Concurrent user edit.\n")...)
					writeImplementationFile(t, filepath.Join(updatedRoot, selected), string(data))
					before[filepath.ToSlash(selected)] = data
				}
				return failure
			}
			_, err := implementationselect.Select(t.Context(), options)
			if !observed || !errors.Is(err, failure) {
				t.Fatalf("validator called %t, Select = %v", observed, err)
			}
			if concurrent {
				if !errors.Is(err, atomicfs.ErrConcurrentChange) {
					t.Fatalf("concurrent edit lost conflict classification: %v", err)
				}
				after := implementationProjectTree(t, root)
				recoverable := false
				for path, data := range after {
					if strings.HasPrefix(path, ".plystra-files-") {
						recoverable = recoverable || strings.Contains(path, "/backup/") && bytes.Equal(data, original)
						delete(after, path)
					}
				}
				if !recoverable || !equalImplementationTrees(after, before) {
					t.Fatalf("concurrent edit or rollback state lost; original YAML recoverable %t", recoverable)
				}
			} else {
				assertSelectionTree(t, root, before)
			}
		})
	}
}

func TestUnifiedInterfaceSelectionRejectsDeselectedDependencyDriftDuringValidation(t *testing.T) {
	root := writeResourceSelectionProject(t)
	writeSelectionInterfaceOwners(t, root)
	document := "interfaces:\n  require: [email.send/v1]\n  use: {email.send/v1: " + selectionModule + "/dependent.New}\nconfig:\n  " + selectionModule + "/dependent.New: {name: PRIVATE_OLD}\n  " + selectionModule + "/shared.New: {name: PRIVATE_TRANSITIVE}\n"
	options, selected := resourceSelectionLayer(t, root, "default", document)
	options.Target, options.Constructor = "email.send/v1", selectionModule+"/dependent.New"
	if _, err := implementationselect.Select(t.Context(), options); err != nil {
		t.Fatalf("establish dependent selection: %v", err)
	}
	writeImplementationFile(t, filepath.Join(root, "go.mod"), string(readSelectionFile(t, root, "go.mod"))+"\n\n")
	before := implementationProjectTree(t, root)
	const sourcePath = "dependent/implementation.go"
	source := string(before[sourcePath])
	changed := strings.Replace(source, "copier copyv1.Interface", "copier sendv1.Interface", 1)
	changed = strings.Replace(changed, " copyv1 \""+selectionModule+"/interfaces/email/copy/v1\"\n", "", 1)
	if changed == source || strings.Contains(changed, "copyv1") {
		t.Fatal("fixture failed to change the deselected constructor's required Interface")
	}
	options.Constructor = selectionModule + "/local.New"
	validated := false
	options.Validate = func(ctx context.Context, updatedRoot string) error {
		assertSelectionValue(t, readSelectionYAML(t, updatedRoot, selected), options.Constructor, "interfaces", "use", options.Target)
		assembly := readSelectionFile(t, updatedRoot, "generated/go/assembly/interfaces_gen.go")
		if !bytes.Contains(assembly, []byte(options.Constructor)) || bytes.Contains(assembly, []byte(selectionModule+"/dependent.New")) {
			t.Error("validator did not see the new selected assembly")
		}
		command := exec.CommandContext(ctx, "go", "test", "-mod=readonly", "./...")
		command.Dir, command.Env = updatedRoot, options.Environment
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("validate generated replacement: %w\n%s", err, output)
		}
		validated = true
		writeImplementationFile(t, filepath.Join(updatedRoot, sourcePath), changed)
		return nil
	}
	_, err := implementationselect.Select(t.Context(), options)
	if !validated {
		t.Fatalf("real replacement validation did not reach the authored edit: %v", err)
	}
	if !errors.Is(err, implementationselect.ErrSelect) || !errors.Is(err, applicationresolve.ErrConcurrentChange) {
		t.Errorf("deselected dependency drift was not rejected as concurrent: %v", err)
	}
	before[sourcePath] = []byte(changed)
	assertSelectionTree(t, root, before)
}

func TestUnifiedInterfaceSelectionCollectsOwnersPastMissingOldDependency(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(fmt.Sprintf("transitive-owner-retained-%t", retained), func(t *testing.T) {
			root := writeResourceSelectionProject(t)
			writeSelectionInterfaceOwners(t, root)
			writeImplementationFile(t, filepath.Join(root, "interfaces/aaa/missing/v1/interface.go"), `package missingv1
import "context"
//plystra:interface aaa.missing/v1
type Interface interface { Missing(context.Context,Request)(Response,error) }
type Request struct{}
type Response struct{}
`)
			source := string(readSelectionFile(t, root, "dependent/implementation.go"))
			source = strings.Replace(source, "import (", "import (\n missingv1 \""+selectionModule+"/interfaces/aaa/missing/v1\"", 1)
			source = strings.Replace(source, "c Config, copier copyv1.Interface", "c Config, missing missingv1.Interface, copier copyv1.Interface", 1)
			writeImplementationFile(t, filepath.Join(root, "dependent/implementation.go"), source)
			require := "email.send/v1"
			if retained {
				require += ", email.copy/v1"
			}
			document := "interfaces:\n  require: [" + require + "]\n  use: {email.send/v1: " + selectionModule + "/dependent.New}\nconfig:\n  " + selectionModule + "/dependent.New: {name: PRIVATE_OLD}\n  " + selectionModule + "/shared.New: {name: PRIVATE_LATER_BRANCH}\n"
			options, _ := resourceSelectionLayer(t, root, "default", document)
			if _, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{Start: root, Environment: options.Environment}); !errors.Is(err, constructorgraph.ErrMissingBinding) {
				t.Fatalf("old graph did not have the expected early missing dependency: %v", err)
			}
			options.Target, options.Constructor = "email.send/v1", selectionModule+"/local.New"
			if _, err := implementationselect.Select(t.Context(), options); err != nil {
				t.Fatalf("selection could not repair the incomplete old dependency graph: %v", err)
			}
			manifest := resolveSelectionManifest(t, options)
			if _, exists := selectionValue(manifest, "config", selectionModule+"/dependent.New"); exists {
				t.Fatal("old explicit constructor Config survived replacement")
			}
			value, exists := selectionValue(manifest, "config", selectionModule+"/shared.New", "name")
			if exists != retained || exists && value != "PRIVATE_LATER_BRANCH" {
				t.Fatalf("later branch Config ownership: exists %t, want retained %t", exists, retained)
			}
			assertSelectionGeneratedCurrent(t, options)
		})
	}
}

func writeResourceSelectionProject(t *testing.T) string {
	t.Helper()
	root := writeTransactionalImplementationProject(t)
	for _, contract := range []struct{ pkg, id string }{{"database", "storage.database/v1"}, {"other", "storage.other/v1"}} {
		writeImplementationFile(t, filepath.Join(root, contract.pkg, "resource.go"), "package "+contract.pkg+"\n//plystra:resource "+contract.id+"\ntype Resource interface { Value() string }\n")
	}
	for _, provider := range []struct{ pkg, id, field string }{
		{"resourceold", "storage.database/v1", "Name string `yaml:\"name\" plystra:\"required\"`; Password configuration.Secret `yaml:\"password\"`"},
		{"resourcenew", "storage.database/v1", "Name string `yaml:\"name\" plystra-default:\"new-default\"`; Password configuration.Secret `yaml:\"password\"`"},
		{"resourcerequired", "storage.database/v1", "Name string `yaml:\"name\" plystra:\"required\"`; Password configuration.Secret `yaml:\"password\"`"},
		{"resourceother", "storage.other/v1", "Name string `yaml:\"name\" plystra-default:\"other-default\"`; Password configuration.Secret `yaml:\"password\"`"},
	} {
		writeImplementationFile(t, filepath.Join(root, provider.pkg, "provider.go"), fmt.Sprintf(`package %s
import "github.com/plystra/kernel/configuration"
type Config struct { %s }
type value struct { name string }
var Names []string
//plystra:implements-resource %s
func New(c Config) (*value, error) { Names=append(Names,c.Name);return &value{name:c.Name},nil }
func (v *value) Value() string { return v.name }
`, provider.pkg, provider.field, provider.id))
	}
	for _, consumer := range []struct{ pkg, dependency, parameters, value string }{
		{"resourcewrapper", "database", "upstream database.Resource, Replica database.Resource", "upstream.Value()+Replica.Value()"},
		{"resourceone", "database", "Replica database.Resource", "Replica.Value()"},
		{"resourcerenamed", "database", "replica database.Resource", "replica.Value()"},
		{"resourcechanged", "other", "Replica other.Resource", "Replica.Value()"},
	} {
		writeImplementationFile(t, filepath.Join(root, consumer.pkg, "provider.go"), fmt.Sprintf(`package %s
import "%s/%s"
type Config struct { Name string `+"`yaml:\"name\"`"+` }
type value struct { name string }
//plystra:implements-resource storage.database/v1
func New(c Config, %s) (*value,error) { return &value{name:c.Name+%s},nil }
func (v *value) Value() string { return v.name }
`, consumer.pkg, selectionModule, consumer.dependency, consumer.parameters, consumer.value))
	}
	return root
}

func resourceSelectionLayer(t *testing.T, root, mode, document string) (implementationselect.Options, string) {
	t.Helper()
	options := implementationselect.Options{Start: filepath.Join(root, "local"), Environment: implementationTestEnvironment()}
	selected := "plystra.yaml"
	switch mode {
	case "default":
		writeImplementationFile(t, filepath.Join(root, selected), document)
	case "environment":
		writeImplementationFile(t, filepath.Join(root, selected), document)
		selected, options.EnvironmentName = "plystra.production.yaml", "production"
		writeImplementationFile(t, filepath.Join(root, selected), "# Preserve selected overlay comment.\n{}\n")
		options.Environment = append(options.Environment, "PLYSTRA_CONFIG=absent.yaml")
	case "replacement":
		marker := "interfaces: {require: [absent.interface/v1]}\n"
		writeImplementationFile(t, filepath.Join(root, selected), marker)
		selected, options.ConfigurationPath = "deploy/customer.yaml", "deploy/customer.yaml"
		writeImplementationFile(t, filepath.Join(root, selected), document)
		options.Environment = append(options.Environment, "PLYSTRA_ENV=absent")
	default:
		t.Fatalf("unknown selector mode %q", mode)
	}
	writeImplementationFile(t, filepath.Join(root, "plystra.unselected.yaml"), "not: [valid YAML\n")
	return options, selected
}

func writeSelectionInterfaceOwners(t *testing.T, root string) {
	t.Helper()
	writeImplementationFile(t, filepath.Join(root, "interfaces/email/copy/v1/interface.go"), `package copyv1
import "context"
//plystra:interface email.copy/v1
type Interface interface { Copy(context.Context,Request)(Response,error) }
type Request struct{}
type Response struct{}
`)
	writeImplementationFile(t, filepath.Join(root, "shared/implementation.go"), `package shared
import (
 "context"
 sendv1 "example.com/acme/implementation-rollback/interfaces/email/send/v1"
 copyv1 "example.com/acme/implementation-rollback/interfaces/email/copy/v1"
)
type Config struct { Name string `+"`yaml:\"name\" plystra:\"required\"`"+` }
type Service struct{}
//plystra:implements email.send/v1
//plystra:implements email.copy/v1
func New(c Config)(*Service,error){return &Service{},nil}
func (*Service) Send(context.Context,sendv1.Request)(sendv1.Response,error){return sendv1.Response{},nil}
func (*Service) Copy(context.Context,copyv1.Request)(copyv1.Response,error){return copyv1.Response{},nil}
`)
	writeImplementationFile(t, filepath.Join(root, "dependent/implementation.go"), `package dependent
import (
 "context"
 sendv1 "example.com/acme/implementation-rollback/interfaces/email/send/v1"
 copyv1 "example.com/acme/implementation-rollback/interfaces/email/copy/v1"
)
type Config struct { Name string `+"`yaml:\"name\" plystra:\"required\"`"+` }
type Service struct{}
//plystra:implements email.send/v1
func New(c Config, copier copyv1.Interface)(*Service,error){return &Service{},nil}
func (*Service) Send(context.Context,sendv1.Request)(sendv1.Response,error){return sendv1.Response{},nil}
`)
}

func readSelectionFile(t testing.TB, root, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func readSelectionYAML(t testing.TB, root, name string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := yaml.Unmarshal(readSelectionFile(t, root, name), &document); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return document
}

func resolveSelectionManifest(t *testing.T, options implementationselect.Options) map[string]any {
	t.Helper()
	resolved, err := applicationresolve.Resolve(t.Context(), applicationresolve.Options{
		Start: options.Start, ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName, Environment: options.Environment,
	})
	if err != nil {
		t.Fatalf("resolve committed selection: %v", err)
	}
	manifest := resolved.Manifest()
	instances, bindings, configs := map[string]any{}, map[string]any{}, map[string]any{}
	for _, instance := range manifest.ResourceInstances() {
		entry := map[string]any{"use": instance.Provider().String()}
		if instance.HasConfiguration() {
			var value map[string]any
			if err := yaml.Unmarshal(instance.ConfigurationYAML(), &value); err != nil {
				t.Fatal(err)
			}
			entry["config"] = value
		}
		instances[instance.Name()] = entry
	}
	for _, binding := range manifest.ResourceBindings() {
		if bindings[binding.Namespace()] == nil {
			bindings[binding.Namespace()] = map[string]any{}
		}
		namespace := bindings[binding.Namespace()].(map[string]any)
		if namespace[binding.Consumer()] == nil {
			namespace[binding.Consumer()] = map[string]any{}
		}
		namespace[binding.Consumer()].(map[string]any)[binding.ParameterName()] = binding.Target()
	}
	for _, config := range manifest.Configurations() {
		var value map[string]any
		if err := yaml.Unmarshal(config.YAML(), &value); err != nil {
			t.Fatal(err)
		}
		configs[config.Constructor().String()] = value
	}
	return map[string]any{"resources": map[string]any{"instances": instances, "bind": bindings}, "config": configs}
}

func selectionValue(document map[string]any, path ...string) (any, bool) {
	var value any = document
	for _, key := range path {
		mapping, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = mapping[key]
		if !ok {
			return nil, false
		}
	}
	return value, true
}

func assertSelectionValue(t testing.TB, document map[string]any, want any, path ...string) {
	t.Helper()
	got, exists := selectionValue(document, path...)
	if !exists || got != want {
		t.Fatalf("selected path %v: exists %t, value does not match expected fixture", path, exists)
	}
}

func assertUnifiedSelectionResult(t testing.TB, result implementationselect.Result, root, selected, target, kind, constructor string) {
	t.Helper()
	if result.Target() != target || result.Kind() != kind || result.Constructor().String() != constructor || result.ManifestPath() != filepath.Join(root, filepath.FromSlash(selected)) {
		t.Fatalf("selection result = target %q, kind %q, constructor %s, manifest %q", result.Target(), result.Kind(), result.Constructor(), result.ManifestPath())
	}
}

func assertUnselectedDocuments(t testing.TB, root string, before map[string][]byte, selected string) {
	t.Helper()
	for path, data := range before {
		if strings.HasSuffix(path, ".yaml") && path != filepath.ToSlash(selected) && !strings.HasPrefix(path, "generated/") {
			if !bytes.Equal(data, readSelectionFile(t, root, path)) {
				t.Fatalf("selection changed unselected document %s", path)
			}
		}
	}
}

func assertSelectionTree(t testing.TB, root string, before map[string][]byte) {
	t.Helper()
	after := implementationProjectTree(t, root)
	for path, data := range before {
		if current, exists := after[path]; !exists || !bytes.Equal(current, data) {
			t.Errorf("selection unexpectedly changed %s", path)
		}
	}
	for path := range after {
		if _, exists := before[path]; !exists {
			t.Errorf("selection unexpectedly created %s", path)
		}
	}
}

func assertSelectionUnchanged(t *testing.T, options implementationselect.Options) {
	t.Helper()
	before := implementationProjectTree(t, filepath.Dir(options.Start))
	result, err := implementationselect.Select(t.Context(), options)
	if err != nil || result.Changed() {
		t.Fatalf("idempotent selection: changed %t, %v", result.Changed(), err)
	}
	assertSelectionTree(t, filepath.Dir(options.Start), before)
}

func assertSelectionGeneratedCurrent(t *testing.T, options implementationselect.Options) {
	t.Helper()
	root := filepath.Dir(options.Start)
	before := implementationProjectTree(t, root)
	if _, err := applicationgenerate.Generate(t.Context(), applicationgenerate.Options{
		Start: options.Start, ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName, Environment: options.Environment, Check: true,
	}); err != nil {
		t.Fatalf("selected generated state is stale: %v", err)
	}
	assertSelectionTree(t, root, before)
}

func runSelectedResourceRuntime(t *testing.T, root string) {
	t.Helper()
	writeImplementationFile(t, filepath.Join(root, "selected_runtime_test.go"), `package application_test
import (
 "context"
 "path/filepath"
 "testing"
 "example.com/acme/implementation-rollback/generated/go/bootstrap"
 "example.com/acme/implementation-rollback/resourceold"
 "example.com/acme/implementation-rollback/resourcenew"
)
func TestSelectedResourceValues(t *testing.T) {
 root,err:=filepath.Abs(".");if err!=nil{t.Fatal(err)}
 app,err:=bootstrap.New(context.Background(),bootstrap.RuntimeOptions{Arguments:[]string{"--configuration-root",root,"--runtime-baseline",filepath.Join(root,"dist/runtime-baseline.json")},Environment:[]string{}})
 if err!=nil{t.Fatal(err)}
 if err:=app.Start(context.Background());err!=nil{t.Fatal(err)}
 defer func(){if err:=app.Stop(context.Background());err!=nil{t.Error(err)}}()
 if len(resourcenew.Names)!=1 || resourcenew.Names[0]!="new-default" {t.Fatal("old primary Config migrated to new provider")}
 if len(resourceold.Names)!=1 || resourceold.Names[0]!="PRIVATE_REPLICA" {t.Fatal("still-owned replica Config changed")}
}
`)
	command := exec.CommandContext(t.Context(), "go", "test", "-race", "-mod=readonly", "-count=1", ".")
	command.Dir, command.Env = root, implementationTestEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("selected generated runtime: %v\n%s", err, output)
	}
}

const resourceSelectionDocument = `# Preserve resource ownership comments.
interfaces:
  require: [email.send/v1]
  use: {email.send/v1: example.com/acme/implementation-rollback/smtp.New}
resources:
  instances:
    database.primary:
      use: example.com/acme/implementation-rollback/resourceold.New
      config: {name: PRIVATE_PRIMARY, password: {env: PRIVATE_PRIMARY_SECRET}}
    database.replica:
      use: example.com/acme/implementation-rollback/resourceold.New
      config: {name: PRIVATE_REPLICA}
`

const resourceConsumerDocument = `# Preserve resource ownership comments.
interfaces:
  require: [email.send/v1]
  use: {email.send/v1: example.com/acme/implementation-rollback/smtp.New}
resources:
  instances:
    database.primary:
      use: example.com/acme/implementation-rollback/resourceold.New
      config: {name: PRIVATE_PRIMARY}
    database.replica:
      use: example.com/acme/implementation-rollback/resourceold.New
      config: {name: PRIVATE_REPLICA}
    database.wrapper:
      use: example.com/acme/implementation-rollback/resourcewrapper.New
      config: {name: PRIVATE_WRAPPER}
    database.still-wrapper:
      use: example.com/acme/implementation-rollback/resourcewrapper.New
      config: {name: PRIVATE_STILL_OWNED}
    database.other:
      use: example.com/acme/implementation-rollback/resourceother.New
  bind:
    instances:
      database.wrapper: {upstream: database.primary, Replica: database.replica}
      database.still-wrapper: {upstream: database.primary, Replica: database.replica}
`
