package scaffoldplan

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/changerequest"
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/projectstage"
)

// ErrPrivateCapture rejects implicit serialization of captured private evidence.
var ErrPrivateCapture = errors.New("private candidate capture cannot be serialized")

// Capture retains the normalized private request, original input evidence, and
// exact candidate write images after temporary-tree cleanup. It does not prove
// complete semantic inputs, grant installation authority, or make a plan exact.
type Capture struct {
	// Invalid-verb diagnostics can dereference one struct pointer when fmt skips
	// methods on an unexported wrapper field. Two levels keep contents opaque.
	state **captureState
}

type captureState struct {
	request   []byte
	inputs    projectstage.Inputs
	selection applicationresolve.ConfigurationSelection
	writes    []CapturedWrite
	model     string
}

// CapturedWrite owns one original preimage and its candidate postimage. Paths
// are Project-relative; content, modes, and content equality remain private.
type CapturedWrite struct {
	state **capturedWrite
}

type capturedWrite struct {
	path        string
	before      projectstage.FileImage
	after       []byte
	afterExists bool
	afterMode   os.FileMode
}

func (Capture) String() string                         { return "<private-candidate-capture>" }
func (c Capture) GoString() string                     { return c.String() }
func (c Capture) Format(state fmt.State, _ rune)       { _, _ = state.Write([]byte(c.String())) }
func (c Capture) LogValue() slog.Value                 { return slog.StringValue(c.String()) }
func (Capture) MarshalJSON() ([]byte, error)           { return nil, ErrPrivateCapture }
func (Capture) MarshalText() ([]byte, error)           { return nil, ErrPrivateCapture }
func (Capture) MarshalYAML() (any, error)              { return nil, ErrPrivateCapture }
func (CapturedWrite) String() string                   { return "<private-candidate-write>" }
func (w CapturedWrite) GoString() string               { return w.String() }
func (w CapturedWrite) Format(state fmt.State, _ rune) { _, _ = state.Write([]byte(w.String())) }
func (w CapturedWrite) LogValue() slog.Value           { return slog.StringValue(w.String()) }
func (CapturedWrite) MarshalJSON() ([]byte, error)     { return nil, ErrPrivateCapture }
func (CapturedWrite) MarshalText() ([]byte, error)     { return nil, ErrPrivateCapture }
func (CapturedWrite) MarshalYAML() (any, error)        { return nil, ErrPrivateCapture }

func (c Capture) captured() *captureState {
	if c.state == nil {
		return nil
	}
	return *c.state
}

func (w CapturedWrite) captured() *capturedWrite {
	if w.state == nil {
		return nil
	}
	return *w.state
}

func (c Capture) Valid() bool {
	state := c.captured()
	return state != nil && len(state.request) != 0 && state.model != ""
}

// CanonicalRequest returns private requested intent, including configured values
// and Secret-reference targets. It must never become a public plan projection.
func (c Capture) CanonicalRequest() []byte {
	state := c.captured()
	if state == nil {
		return nil
	}
	return bytes.Clone(state.request)
}

// MatchesRequest compares the complete private request, not the deliberately
// value-free public request digest. Equal public plans cannot select a capture.
func (c Capture) MatchesRequest(request changerequest.Request) bool {
	return c.Valid() && request.Valid() && bytes.Equal(c.captured().request, request.CanonicalJSON())
}

func (c Capture) Writes() []CapturedWrite {
	state := c.captured()
	if state == nil {
		return nil
	}
	return append([]CapturedWrite(nil), state.writes...)
}
func (c Capture) ConfigurationSelection() applicationresolve.ConfigurationSelection {
	state := c.captured()
	if state == nil {
		return applicationresolve.ConfigurationSelection{}
	}
	return state.selection
}

// Recheck remains usable after Preview has removed its private working tree.
// It checks original staging inputs, not a mutation lock or installation guard.
func (c Capture) Recheck(ctx context.Context) error {
	if !c.Valid() {
		return ErrPreview
	}
	return c.captured().inputs.Recheck(ctx)
}

func (w CapturedWrite) Path() string {
	state := w.captured()
	if state == nil {
		return ""
	}
	return state.path
}
func (w CapturedWrite) Before() projectstage.FileImage {
	state := w.captured()
	if state == nil {
		return projectstage.FileImage{}
	}
	return state.before
}
func (w CapturedWrite) AfterExists() bool {
	state := w.captured()
	return state != nil && state.afterExists
}
func (w CapturedWrite) After() []byte {
	state := w.captured()
	if state == nil {
		return nil
	}
	return bytes.Clone(state.after)
}
func (w CapturedWrite) AfterMode() os.FileMode {
	state := w.captured()
	if state == nil {
		return 0
	}
	return state.afterMode
}

func (c Capture) writeSet() []commandschema.ChangePlanPathInput {
	writes := c.Writes()
	result := make([]commandschema.ChangePlanPathInput, len(writes))
	for index, write := range writes {
		result[index] = commandschema.ChangePlanPathInput{
			Path: write.Path(), Kind: pathKind(write.Path()), BeforeExists: write.Before().Exists(), AfterExists: write.AfterExists(),
		}
	}
	return result
}

func captureCandidate(ctx context.Context, stage *projectstage.Stage, request changerequest.Request, before, after map[string]entry, selection applicationresolve.ConfigurationSelection, forceConfiguration bool, model string) (Capture, error) {
	if err := stage.CheckSources(ctx); err != nil {
		return Capture{}, errors.Join(ErrPreview, err)
	}
	state := &captureState{request: request.CanonicalJSON(), inputs: stage.Inputs(), selection: selection, model: model}
	result := Capture{state: &state}
	root, err := os.OpenRoot(stage.Root())
	if err != nil {
		return Capture{}, ErrPreview
	}
	defer root.Close()
	for _, path := range differences(before, after, selection.Path(), forceConfiguration) {
		original, err := state.inputs.ProjectFile(ctx, path.Path)
		if err != nil || original.Exists() != path.BeforeExists {
			return Capture{}, errors.Join(ErrPreview, err)
		}
		stored := &capturedWrite{path: path.Path, before: original, afterExists: path.AfterExists}
		write := CapturedWrite{state: &stored}
		if current, exists := after[path.Path]; exists {
			stored.after, err = readCapturedFile(ctx, root, path.Path, current)
			if err == nil {
				stored.after, err = stage.OriginalProjectBytes(path.Path, stored.after)
			}
			if err != nil {
				return Capture{}, errors.Join(ErrPreview, err)
			}
			stored.afterMode = current.info.Mode()
		}
		state.writes = append(state.writes, write)
	}
	return result, nil
}
