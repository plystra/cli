package newproject

import "testing"

func TestSanitizeCommandOutput(t *testing.T) {
	t.Parallel()

	const staging = `C:\Users\person\workspace\.app.plystra-123`
	input := "go: reading https://person:secret@example.com/private?token=secret: denied\n" + staging + `\go.mod`
	want := "go: reading <redacted-url> denied\n.\\go.mod"
	if got := sanitizeCommandOutput(input, staging); got != want {
		t.Fatalf("sanitizeCommandOutput() = %q, want %q", got, want)
	}
}
