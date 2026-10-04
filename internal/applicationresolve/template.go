package applicationresolve

import (
	"errors"
	"fmt"
	"strings"

	"github.com/plystra/cli/internal/applicationmeta"
)

var (
	ErrTemplate           = errors.New("resolve template ancestry")
	ErrTemplateCycle      = errors.New("template ancestry repeats a Project module")
	ErrTemplateNotFound   = errors.New("template module is absent from the effective Go Module graph")
	ErrTemplateNotProject = errors.New("template module is not a Plystra Project")
)

// TemplateError retains only module identities and declaration sources, never
// configuration values or local dependency paths.
type TemplateError struct {
	cause   error
	modules []string
	sources []applicationmeta.ConfigurationDeclarationSource
}

func newTemplateError(cause error, modules []string, sources []applicationmeta.ConfigurationDeclarationSource) *TemplateError {
	return &TemplateError{cause: cause, modules: append([]string(nil), modules...), sources: append([]applicationmeta.ConfigurationDeclarationSource(nil), sources...)}
}

func (e *TemplateError) Error() string {
	if e == nil {
		return ErrTemplate.Error()
	}
	return fmt.Sprintf("%s: %s: %s", ErrTemplate, e.cause, strings.Join(e.modules, " -> "))
}

func (e *TemplateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *TemplateError) Is(target error) bool { return e != nil && target == ErrTemplate }

// Sources returns each relationship declaration in nearest-to-oldest traversal
// order, including the final invalid edge.
func (e *TemplateError) Sources() []applicationmeta.ConfigurationDeclarationSource {
	if e == nil {
		return nil
	}
	return append([]applicationmeta.ConfigurationDeclarationSource(nil), e.sources...)
}

// Modules returns the complete traversed chain, including the invalid target.
func (e *TemplateError) Modules() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.modules...)
}
