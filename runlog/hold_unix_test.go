//go:build unix

package runlog

import "github.com/birdtable/go-files/lock"

// takeHold is the hold Append takes on a log on unix: flock on the file, which go-files' lock takes.
func takeHold(path string) (release func() error, err error) {
	h, err := lock.Take(path, "")
	if err != nil {
		return nil, err
	}
	return h.Release, nil
}
