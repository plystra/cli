// Package runtimebaseline encodes private deployment inputs separately from generated source.
package runtimebaseline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/plystra/cli/internal/privatefile"
)

const Schema = "plystra.runtime-baseline/v1"
const Path = "dist/runtime-baseline.json"
const MaximumBytes = 16 << 20
const PermissionsModule = "golang.org/x/sys"
const PermissionsVersion = "v0.47.0"

var ErrBaseline = errors.New("invalid private runtime baseline; regenerate and supply the matching owner-private deployment input")

type Document struct {
	Schema     string                     `json:"schema"`
	ContractID string                     `json:"runtime_contract"`
	Contract   json.RawMessage            `json:"contract"`
	Defaults   map[string]json.RawMessage `json:"defaults"`
}

func (Document) String() string   { return "<private-runtime-baseline>" }
func (Document) GoString() string { return "<private-runtime-baseline>" }

// ContractID hashes only the explicitly public contract projection supplied by the compiler.
func ContractID(contract []byte) string {
	sum := sha256.Sum256(contract)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func Encode(document Document) ([]byte, error) {
	if document.Schema != Schema || !json.Valid(document.Contract) || document.ContractID != ContractID(document.Contract) || document.Defaults == nil {
		return nil, ErrBaseline
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil || len(data)+1 > MaximumBytes || !uniqueKeys(data) {
		return nil, ErrBaseline
	}
	return append(data, '\n'), nil
}

// Decode accepts the compiler's canonical build-output format. The round trip
// also rejects duplicate keys, unknown fields, trailing values and alternate names.
func Decode(data []byte) (Document, error) {
	if len(data) > MaximumBytes || !uniqueKeys(data) {
		return Document{}, ErrBaseline
	}
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil {
		return Document{}, ErrBaseline
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return Document{}, ErrBaseline
	}
	// MarshalIndent expands RawMessage whitespace; normalize the public projection
	// back to its canonical compact representation before verifying its identity.
	var compact bytes.Buffer
	if json.Compact(&compact, document.Contract) != nil {
		return Document{}, ErrBaseline
	}
	document.Contract = append(json.RawMessage(nil), compact.Bytes()...)
	for symbol, value := range document.Defaults {
		compact.Reset()
		if json.Compact(&compact, value) != nil {
			return Document{}, ErrBaseline
		}
		document.Defaults[symbol] = append(json.RawMessage(nil), compact.Bytes()...)
	}
	canonical, err := Encode(document)
	if err != nil || !bytes.Equal(data, canonical) {
		return Document{}, ErrBaseline
	}
	return document, nil
}

// Read opens one explicit baseline with stable identity and native private permissions.
func Read(name string) (Document, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return Document{}, ErrBaseline
	}
	directory, err := os.OpenRoot(filepath.Dir(absolute))
	if err != nil {
		return Document{}, ErrBaseline
	}
	defer directory.Close()
	return ReadAt(directory, filepath.Base(absolute))
}

// ReadAt confines relative inputs to the selected configuration root and rejects
// symbolic components, including links that point back inside that root.
func ReadAt(directory *os.Root, name string) (Document, error) {
	if directory == nil || !filepath.IsLocal(name) {
		return Document{}, ErrBaseline
	}
	leaf := filepath.Clean(name)
	components := strings.Split(leaf, string(filepath.Separator))
	var parents []os.FileInfo
	parent := "."
	for _, part := range components[:len(components)-1] {
		parent = filepath.Join(parent, part)
		info, err := directory.Lstat(parent)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Document{}, ErrBaseline
		}
		parents = append(parents, info)
	}
	before, err := directory.Lstat(leaf)
	if err != nil || !before.Mode().IsRegular() || before.Size() > MaximumBytes {
		return Document{}, ErrBaseline
	}
	file, err := directory.Open(leaf)
	if err != nil {
		return Document{}, ErrBaseline
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !sameFile(before, opened) {
		return Document{}, ErrBaseline
	}
	permissions, err := privatefile.Snapshot(file)
	if err != nil {
		return Document{}, ErrBaseline
	}
	data, err := io.ReadAll(io.LimitReader(file, MaximumBytes+1))
	if err != nil || len(data) > MaximumBytes {
		return Document{}, ErrBaseline
	}
	defer clear(data)
	after, err := file.Stat()
	current, pathErr := directory.Lstat(leaf)
	currentPermissions, permissionErr := privatefile.Snapshot(file)
	if err != nil || pathErr != nil || !sameFile(opened, after) || !sameFile(opened, current) || permissionErr != nil || permissions != currentPermissions {
		return Document{}, ErrBaseline
	}
	parent = "."
	for index, previous := range parents {
		parent = filepath.Join(parent, components[index])
		current, err := directory.Lstat(parent)
		if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(previous, current) {
			return Document{}, ErrBaseline
		}
	}
	return Decode(data)
}

func uniqueKeys(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value func(int) bool
	value = func(depth int) bool {
		// A 64-level Go object schema uses three JSON containers per level.
		if depth > 256 {
			return false
		}
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		delimiter, composite := token.(json.Delim)
		if !composite {
			return true
		}
		keys := make(map[string]bool)
		for decoder.More() {
			if delimiter == '{' {
				key, err := decoder.Token()
				name, ok := key.(string)
				if err != nil || !ok || keys[name] {
					return false
				}
				keys[name] = true
			}
			if !value(depth + 1) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && (delimiter == '{' && end == json.Delim('}') || delimiter == '[' && end == json.Delim(']'))
	}
	if !value(0) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func sameFile(a, b os.FileInfo) bool {
	return a != nil && b != nil && a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}
