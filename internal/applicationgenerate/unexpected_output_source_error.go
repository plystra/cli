package applicationgenerate

const unexpectedOutputSourceKind = "generated-artifact"

// UnexpectedOutputSourceError attaches the current Project identity and every
// unowned generated path to a strict generation failure without changing its
// human message or typed error chain.
type UnexpectedOutputSourceError struct {
	modulePath string
	paths      []string
	cause      error
}

// ModulePath returns the current Project module containing the unowned paths.
func (e *UnexpectedOutputSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// Paths returns defensive, sorted module-relative generated paths.
func (e *UnexpectedOutputSourceError) Paths() []string {
	if e == nil {
		return nil
	}
	return append([]string(nil), e.paths...)
}

// SourceKind returns the canonical diagnostic source category.
func (e *UnexpectedOutputSourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return unexpectedOutputSourceKind
}

func (e *UnexpectedOutputSourceError) Error() string {
	if e == nil || e.cause == nil {
		return "unexpected generated output"
	}
	return e.cause.Error()
}

// Unwrap preserves the original generated-output failure chain.
func (e *UnexpectedOutputSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func unexpectedOutputSourceError(modulePath string, paths []string, cause error) error {
	if modulePath == "" || len(paths) == 0 || cause == nil {
		return cause
	}
	return &UnexpectedOutputSourceError{
		modulePath: modulePath,
		paths:      append([]string(nil), paths...),
		cause:      cause,
	}
}
