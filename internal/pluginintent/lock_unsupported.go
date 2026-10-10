//go:build !unix && !windows

package pluginintent

import "os"

func tryLock(*os.File) error { return ErrUnsupported }
func unlock(*os.File) error  { return ErrUnsupported }
