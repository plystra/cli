// Package version defines the installed Plystra release facts.
package version

const (
	// Current is the canonical Semantic Versioning 2.0.0 CLI release version.
	Current = "0.1.0"
	// KernelVersion is the exact Kernel release supported by this CLI release.
	KernelVersion = "v0.0.0-20261004024423-1e554d6f14ac"
	// GoRequirement is the language version required by the installed CLI
	// module and the Projects it generates.
	GoRequirement = "1.26"
	// SpecificationRevision is the core-philosophy revision implemented by the
	// installed release guidance catalog.
	SpecificationRevision = "5eff10a43cc8bcf426deeb4f0b520a4968e552be"
)
