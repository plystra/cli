// Package scaffoldplan validates complete source and dependency candidates in private stages.
package scaffoldplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/plystra/cli/internal/applicationgen"
	"github.com/plystra/cli/internal/applicationgenerate"
	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/configurationimpact"
	"github.com/plystra/cli/internal/generatedfiles"
	"github.com/plystra/cli/internal/gocommand"
	"github.com/plystra/cli/internal/implementationcreate"
	"github.com/plystra/cli/internal/implementationselect"
	"github.com/plystra/cli/internal/interfacecreate"
	"github.com/plystra/cli/internal/interfacemeta"
	"github.com/plystra/cli/internal/modulemutation"
	"github.com/plystra/cli/internal/projectlocate"
	"github.com/plystra/cli/internal/projectstage"
	"github.com/plystra/cli/internal/runtimebaseline"
)

var ErrPreview = errors.New("preview staged candidate")

// ErrPrivateResult rejects implicit serialization of a private staged plan.
var ErrPrivateResult = errors.New("private scaffold plan cannot be serialized")

type Options struct {
	Start, ConfigurationPath, EnvironmentName string
	GoCommand, TemporaryParent                string
	Environment                               []string
}

// Result retains public-safe projections and private captured configuration.
// It confers no installation authority. Effects remain available on failure.
type Result struct {
	// fmt can bypass methods on unexported wrapper fields. Two pointer levels
	// prevent its recursive fallback from reaching private nested inputs.
	state **resultState
}

type resultState struct {
	inputs         applicationresolve.SelectionInputs
	updated        []byte
	impact         configurationimpact.Impact
	capture        Capture
	reads          []commandschema.ChangePlanPathInput
	effects        commandschema.Effects
	model          string
	forceWrite     bool
	rootDigest     string
	selectedDigest string
}

func (Result) String() string                   { return "<private-scaffold-plan>" }
func (r Result) GoString() string               { return r.String() }
func (r Result) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(r.String())) }
func (r Result) LogValue() slog.Value           { return slog.StringValue(r.String()) }
func (Result) MarshalJSON() ([]byte, error)     { return nil, ErrPrivateResult }
func (Result) MarshalText() ([]byte, error)     { return nil, ErrPrivateResult }
func (Result) MarshalYAML() (any, error)        { return nil, ErrPrivateResult }

func (r Result) stored() *resultState {
	if r.state == nil {
		return nil
	}
	return *r.state
}

func (r Result) Inputs() applicationresolve.SelectionInputs {
	if state := r.stored(); state != nil {
		return state.inputs
	}
	return applicationresolve.SelectionInputs{}
}
func (r Result) Updated() []byte {
	if state := r.stored(); state != nil {
		return append([]byte(nil), state.updated...)
	}
	return nil
}
func (r Result) ConfigurationImpact() configurationimpact.Impact {
	if state := r.stored(); state != nil {
		return state.impact
	}
	return configurationimpact.Impact{}
}
func (r Result) Capture() Capture {
	if state := r.stored(); state != nil {
		return state.capture
	}
	return Capture{}
}
func (r Result) WriteSet() []commandschema.ChangePlanPathInput {
	return r.Capture().writeSet()
}
func (r Result) ReadSet() []commandschema.ChangePlanPathInput {
	if state := r.stored(); state != nil {
		return append([]commandschema.ChangePlanPathInput(nil), state.reads...)
	}
	return nil
}
func (r Result) Effects() commandschema.Effects {
	if state := r.stored(); state != nil {
		return state.effects
	}
	return commandschema.Effects{}
}
func (r Result) FinalModelDigest() string {
	if state := r.stored(); state != nil {
		return state.model
	}
	return ""
}
func (r Result) OriginalConfigurationDigests() (string, string) {
	if state := r.stored(); state != nil {
		return state.rootDigest, state.selectedDigest
	}
	return "", ""
}

