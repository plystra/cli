package projectstage

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plystra/cli/internal/privatefile"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

// Origin payloads and Go's later extracted files have different identities.
// The checksum below describes content; it is not independent authority.
type proxyArtifact struct {
	origin        string
	request       string
	file          fileSnapshot
	module        module.Version
	inventory     []proxyRecord
	expandedBytes int64
	checksum      string
}

type proxyRecord struct {
	name   string
	digest [sha256.Size]byte
}

var errProxyNotFound = errors.New("dependency acquisition origin not found")

func (s *Stage) retainProxyFile(ctx context.Context, path, request string) ([]byte, error) {
	return s.retainAcquisitionFile(ctx, path, request, "proxy")
}

// ArchiveInput identifies an original cached module archive. Content identity
// does not grant independent checksum authority for its extracted sources.
type ArchiveInput struct {
	Path   string
	Module module.Version
}

// RetainArchives preserves bounded archive bytes and original input identities.
// Evidence survives failed validation and Stage.Close without refreshing a prior
// capture. Proxy origins and cache files remain distinct original inputs.
func (s *Stage) RetainArchives(ctx context.Context, archives []ArchiveInput) error {
	return s.WithMetadataBatch(ctx, func(retain MetadataRetention) error { return retain.Archives(archives) })
}

