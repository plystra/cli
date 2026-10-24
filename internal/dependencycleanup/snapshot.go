package dependencycleanup

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/projectlocate"
)

// Snapshot binds old ownership discovery to the explicit dependency command.
type Snapshot struct {
	inputs   applicationresolve.SelectionInputs
	evidence applicationresolve.DependencyOwnershipSnapshot
	options  Options
	change   *moduleChange
}

func (Snapshot) String() string                   { return "<private-dependency-snapshot>" }
func (s Snapshot) GoString() string               { return s.String() }
func (s Snapshot) Format(state fmt.State, _ rune) { _, _ = io.WriteString(state, s.String()) }
func (s Snapshot) LogValue() slog.Value           { return slog.StringValue(s.String()) }

type moduleChange struct {
	observed bool
	module   modulePostimage
	err      error
}
type modulePostimage struct {
	info   fs.FileInfo
	digest [sha256.Size]byte
}

// Capture retains original selection, ownership inputs and environment before
// mutation. Unloadable old declarations provide no declaration ownership proof.
func Capture(ctx context.Context, options Options) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, errors.New("dependency capture context is nil")
	}
	if options.Environment == nil {
		options.Environment = os.Environ()
	}
	options.Environment = append([]string(nil), options.Environment...)
	project, err := projectlocate.Find(options.Start)
	if err != nil {
		return Snapshot{}, err
	}
	options.Start = project.Path()
	evidence, err := applicationresolve.CaptureDependencyOwnership(ctx, applicationresolve.Options{
		Start: options.Start, ConfigurationPath: options.ConfigurationPath, EnvironmentName: options.EnvironmentName,
		GoCommand: options.GoCommand, Environment: options.Environment, DependencyOutputLimit: options.DependencyOutputLimit,
	})
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{inputs: evidence.Previous(), evidence: evidence, options: options, change: &moduleChange{}}, nil
}

// Validate checks the complete old snapshot immediately before go get.
func (s Snapshot) Validate(ctx context.Context) error { return s.evidence.ValidateSnapshot(ctx) }

func (s Snapshot) validate(ctx context.Context, selected []byte) error {
	return s.evidence.ValidateAuthoredSnapshot(ctx, selected)
}

func (s Snapshot) validateDependencies(current applicationresolve.SelectionInputs) error {
	return s.evidence.ValidateModuleSources(current)
}

// ObserveChange is supplied to modulemutation.ChangeOptions.Observe. It pins
// the command's exact go.mod postimage before planning subprocesses run, without
// accepting any new authored YAML or declaration preimages.
func (s Snapshot) ObserveChange(started bool) {
	if s.change == nil {
		return
	}
	if s.change.observed || !started {
		s.change.err = applicationresolve.ErrConcurrentChange
		return
	}
	s.change.observed = true
	s.change.module, s.change.err = readModulePostimage(s.options.Start)
}

func (s Snapshot) validateChange() error {
	if s.change == nil || !s.change.observed || s.change.err != nil {
		return fmt.Errorf("%w: dependency command postimage is unavailable", applicationresolve.ErrConcurrentChange)
	}
	current, err := readModulePostimage(s.options.Start)
	before := s.change.module
	if err != nil || before.info.Mode() != current.info.Mode() || !os.SameFile(before.info, current.info) || before.digest != current.digest {
		return fmt.Errorf("%w: dependency command postimage changed", applicationresolve.ErrConcurrentChange)
	}
	return nil
}

func readModulePostimage(directory string) (modulePostimage, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return modulePostimage{}, err
	}
	defer root.Close()
	info, err := root.Lstat("go.mod")
	if err != nil || !info.Mode().IsRegular() {
		return modulePostimage{}, applicationresolve.ErrConcurrentChange
	}
	file, err := root.Open("go.mod")
	if err != nil {
		return modulePostimage{}, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return modulePostimage{}, applicationresolve.ErrConcurrentChange
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return modulePostimage{}, err
	}
	after, err := root.Lstat("go.mod")
	if err != nil || !os.SameFile(info, after) || info.Mode() != after.Mode() || info.Size() != after.Size() || !info.ModTime().Equal(after.ModTime()) {
		return modulePostimage{}, applicationresolve.ErrConcurrentChange
	}
	result := modulePostimage{info: info}
	copy(result.digest[:], hash.Sum(nil))
	return result, nil
}
