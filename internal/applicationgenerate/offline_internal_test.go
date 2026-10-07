package applicationgenerate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestGenerationEnvironmentOfflineDisablesNetworkAndPreservesInput(t *testing.T) {
	input := []string{
		"GOPROXY=https://proxy.example.invalid",
		"GOSUMDB=sum.golang.org",
		"GOTOOLCHAIN=auto",
		"GONOPROXY=",
		"KEEP=value",
	}
	wantInput := append([]string(nil), input...)

	got := generationEnvironment(input, true)
	if !reflect.DeepEqual(input, wantInput) {
		t.Fatalf("generationEnvironment mutated input: %#v", input)
	}
	want := []string{"KEEP=value", "GOPROXY=off", "GONOPROXY=none", "GOSUMDB=off", "GOTOOLCHAIN=local"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("offline environment = %#v, want %#v", got, want)
	}
}

func TestGenerateRejectsOfflineModuleMutation(t *testing.T) {
	_, err := Generate(context.Background(), Options{
		Offline: true,
		MutateModule: func(context.Context, string, []ModuleRequirement, func() error) error {
			t.Fatal("offline generation invoked module mutation")
			return nil
		},
	})
	if !errors.Is(err, ErrGenerate) || !strings.Contains(err.Error(), "offline generation cannot mutate Go module metadata") {
		t.Fatalf("offline module mutation error = %v", err)
	}
}
