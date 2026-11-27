// Package version reports which source a build of the app or inkunactl came
// from. The build stamps it with -ldflags -X (Taskfile.yml's LDFLAGS), so both
// binaries read the same two variables and `inkunactl update` can tell whether
// an install is behind.
package version

// Dev is what a build without stamps, such as `go run`, reports for both its
// version and its commit.
const Dev = "dev"

// version and commit are set at link time:
//
//	-X github.com/inkuna-ai/inkuna/internal/version.version=$(git describe --tags --always --dirty)
//	-X github.com/inkuna-ai/inkuna/internal/version.commit=$(git rev-parse HEAD)
//
// They are variables rather than constants because -X can only set a string
// variable, and unexported so nothing else in the program can change them.
var (
	version string
	commit  string
)

// Info is the version and commit a build was made from.
type Info struct {
	// Version is `git describe --tags --always --dirty` at build time: the
	// nearest tag, or the short commit until the repository has tags, with
	// "-dirty" when the checkout had uncommitted changes.
	Version string `json:"version"`
	// Commit is the full commit the build was made from.
	Commit string `json:"commit"`
}

// Current is this build's version and commit, with Dev for whichever the build
// did not stamp.
func Current() Info {
	return info(version, commit)
}

func info(version, commit string) Info {
	if version == "" {
		version = Dev
	}
	if commit == "" {
		commit = Dev
	}
	return Info{Version: version, Commit: commit}
}

// String is the version and commit on one line, for a health check or a log.
func (info Info) String() string {
	return info.Version + " (commit " + info.Commit + ")"
}
