package applicationresolve

import "github.com/plystra/cli/internal/datacompiler"

// DataCompilerAcquisition is the public-safe identity of the compiler that
// was selected and verified for one resolution. It deliberately excludes the
// private executable and cache paths; those remain an internal handoff to the
// later emit phase.
type DataCompilerAcquisition struct {
	modulePath     string
	moduleVersion  string
	moduleChecksum string
	manifestDigest string
	binaryDigest   string
	goToolchain    string
	goos           string
	goarch         string
	cacheHit       bool
	offline        bool
}

func newDataCompilerAcquisition(artifact datacompiler.Artifact, offline bool) DataCompilerAcquisition {
	return DataCompilerAcquisition{
		modulePath: artifact.ModulePath, moduleVersion: artifact.ModuleVersion,
		moduleChecksum: artifact.ModuleChecksum, manifestDigest: artifact.ManifestDigest,
		binaryDigest: artifact.BinaryDigest, goToolchain: artifact.GoToolchain,
		goos: artifact.GOOS, goarch: artifact.GOARCH, cacheHit: artifact.CacheHit,
		offline: offline,
	}
}

// Valid reports whether the acquisition contains a complete compiler identity.
func (a DataCompilerAcquisition) Valid() bool {
	return a.modulePath != "" && a.moduleVersion != "" && a.moduleChecksum != "" &&
		a.manifestDigest != "" && a.binaryDigest != "" && a.goToolchain != "" &&
		a.goos != "" && a.goarch != ""
}

func (a DataCompilerAcquisition) ModulePath() string     { return a.modulePath }
func (a DataCompilerAcquisition) ModuleVersion() string  { return a.moduleVersion }
func (a DataCompilerAcquisition) ModuleChecksum() string { return a.moduleChecksum }
func (a DataCompilerAcquisition) ManifestDigest() string { return a.manifestDigest }
func (a DataCompilerAcquisition) BinaryDigest() string   { return a.binaryDigest }
func (a DataCompilerAcquisition) GoToolchain() string    { return a.goToolchain }
func (a DataCompilerAcquisition) GOOS() string           { return a.goos }
func (a DataCompilerAcquisition) GOARCH() string         { return a.goarch }
func (a DataCompilerAcquisition) CacheHit() bool         { return a.cacheHit }
func (a DataCompilerAcquisition) Offline() bool          { return a.offline }

// CacheMaterialized reports whether this resolution created or refreshed the
// private compiler executable rather than reusing a verified cache entry.
func (a DataCompilerAcquisition) CacheMaterialized() bool {
	return a.Valid() && !a.cacheHit
}
