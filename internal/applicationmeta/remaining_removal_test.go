package applicationmeta_test

import (
	"testing"

	"github.com/plystra/cli/internal/applicationmeta"
)

func TestRemainingStableKeyCollectionsComposeByExactKey(t *testing.T) {
	t.Parallel()
	root := resourceManifest(t, "plystra.yaml", `resources:
  instances:
    database.primary: {use: example.com/database.New}
    database.legacy: {use: example.com/database.New}
  bind:
    implementations:
      example.com/service.New:
        primary: database.primary
        obsolete: database.legacy
    instances:
      database.primary:
        obsolete: database.legacy
        retained: database.primary
`)
	overlay := resourceOverlayManifest(t, "plystra.production.yaml", `resources:
  instances:
    database.legacy: {$remove: true}
  bind:
    implementations:
      example.com/service.New:
        obsolete: {$remove: true}
        absent: {$remove: true}
    instances:
      database.primary:
        obsolete: {$remove: true}
        absent: {$remove: true}
`)
	lookup := resourceLookup(t, "Value string")
	selected, err := applicationmeta.ApplyOverlay(root, overlay, lookup)
	if err != nil {
		t.Fatal(err)
	}
	composition, err := applicationmeta.Compose(nil, selected, lookup)
	if err != nil {
		t.Fatal(err)
	}

	instances := composition.Manifest().ResourceInstances()
	if len(instances) != 1 || instances[0].Name() != "database.primary" {
		t.Fatalf("effective Resource instances = %#v", instances)
	}
	bindings := composition.Manifest().ResourceBindings()
	if len(bindings) != 2 || bindings[0].Namespace() != "implementations" || bindings[0].ParameterName() != "primary" || bindings[1].Namespace() != "instances" || bindings[1].ParameterName() != "retained" {
		t.Fatalf("effective Resource bindings = %#v", bindings)
	}
	wantRemovals := map[string]bool{
		`resources.instances["database.legacy"]`:                                true,
		`resources.bind.implementations["example.com/service.New"]["obsolete"]`: true,
		`resources.bind.implementations["example.com/service.New"]["absent"]`:   true,
		`resources.bind.instances["database.primary"]["obsolete"]`:              true,
		`resources.bind.instances["database.primary"]["absent"]`:                true,
	}
	decisions, err := applicationmeta.ConfigurationDecisions(composition.Manifest(), lookup)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range decisions {
		if _, ok := wantRemovals[decision.Path()]; !ok {
			continue
		}
		if !decision.Removed() || decision.Summary() != applicationmeta.ConfigurationSummaryRemoval || decision.Source() != "plystra.production.yaml "+decision.Path() {
			t.Fatalf("removal decision = %#v", decision)
		}
		delete(wantRemovals, decision.Path())
	}
	if len(wantRemovals) != 0 {
		t.Fatalf("missing exact removal decisions: %v", wantRemovals)
	}
}
