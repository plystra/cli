package applicationresolve

import (
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/applicationmeta"
	"github.com/plystra/cli/internal/constructorgraph"
	"github.com/plystra/cli/internal/constructorsymbol"
	"github.com/plystra/cli/internal/interfaceid"
	"github.com/plystra/cli/internal/interfaceinventory"
)

const (
	dataDatabaseResourceID = "data.database/v1"
	dataDatabasePackage    = "github.com/plystra/data/database"
	dataPostgresProvider   = "github.com/plystra/data/postgres.New"
	dataPostgresPackage    = "github.com/plystra/data/postgres"
	dataModulePath         = "github.com/plystra/data"
	dataPostgresBackend    = "postgres/v1"
	dataGeneratedParameter = "database"
)

// buildDataGeneratedResources converts accepted access identities into graph
// inputs. It does not select or validate a backend; that fact belongs to the
// selected Resource graph after ordinary Resource resolution.
func buildDataGeneratedResources(modulePath string, manifest applicationmeta.Manifest, activation DataActivation) ([]constructorgraph.GeneratedResourceInput, error) {
	if !activation.Valid() {
		return nil, fmt.Errorf("accepted Data activation is incomplete")
	}
	members := make(map[string]applicationmeta.DataMember, len(manifest.DataMembers()))
	for _, member := range manifest.DataMembers() {
		members[member.ID()] = member
	}
	databaseID, err := interfaceid.Parse(dataDatabaseResourceID)
	if err != nil {
		return nil, fmt.Errorf("parse official Data Resource ID: %w", err)
	}
	result := make([]constructorgraph.GeneratedResourceInput, 0, len(activation.Assignments()))
	for _, assignment := range activation.Assignments() {
		if assignment.Access() == "" {
			continue
		}
		member, exists := members[assignment.MemberID()]
		if !exists {
			return nil, fmt.Errorf("Data member %q has no effective configuration source", assignment.MemberID())
		}
		accessID, err := interfaceid.Parse(assignment.AccessID())
		if err != nil {
			return nil, fmt.Errorf("Data member %q has invalid access Resource ID: %w", assignment.MemberID(), err)
		}
		packagePath := strings.TrimSuffix(modulePath, "/") + "/generated/data/" + strings.ReplaceAll(assignment.Access(), ".", "/")
		constructor, err := constructorsymbol.New(packagePath, "New")
		if err != nil {
			return nil, fmt.Errorf("Data member %q generated provider identity: %w", assignment.MemberID(), err)
		}
		source := member.DeclarationSource()
		result = append(result, constructorgraph.GeneratedResourceInput{
			MemberID: assignment.MemberID(), Name: assignment.Access(), ResourceID: accessID,
			PackagePath: assignment.AccessPackage(), TypeName: assignment.AccessType(), Constructor: constructor,
			DatabaseResourceID: databaseID, DatabasePackagePath: dataDatabasePackage,
			DatabaseParameter: dataGeneratedParameter, DatabaseParameterPosition: 1,
			Sources: []constructorgraph.ResourceSource{{Reference: member.Source(), ModulePath: source.ModulePath(), Path: source.Path(), Line: source.Line(), Column: source.Column()}},
		})
	}
	return result, nil
}

// validateDataBackend proves the exact initial Data database provider tuple against the
// already resolved ordinary Resource graph. It deliberately remains outside
// the generic Resource inventory and constructor graph.
func validateDataBackend(graph constructorgraph.Graph, resources interfaceinventory.ResourceIndex, activation DataActivation) error {
	contracts := make(map[string]interfaceinventory.Resource)
	for _, contract := range resources.Resources() {
		contracts[contract.ID()] = contract
	}
	for _, assignment := range activation.Assignments() {
		var selected constructorgraph.ResourceNode
		found := false
		for _, node := range graph.ResourceConstructionOrder() {
			if node.Name() == assignment.Resource() {
				selected, found = node, true
				break
			}
		}
		if !found {
			return fmt.Errorf("Data member %q selected Resource %q is not in the resolved Resource graph", assignment.MemberID(), assignment.Resource())
		}
		if selected.ResourceID().String() != dataDatabaseResourceID {
			return fmt.Errorf("Data member %q requires Resource %q, got %q", assignment.MemberID(), dataDatabaseResourceID, selected.ResourceID())
		}
		contract, exists := contracts[selected.ResourceID().String()]
		if !exists || contract.PackagePath() != dataDatabasePackage {
			return fmt.Errorf("Data member %q selected Resource contract is not %s", assignment.MemberID(), dataDatabasePackage)
		}
		provider := selected.Provider()
		if provider.ID() != dataDatabaseResourceID || provider.Symbol().String() != dataPostgresProvider || provider.PackagePath() != dataPostgresPackage || provider.ModulePath() != dataModulePath {
			return fmt.Errorf("Data member %q selected Resource %q is not the official PostgreSQL provider", assignment.MemberID(), assignment.Resource())
		}
	}
	return nil
}
