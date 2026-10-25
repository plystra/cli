package moduledependency

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMetadataBatchCannotDiscardCapture(t *testing.T) {
	for _, mode := range []string{"skipped", "repeated", "unbounded", "extended", "nil-context", "masked-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			rejected := errors.New("capture rejected")
			calls := 0
			var retained func(context.Context) error
			err := captureMetadataBatch(ctx, func(ctx context.Context, capture func(context.Context) error) error {
				retained = capture
				switch mode {
				case "skipped":
					return nil
				case "repeated":
					_ = capture(ctx)
					_ = capture(ctx)
				case "unbounded":
					_ = capture(context.Background())
				case "extended":
					extended, stop := context.WithTimeout(context.Background(), time.Minute)
					defer stop()
					_ = capture(extended)
				case "nil-context":
					_ = capture(nil)
				case "masked-error":
					_ = capture(ctx)
				}
				return nil
			}, func(context.Context) error {
				calls++
				return rejected
			})
			if mode == "masked-error" || mode == "repeated" {
				if calls != 1 || !errors.Is(err, rejected) {
					t.Fatal("batch discarded or repeated a capture", calls, err)
				}
			} else if calls != 0 || !errors.Is(err, ErrSourceIntegrity) {
				t.Fatal("batch bypassed bounded capture", calls, err)
			}
			if !errors.Is(retained(ctx), ErrSourceIntegrity) {
				t.Fatal("capture escaped its batch lifetime")
			}
		})
	}
}
