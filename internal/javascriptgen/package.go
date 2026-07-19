package javascriptgen

import (
	"errors"
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/modulepath"
	"golang.org/x/mod/module"
)

// ErrPackageIdentity reports a Go Module path that cannot produce one
// canonical application-owned npm package identity.
var ErrPackageIdentity = errors.New("infer JavaScript SDK package identity")

// InferPackageName derives the documented application SDK identity from a Go
// Module path. The first path component below the module host becomes the npm
// scope when another component remains; deeper components form one hyphenated
// package name. A semantic import version never changes the package identity.
func InferPackageName(modulePath string) (string, error) {
	if err := modulepath.CheckProject(modulePath); err != nil {
		return "", fmt.Errorf("%w: invalid Go Module path %q: %v", ErrPackageIdentity, modulePath, err)
	}
	prefix, _, ok := module.SplitPathVersion(modulePath)
	if !ok {
		return "", fmt.Errorf("%w: invalid semantic import version in %q", ErrPackageIdentity, modulePath)
	}
	parts := strings.Split(prefix, "/")
	componentStart := 0
	if strings.Contains(parts[0], ".") {
		if len(parts) < 2 {
			return "", fmt.Errorf("%w: module path %q has no application name below its host", ErrPackageIdentity, modulePath)
		}
		componentStart = 1
	}
	components := make([]string, len(parts)-componentStart)
	for index, value := range parts[componentStart:] {
		if value != strings.ToLower(value) || !validPackagePart(value) {
			return "", fmt.Errorf("%w: Go Module path component %q is not a canonical lower-case npm package part", ErrPackageIdentity, value)
		}
		components[index] = value
	}

	var packageName string
	if len(components) == 1 {
		packageName = components[0] + "-sdk"
	} else {
		packageName = "@" + components[0] + "/" + strings.Join(components[1:], "-") + "-sdk"
	}
	if !validPackageName(packageName) {
		return "", fmt.Errorf("%w: Go Module path %q derives non-canonical or oversized npm package name %q", ErrPackageIdentity, modulePath, packageName)
	}
	return packageName, nil
}
