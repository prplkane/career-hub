//go:build !windows && !linux && !darwin

package runtimepaths

import "io/fs"

// Open rejects unsupported native platforms before any file operation.
func isLinkLike(info fs.FileInfo) bool { return true }
func readOnlyFlags() int               { return 0 }
