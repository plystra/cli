package applicationmeta

import (
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/resourcename"
	"go.yaml.in/yaml/v3"
)

// ErrRemoveResourceInstance reports an unsafe exact named-instance removal.
var ErrRemoveResourceInstance = errors.New("remove Resource instance")

// RemoveResourceInstance deletes one complete instance entry. Binding cleanup
// and final-state validation remain the caller's responsibility.
func RemoveResourceInstance(data []byte, name string) ([]byte, bool, error) {
	return removeResourceInstance(data, name, false)
}

// RemoveResourceInstanceOverlay excludes one whole instance without exposing a
// lower provider or copying inherited configuration into the selected overlay.
func RemoveResourceInstanceOverlay(data []byte, name string) ([]byte, bool, error) {
	return removeResourceInstance(data, name, true)
}

func removeResourceInstance(data []byte, name string, overlay bool) ([]byte, bool, error) {
	if resourcename.Check(name) != nil {
		return nil, false, fmt.Errorf("%w: invalid instance name", ErrRemoveResourceInstance)
	}
	return editSelectionDocument(data, overlay, ErrRemoveResourceInstance, func(root *yaml.Node) (bool, error) {
		return removeSelectionLeaf(root, []string{"resources", "instances", name}, overlay)
	})
}