func (s *Stage) retainArchives(ctx context.Context, archives []ArchiveInput) error {
	if len(archives) > MaximumEntries {
		return ErrStage
	}
	for _, archive := range archives {
		path, pathErr := module.EscapePath(archive.Module.Path)
		version, versionErr := module.EscapeVersion(archive.Module.Version)
		if pathErr != nil || versionErr != nil || module.Check(archive.Module.Path, archive.Module.Version) != nil ||
			module.CanonicalVersion(archive.Module.Version) != archive.Module.Version || !filepath.IsAbs(archive.Path) {
			return ErrSourceIntegrity
		}
		if _, err := s.captureAcquisitionFile(ctx, archive.Path, path+"/@v/"+version+".zip", "cache"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Stage) retainAcquisitionFile(ctx context.Context, path, request, origin string) (data []byte, resultErr error) {
	defer s.lock()()
	defer func() {
		if resultErr != nil && !errors.Is(resultErr, errProxyNotFound) && s.proxyErr == nil {
			s.proxyErr = errors.Join(ErrStage, resultErr)
		}
	}()
	if err := s.recheck(ctx); err != nil {
		return nil, err
	}
	data, resultErr = s.captureAcquisitionFile(ctx, path, request, origin)
	if resultErr != nil && !errors.Is(resultErr, errProxyNotFound) {
		return nil, resultErr
	}
	if err := s.recheck(ctx); err != nil {
		return nil, err
	}
	return data, resultErr
}

// The caller holds the Stage lock and owns the surrounding complete rechecks.
func (s *Stage) captureAcquisitionFile(ctx context.Context, path, request, origin string) (data []byte, resultErr error) {
	defer func() {
		if resultErr != nil && !errors.Is(resultErr, errProxyNotFound) && s.proxyErr == nil {
			s.proxyErr = errors.Join(ErrStage, resultErr)
		}
	}()
	if !filepath.IsAbs(path) {
		return nil, ErrStage
	}
	parentPath := filepath.Dir(path)
	if origin == "proxy" {
		// Preserve only the requested leaf's absence. Creating a previously
		// missing parent for an unrelated sibling must remain permissible.
		for {
			if _, err := os.Lstat(parentPath); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) || filepath.Dir(parentPath) == parentPath {
				return nil, ErrStage
			}
			parentPath = filepath.Dir(parentPath)
		}
	}
	parent, err := s.captureDirectory(s.project, parentPath)
	if err != nil {
		return nil, ErrStage
	}
	name, err := filepath.Rel(parentPath, path)
	if err != nil || !filepath.IsLocal(name) {
		return nil, ErrStage
	}
	guarded := parent
	if origin == "proxy" {
		// A missing origin may share an existing ancestor with the private
		// stage. Only the requested path must remain outside that stage.
		guarded = filepath.Join(parent, name)
	}
	if within(s.temporary, guarded) || within(guarded, s.temporary) {
		return nil, ErrStage
	}
	for _, artifact := range s.artifacts {
		if artifact.origin == origin && artifact.request == request {
			if filepath.Join(artifact.file.parent, artifact.file.name) != filepath.Join(parent, name) {
				return nil, ErrConcurrentChange
			}
			s.inputs = s.captureInputs()
			if artifact.file.info == nil {
				return nil, errProxyNotFound
			}
			return artifact.file.data, nil
		}
	}
	used := s.inputBudget()
	if used.entries >= MaximumEntries || used.bytes >= MaximumTotalBytes {
		return nil, ErrStage
	}
	limit := int64(maximumMetadataBytes)
	if strings.HasSuffix(request, ".zip") {
		limit = MaximumTotalBytes - used.bytes
	}
	file, err := readInputFile(parent, name, origin == "proxy", limit)
	if err != nil {
		return nil, err
	}
	if used.bytes+int64(len(file.data)) > MaximumTotalBytes {
		return nil, ErrStage
	}
	artifact := proxyArtifact{origin: origin, request: request, file: file}
	// Retain the complete origin read even if archive validation, cancellation,
	// or the final recheck subsequently prevents delivery.
	index := len(s.artifacts)
	s.artifacts = append(s.artifacts, artifact)
	s.inputs = s.captureInputs()
	if file.info != nil && strings.HasSuffix(request, ".zip") {
		artifact.module, err = proxyModule(request, ".zip")
		if err == nil {
			err = s.checkProxyArchive(ctx, &artifact, used)
		}
		if err != nil {
			return nil, errors.Join(ErrSourceIntegrity, err)
		}
		s.artifacts[index] = artifact
		s.inputs = s.captureInputs()
	}
	if file.info == nil {
		return nil, errProxyNotFound
	}
	return file.data, nil
}

func proxyModule(request, extension string) (module.Version, error) {
	path, version, ok := strings.Cut(request, "/@v/")
	if !ok || !strings.HasSuffix(version, extension) {
		return module.Version{}, ErrSourceIntegrity
	}
	path, err := module.UnescapePath(path)
	if err != nil {
		return module.Version{}, ErrSourceIntegrity
	}
	version, err = module.UnescapeVersion(strings.TrimSuffix(version, extension))
	if err != nil || module.CanonicalVersion(version) != version || module.Check(path, version) != nil {
		return module.Version{}, ErrSourceIntegrity
	}
	return module.Version{Path: path, Version: version}, nil
}

func (s *Stage) checkProxyArchive(ctx context.Context, artifact *proxyArtifact, used budget) (resultErr error) {
	if err := checkProxyDirectory(ctx, artifact.file.data, MaximumEntries-used.entries-1); err != nil {
		return err
	}
	reader, err := zip.NewReader(bytes.NewReader(artifact.file.data), int64(len(artifact.file.data)))
	if err != nil {
		return ErrSourceIntegrity
	}
	if len(reader.File) > MaximumEntries-used.entries-1 {
		return ErrStage
	}
	directory, err := privatefile.MkdirTempRoot(s.files, "acquisition-")
	if err != nil {
		return ErrStage
	}
	defer func() { resultErr = errors.Join(resultErr, s.files.RemoveAll(directory)) }()
	name := filepath.Join(directory, "source.zip")
	if err := writePrivate(s.files, name, artifact.file.data); err != nil {
		return ErrStage
	}
	if _, err := modzip.CheckZip(artifact.module, filepath.Join(s.temporary, name)); err != nil {
		return ErrSourceIntegrity
	}
	used.entries++
	used.bytes += int64(len(artifact.file.data))
	inventory := make([]proxyRecord, 0, len(reader.File))
	var expanded int64
	for _, file := range reader.File {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if file.UncompressedSize64 > MaximumFileBytes || used.entries >= MaximumEntries || file.UncompressedSize64 > uint64(MaximumTotalBytes-used.bytes) {
			return ErrStage
		}
		body, err := file.Open()
		if err != nil {
			return ErrSourceIntegrity
		}
		hash := sha256.New()
		count, readErr := io.Copy(hash, io.LimitReader(body, MaximumFileBytes+1))
		closeErr := body.Close()
		if readErr != nil || closeErr != nil || !strings.HasSuffix(file.Name, "/") && count != int64(file.UncompressedSize64) || count > MaximumFileBytes {
			return ErrSourceIntegrity
		}
		inventory = append(inventory, proxyRecord{name: file.Name, digest: [sha256.Size]byte(hash.Sum(nil))})
		used.entries++
		used.bytes += count
		expanded += count
	}
	sort.Slice(inventory, func(i, j int) bool { return inventory[i].name < inventory[j].name })
	hash := sha256.New()
	for _, record := range inventory {
		fmt.Fprintf(hash, "%x  %s\n", record.digest, record.name)
	}
	artifact.inventory, artifact.expandedBytes = inventory, expanded
	artifact.checksum = "h1:" + base64.StdEncoding.EncodeToString(hash.Sum(nil))
	return nil
}
