//go:build !windows

package runlog

// modesRefused is nil: Perm and DirPerm are the modes of the log and its folder.
func (l Log) modesRefused() error { return nil }
