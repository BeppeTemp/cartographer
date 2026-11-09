//go:build windows

package safewrite

import (
	"os"
	"syscall"
)

// openNoFollow opens path for writing, creating it, without following a reparse
// point (FILE_FLAG_OPEN_REPARSE_POINT opens the point itself), and without
// truncating (OPEN_ALWAYS). A handle on a reparse point or a directory is
// closed and refused. perm has no Windows meaning, as for os.OpenFile.
func openNoFollow(path string, _ os.FileMode) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var fi syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &fi); err != nil {
		syscall.CloseHandle(h)
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	if fi.FileAttributes&(syscall.FILE_ATTRIBUTE_REPARSE_POINT|syscall.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		syscall.CloseHandle(h)
		return nil, errNotPlain
	}
	return os.NewFile(uintptr(h), path), nil
}
