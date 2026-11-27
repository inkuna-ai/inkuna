package install

import (
	"errors"
	"path/filepath"

	"github.com/inkuna-ai/inkuna/internal/filelock"
)

// LockFile is the name of the install lock, which lives beside the
// installed inkunactl (Layout.BinDir). It is hidden, since that folder is on
// PATH.
const LockFile = ".inkuna-install.lock"

// ErrLocked is Lock's error when another install holds the lock.
var ErrLocked = errors.New("another inkunactl install is running")

// Lock takes the install lock in dir, so only one install at a time swaps
// builds into place there. The wizard locks the folder that holds the
// installed CLI: every install for a user puts its app and CLI in the same
// places, whatever data directory it uses, so the lock belongs with them. It fails at once with
// ErrLocked rather than waiting. The lock is the operating system's lock on
// an open file, which it releases when the process ends, so an install that
// is killed never leaves it held. unlock releases it.
func Lock(dir string) (unlock func(), err error) {
	unlock, err = filelock.Lock(filepath.Join(dir, LockFile))
	if errors.Is(err, filelock.ErrLocked) {
		return nil, ErrLocked
	}
	return unlock, err
}
