package projectcheck_test

import (
	"errors"
	"testing"

	"github.com/plystra/cli/internal/projectcheck"
)

func TestCheckRejectsNilContext(t *testing.T) {
	t.Parallel()

	//lint:ignore SA1012 This test verifies that the public boundary rejects a nil context.
	if _, err := projectcheck.Check(nil, projectcheck.Options{}); !errors.Is(err, projectcheck.ErrCheck) {
		t.Fatalf("Check error = %v", err)
	}
}
