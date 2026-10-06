package applicationmeta

import (
	"errors"
	"fmt"
	"sort"

	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/resourcename"
	"go.yaml.in/yaml/v3"
)

// DataMember is one explicitly active Data member and its database assignment.
type DataMember struct {
	id                string
	resource          string
	access            string
	source            string
	declarationSource ConfigurationDeclarationSource
}

func (m DataMember) ID() string       { return m.id }
func (m DataMember) Resource() string { return m.resource }
func (m DataMember) Access() string   { return m.access }
func (m DataMember) Source() string   { return m.source }
func (m DataMember) DeclarationSource() ConfigurationDeclarationSource {
	return m.declarationSource
}

type dataMemberRemoval struct {
	id                string
	source            string
	declarationSource ConfigurationDeclarationSource
}

// ErrInvalidDataMember reports a malformed Data member assignment.
var ErrInvalidDataMember = errors.New("invalid Data member configuration")

// DataMemberMetadataError retains the offending source span without exposing
// submitted configuration values in the diagnostic.
type DataMemberMetadataError struct {
	source ConfigurationDeclarationSource
	field  string
	rule   string
}

func (e *DataMemberMetadataError) Error() string {
	return fmt.Sprintf("%s: %s %s", ErrInvalidDataMember, e.field, e.rule)
}
func (*DataMemberMetadataError) Unwrap() []error {
	return []error{ErrInvalidManifest, ErrInvalidDataMember}
}
func (e *DataMemberMetadataError) Source() ConfigurationDeclarationSource { return e.source }
func (e *DataMemberMetadataError) Field() string                          { return e.field }

func dataMemberError(source, path, rule string, node *yaml.Node) error {
	return &DataMemberMetadataError{source: resourceLocation(source, node), field: path, rule: rule}
}

func dataMemberMapping(source, path string, node *yaml.Node) (map[string]*yaml.Node, error) {
	values, err := safeConstructorConfigMapping(node)
	if err != nil {
		return nil, dataMemberError(source, path, "must be a mapping with unique string keys", node)
	}
	return values, nil
}

func (m Manifest) DataMembers() []DataMember {
	return append([]DataMember(nil), m.dataMembers...)
}

func dataMemberPath(id string) string { return fmt.Sprintf("data.members[%q]", id) }

func parseDataMembers(manifest *Manifest, node *yaml.Node, allowRemoval bool) error {
	if node == nil {
		return nil
	}
	fields, err := dataMemberMapping(manifest.source, "data", node)
	if err != nil {
		return err
	}
	for _, field := range sortedNodeKeys(fields) {
		if field != "members" {
			return dataMemberError(manifest.source, "data", "contains an unknown field", resourceKeyNode(node, field))
		}
	}
	if fields["members"] == nil {
		return nil
	}
	members, err := dataMemberMapping(manifest.source, "data.members", fields["members"])
	if err != nil {
		return err
	}
	for _, id := range sortedNodeKeys(members) {
		key := resourceKeyNode(fields["members"], id)
		if _, err := interfaceid.Parse(id); err != nil {
			return dataMemberError(manifest.source, "data.members", "contains an invalid member ID", key)
		}
		path := dataMemberPath(id)
		member := DataMember{id: id, source: "plystra.yaml " + path, declarationSource: resourceLocation(manifest.source, key)}
		if isRemovalMapping(members[id]) {
			if !allowRemoval {
				return dataMemberError(manifest.source, path, "removal markers are valid only in environment overlays", key)
			}
			manifest.removedDataMembers = append(manifest.removedDataMembers, dataMemberRemoval{id: id, source: member.source, declarationSource: member.declarationSource})
			continue
		}
		entry, err := dataMemberMapping(manifest.source, path, members[id])
		if err != nil {
			return err
		}
		for _, field := range sortedNodeKeys(entry) {
			if field != "resource" && field != "access" {
				return dataMemberError(manifest.source, path, "contains an unknown field", resourceKeyNode(members[id], field))
			}
		}
		resource, err := strictString(entry["resource"])
		if err != nil || resourcename.Check(resource) != nil {
			location := entry["resource"]
			if location == nil {
				location = key
			}
			return dataMemberError(manifest.source, path+".resource", "must name one Resource instance", location)
		}
		member.resource = resource
		if access := entry["access"]; access != nil {
			value, err := strictString(access)
			if err != nil || resourcename.Check(value) != nil {
				return dataMemberError(manifest.source, path+".access", "must name one access instance", access)
			}
			member.access = value
		}
		manifest.dataMembers = append(manifest.dataMembers, member)
	}
	return nil
}

func overlayDataMembers(base, overlay Manifest) ([]DataMember, []dataMemberRemoval) {
	values := make(map[string]DataMember)
	removals := make(map[string]dataMemberRemoval)
	for _, member := range base.dataMembers {
		values[member.id] = member
	}
	for _, removal := range base.removedDataMembers {
		removals[removal.id] = removal
	}
	for _, member := range overlay.dataMembers {
		values[member.id] = member
		delete(removals, member.id)
	}
	for _, removal := range overlay.removedDataMembers {
		delete(values, removal.id)
		removals[removal.id] = removal
	}
	members := make([]DataMember, 0, len(values))
	for _, member := range values {
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].id < members[j].id })
	removed := make([]dataMemberRemoval, 0, len(removals))
	for _, removal := range removals {
		removed = append(removed, removal)
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].id < removed[j].id })
	return members, removed
}

func dataMemberConfigurationDecisions(manifest Manifest) []ConfigurationDecision {
	result := make([]ConfigurationDecision, 0, len(manifest.dataMembers)+len(manifest.removedDataMembers))
	for _, member := range manifest.dataMembers {
		result = append(result, ConfigurationDecision{
			path: dataMemberPath(member.id), digest: digestStrings("data.member/v1", member.id, member.resource, member.access),
			summary: ConfigurationSummaryObject, source: member.source, resolutionRelevant: true,
		})
	}
	for _, removal := range manifest.removedDataMembers {
		result = append(result, ConfigurationDecision{
			path: dataMemberPath(removal.id), digest: digestStrings("data.member/v1", removal.id, "removed"),
			summary: ConfigurationSummaryRemoval, removed: true, source: removal.source, resolutionRelevant: true,
		})
	}
	return result
}
