package capabilitycreate

import (
	"errors"
	"fmt"
	"path"
	"strconv"

	"github.com/plystra/cli/internal/atomicfs"
	"github.com/plystra/cli/internal/capabilitymeta"
)

// ErrRenderSchema reports that a capability creation plan and its resolved
// source snapshot could not produce one safe target write.
var ErrRenderSchema = errors.New("render capability schema write")

// RenderSchemaWrite returns the guarded module-relative capability.yaml write
// for plan without changing the filesystem. First versions receive a complete
// explicit profile-expanded wire schema; later versions copy the deterministic
// first equal source unchanged apart from the exact Capability ID.
func RenderSchemaWrite(plan Plan, sources []ResolvedSource) (atomicfs.Write, error) {
	targetPlugin := plan.Target()
	target := plan.Version().Target()
	if targetPlugin.ID() == "" || targetPlugin.Directory() == "" || targetPlugin.ModuleRoot() == "" || target.String() == "" {
		return atomicfs.Write{}, fmt.Errorf("%w: plan is empty", ErrRenderSchema)
	}
	plannedProviders := plan.SourceProviders()
	if len(sources) != len(plannedProviders) {
		return atomicfs.Write{}, fmt.Errorf("%w: resolved source count %d does not match planned count %d", ErrRenderSchema, len(sources), len(plannedProviders))
	}
	for index := range plannedProviders {
		if sources[index].Provider() != plannedProviders[index] {
			return atomicfs.Write{}, fmt.Errorf("%w: resolved source %d belongs to provider %s, expected %s", ErrRenderSchema, index, sources[index].Provider().PluginID(), plannedProviders[index].PluginID())
		}
	}

	sourceID, hasSource := plan.Version().Source()
	var data []byte
	if !hasSource {
		if len(sources) != 0 {
			return atomicfs.Write{}, fmt.Errorf("%w: plan has resolved sources without a source version", ErrRenderSchema)
		}
		switch plan.Intent() {
		case IntentProfileQuery:
			data = renderQuerySchema(target.String())
		case "":
			return atomicfs.Write{}, fmt.Errorf("%w: new Capability identity %s requires an explicit intent profile", ErrRenderSchema, target)
		default:
			return atomicfs.Write{}, fmt.Errorf("%w: unsupported intent profile %q", ErrRenderSchema, plan.Intent())
		}
	} else {
		if plan.Intent() != "" {
			return atomicfs.Write{}, fmt.Errorf("%w: intent profile %q cannot replace source semantics for %s", ErrRenderSchema, plan.Intent(), sourceID)
		}
		if len(sources) == 0 {
			return atomicfs.Write{}, fmt.Errorf("%w: source %s was not resolved", ErrRenderSchema, sourceID)
		}
		for index, source := range sources {
			if source.Source().ID() != sourceID {
				return atomicfs.Write{}, fmt.Errorf("%w: resolved source %d declares %s, expected %s", ErrRenderSchema, index, source.Source().ID(), sourceID)
			}
		}
		var err error
		data, err = capabilitymeta.RetargetSchema(sources[0].Source().Data(), target)
		if err != nil {
			return atomicfs.Write{}, fmt.Errorf("%w: %w", ErrRenderSchema, err)
		}
	}
	if _, err := capabilitymeta.NormalizeSchema(data); err != nil {
		return atomicfs.Write{}, fmt.Errorf("%w: validate rendered schema: %w", ErrRenderSchema, err)
	}
	declared, err := capabilitymeta.ParseID(data)
	if err != nil || declared != target {
		return atomicfs.Write{}, fmt.Errorf("%w: rendered schema does not declare %s", ErrRenderSchema, target)
	}

	return atomicfs.Write{
		Path: path.Join(
			targetPlugin.Directory(),
			"capabilities",
			target.Name(),
			"v"+strconv.FormatUint(target.Major(), 10),
			"capability.yaml",
		),
		Data:               data,
		Mode:               0o644,
		MustNotExist:       true,
		ParentMustNotExist: true,
	}, nil
}

func renderQuerySchema(identifier string) []byte {
	return []byte("id: " + identifier + `

request: {}
response: {}
errors: []

semantics:
  kind: query
  effects: none
  idempotency:
    mode: inherent
  retry:
    safety: safe
  cancellation:
    mode: best-effort
  completion:
    mode: completed-before-return
  ordering:
    mode: none
  data:
    request: public
    response: public
`)
}