// Preview never installs candidate files into an authored Project. Every
// scaffold and dependency is present before final-state planning and generation.
func Preview(ctx context.Context, options Options, request changerequest.Request) (result Result, resultErr error) {
	if ctx == nil || !request.Valid() {
		return result, ErrPreview
	}
	if err := ctx.Err(); err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	scaffolds := 0
	var dependencies []modulemutation.DependencyChange
	for _, operation := range request.Operations() {
		if change, ok := operation.Dependency(); ok {
			dependencies = append(dependencies, modulemutation.DependencyChange{Action: string(change.Action()), Module: change.Module()})
		}
		if operation.Kind() == changerequest.KindInterfaceCreate || operation.Kind() == changerequest.KindImplementationCreate {
			scaffolds++
		}
	}
	if scaffolds == 0 && len(dependencies) == 0 {
		return result, ErrPreview
	}
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	target, err := applicationresolve.SelectConfigurationTarget(project.ModulePath(), project.Path(), options.ConfigurationPath, options.EnvironmentName, options.Environment)
	if err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	// Bind ambient and absolute selectors before relocating the Project.
	options.ConfigurationPath, options.EnvironmentName = "", ""
	switch target.Selection().Mode() {
	case applicationgen.ConfigurationModeEnvironment:
		options.EnvironmentName = target.Selection().Environment()
	case applicationgen.ConfigurationModeExplicit:
		options.ConfigurationPath = target.Selection().Path()
	}
	state := &resultState{}
	result = Result{state: &state}
	accounting := commandschema.EffectsInput{}
	defer func() {
		state.effects, err = commandschema.NewEffects(accounting)
		if err != nil {
			resultErr = errors.Join(resultErr, ErrPreview, err)
		}
		if resultErr != nil {
			state.capture, state.model = Capture{}, ""
		}
	}()
	effect := func(id string, class commandschema.EffectClass, reason string, verification []string) commandschema.Effect {
		value, buildErr := commandschema.NewEffect(commandschema.EffectInput{
			ID: id, Class: class, Phase: "plan", Target: ".", Owner: commandschema.Owner{Module: project.ModulePath(), Path: "."},
			Reason: reason, Reversibility: "none", Verification: verification,
		})
		if buildErr != nil {
			err = errors.Join(err, buildErr)
		}
		return value
	}
	goEnvironment := effect("candidate-go-environment", commandschema.EffectTrustedCodeExecution, "inspect_go_selection", []string{"go", "env", "-json", "GOWORK", "GOFLAGS", "GOENV"})
	temporaryTree := effect("candidate-stage", commandschema.EffectTemporaryFile, "private_candidate_stage", []string{"plystra", "change", "plan", "--request", "-"})
	accounting.Unverified = append(accounting.Unverified,
		effect("candidate-cache", commandschema.EffectCacheMaterialization, "candidate_tool_cache_unknown", []string{"go", "env", "GOCACHE", "GOMODCACHE"}),
		effect("candidate-download", commandschema.EffectDownload, "candidate_downloads_unknown", []string{"go", "env", "GOPROXY"}),
		effect("candidate-helpers", commandschema.EffectTrustedCodeExecution, "candidate_helpers_unknown", []string{"plystra", "inspect", "capabilities"}))
	environmentStarted, treeCreated := false, false
	stage, err := projectstage.Create(ctx, projectstage.Options{
		Start: options.Start, Environment: options.Environment, GoCommand: options.GoCommand, TemporaryParent: options.TemporaryParent,
		Observe: func(event projectstage.Event) {
			switch event {
			case projectstage.GoEnvironmentStarted:
				environmentStarted = true
				accounting.Observed = append(accounting.Observed, goEnvironment)
			case projectstage.TemporaryTreeCreated:
				treeCreated = true
				accounting.Observed = append(accounting.Observed, temporaryTree)
			}
		},
	})
	if !environmentStarted {
		accounting.Skipped = append(accounting.Skipped, goEnvironment)
	}
	if !treeCreated {
		accounting.Skipped = append(accounting.Skipped, temporaryTree)
	}
	if err != nil {
		if stage != nil {
			err = errors.Join(err, stage.Close())
		}
		return result, errors.Join(ErrPreview, err)
	}
	defer func() {
		if closeErr := stage.Close(); closeErr != nil {
			state.model = ""
			resultErr = errors.Join(resultErr, ErrPreview, closeErr)
		}
	}()
	baseline, err := capture(ctx, stage.Root())
	if err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	var previous applicationresolve.SelectionInputs
	if len(dependencies) != 0 {
		if err := retainDependencySources(ctx, stage, options.GoCommand); err != nil {
			return result, errors.Join(ErrPreview, err)
		}
		// Unloadable old authored declarations cannot block a same-request
		// repair or prove old ownership, but their module sources must be safe.
		previous, _ = applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
			Start: stage.Root(), ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
			GoCommand: options.GoCommand, Environment: stage.Environment(),
		})
	}
	build := effect("candidate-build", commandschema.EffectTrustedCodeExecution, "validate_complete_candidate", []string{"go", "build", "-mod=readonly", "./..."})
	validateCandidate := func(mutate applicationgenerate.ModuleMutation) error {
		if err := retainDependencySources(ctx, stage, options.GoCommand); err != nil {
			return err
		}
		var implementations []implementationcreate.StageRequest
		for _, operation := range request.Operations() {
			if creation, ok := operation.InterfaceCreate(); ok {
				if _, err := interfacecreate.Materialize(ctx, stage, creation.Name(), interfacemeta.OperationKind(creation.Semantics())); err != nil {
					return errors.Join(ErrPreview, err)
				}
			} else if creation, ok := operation.ImplementationCreate(); ok {
				implementations = append(implementations, implementationcreate.StageRequest{ContractID: creation.Contract(), Package: creation.Package()})
			}
		}
		if len(implementations) != 0 {
			if _, err := implementationcreate.Materialize(ctx, stage, implementations); err != nil {
				return errors.Join(ErrPreview, err)
			}
		}
		remaining, err := selectionRequest(request)
		if err != nil {
			return errors.Join(ErrPreview, err)
		}
		selectionOptions := implementationselect.Options{
			Start: stage.Root(), ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
			GoCommand: options.GoCommand, Environment: stage.Environment(),
		}
		var planned implementationselect.PlanResult
		if len(dependencies) != 0 {
			state.inputs, err = applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
				Start: stage.Root(), ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
				GoCommand: options.GoCommand, Environment: stage.Environment(),
			})
			if err == nil {
				planned, err = implementationselect.PreviewDependencyConfigurations(ctx, previous, state.inputs, request)
			}
			if err != nil {
				return errors.Join(ErrPreview, err)
			}
		} else if len(remaining) != 0 {
			filtered, err := changerequest.Parse(remaining)
			if err != nil {
				return errors.Join(ErrPreview, err)
			}
			planned, err = previewSelection(ctx, selectionOptions, filtered)
			if err != nil {
				return errors.Join(ErrPreview, err)
			}
		}
		if len(dependencies) != 0 || len(remaining) != 0 {
			state.inputs, state.updated, state.impact, state.forceWrite = planned.Inputs(), planned.Updated(), planned.ConfigurationImpact(), planned.Changed()
			if state.forceWrite {
				if err := atomicfs.WriteFiles(stage.Root(), []atomicfs.Write{{Path: state.inputs.ConfigurationSelection().Path(), Data: state.updated, ExpectedData: planned.Original(), OwnerPrivate: true}}, func(string) error { return stage.Recheck(ctx) }); err != nil {
					return errors.Join(ErrPreview, err)
				}
			}
		} else {
			state.inputs, err = applicationresolve.DiscoverSelectionInputs(ctx, applicationresolve.Options{
				Start: stage.Root(), ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
				GoCommand: options.GoCommand, Environment: stage.Environment(),
			})
			if err != nil {
				return errors.Join(ErrPreview, err)
			}
			state.updated = state.inputs.SelectedSnapshot().Data()
			state.impact, err = configurationimpact.New(configurationimpact.Input{})
			if err != nil {
				return errors.Join(ErrPreview, err)
			}
		}
		if len(dependencies) == 0 || previous.Module().Path() != "" {
			original := state.inputs
			if len(dependencies) != 0 {
				original = previous
			}
			state.rootDigest, state.selectedDigest, _, err = original.ConfigurationLayerDigests(original.SelectedSnapshot().Data())
		} else {
			// Without an old schema no old field is proven public. Hash only the
			// value-free fallback instead of reinterpreting old private data as public.
			state.rootDigest, err = applicationmeta.ConfigurationLayerDigest(state.inputs.RootManifest(), nil)
			if err == nil {
				state.selectedDigest, err = applicationmeta.ConfigurationLayerDigest(state.inputs.SelectedManifest(), nil)
			}
		}
		if err != nil {
			return errors.Join(ErrPreview, err)
		}
		if err := stage.Recheck(ctx); err != nil {
			return errors.Join(ErrPreview, err)
		}
		_, err = applicationgenerate.Generate(ctx, applicationgenerate.Options{
			Start: stage.Root(), ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
			GoCommand: options.GoCommand, Environment: stage.Environment(), MutateModule: mutate, RejectUnexpected: true,
			Validate: func(ctx context.Context, root string) error {
				if err := retainDependencySources(ctx, stage, options.GoCommand); err != nil {
					return err
				}
				started, buildErr := gocommand.RunObserved(ctx, gocommand.Options{Command: options.GoCommand, Directory: root, Environment: stage.Environment()}, "build", "-mod=readonly", "./...")
				if started {
					accounting.Observed = append(accounting.Observed, build)
				} else {
					accounting.Skipped = append(accounting.Skipped, build)
				}
				return buildErr
			},
		})
		return err
	}
	if len(dependencies) == 0 {
		err = modulemutation.Tidy(ctx, stage.Root(), options.GoCommand, stage.Environment(), validateCandidate)
	} else {
		get := effect("candidate-dependencies", commandschema.EffectTrustedCodeExecution, "resolve_dependency_batch", []string{"go", "get"})
		reported := false
		err = modulemutation.ChangeDependencies(ctx, stage.Root(), modulemutation.DependencyBatchOptions{
			GoCommand: options.GoCommand, Environment: stage.Environment(), Changes: dependencies,
			Observe: func(started bool) {
				reported = true
				if started {
					accounting.Observed = append(accounting.Observed, get)
				} else {
					accounting.Skipped = append(accounting.Skipped, get)
				}
			},
		}, validateCandidate)
		if !reported {
			accounting.Skipped = append(accounting.Skipped, get)
		}
	}
	if err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	if err := stage.Recheck(ctx); err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	final, err := capture(ctx, stage.Root())
	if err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	for _, name := range []string{"go.mod", "go.sum", generatedfiles.ManifestPath, generatedfiles.ApplicationManifestPath, runtimebaseline.Path} {
		_, exists := baseline[name]
		state.reads = append(state.reads, commandschema.ChangePlanPathInput{Path: name, Kind: pathKind(name), BeforeExists: exists})
	}
	data, err := os.ReadFile(filepath.Join(stage.Root(), generatedfiles.ApplicationManifestPath))
	if err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	provenance, err := applicationgen.DecodeManifestProvenance(data)
	if err != nil {
		return result, errors.Join(ErrPreview, err)
	}
	state.model = provenance.ApplicationModelDigest()
	state.capture, err = captureCandidate(ctx, stage, request, baseline, final, state.inputs.ConfigurationSelection(), state.forceWrite, state.model)
	if err != nil {
		state.model = ""
		return result, errors.Join(ErrPreview, err)
	}
	if err := stage.Recheck(ctx); err != nil {
		state.model = ""
		return result, errors.Join(ErrPreview, err)
	}
	return result, nil
}

