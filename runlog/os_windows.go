package runlog

import (
	"errors"
	"fmt"
)

// modesRefused refuses a Perm or DirPerm: a Go file mode on Windows sets only the read-only bit, so a mode
// set here would be accepted and ignored.
func (l Log) modesRefused() error {
	if l.Perm != 0 || l.DirPerm != 0 {
		return fmt.Errorf("runlog: Perm and DirPerm are unix only, and the log takes its folder's access list here: %w", errors.ErrUnsupported)
	}
	return nil
}
