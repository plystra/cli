package moduledependency

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/gocommand"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

type preparationInputs map[string]fileSnapshot

func (inputs preparationInputs) read(path string, optional bool) (fileSnapshot, error) {
	if saved, exists := inputs[path]; exists {
		return saved, nil
	}
	var snapshot fileSnapshot
	var err error
	if optional {
		snapshot, err = optionalGoSum(path)
	} else {
		snapshot, err = readGoMod(path)
	}
	if err == nil {
		inputs[path] = snapshot
	}
	return snapshot, err
}

func (inputs preparationInputs) recheck() error {
	for path, before := range inputs {
		after, err := optionalGoSum(path)
		if err != nil || (before.info != nil || after.info != nil) && !sameSnapshot(before, after) {
			return ErrConcurrentChange
		}
	}
	return nil
}

type preparationMember struct {
	root string
	mod  *modfile.File
	sum  []byte
}

type preparationWorkspace struct {
	path    string
	work    *modfile.WorkFile
	sum     []byte
	members []preparationMember
}

func (workspace *preparationWorkspace) writeDiscovery(root string) (string, error) {
	work, err := modfile.ParseWork("go.work", modfile.Format(workspace.work.Syntax), nil)
	if err != nil {
		return "", err
	}
	for index, use := range work.Use {
		use.Path = filepath.ToSlash(workspace.members[index].root)
		use.Syntax.Token[len(use.Syntax.Token)-1] = modfile.AutoQuote(use.Path)
	}
	for _, replacement := range work.Replace {
		if replacement.New.Version == "" && !filepath.IsAbs(replacement.New.Path) {
			replacement.New.Path = filepath.Join(filepath.Dir(workspace.path), replacement.New.Path)
			for index, token := range replacement.Syntax.Token {
				if token == "=>" {
					replacement.Syntax.Token[index+1] = modfile.AutoQuote(replacement.New.Path)
					break
				}
			}
		}
	}
	path := filepath.Join(root, "go.work")
	if err := os.WriteFile(path, modfile.Format(work.Syntax), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(path+".sum", workspace.sum, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func capturePreparationWorkspace(ctx context.Context, root string, options Options, inputs preparationInputs) (*preparationWorkspace, error) {
	work, explicit := "", false
	for _, entry := range options.Environment {
		if name, value, _ := strings.Cut(entry, "="); strings.EqualFold(name, "GOWORK") {
			work, explicit = value, value != ""
		}
	}
	if !explicit {
		output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: root, Environment: options.Environment, OutputLimit: maximumGoModSize}, "env", "GOWORK")
		if err != nil {
			return nil, err
		}
		work = strings.TrimSpace(string(output))
	}
	if work == "" || work == "off" {
		return nil, nil
	}
	if !filepath.IsAbs(work) {
		return nil, ErrInvalidOutput
	}
	manifest, err := inputs.read(work, false)
	if err != nil {
		return nil, err
	}
	parsed, err := modfile.ParseWork("go.work", manifest.data, nil)
	if err != nil || len(parsed.Use) == 0 || len(parsed.Use) > 1_024 {
		return nil, ErrInvalidGoMod
	}
	sums, err := inputs.read(work+".sum", true)
	if err != nil {
		return nil, err
	}
	workspace := &preparationWorkspace{path: work, work: parsed, sum: sums.data}
	for _, use := range parsed.Use {
		memberRoot := use.Path
		if !filepath.IsAbs(memberRoot) {
			memberRoot = filepath.Join(filepath.Dir(work), memberRoot)
		}
		manifest, err := inputs.read(filepath.Join(memberRoot, "go.mod"), false)
		if err != nil {
			return nil, err
		}
		member, err := modfile.Parse("go.mod", manifest.data, nil)
		if err != nil || member.Module == nil {
			return nil, ErrInvalidGoMod
		}
		sums, err := inputs.read(filepath.Join(memberRoot, "go.sum"), true)
		if err != nil {
			return nil, err
		}
		workspace.members = append(workspace.members, preparationMember{root: memberRoot, mod: member, sum: sums.data})
	}
	return workspace, nil
}

// write constructs metadata-only roots. Selection retains original replacement
// declarations, while downloads receive literal effective identities and only
// the checksum authorities. No original dependency graph or package is loaded.
func (workspace *preparationWorkspace) write(root string, requested []module.Version, selection bool) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	goVersion := "1.18"
	if workspace.work.Go != nil {
		goVersion = workspace.work.Go.Version
	}
	workSource := "go " + goVersion + "\n"
	if selection {
		workSource += preparationReplacements(workspace.work.Replace, filepath.Dir(workspace.path))
	}
	work, err := modfile.ParseWork("go.work", []byte(workSource), nil)
	if err != nil {
		return "", err
	}
	mainPaths := map[string]bool{}
	for _, member := range workspace.members {
		mainPaths[member.mod.Module.Mod.Path] = true
	}
	for index, member := range workspace.members {
		directory := filepath.Join(root, fmt.Sprintf("member-%d", index))
		if err := os.Mkdir(directory, 0o700); err != nil {
			return "", err
		}
		manifestSource := "module " + member.mod.Module.Mod.Path + "\n\ngo 1.18\n"
		if selection {
			manifestSource += preparationReplacements(member.mod.Replace, member.root)
		}
		manifest, err := modfile.Parse("go.mod", []byte(manifestSource), nil)
		if err != nil {
			return "", err
		}
		if selection {
			if index == 0 {
				for _, version := range requested {
					if !mainPaths[version.Path] {
						if err := manifest.AddRequire(version.Path, version.Version); err != nil {
							return "", err
						}
					}
				}
			}
		}
		data, err := manifest.Format()
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(directory, "go.mod"), data, 0o600); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(directory, "go.sum"), member.sum, 0o600); err != nil {
			return "", err
		}
		if err := work.AddUse(filepath.ToSlash(directory), ""); err != nil {
			return "", err
		}
	}
	path := filepath.Join(root, "go.work")
	if err := os.WriteFile(path, modfile.Format(work.Syntax), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(path+".sum", workspace.sum, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func preparationReplacements(replacements []*modfile.Replace, base string) string {
	var output strings.Builder
	for _, replacement := range replacements {
		path := replacement.New.Path
		if replacement.New.Version == "" && !filepath.IsAbs(path) {
			path = filepath.Join(base, path)
		}
		fmt.Fprintf(&output, "replace %s", modfile.AutoQuote(replacement.Old.Path))
		if replacement.Old.Version != "" {
			fmt.Fprintf(&output, " %s", replacement.Old.Version)
		}
		fmt.Fprintf(&output, " => %s", modfile.AutoQuote(path))
		if replacement.New.Version != "" {
			fmt.Fprintf(&output, " %s", replacement.New.Version)
		}
		output.WriteByte('\n')
	}
	return output.String()
}

func (workspace *preparationWorkspace) prepare(ctx context.Context, root string, requested []module.Version, options Options, environment []string, inputs preparationInputs, retain func([]Module) error) error {
	if len(requested) == 0 {
		return nil
	}
	work, err := workspace.write(filepath.Join(root, "selection"), requested, true)
	if err != nil {
		return err
	}
	selectionRoot := filepath.Join(filepath.Dir(work), "member-0")
	environment = environmentWith(environment, "GOWORK", work)
	if err := workspace.retainPreparationMetadata(ctx, selectionRoot, requested, options, environment, inputs); err != nil {
		return err
	}
	var arguments = []string{"list", "-m", "-json"}
	versions := map[string]string{}
	for _, version := range requested {
		arguments = append(arguments, version.Path)
		versions[version.Path] = version.Version
	}
	output, err := gocommand.Output(ctx, gocommand.Options{Command: options.GoCommand, Directory: selectionRoot, Environment: environment, OutputLimit: options.OutputLimit}, arguments...)
	if err != nil {
		return errors.Join(ErrModuleUnavailable, err)
	}
	var sources []Module
	decoder := json.NewDecoder(bytes.NewReader(output))
	for {
		var selected listedModule
		if err := decoder.Decode(&selected); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return ErrInvalidOutput
		}
		version, exists := versions[selected.Path]
		if !exists || selected.Error != nil || !selected.Main && selected.Version != version {
			return ErrInvalidOutput
		}
		delete(versions, selected.Path)
		if selected.Main || selected.Replace != nil && selected.Replace.Version == "" {
			continue
		}
		dependency := Module{path: selected.Path, selectedVersion: version, sourcePath: selected.Path}
		if selected.Replace != nil {
			dependency.replacement = Replacement{path: selected.Replace.Path, version: selected.Replace.Version}
			dependency.sourcePath = selected.Replace.Path
		}
		if effective := sourceVersion(dependency); module.Check(effective.Path, effective.Version) != nil || options.Offline && !workspace.recorded(effective) {
			return ErrSourceIntegrity
		}
		sources = append(sources, dependency)
	}
	if len(versions) != 0 {
		return ErrInvalidOutput
	}
	// Restore the original authorities in a separate workspace so module
	// selection cannot add sums that mask a conflict in a member file.
	work, err = workspace.write(filepath.Join(root, "checksums"), nil, false)
	if err != nil {
		return err
	}
	var memberSums [][]byte
	for _, member := range workspace.members {
		memberSums = append(memberSums, member.sum)
	}
	archives, err := rootChecksums(workspace.sum, memberSums, false)
	if err != nil {
		return err
	}
	manifests, err := rootManifestChecksums(workspace.sum, memberSums)
	if err != nil {
		return err
	}
	return downloadAndRetainSources(ctx, filepath.Join(filepath.Dir(work), "member-0"), sources, preparationChecksums{archives: archives, manifests: manifests}, options, environmentWith(environment, "GOWORK", work), inputs, retain)
}

func (workspace *preparationWorkspace) recorded(version module.Version) bool {
	files := [][]byte{workspace.sum}
	for _, member := range workspace.members {
		files = append(files, member.sum)
	}
	for _, data := range files {
		for _, line := range bytes.Split(data, []byte{'\n'}) {
			fields := strings.Fields(string(line))
			if len(fields) == 3 && fields[0] == version.Path && fields[1] == version.Version && validSourceChecksum(fields[2]) {
				return true // Go still checks conflicts across the separate files.
			}
		}
	}
	return false
}

func (workspace *preparationWorkspace) resolveChecksums(ctx context.Context, modules []Module, options Options) (resultErr error) {
	var sources []Module
	var indices []int
	for index, dependency := range modules {
		version := sourceVersion(dependency)
		if version.Path == "" {
			continue
		}
		if options.Offline && options.RequireSourceChecksums && !workspace.recorded(version) {
			return ErrSourceIntegrity
		}
		indices = append(indices, index)
		sources = append(sources, dependency)
	}
	if len(sources) == 0 {
		return nil
	}
	root, err := os.MkdirTemp("", "plystra-workspace-checksums-")
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	work, err := workspace.write(filepath.Join(root, "checksums"), nil, false)
	if err != nil {
		return err
	}
	if err := downloadPreparedSources(ctx, filepath.Join(filepath.Dir(work), "member-0"), sources, nil, options, environmentWith(preparationEnvironment(options), "GOWORK", work)); err != nil {
		return err
	}
	for index, source := range sources {
		original := modules[indices[index]]
		if !sameDirectory(original.root, source.root) || original.checksum != "" && original.checksum != source.checksum || original.goModChecksum != "" && original.goModChecksum != source.goModChecksum {
			return ErrConcurrentChange
		}
		modules[indices[index]].checksum = source.checksum
		modules[indices[index]].goModChecksum = source.goModChecksum
		modules[indices[index]].goModFile = source.goModFile
	}
	return nil
}
