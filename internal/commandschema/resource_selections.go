package commandschema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/modulepath"
	"github.com/plystra/cli/internal/resourcename"
)

const ResourceSelectionsSchemaV1 = "plystra.resource-selections/v1"

var ErrResourceSelections = errors.New("build Plystra Resource selections")

// ResourceSelectionInput is a configured instance, not a resolved graph node.
type ResourceSelectionInput struct {
	Name            string
	Provider        string
	SourcePath      string
	SourceLine      int
	SourceColumn    int
	ProviderVisible bool
	ResourceID      string
}

type ResourceSelections struct {
	modulePath    string
	instances     []ResourceSelectionInput
	canonicalJSON []byte
	prepared      bool
}

type resourceSelectionsDocument struct {
	Schema     string                      `json:"schema"`
	ModulePath string                      `json:"module_path"`
	Scope      string                      `json:"scope"`
	Instances  []resourceSelectionDocument `json:"instances"`
}

type resourceSelectionDocument struct {
	Name            string `json:"name"`
	Provider        string `json:"provider"`
	SourcePath      string `json:"source_path"`
	SourceLine      int    `json:"source_line"`
	SourceColumn    int    `json:"source_column"`
	ProviderVisible bool   `json:"provider_visible"`
	ResourceID      string `json:"resource_id,omitempty"`
}

func NewResourceSelections(modulePath string, instances []ResourceSelectionInput) (ResourceSelections, error) {
	if err := modulepath.CheckProject(modulePath); err != nil {
		return ResourceSelections{}, fmt.Errorf("%w: invalid Project module", ErrResourceSelections)
	}
	if len(instances) > 4096 {
		return ResourceSelections{}, fmt.Errorf("%w: too many instances", ErrResourceSelections)
	}
	for index, instance := range instances {
		if resourcename.Check(instance.Name) != nil || !validRelativePath(instance.SourcePath, false) || instance.SourceLine < 1 || instance.SourceColumn < 1 {
			return ResourceSelections{}, fmt.Errorf("%w: invalid instance at %d", ErrResourceSelections, index)
		}
		if _, err := constructorsymbol.Parse(instance.Provider); err != nil {
			return ResourceSelections{}, fmt.Errorf("%w: invalid provider at %d", ErrResourceSelections, index)
		}
		if instance.ProviderVisible {
			if _, err := interfaceid.Parse(instance.ResourceID); err != nil {
				return ResourceSelections{}, fmt.Errorf("%w: invalid Resource ID at %d", ErrResourceSelections, index)
			}
		} else if instance.ResourceID != "" {
			return ResourceSelections{}, fmt.Errorf("%w: invisible provider has Resource ID", ErrResourceSelections)
		}
		if index > 0 && instances[index-1].Name >= instance.Name {
			return ResourceSelections{}, fmt.Errorf("%w: instances are not uniquely name-sorted", ErrResourceSelections)
		}
	}
	copyInstances := append([]ResourceSelectionInput(nil), instances...)
	result := ResourceSelections{modulePath: modulePath, instances: copyInstances, prepared: true}
	document := resourceSelectionsDocument{Schema: ResourceSelectionsSchemaV1, ModulePath: modulePath, Scope: "configured_only", Instances: make([]resourceSelectionDocument, len(copyInstances))}
	for index, instance := range copyInstances {
		document.Instances[index] = resourceSelectionDocument(instance)
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return ResourceSelections{}, fmt.Errorf("%w: encode: %v", ErrResourceSelections, err)
	}
	result.canonicalJSON = canonical
	return result, nil
}

func (p ResourceSelections) Valid() bool {
	if !p.prepared {
		return false
	}
	rebuilt, err := NewResourceSelections(p.modulePath, p.instances)
	return err == nil && bytes.Equal(rebuilt.canonicalJSON, p.canonicalJSON)
}

func (ResourceSelections) Schema() string { return ResourceSelectionsSchemaV1 }
func (p ResourceSelections) Instances() []ResourceSelectionInput {
	return append([]ResourceSelectionInput(nil), p.instances...)
}
func (p ResourceSelections) CanonicalJSON() []byte { return append([]byte(nil), p.canonicalJSON...) }
func (ResourceSelections) commandPayload()         {}
