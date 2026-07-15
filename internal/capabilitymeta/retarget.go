package capabilitymeta

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/plystra/cli/internal/capabilityid"
	"go.yaml.in/yaml/v3"
)

// ErrRetargetSchema reports that a validated capability declaration could not
// be copied to another version of the same capability name.
var ErrRetargetSchema = errors.New("retarget capability schema")

// RetargetSchema returns a deterministic copy declaring target. Copying the
// same exact ID preserves the source bytes. Copying to another version uses the
// YAML syntax tree so comments and human description remain source material.
func RetargetSchema(data []byte, target capabilityid.Identifier) ([]byte, error) {
	if target.String() == "" {
		return nil, fmt.Errorf("%w: target is empty", ErrRetargetSchema)
	}
	source, err := ParseID(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRetargetSchema, err)
	}
	if _, err := NormalizeSchema(data); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRetargetSchema, err)
	}
	if source == target {
		return append([]byte(nil), data...), nil
	}
	if source.Name() != target.Name() {
		return nil, fmt.Errorf("%w: source %s and target %s have different capability names", ErrRetargetSchema, source, target)
	}

	document, err := decodeYAMLDocument(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRetargetSchema, err)
	}
	root := document.Content[0]
	updated := false
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		if key.Kind == yaml.ScalarNode && key.Tag == "!!str" && key.Value == "id" {
			value.Value = target.String()
			updated = true
			break
		}
	}
	if !updated {
		return nil, fmt.Errorf("%w: validated source has no identity node", ErrRetargetSchema)
	}

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		_ = encoder.Close()
		return nil, fmt.Errorf("%w: encode schema: %w", ErrRetargetSchema, err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("%w: close schema encoder: %w", ErrRetargetSchema, err)
	}
	retargeted := output.Bytes()
	if _, err := NormalizeSchema(retargeted); err != nil {
		return nil, fmt.Errorf("%w: validate retargeted schema: %w", ErrRetargetSchema, err)
	}
	declared, err := ParseID(retargeted)
	if err != nil || declared != target {
		return nil, fmt.Errorf("%w: retargeted schema does not declare %s", ErrRetargetSchema, target)
	}
	return append([]byte(nil), retargeted...), nil
}
