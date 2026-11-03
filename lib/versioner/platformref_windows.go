// Copyright (C) 2026 The Syncthing Authors.
// SPDX-License-Identifier: MPL-2.0

package versioner

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/syncthing/syncthing/lib/fs"
	"golang.org/x/sys/windows"
)

// NTFS extended attributes support an atomic update of this small private
// reference. Foreign platform metadata stays in the compressed sidecar; general
// Unix xattr synchronization remains unsupported on Windows.
const platformRefEA = "SYNCTHING.PLATFORM-HISTORY"
const platformRefLimit = 4096

func platformNTError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}

func openPlatformRef(fsys fs.Filesystem, name string, write bool) (windows.Handle, error) {
	if fsys.Type() != fs.FilesystemTypeBasic || strings.Contains(name, ":") {
		return 0, os.ErrInvalid
	}
	relative, err := fs.Canonicalize(name)
	if err != nil {
		return 0, err
	}
	info, err := fsys.Lstat(relative)
	if err != nil {
		return 0, err
	}
	if !info.IsRegular() {
		return 0, os.ErrInvalid
	}
	path, err := windows.UTF16PtrFromString(extendedPlatformPath(filepath.Join(fsys.URI(), relative)))
	if err != nil {
		return 0, err
	}
	access := uint32(windows.FILE_READ_EA | windows.FILE_READ_ATTRIBUTES)
	if write {
		access |= windows.GENERIC_READ | windows.GENERIC_WRITE
	}
	handle, err := windows.CreateFile(path, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	var basic windows.ByHandleFileInformation
	if err = windows.GetFileInformationByHandle(handle, &basic); err == nil && basic.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		err = os.ErrInvalid
	}
	if err != nil {
		windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func readPlatformRefData(fsys fs.Filesystem, name string) ([]byte, error) {
	handle, err := openPlatformRef(fsys, name, false)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(handle)
	// FILE_GET_EA_INFORMATION names only our private attribute, leaving all other
	// native metadata untouched and bounding the response even on untrusted files.
	query := make([]byte, 5+len(platformRefEA)+1)
	query[4] = byte(len(platformRefEA))
	copy(query[5:], platformRefEA)
	data := make([]byte, 8+len(platformRefEA)+1+platformRefLimit)
	var iosb windows.IO_STATUS_BLOCK
	err = windows.NtQueryEaFile(handle, &iosb, &data[0], uint32(len(data)), true, &query[0], uint32(len(query)), nil, true)
	if err == windows.STATUS_NO_EAS_ON_FILE || err == windows.STATUS_NONEXISTENT_EA_ENTRY || err == windows.STATUS_NO_MORE_EAS {
		return nil, nil
	}
	if err != nil {
		return nil, platformNTError(err)
	}
	offset := 8 + int(data[5]) + 1
	size := int(binary.LittleEndian.Uint16(data[6:8]))
	if offset > len(data) || size > len(data)-offset || size > platformRefLimit {
		return nil, errors.New("invalid platform history reference attribute")
	}
	return data[offset : offset+size], nil
}
func writePlatformRefData(fsys fs.Filesystem, name string, data []byte) error {
	if len(data) > platformRefLimit {
		return errors.New("platform history reference exceeds size limit")
	}
	handle, err := openPlatformRef(fsys, name, true)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return setPlatformRefEA(handle, data)
}
func setPlatformRefEA(handle windows.Handle, data []byte) error {
	// FILE_FULL_EA_INFORMATION: one atomic native EA update, never a truncate and
	// rewrite of an existing data stream. A zero-length value removes the EA.
	buffer := make([]byte, 8+len(platformRefEA)+1+len(data))
	buffer[5] = byte(len(platformRefEA))
	binary.LittleEndian.PutUint16(buffer[6:8], uint16(len(data)))
	copy(buffer[8:], platformRefEA)
	copy(buffer[9+len(platformRefEA):], data)
	var iosb windows.IO_STATUS_BLOCK
	if err := windows.NtSetEaFile(handle, &iosb, &buffer[0], uint32(len(buffer))); err != nil {
		return platformNTError(err)
	}
	return windows.FlushFileBuffers(handle)
}
func removePlatformRefData(fsys fs.Filesystem, name string) error {
	return writePlatformRefData(fsys, name, nil)
}
func syncPlatformDir(fsys fs.Filesystem, path string) error {
	relative, err := fs.Canonicalize(path)
	if err != nil {
		return err
	}
	name, err := windows.UTF16PtrFromString(extendedPlatformPath(filepath.Join(fsys.URI(), relative)))
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.FlushFileBuffers(handle)
}
func extendedPlatformPath(path string) string {
	if strings.HasPrefix(path, `\\?\`) {
		return path
	}
	if strings.HasPrefix(path, `UNC\`) {
		return `\\?\` + path
	}
	if strings.HasPrefix(path, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(path, `\\`)
	}
	return `\\?\` + path
}
func syncPlatformFile(fsys fs.Filesystem, name string) error {
	var file fs.File
	// Keep the writable handle through restoration of the read-only attribute so
	// the final flush commits both the reference and the final permissions.
	err := fs.WithOwnerWrite(fsys, name, func() error { var err error; file, err = fsys.OpenFile(name, fs.OptReadWrite, 0); return err })
	if file == nil {
		return err
	}
	return errors.Join(err, file.Sync(), file.Close())
}
func platformRestoreRename(fsys fs.Filesystem, source, target string) error {
	info, err := fsys.Lstat(target)
	if fs.IsNotExist(err) {
		return fsys.Rename(source, target)
	}
	if err != nil {
		return err
	}
	mode := info.Mode() & fs.ModePerm
	if mode&0o200 == 0 {
		if err := fsys.Chmod(target, mode|0o200); err != nil {
			return err
		}
	}
	err = fsys.Rename(source, target)
	if err != nil && mode&0o200 == 0 {
		return errors.Join(err, fsys.Chmod(target, mode))
	}
	return err
}
