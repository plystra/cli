package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/modulepath"
	"golang.org/x/mod/module"
)

const ProjectCreatedSchemaV1 = "plystra.project-created/v1"

// ErrProjectCreated reports an invalid project-created payload.
var ErrProjectCreated = errors.New("build plystra.project-created result")

// ProjectCreatedInput is the construction-only input for one successful
// plystra new payload.
type ProjectCreatedInput struct {
	ModulePath string
	Directory  string
}

// ProjectCreated is one immutable plystra.project-created/v1 payload.
type ProjectCreated struct {
	modulePath    string
	directory     string
	canonicalJSON []byte
	prepared      bool
}

type projectCreatedDocument struct {
	Schema     string `json:"schema"`
	ModulePath string `json:"module_path"`
	Directory  string `json:"directory"`
}

// NewProjectCreated validates and constructs one project-created payload.
func NewProjectCreated(input ProjectCreatedInput) (ProjectCreated, error) {
	if err := modulepath.CheckProject(input.ModulePath); err != nil {
		return ProjectCreated{}, fmt.Errorf("%w: module path: %v", ErrProjectCreated, err)
	}
	if !validProjectDirectory(input.Directory) {
		return ProjectCreated{}, fmt.Errorf("%w: directory must be one lower-case kebab-case child", ErrProjectCreated)
	}
	canonical, err := json.Marshal(projectCreatedDocument{
		Schema:     ProjectCreatedSchemaV1,
		ModulePath: input.ModulePath,
		Directory:  input.Directory,
	})
	if err != nil {
		return ProjectCreated{}, fmt.Errorf("%w: encode: %v", ErrProjectCreated, err)
	}
	return ProjectCreated{modulePath: input.ModulePath, directory: input.Directory, canonicalJSON: canonical, prepared: true}, nil
}

// Valid reports whether NewProjectCreated produced this payload.
func (p ProjectCreated) Valid() bool {
	if !p.prepared {
		return false
	}
	canonical, err := json.Marshal(projectCreatedDocument{
		Schema:     ProjectCreatedSchemaV1,
		ModulePath: p.modulePath,
		Directory:  p.directory,
	})
	return err == nil && modulepath.CheckProject(p.modulePath) == nil && validProjectDirectory(p.directory) && bytes.Equal(canonical, p.canonicalJSON)
}

// Schema returns the immutable payload schema identity.
func (ProjectCreated) Schema() string { return ProjectCreatedSchemaV1 }

// ModulePath returns the created Go Module identity.
func (p ProjectCreated) ModulePath() string { return p.modulePath }

// Directory returns the project-relative created directory.
func (p ProjectCreated) Directory() string { return p.directory }

// CanonicalJSON returns a defensive copy of the payload document.
func (p ProjectCreated) CanonicalJSON() []byte { return append([]byte(nil), p.canonicalJSON...) }

func (ProjectCreated) commandPayload() {}

func validProjectDirectory(value string) bool {
	return len(value) <= 64 && validLowerKebab(value, 64) && module.CheckImportPath(value) == nil
}
