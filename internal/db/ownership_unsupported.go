//go:build !unix

package db

import "os"

// Platforms without flock get a refusal rather than a permissive stub. A
// maintenance path that suspends immutability triggers must not run where it
// cannot establish that nothing else is writing.
func tryLockExclusive(*os.File) (bool, error) {
	return false, ErrOwnershipUnsupported
}

func unlock(*os.File) error {
	return ErrOwnershipUnsupported
}
