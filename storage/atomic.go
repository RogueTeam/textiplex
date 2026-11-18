package storage

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/RogueTeam/textiplex/pointers"
	"github.com/zeebo/xxh3"
	"golang.org/x/sys/unix"
)

var (
	// The file is not a textiplex index or is damaged. Every Load and Verify
	// error caused by the file's contents wraps it.
	ErrCorrupt = errors.New("storage: corrupt index")
	// The file was written by a newer version of textiplex.
	ErrUnsupportedVersion = errors.New("storage: unsupported index version")
	// The file's contents do not match the checksum in its footer.
	ErrChecksum = fmt.Errorf("%w: checksum mismatch", ErrCorrupt)
	// A document id or token is longer than MaxRawValueSize bytes.
	ErrValueTooLong = errors.New("storage: value longer than MaxRawValueSize bytes")
	// More documents than a uint32 internal document index can address.
	ErrTooManyDocuments = errors.New("storage: more than 4294967295 documents")
)

func corruptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}

// atomicFile is written under a temporary name in the destination directory
// and only appears under its final name, complete and on disk, once Commit
// succeeds. Until then the destination (and anything mapping it) is untouched.
type atomicFile struct {
	*os.File
	final     string
	noReplace bool
	done      bool
}

func createAtomic(name string, noReplace bool) (af *atomicFile, err error) {
	var mode os.FileMode = 0o666 // narrowed by the umask, like os.Create
	var replacing bool
	if noReplace {
		if _, err := os.Lstat(name); err == nil {
			return nil, &os.PathError{Op: "create", Path: name, Err: os.ErrExist}
		}
	} else if info, err := os.Stat(name); err == nil {
		// Replacing a file: write through a symlink to its target, as
		// os.Create did, and keep the existing file's permissions.
		if resolved, err := filepath.EvalSymlinks(name); err == nil {
			name = resolved
		}
		mode = info.Mode().Perm()
		replacing = true
	}

	dir, base := filepath.Split(name)
	if dir == "" {
		dir = "."
	}
	for range 100 {
		tmp := filepath.Join(dir, "."+base+"."+strconv.FormatUint(rand.Uint64(), 36)+tempSuffix)
		file, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create temporary file: %w", err)
		}
		if replacing {
			// OpenFile applied the umask; restore the replaced file's exact bits.
			if err = file.Chmod(mode); err != nil {
				file.Close()
				os.Remove(tmp)
				return nil, fmt.Errorf("failed to set permissions: %w", err)
			}
		}
		return &atomicFile{File: file, final: name, noReplace: noReplace}, nil
	}
	return nil, fmt.Errorf("failed to create a temporary file next to %s", name)
}

// Suffix of the temporary files SaveTo and Merge write before renaming them
// into place. A crash can leave one behind; see IsTemporaryFile.
const tempSuffix = ".tmp"

// IsTemporaryFile reports whether name (a base name) is a temporary file left
// by SaveTo or Merge for the final file final (also a base name): one that was
// never renamed into place because the write failed or the process died.
func IsTemporaryFile(name, final string) bool {
	rest, ok := strings.CutPrefix(name, "."+final+".")
	if !ok {
		return false
	}
	random, ok := strings.CutSuffix(rest, tempSuffix)
	if !ok || random == "" {
		return false
	}
	for _, c := range random {
		if (c < '0' || c > '9') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

// Commit makes the file durable and visible under its final name. With
// noReplace it fails with an error matching os.ErrExist if the name is taken.
// If Commit fails with noReplace, the final name does not hold the file.
func (af *atomicFile) Commit() (err error) {
	if err = af.File.Sync(); err != nil {
		return fmt.Errorf("failed to sync file: %w", err)
	}
	err = af.File.Close()
	af.done = true
	if err != nil {
		os.Remove(af.File.Name())
		return fmt.Errorf("failed to close file: %w", err)
	}

	if af.noReplace {
		err = renameNoReplace(af.File.Name(), af.final)
	} else {
		err = os.Rename(af.File.Name(), af.final)
	}
	if err != nil {
		os.Remove(af.File.Name())
		return err
	}
	if err = syncDir(filepath.Dir(af.final)); err != nil {
		if af.noReplace {
			// The rename may not be durable. Nothing was at the name before,
			// so undo it: the caller sees a clean failure and can retry.
			os.Remove(af.final)
		}
		return err
	}
	return nil
}

// Abort discards the temporary file. It is a no-op after Commit.
func (af *atomicFile) Abort() {
	if af == nil || af.done {
		return
	}
	af.done = true
	af.File.Close()
	os.Remove(af.File.Name())
}

func renameNoReplace(oldpath, newpath string) (err error) {
	err = unix.Renameat2(unix.AT_FDCWD, oldpath, unix.AT_FDCWD, newpath, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EOPNOTSUPP):
		// Filesystem without RENAME_NOREPLACE: link(2) also refuses to replace.
		if err = os.Link(oldpath, newpath); err != nil {
			return err
		}
		return os.Remove(oldpath)
	default:
		return &os.LinkError{Op: "rename", Old: oldpath, New: newpath, Err: err}
	}
}

// syncDir makes a create, rename or unlink in dir durable.
func syncDir(dir string) (err error) {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open directory for sync: %w", err)
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return fmt.Errorf("failed to sync directory: %w", err)
	}
	return nil
}

// SyncDir makes creates, renames and removals in dir durable.
func SyncDir(dir string) error { return syncDir(dir) }

// appendFooter checksums everything written to file so far and appends the
// VersionV2 footer.
func appendFooter(file *os.File) (err error) {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}
	size := info.Size()

	var footer = Footer{Checksum: xxh3.Hash(nil), Magic: FooterMagic}
	if size > 0 {
		data, err := unix.Mmap(int(file.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			return fmt.Errorf("failed to map file for checksum: %w", err)
		}
		unix.Madvise(data, unix.MADV_SEQUENTIAL)
		footer.Checksum = xxh3.Hash(data)
		if err = unix.Munmap(data); err != nil {
			return fmt.Errorf("failed to unmap file after checksum: %w", err)
		}
	}

	if _, err = file.WriteAt(pointers.UnsafeValueBytes(&footer), size); err != nil {
		return fmt.Errorf("failed to write footer: %w", err)
	}
	return nil
}
