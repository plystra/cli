package applicationresolve

import (
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestValidateDataAssignmentsRejectsAccessCollisionsAndMismatch(t *testing.T) {
	acceptance := acceptedAssignmentAnalysis(map[string]dataAnalysisMember{
		"example.records/v1": {
			model:         dataModel{Namespace: "records"},
			accessPackage: "example.com/resource", accessType: "Resource", accessID: "data.database/v1", modelDigest: testDataDigest("1"),
		},
	})
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
	acceptance := acceptedAssignmentAnalysis(map[string]dataAnalysisMember{
		"example.a/v1": {model: dataModel{Namespace: "a", Imports: []string{"example.b/v1"}}, migrationOnly: true, modelDigest: testDataDigest("1")},
		"example.b/v1": {model: dataModel{Namespace: "b"}, migrationOnly: true, modelDigest: testDataDigest("2")},
	})
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

func TestBuildDataActivationReturnsCanonicalDefensiveAssignments(t *testing.T) {
	acceptance := acceptedAssignmentAnalysis(map[string]dataAnalysisMember{
		"example.a/v1": {model: dataModel{Namespace: "a", Imports: []string{"example.b/v1"}}, accessPackage: "example.com/access", accessType: "Resource", accessID: "data.access/v1", modelDigest: testDataDigest("1")},
		"example.b/v1": {model: dataModel{Namespace: "b"}, migrationOnly: true, modelDigest: testDataDigest("2")},
	})
	manifest, err := applicationmeta.ParseSource("plystra.yaml", []byte("resources: {instances: {database.primary: {use: example.com/provider.New}}}\ndata: {members: {example.b/v1: {resource: database.primary}, example.a/v1: {resource: database.primary, access: example.access}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	activation, err := BuildDataActivation(manifest, acceptance)
	if err != nil {
		t.Fatalf("BuildDataActivation: %v", err)
	}
	if !activation.Valid() {
		t.Fatalf("activation is invalid: %#v", activation)
	}
	want := []DataAssignment{
		{memberID: "example.a/v1", resource: "database.primary", namespace: "a", access: "example.access", accessPackage: "example.com/access", accessType: "Resource", accessID: "data.access/v1", modelDigest: testDataDigest("1")},
		{memberID: "example.b/v1", resource: "database.primary", namespace: "b", modelDigest: testDataDigest("2")},
	}
	if got := activation.Assignments(); !reflect.DeepEqual(got, want) {
		t.Fatalf("assignments = %#v, want %#v", got, want)
	}
	copy := activation.Assignments()
	copy[0] = DataAssignment{}
	if reflect.DeepEqual(activation.Assignments(), copy) {
		t.Fatal("Assignments returned shared mutable storage")
	}
}

func TestBuildDataActivationRejectsMissingExplicitMemberAndReservedAccess(t *testing.T) {
	acceptance := acceptedAssignmentAnalysis(map[string]dataAnalysisMember{
		"example.a/v1": {model: dataModel{Namespace: "a"}, accessPackage: "example.com/resource", accessType: "Resource", accessID: "data.access/v1", modelDigest: testDataDigest("1")},
		"example.b/v1": {model: dataModel{Namespace: "b"}, migrationOnly: true, modelDigest: testDataDigest("2")},
	})
	missing, err := applicationmeta.ParseSource("plystra.yaml", []byte("resources: {instances: {database.primary: {use: example.com/provider.New}}}\ndata: {members: {example.a/v1: {resource: database.primary, access: example.access}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource missing member: %v", err)
	}
	if _, err := BuildDataActivation(missing, acceptance); err == nil || !strings.Contains(err.Error(), "has no explicit activation") {
		t.Fatalf("missing explicit member error = %v", err)
	}

	reserved, err := applicationmeta.ParseSource("plystra.yaml", []byte("resources: {instances: {database.primary: {use: example.com/provider.New}}}\ndata: {members: {example.a/v1: {resource: database.primary, access: kernel.health}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource reserved access: %v", err)
	}
	delete(acceptance.members, "example.b/v1")
	delete(acceptance.modelDigests, "example.b/v1")
	if _, err := BuildDataActivation(reserved, acceptance); err == nil || !strings.Contains(err.Error(), "reserved intrinsic name") {
		t.Fatalf("reserved access error = %v", err)
	}
}

func TestBuildDataActivationRejectsSameResourceNamespaceCollision(t *testing.T) {
	acceptance := acceptedAssignmentAnalysis(map[string]dataAnalysisMember{
		"example.a/v1": {model: dataModel{Namespace: "shared"}, migrationOnly: true, modelDigest: testDataDigest("1")},
		"example.b/v1": {model: dataModel{Namespace: "shared"}, migrationOnly: true, modelDigest: testDataDigest("2")},
	})
	manifest, err := applicationmeta.ParseSource("plystra.yaml", []byte("resources: {instances: {database.primary: {use: example.com/provider.New}}}\ndata: {members: {example.a/v1: {resource: database.primary}, example.b/v1: {resource: database.primary}}}\n"))
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	if _, err := BuildDataActivation(manifest, acceptance); err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Fatalf("namespace collision error = %v", err)
	}
}

func acceptedAssignmentAnalysis(members map[string]dataAnalysisMember) DataAnalysisAcceptance {
	modelDigests := make(map[string]string, len(members))
	for memberID, member := range members {
		modelDigests[memberID] = member.modelDigest
	}
	return DataAnalysisAcceptance{
		resultDigest: testDataDigest("result"), snapshotDigest: testDataDigest("snapshot"),
		modelDigests: modelDigests, members: members,
	}
}

func testDataDigest(value string) string {
	value = strings.Repeat(value, (64+len(value)-1)/len(value))
	return "sha256:" + value[:64]
}
