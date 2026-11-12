package extension

import (
	"os"
	"path/filepath"
)

// reachableByNobody opens dir and its parents up to the temporary directory
// so that an extension dropped to nobody can run a binary in it. It matters
// only when the tests run as root: oapd then runs extensions as nobody, and
// Go's temporary directories are private to their owner.
func reachableByNobody(dir string) {
	if os.Geteuid() != 0 {
		return
	}
	stop := filepath.Clean(os.TempDir())
	for d := filepath.Clean(dir); d != stop && d != filepath.Dir(d); d = filepath.Dir(d) {
		os.Chmod(d, 0o755)
	}
}
