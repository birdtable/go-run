package runlog

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// takeHold is the hold Append takes on a log on Windows: LockFileEx on the one byte at 0x7fffffff00000000, which is
// not the first byte go-files' lock takes, so lock.Take does not block an Append there.
func takeHold(path string) (release func() error, err error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	far := &windows.Overlapped{Offset: 0, OffsetHigh: 0x7fffffff}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, far); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f.Close, nil
}
