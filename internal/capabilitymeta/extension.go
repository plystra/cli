package capabilitymeta

import kernelmanifest "github.com/plystra/kernel/plugin/manifest"

// CapabilityExtension is Kernel's immutable normalized namespaced build-time
// metadata value. CLI does not maintain a parallel extension model.
type CapabilityExtension = kernelmanifest.CapabilityExtension

// CapabilityExtensions is Kernel's immutable namespace-sorted metadata set.
type CapabilityExtensions = kernelmanifest.CapabilityExtensions
