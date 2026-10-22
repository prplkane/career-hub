//go:build !windows

package runtimepaths

import "os"

const volumeCheckSupported = false

func checkLocalVolume(*os.File) *Error { return failure("VOLUME_PLATFORM_UNSUPPORTED") }
