// Package testmodulecache prepares external Go modules used by offline test fixtures.
package testmodulecache

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"testing"
)

var downloads sync.Map

// Ensure downloads each exact module query once before a test fixture disables
// module lookup or copies the module into an isolated file proxy.
func Ensure(t testing.TB, queries ...string) {
	t.Helper()
	for _, query := range queries {
		if strings.TrimSpace(query) == "" || query != strings.TrimSpace(query) {
			t.Fatalf("invalid test module query %q", query)
		}
		ensure(t, query)
	}
}

func ensure(t testing.TB, query string) {
	t.Helper()
	download := sync.OnceValue(func() error {
		command := exec.Command("go", "mod", "download", query)
		command.Env = goEnvironment()
		if err := command.Run(); err != nil {
			return fmt.Errorf("go mod download %s: %w", query, err)
		}
		return nil
	})
	value, _ := downloads.LoadOrStore(query, download)
	if err := value.(func() error)(); err != nil {
		t.Fatal(err)
	}
}

func goEnvironment() []string {
	overrides := map[string]string{
		"GOENV":       "off",
		"GOFLAGS":     "",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	}
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[strings.ToUpper(key)]; !replaced {
			environment = append(environment, entry)
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		environment = append(environment, key+"="+overrides[key])
	}
	return environment
}
