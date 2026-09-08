package applicationmeta

import (
	"errors"
	"reflect"
	"testing"
)

func TestValidateCurrentConstructorConfigDecisionRetainsBothProjectSources(t *testing.T) {
	t.Parallel()

	lowerSource := ConfigurationDeclarationSource{modulePath: "example.com/dependency", path: "plystra.yaml", line: 1, column: 1}
	currentSource := ConfigurationDeclarationSource{modulePath: "example.com/app", path: "plystra.production.yaml", line: 1, column: 1}
	lower := constructorConfigDecision{
		kind:              constructorConfigValue,
		valueType:         "string:string",
		source:            `example.com/dependency@v1.0.0/plystra.yaml config["example.com/smtp.New"]["settings"]`,
		declarationSource: lowerSource,
	}
	current := constructorConfigDecision{
		kind:              constructorConfigObject,
		valueType:         "example.com/settings.Config",
		source:            `plystra.production.yaml config["example.com/smtp.New"]["settings"]`,
		declarationSource: currentSource,
	}
	path := `config["example.com/smtp.New"]["settings"]`
	err := validateCurrentConstructorConfigDecision(path, current, map[string]*constructorConfigCandidate{
		"lower": {
			decision:     lower,
			sources:      map[string]struct{}{lower.source: {}},
			declarations: configurationDeclarationSources{lowerSource: {}},
		},
	})
	var conflict *InheritedConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrInheritedConflict) || conflict.Field() != path {
		t.Fatalf("validateCurrentConstructorConfigDecision = %v", err)
	}
	if got := conflict.Sources(); !reflect.DeepEqual(got, []ConfigurationDeclarationSource{currentSource, lowerSource}) {
		t.Fatalf("Sources = %#v", got)
	}
}
