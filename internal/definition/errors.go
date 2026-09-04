package definition

import "errors"

var (
	// ErrInvalidManifest is a publisher error: the authored document is
	// malformed, contradicts itself, or tries to author a derived field.
	// Registration fails; the definition never reaches a materialization
	// state, because there is nothing coherent to materialize.
	ErrInvalidManifest = errors.New("definition: invalid manifest")

	// ErrMaterialMissing means the package tree does not carry a file the
	// manifest references. Distinct from ErrInvalidManifest because the
	// manifest itself is well-formed — the package is incomplete.
	ErrMaterialMissing = errors.New("definition: referenced material is missing")

	// ErrUnsupportedRange is a malformed compatibility range.
	ErrUnsupportedRange = errors.New("definition: unsupported version range")
)
