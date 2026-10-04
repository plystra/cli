package resourcename_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/cli/internal/resourcename"
)

func TestCheckCanonicalResourceInstanceNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a", "database.primary", "a0.b2-c3", "a-0.b-1", strings.Repeat("a", 128), "a." + strings.Repeat("b", 126)} {
		if err := resourcename.Check(name); err != nil {
			t.Fatalf("Check(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", ".", "..", "a.", ".a", "a..b", "a.-b", "a-.b", "a--b", "-a", "a-", "0a", "a.0b", "A", "a.B", "a_b", "a/b", "a\\b", "a:b", "a@b", "a b", " a", "a ", "a\n", "a\x00", "a\u00e9", "\u03b4", strings.Repeat("a", 129), "a." + strings.Repeat("b", 127)} {
		if err := resourcename.Check(name); !errors.Is(err, resourcename.ErrInvalid) || strings.Contains(err.Error(), name) && name != "" && len(name) > 1 {
			t.Fatalf("Check rejected name incorrectly: %v", err)
		}
	}
}

func FuzzCheckResourceInstanceName(f *testing.F) {
	for _, name := range []string{"database.primary", "a-0.b", "", "a..b", strings.Repeat("a", 128)} {
		f.Add(name)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if resourcename.Check(name) != nil {
			return
		}
		if len(name) == 0 || len(name) > resourcename.MaximumLength {
			t.Fatal("accepted out-of-bounds name")
		}
		for _, segment := range strings.Split(name, ".") {
			if segment == "" || segment[0] < 'a' || segment[0] > 'z' {
				t.Fatal("accepted invalid segment start")
			}
			for _, part := range strings.Split(segment, "-") {
				if part == "" {
					t.Fatal("accepted empty kebab component")
				}
				for _, character := range []byte(part) {
					if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9') {
						t.Fatal("accepted non-ASCII alphanumeric component")
					}
				}
			}
		}
	})
}