func selectionRequest(request changerequest.Request) ([]byte, error) {
	var document struct {
		Schema     string            `json:"schema"`
		Operations []json.RawMessage `json:"operations"`
	}
	if err := json.Unmarshal(request.CanonicalJSON(), &document); err != nil {
		return nil, err
	}
	kept := document.Operations[:0]
	for _, operation := range document.Operations {
		var identity struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(operation, &identity); err != nil {
			return nil, err
		}
		if identity.Kind != string(changerequest.KindInterfaceCreate) && identity.Kind != string(changerequest.KindImplementationCreate) && identity.Kind != string(changerequest.KindDependency) {
			kept = append(kept, operation)
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}
	document.Operations = kept
	return json.Marshal(document)
}

func previewSelection(ctx context.Context, options implementationselect.Options, request changerequest.Request) (implementationselect.PlanResult, error) {
	operations := request.Operations()
	if len(operations) == 1 {
		operation := operations[0]
		if change, ok := operation.InterfaceRoot(); ok {
			options.Target = change.Interface()
			return implementationselect.PreviewInterfaceRoot(ctx, options, string(change.Root()), change.Action() == changerequest.RootPresent)
		}
		if change, ok := operation.ImplementationSelection(); ok {
			options.Target = change.Interface()
			if change.Action() == changerequest.SelectionRemove {
				return implementationselect.PreviewRemoval(ctx, options)
			}
			options.Constructor, _ = change.Constructor()
			return implementationselect.Preview(ctx, options)
		}
		if change, ok := operation.ResourceSelection(); ok {
			options.Target = change.Instance()
			if change.Action() == changerequest.SelectionRemove {
				return implementationselect.PreviewResourceRemoval(ctx, options)
			}
			options.Constructor, _ = change.Provider()
			return implementationselect.Preview(ctx, options)
		}
	}
	configurationOnly := true
	for _, operation := range operations {
		configurationOnly = configurationOnly && operation.Kind() == changerequest.KindConfiguration
	}
	if configurationOnly {
		return implementationselect.PreviewConfigurations(ctx, options, request)
	}
	return implementationselect.PreviewSelectionConfigurations(ctx, options, request)
}
