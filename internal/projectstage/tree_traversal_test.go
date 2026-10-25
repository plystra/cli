package projectstage

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureNestedTreeRetainsContentAndParentBinding(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace=%t", replace), func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "nested")
			if err := os.Mkdir(directory, 0o700); err != nil {
				t.Fatal(err)
			}
			data := []byte("package example\n")
			for _, name := range []string{"first.go", "second.go"} {
				if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			used := &budget{}
			ctx := &treeMutationContext{Context: t.Context(), used: used}
			if replace {
				backup := filepath.Join(t.TempDir(), "original")
				ctx.mutate = func() {
					if err := os.Rename(directory, backup); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(directory, 0o700); err != nil {
						t.Fatal(err)
					}
					for _, name := range []string{"first.go", "second.go"} {
						if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			captured, err := captureTree(ctx, root, "", nil, used, true)
			if replace {
				if !ctx.mutated || !errors.Is(err, ErrConcurrentChange) {
					t.Fatalf("replaced parent binding was accepted: mutated=%t, %v", ctx.mutated, err)
				}
				return
			}
			if err != nil || len(captured.entries) != 4 {
				t.Fatalf("unchanged nested tree: entries=%d, %v", len(captured.entries), err)
			}
			for _, name := range []string{"first.go", "second.go"} {
				if captured.entries[filepath.Join("nested", name)].digest != sha256.Sum256(data) {
					t.Fatal("nested content or logical snapshot path changed")
				}
			}
		})
	}
}

type treeMutationContext struct {
	context.Context
	used    *budget
	mutate  func()
	mutated bool
}

func (ctx *treeMutationContext) Err() error {
	// The root, nested directory and first file have been entered, so the
	// replacement occurs between children while the directory root is held.
	if !ctx.mutated && ctx.mutate != nil && ctx.used.entries >= 3 {
		ctx.mutated = true
		ctx.mutate()
	}
	return ctx.Context.Err()
}

func BenchmarkCaptureNestedTree(b *testing.B) {
	root := b.TempDir()
	directory := root
	for depth := range 6 {
		directory = filepath.Join(directory, fmt.Sprintf("level-%d", depth))
		if err := os.Mkdir(directory, 0o700); err != nil {
			b.Fatal(err)
		}
		for index := range 24 {
			if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("file-%d.go", index)), []byte("package example\n"), 0o600); err != nil {
				b.Fatal(err)
			}
		}
	}
	b.ResetTimer()
	for range b.N {
		captured, err := captureTree(b.Context(), root, "", nil, &budget{}, true)
		if err != nil || len(captured.entries) != 151 {
			b.Fatal("nested capture", len(captured.entries), err)
		}
	}
}
