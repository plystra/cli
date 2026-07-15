// Package generation defines version v1 of the trusted build-time protocol
// between Plystra CLI and plugin-provided generation extensions.
//
// A Context contains only normalized public contracts, selected providers,
// exposure, aliases, module provenance, and explicitly build-visible metadata.
// It contains no runtime configuration, Secret value, process environment,
// filesystem path, writable handle, or generated-output location.
//
// Generation extensions are trusted Go build dependencies. This package makes
// supported inputs immutable and deterministic; it is not a security sandbox.
// A generation package exposes this compile-time checked entry point:
//
//	func Generate(context generation.GenerationContext) (generation.Output, error)
//
// GenerateFunc captures that signature for CLI-generated helper programs.
package generation

// Version is the exact generation-extension API version implemented here.
const Version = "v1"
