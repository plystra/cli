// Package projectsmoke builds and runs the generated application lifecycle smoke path.
package projectsmoke

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/plystra/cli/internal/gocommand"
)

const (
	temporaryDirectory  = ".plystra-smoke"
	defaultBuildTimeout = 5 * time.Minute
	defaultSmokeTimeout = 30 * time.Second
	applicationPackage  = "./generated/go/application"
)

var (
	// ErrRun reports a generated Project lifecycle smoke failure.
	ErrRun = errors.New("smoke test generated Project")
	// ErrInvalidOptions reports an invalid Project root or timeout.
	ErrInvalidOptions = errors.New("invalid Project smoke options")
	// ErrTemporaryOutput reports a conflicting or unremovable smoke output path.
	ErrTemporaryOutput = errors.New("manage temporary Project smoke output")
	// ErrBuild reports failure to build the generated application executable.
	ErrBuild = errors.New("build generated application smoke executable")
	// ErrSmoke reports failure of the generated application's internal lifecycle probe.
	ErrSmoke = errors.New("run generated application lifecycle smoke")
)

// Options controls one bounded generated Project lifecycle smoke run.
type Options struct {
	Root         string
	GoCommand    string
	Environment  []string
	BuildTimeout time.Duration
	SmokeTimeout time.Duration
}

// Run builds the generated application with ordinary read-only Go Module
// resolution, runs its private --smoke lifecycle path, suppresses all child
// output, and removes its temporary executable on every return path.
func Run(ctx context.Context, options Options) (result error) {
	if ctx == nil {
		return fmt.Errorf("%w: %w: context is required", ErrRun, ErrInvalidOptions)
	}
	root, buildTimeout, smokeTimeout, err := validate(options)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRun, err)
	}

	temporaryRoot := filepath.Join(root, temporaryDirectory)
	if err := os.Mkdir(temporaryRoot, 0o700); err != nil {
		return fmt.Errorf("%w: %w: create isolated output directory", ErrRun, ErrTemporaryOutput)
	}
	defer func() {
		if err := os.RemoveAll(temporaryRoot); err != nil {
			cleanupError := fmt.Errorf("%w: %w: remove isolated output directory", ErrRun, ErrTemporaryOutput)
			if result == nil {
				result = cleanupError
			} else {
				result = errors.Join(result, cleanupError)
			}
		}
	}()

	binaryName := "application"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryRelative := filepath.Join(temporaryDirectory, binaryName)
	binaryPath := filepath.Join(root, binaryRelative)
	environment := withGOWORKOff(options.Environment)

	buildContext, cancelBuild := context.WithTimeout(ctx, buildTimeout)
	err = gocommand.Run(buildContext, gocommand.Options{
		Command:     options.GoCommand,
		Directory:   root,
		Environment: environment,
	}, "build", "-mod=readonly", "-o", binaryRelative, applicationPackage)
	cancelBuild()
	if err != nil {
		return fmt.Errorf("%w: %w: %w", ErrRun, ErrBuild, err)
	}

	smokeContext, cancelSmoke := context.WithTimeout(ctx, smokeTimeout)
	process := exec.CommandContext(smokeContext, binaryPath, "--smoke")
	process.Dir = root
	process.Env = append([]string(nil), environment...)
	process.Stdin = nil
	process.Stdout = io.Discard
	process.Stderr = io.Discard
	err = process.Run()
	contextError := smokeContext.Err()
	cancelSmoke()
	if contextError != nil {
		return fmt.Errorf("%w: %w: %w", ErrRun, ErrSmoke, contextError)
	}
	if err != nil {
		return fmt.Errorf("%w: %w: application process exited unsuccessfully", ErrRun, ErrSmoke)
	}
	return nil
}

func validate(options Options) (string, time.Duration, time.Duration, error) {
	if strings.TrimSpace(options.Root) == "" {
		return "", 0, 0, fmt.Errorf("%w: Project root is empty", ErrInvalidOptions)
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return "", 0, 0, fmt.Errorf("%w: resolve Project root", ErrInvalidOptions)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", 0, 0, fmt.Errorf("%w: Project root is not a directory", ErrInvalidOptions)
	}
	marker, err := os.Stat(filepath.Join(root, "plystra.yaml"))
	if err != nil || !marker.Mode().IsRegular() {
		return "", 0, 0, fmt.Errorf("%w: Project root has no regular plystra.yaml", ErrInvalidOptions)
	}
	buildTimeout := options.BuildTimeout
	if buildTimeout == 0 {
		buildTimeout = defaultBuildTimeout
	}
	smokeTimeout := options.SmokeTimeout
	if smokeTimeout == 0 {
		smokeTimeout = defaultSmokeTimeout
	}
	if buildTimeout < 0 || smokeTimeout < 0 {
		return "", 0, 0, fmt.Errorf("%w: build and smoke timeouts must be positive", ErrInvalidOptions)
	}
	return root, buildTimeout, smokeTimeout, nil
}

func withGOWORKOff(environment []string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	result := make([]string, 0, len(environment)+1)
	found := false
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(key, "GOWORK") {
			if !found {
				result = append(result, "GOWORK=off")
				found = true
			}
			continue
		}
		result = append(result, entry)
	}
	if !found {
		result = append(result, "GOWORK=off")
	}
	return result
}
