//go:build !windows

package modules

import "os"

func replaceFile(source, target string) error { return os.Rename(source, target) }
