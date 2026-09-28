// Package version defines the installed Plystra release facts.
package version

const (
	// Current is the canonical Semantic Versioning 2.0.0 CLI release version.
	Current = "0.1.0"
	// KernelVersion is the exact Kernel release supported by this CLI release.
	KernelVersion = "v0.0.0-20260928055126-4402d1062034"
	// GoRequirement is the language version required by the installed CLI
	// module and the Projects it generates.
	GoRequirement = "1.26"
	// SpecificationRevision is the core-philosophy revision implemented by the
	// installed release guidance catalog.
	SpecificationRevision = "b959f277b7661c0725660f394b84f7175c8df54b"
)
