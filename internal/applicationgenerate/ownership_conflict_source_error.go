package applicationgenerate

const ownershipConflictSourceKind = "generated-artifact"

// OwnershipConflictSourceError attaches the current Project identity and the
// conflicting generated path to an installation failure without changing its
// human message or typed error chain.
type OwnershipConflictSourceError struct {
	modulePath string
	sourcePath string
	cause      error
}

// ModulePath returns the current Project module containing the conflict.
func (e *OwnershipConflictSourceError) ModulePath() string {
	if e == nil {
		return ""
	}
	return e.modulePath
}

// SourcePath returns the canonical module-relative generated path.
func (e *OwnershipConflictSourceError) SourcePath() string {
	if e == nil {
		return ""
	}
	return e.sourcePath
}

// SourceKind returns the canonical diagnostic source category.
func (e *OwnershipConflictSourceError) SourceKind() string {
	if e == nil {
		return ""
	}
	return ownershipConflictSourceKind
}

// Line returns zero because generated ownership conflicts have no source span.
func (e *OwnershipConflictSourceError) Line() int { return 0 }

// Column returns zero because generated ownership conflicts have no source span.
func (e *OwnershipConflictSourceError) Column() int { return 0 }

func (e *OwnershipConflictSourceError) Error() string {
	if e == nil || e.cause == nil {
		return "generated ownership conflict"
	}
	return e.cause.Error()
}

// Unwrap preserves the original generated-file installation failure chain.
func (e *OwnershipConflictSourceError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func ownershipConflictSourceError(modulePath, sourcePath string, cause error) error {
	if modulePath == "" || sourcePath == "" || cause == nil {
		return cause
	}
	return &OwnershipConflictSourceError{
		modulePath: modulePath,
		sourcePath: sourcePath,
		cause:      cause,
	}
}
