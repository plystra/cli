package command

import (
	"github.com/plystra/cli/internal/commandschema"
	"github.com/plystra/cli/internal/interfaceinventory"
)

type policySupportEffects struct {
	module             string
	started, temporary bool
}

func (e *policySupportEffects) observe(event interfaceinventory.PackageLoadEvent) {
	e.module = event.Module
	e.started = e.started || event.GoStarted
	e.temporary = e.temporary || event.TemporaryCreated
}

func (e *policySupportEffects) merge(base commandschema.Effects) (commandschema.Effects, error) {
	if e == nil || e.module == "" {
		return base, nil
	}
	input := commandschema.EffectsInput{Observed: base.Observed(), Planned: base.Planned(), Skipped: base.Skipped(), Unverified: base.Unverified()}
	for _, item := range []struct {
		id, reason string
		class      commandschema.EffectClass
		observed   bool
	}{
		{"policy-support-go", "inspect_selected_kernel_policy_support", commandschema.EffectTrustedCodeExecution, e.started},
		{"policy-support-module", "private_selected_kernel_module", commandschema.EffectTemporaryFile, e.temporary},
		{"policy-support-cache", "selected_kernel_cache_unknown", commandschema.EffectCacheMaterialization, false},
		{"policy-support-download", "selected_kernel_downloads_unknown", commandschema.EffectDownload, false},
	} {
		if !item.observed && item.class != commandschema.EffectCacheMaterialization && item.class != commandschema.EffectDownload {
			continue
		}
		verification := []string{"go", "list", "-deps", "-export", "-json", "-e", "-mod=readonly", "github.com/plystra/kernel/invocation"}
		if item.class == commandschema.EffectCacheMaterialization {
			verification = []string{"go", "env", "GOCACHE", "GOMODCACHE"}
		} else if item.class == commandschema.EffectDownload {
			verification = []string{"go", "env", "GOPROXY"}
		}
		effect, err := commandschema.NewEffect(commandschema.EffectInput{
			ID: item.id, Class: item.class, Phase: "plan", Target: ".", Owner: commandschema.Owner{Module: e.module, Path: "."},
			Reason: item.reason, Reversibility: "none", Verification: verification,
		})
		if err != nil {
			return commandschema.Effects{}, err
		}
		if item.observed {
			input.Observed = append(input.Observed, effect)
		} else {
			input.Unverified = append(input.Unverified, effect)
		}
	}
	return commandschema.NewEffects(input)
}
