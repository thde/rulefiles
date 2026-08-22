package rulefile

import "errors"

var (
	// ErrSyntax indicates invalid rule file syntax.
	ErrSyntax = errors.New("invalid syntax")

	// ErrUnsupported indicates an unsupported feature.
	ErrUnsupported = errors.New("unsupported")
)
