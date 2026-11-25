package runlog

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// plantLink makes link a junction, a mount point reparse point, to the folder target, with
// FSCTL_SET_REPARSE_POINT: any account may make one, where a symbolic link needs a privilege.
func plantLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Mkdir(link, 0o755); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	abs, err := filepath.Abs(target)
	if err != nil {
		t.Fatal(err)
	}
	sub := utf16.Encode([]rune(`\??\` + abs))
	shown := utf16.Encode([]rune(abs))
	// The path buffer holds the substitute name and the print name, each ending in a NUL.
	names := make([]uint16, 0, len(sub)+len(shown)+2)
	names = append(append(append(append(names, sub...), 0), shown...), 0)
	data := make([]byte, 0, 16+2*len(names))
	data = binary.LittleEndian.AppendUint32(data, windows.IO_REPARSE_TAG_MOUNT_POINT)
	data = binary.LittleEndian.AppendUint16(data, uint16(8+2*len(names)))
	data = binary.LittleEndian.AppendUint16(data, 0) // reserved
	data = binary.LittleEndian.AppendUint16(data, 0) // the substitute name's offset
	data = binary.LittleEndian.AppendUint16(data, uint16(2*len(sub)))
	data = binary.LittleEndian.AppendUint16(data, uint16(2*(len(sub)+1)))
	data = binary.LittleEndian.AppendUint16(data, uint16(2*len(shown)))
	for _, u := range names {
		data = binary.LittleEndian.AppendUint16(data, u)
	}
	var got uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &data[0], uint32(len(data)), nil, 0, &got, nil); err != nil {
		t.Fatal(err)
	}
}
