package applicationresolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/datacompiler"
)

// AnalyzeData builds the finite source and Resource-contract snapshot for the
// current selection and accepts only a successful analyze protocol response.
// Core validates active-root assignment separately; complete typed-result
// acceptance and generated installation remain later boundaries.
func (s SelectionInputs) AnalyzeData(ctx context.Context, options Options) (datacompiler.AnalyzeResponse, error) {
	if ctx == nil {
		return datacompiler.AnalyzeResponse{}, fmt.Errorf("%w: context is nil", ErrDataCompilerAnalysisUnavailable)
	}
	selection, err := s.DataCompilerSelection()
	if err != nil {
		return datacompiler.AnalyzeResponse{}, fmt.Errorf("%w: select compiler: %w", ErrDataCompilerAnalysisUnavailable, err)
	}
	artifact, err := s.AcquireDataCompiler(ctx, options)
	if err != nil {
		return datacompiler.AnalyzeResponse{}, fmt.Errorf("%w: acquire compiler: %w", ErrDataCompilerAnalysisUnavailable, err)
	}
	modules := dataSnapshotModules(s, selection)
	snapshot, err := datacompiler.BuildAnalyzeSourceSnapshot(ctx, datacompiler.AnalyzeSourceSnapshotOptions{
		DataPackages:     s.declarations.DataPackages(),
		ResourcePackages: s.declarations.Resources().Resources(),
		Modules:          modules,
		GoDirectory:      s.module.Path(),
		Compiler:         selection,
		GoCommand:        options.GoCommand,
		Environment:      options.Environment,
	})
	if err != nil {
		return datacompiler.AnalyzeResponse{}, fmt.Errorf("%w: build source snapshot: %w", ErrDataCompilerAnalysisUnavailable, err)
	}
	snapshotJSON, err := snapshot.JSON()
	if err != nil {
		return datacompiler.AnalyzeResponse{}, fmt.Errorf("%w: encode source snapshot: %w", ErrDataCompilerAnalysisUnavailable, err)
	}
	request, err := datacompiler.BuildAnalyzeRequest(artifact, selection.Manifest, datacompiler.AnalyzeRequestOptions{
		RequestID: "data-analyze-" + strings.TrimPrefix(snapshot.Digest(), "sha256:"),
		Build: datacompiler.AnalyzeBuildContext{
			GOOS: artifact.GOOS, GOARCH: artifact.GOARCH,
			BuildTags: analyzeBuildTags(options.Environment),
		},
		Snapshot: snapshotJSON,
	})
	if err != nil {
		return datacompiler.AnalyzeResponse{}, fmt.Errorf("%w: build request: %w", ErrDataCompilerAnalysisUnavailable, err)
	}
	analyzeOptions := datacompiler.AnalyzeOptions{Environment: options.Environment, Timeout: options.ExecutionTimeout}
	response, err := datacompiler.Analyze(ctx, artifact, request, analyzeOptions)
	if err != nil {
		return datacompiler.AnalyzeResponse{}, fmt.Errorf("%w: invoke compiler: %w", ErrDataCompilerAnalysisUnavailable, err)
	}
	if response.Status != "succeeded" {
		return datacompiler.AnalyzeResponse{}, fmt.Errorf("%w: compiler rejected the Data source snapshot", ErrDataCompilerAnalysisUnavailable)
	}
	return response, nil
}

func dataSnapshotModules(inputs SelectionInputs, selection datacompiler.Selection) []datacompiler.SnapshotModule {
	result := make([]datacompiler.SnapshotModule, 0, len(inputs.dependencies.Modules())+2)
	seen := make(map[string]struct{})
	add := func(modulePath, version, root string) {
		if modulePath == "" || root == "" {
			return
		}
		key := modulePath + "\x00" + version
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, datacompiler.SnapshotModule{ModulePath: modulePath, ModuleVersion: version, Root: root})
	}
	add(inputs.module.ModulePath(), "", inputs.module.Path())
	for _, dependency := range inputs.dependencies.Modules() {
		add(dependency.Path(), dependency.SelectedVersion(), dependency.Root())
	}
	add(selection.ModulePath, selection.ModuleVersion, selection.Root)
	sort.Slice(result, func(left, right int) bool {
		if result[left].ModulePath != result[right].ModulePath {
			return result[left].ModulePath < result[right].ModulePath
		}
		return result[left].ModuleVersion < result[right].ModuleVersion
	})
	return result
}

func analyzeBuildTags(environment []string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	flags := ""
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, "GOFLAGS") {
			flags = value
		}
	}
	fields := strings.Fields(flags)
	tags := make([]string, 0)
	for index := 0; index < len(fields); index++ {
		value := ""
		if strings.HasPrefix(fields[index], "-tags=") {
			value = strings.TrimPrefix(fields[index], "-tags=")
		} else if fields[index] == "-tags" && index+1 < len(fields) {
			index++
			value = fields[index]
		}
		for _, tag := range strings.Split(value, ",") {
			if tag != "" {
				tags = append(tags, tag)
			}
		}
	}
	sort.Strings(tags)
	return tags
}

// ValidateDataRoots checks the part of the analyze result that belongs to the
// Core activation boundary. The independent compiler owns model typing; Core
// owns whether every returned root has an explicit active assignment.
func ValidateDataRoots(response datacompiler.AnalyzeResponse, members []string) error {
	if response.Status != "succeeded" {
		return errors.New("Data analyze result is not successful")
	}
	var output struct {
		Roots []struct {
			MemberID      string `json:"member_id"`
			AccessPackage string `json:"access_package,omitempty"`
			AccessType    string `json:"access_type,omitempty"`
			AccessID      string `json:"access_id,omitempty"`
		} `json:"roots"`
	}
	if err := json.Unmarshal(response.Output, &output); err != nil {
		return fmt.Errorf("decode accepted Data roots: %w", err)
	}
	active := make(map[string]struct{}, len(members))
	for _, member := range members {
		active[member] = struct{}{}
	}
	seen := make(map[string]struct{}, len(output.Roots))
	for _, root := range output.Roots {
		if _, ok := active[root.MemberID]; !ok {
			return fmt.Errorf("analyze returned unassigned Data member %q", root.MemberID)
		}
		if _, ok := seen[root.MemberID]; ok {
			return fmt.Errorf("analyze returned duplicate Data member %q", root.MemberID)
		}
		seen[root.MemberID] = struct{}{}
		if (root.AccessPackage == "") != (root.AccessType == "") || (root.AccessPackage == "") != (root.AccessID == "") {
			return fmt.Errorf("analyze returned incomplete access Resource identity for %q", root.MemberID)
		}
	}
	if len(seen) != len(active) {
		return fmt.Errorf("analyze returned %d Data roots for %d active members", len(seen), len(active))
	}
	return nil
}
