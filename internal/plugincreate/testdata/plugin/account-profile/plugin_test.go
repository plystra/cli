package accountprofile

import "testing"

func TestNew(t *testing.T) {
	t.Parallel()

	if plugin := New(Config{}); plugin == nil {
		t.Fatal("New returned nil")
	}
}
