package applicationresolve

import (
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestValidateDataAssignmentsRejectsAccessCollisionsAndMismatch(t *testing.T) {
	acceptance := DataAnalysisAcceptance{members: map[string]dataAnalysisMember{
		"example.records/v1": {
			accessPackage: "example.com/resource", accessType: "Resource", accessID: "data.database/v1",
		},
	}}
	collision, err := applicationmeta.ParseSource("plystra.yaml", []byte("resources: {instances: {database.primary: {use: example.com/provider.New}, records: {use: example.com/provider.New}}}\ndata: {members: {example.records/v1: {resource: database.primary, access: records}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource collision: %v", err)
	}
	if err := ValidateDataAssignments(collision, acceptance); err == nil || !strings.Contains(err.Error(), "collides with a Resource") {
		t.Fatalf("access collision error = %v", err)
	}

	mismatch, err := applicationmeta.ParseSource("plystra.yaml", []byte("resources: {instances: {database.primary: {use: example.com/provider.New}}}\ndata: {members: {example.records/v1: {resource: database.primary}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource mismatch: %v", err)
	}
	if err := ValidateDataAssignments(mismatch, acceptance); err == nil || !strings.Contains(err.Error(), "requires an explicit access instance") {
		t.Fatalf("access mismatch error = %v", err)
	}
}

func TestValidateDataAssignmentsRequiresResourceAndRejectsCrossResourceImports(t *testing.T) {
	acceptance := DataAnalysisAcceptance{members: map[string]dataAnalysisMember{
		"example.a/v1": {model: dataModel{Imports: []string{"example.b/v1"}}, migrationOnly: true},
		"example.b/v1": {migrationOnly: true},
	}}
	missing, err := applicationmeta.ParseSource("plystra.yaml", []byte("data: {members: {example.a/v1: {resource: database.primary}, example.b/v1: {resource: database.primary}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource missing Resource: %v", err)
	}
	if err := ValidateDataAssignments(missing, acceptance); err == nil || !strings.Contains(err.Error(), "missing Resource instance") {
		t.Fatalf("missing Resource assignment error = %v", err)
	}

	crossResource, err := applicationmeta.ParseSource("plystra.yaml", []byte("resources: {instances: {database.primary: {use: example.com/provider.New}, database.replica: {use: example.com/provider.New}}}\ndata: {members: {example.a/v1: {resource: database.primary}, example.b/v1: {resource: database.replica}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource cross-resource: %v", err)
	}
	if err := ValidateDataAssignments(crossResource, acceptance); err == nil || !strings.Contains(err.Error(), "crosses Resource boundary") {
		t.Fatalf("cross-Resource error = %v", err)
	}
}
