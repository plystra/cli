package applicationresolve_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/applicationresolve"
	"github.com/plystra/cli/internal/datacompiler"
)

func TestValidateDataRootsRejectsInvalidNormalizedModel(t *testing.T) {
	response := acceptedDataResponse(t, []any{
		map[string]any{
			"member_id": "example.records/v1", "package_path": "example.com/model", "file_path": "model.go", "symbol": "Records",
			"model": map[string]any{
				"namespace": "records",
				"tables":    []any{map[string]any{"id": "users", "columns": []any{map[string]any{"id": "id", "type": "unknown"}}}},
			},
			"migration_only": true, "source_digest": digestDataTest("model.go"),
			"declaration_pos": map[string]any{"path": "model.go", "start_line": 1, "start_column": 1, "end_line": 1, "end_column": 2},
		},
	})
	if err := applicationresolve.ValidateDataRoots(response, []string{"example.records/v1"}); err == nil || !strings.Contains(err.Error(), "normalized typed model") {
		t.Fatalf("ValidateDataRoots() error = %v", err)
	}
}

func TestValidateDataRootsRequiresMigrationOnlyForMissingAccess(t *testing.T) {
	root := map[string]any{
		"member_id": "example.records/v1", "package_path": "example.com/model", "file_path": "model.go", "symbol": "Records",
		"model": map[string]any{"namespace": "records"}, "migration_only": false, "source_digest": digestDataTest("model.go"),
		"declaration_pos": map[string]any{"path": "model.go", "start_line": 1, "start_column": 1, "end_line": 1, "end_column": 2},
	}
	response := acceptedDataResponse(t, []any{root})
	if err := applicationresolve.ValidateDataRoots(response, []string{"example.records/v1"}); err == nil || !strings.Contains(err.Error(), "migration-only") {
		t.Fatalf("ValidateDataRoots() error = %v", err)
	}
}

func acceptedDataResponse(t *testing.T, roots []any) datacompiler.AnalyzeResponse {
	t.Helper()
	rootBytes, err := json.Marshal(roots)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"roots":` + string(rootBytes) + `,"diagnostics":[],"truncated":false}`)
	sum := sha256.Sum256(append([]byte("plystra.data.analyze/v1\x00"), payload...))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	output, err := json.Marshal(map[string]any{"roots": roots, "digest": digest, "truncated": false, "valid": true})
	if err != nil {
		t.Fatal(err)
	}
	return datacompiler.AnalyzeResponse{Status: "succeeded", OutputDigest: digest, Output: output}
}

func digestDataTest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}
