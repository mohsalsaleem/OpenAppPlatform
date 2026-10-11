package routing

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// Directory authority comes only from trusted host configuration, never the API.
// openat/O_NOFOLLOW pins the directory and rejects symbolic links.
type File struct {
	directory *os.File
	name      string
}

func Open(b Binding) (*File, error) {
	if !namePattern.MatchString(b.Name) {
		return nil, errors.New("invalid native route name")
	}
	fd, e := unix.Open(b.Directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errors.New("native route directory is unavailable")
	}
	return &File{os.NewFile(uintptr(fd), b.Directory), b.Name + ".yaml"}, nil
}
func (f *File) Close() { f.directory.Close() }
func (f *File) Identity() (string, error) {
	var stat unix.Stat_t
	if e := unix.Fstat(int(f.directory.Fd()), &stat); e != nil {
		return "", e
	}
	return Hash([]byte(fmt.Sprintf("%d:%d", stat.Dev, stat.Ino))), nil
}
func (f *File) Read() ([]byte, error) {
	fd, e := unix.Openat(int(f.directory.Fd()), f.name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, errors.New("native route is unavailable or is a symbolic link")
	}
	file := os.NewFile(uintptr(fd), f.name)
	defer file.Close()
	stat, e := file.Stat()
	if e != nil || !stat.Mode().IsRegular() {
		return nil, errors.New("native route must be a regular file")
	}
	data, e := io.ReadAll(io.LimitReader(file, 65537))
	if e != nil || len(data) > 65536 {
		return nil, errors.New("native route exceeds bounded configuration size")
	}
	return data, nil
}
func (f *File) Publish(before string, desired []byte) (string, error) {
	if len(desired) > 65536 {
		return "", errors.New("native route exceeds bounded configuration size")
	}
	current, e := f.Read()
	if e != nil {
		return "", e
	}
	if Hash(current) == Hash(desired) {
		if e = f.directory.Sync(); e != nil {
			return "", errors.New("native route durability is uncertain")
		}
		return "alreadyApplied", nil
	}
	if Hash(current) != before {
		return "", errors.New("native route drift detected; inspect before retrying")
	}
	// API publishers are serialized by the durable application fence. External
	// native editors do not participate; this is not universal compare-and-swap.
	name := ".oap-route-" + rand.Text()
	fd, e := unix.Openat(int(f.directory.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0644)
	if e != nil {
		return "", errors.New("native route directory is not writable")
	}
	temp := os.NewFile(uintptr(fd), name)
	defer unix.Unlinkat(int(f.directory.Fd()), name, 0)
	defer temp.Close()
	if e = temp.Chmod(0644); e != nil {
		return "", e
	}
	if _, e = temp.Write(desired); e != nil {
		return "", e
	}
	if e = temp.Sync(); e != nil {
		return "", e
	}
	current, e = f.Read()
	if e != nil {
		return "", e
	}
	if Hash(current) != before {
		return "", errors.New("native route changed during publication")
	}
	if e = unix.Renameat(int(f.directory.Fd()), name, int(f.directory.Fd()), f.name); e != nil {
		return "", e
	}
	if e = f.directory.Sync(); e != nil {
		return "", e
	}
	current, e = f.Read()
	if e != nil || Hash(current) != Hash(desired) {
		return "", errors.New("native route publication outcome is uncertain")
	}
	return "applied", nil
}
