package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
)

// SettingsTrailer is the commit trailer a settings share carries, holding
// the digest of what it shared, so the branch itself records the share and
// the same edits are never shared twice.
const SettingsTrailer = "Inkuna-Settings"

// Kinds of SettingsChange, against the checkout's HEAD.
const (
	ChangeAdded    = "added"
	ChangeModified = "modified"
	ChangeDeleted  = "deleted"
)

// Settings names the files of a checkout that hold its repository's Inkuna
// settings: everything under Dir, the repository's .inkuna directory, apart
// from its out directory, and Files, such as a rules file inkuna.yaml keeps
// elsewhere. Paths are slash-separated and relative to the checkout's root.
// Inkuna reads settings from the project checkout and its views save them
// there, so edits to them sit uncommitted in it until they are shared.
type Settings struct {
	Dir   string
	Files []string
}

// DefaultSettings is a repository at the root of its checkout that keeps
// every setting in .inkuna.
var DefaultSettings = Settings{Dir: ".inkuna"}

// Holds reports whether a slash-separated path relative to the checkout's
// root is a setting.
func (settings Settings) Holds(file string) bool {
	if slices.Contains(settings.Files, file) {
		return true
	}
	if settings.Dir == "" {
		return false
	}
	out := settings.Dir + "/out"
	if file == out || strings.HasPrefix(file, out+"/") {
		return false
	}
	return strings.HasPrefix(file, settings.Dir+"/")
}

// SettingsChange is one uncommitted change to a setting in a checkout.
type SettingsChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	// OnBase says the base already has the file as the checkout does, so
	// there is nothing to share and the next update of the checkout's
	// branch takes it as merged.
	OnBase bool `json:"onBase,omitempty"`
	// Conflict says the base changed the file after the checkout's commit,
	// so sharing the edit would undo that change.
	Conflict bool `json:"conflict,omitempty"`
	// entry is the file as the checkout has it; empty for a deletion.
	entry treeEntry
}

// SettingsStatus is a checkout's uncommitted settings changes, measured
// against the base they would be shared to.
type SettingsStatus struct {
	BaseRef string           `json:"baseRef,omitempty"`
	Changes []SettingsChange `json:"changes"`
	// Digest identifies what a share would commit: the paths not yet on
	// the base and their content. A share commits only the digest the
	// user confirmed, so an edit made after they looked is never pushed.
	Digest    string `json:"digest,omitempty"`
	Shareable bool   `json:"shareable"`
	Reason    string `json:"reason,omitempty"`
	// SharedOn names the local branch an earlier share committed exactly
	// these changes on.
	SharedOn string `json:"sharedOn,omitempty"`
	// baseCommit is BaseRef as it was measured, which a share commits on,
	// so a fetch made meanwhile cannot slip in a change that was never
	// checked for conflicts.
	baseCommit string
}

// Pending lists the changes a share would commit: those the base lacks.
func (status SettingsStatus) Pending() []SettingsChange {
	pending := []SettingsChange{}
	for _, change := range status.Changes {
		if !change.OnBase {
			pending = append(pending, change)
		}
	}
	return pending
}

// ReadSettings measures a checkout's uncommitted settings changes against
// baseRef, the remote-tracking base a share would commit on, or "" when
// there is none. It reads the working files without writing anything, not
// even objects, so the Branches view can call it on every refresh.
func ReadSettings(ctx context.Context, checkout, baseRef string, settings Settings) (SettingsStatus, error) {
	status := SettingsStatus{BaseRef: baseRef, Changes: []SettingsChange{}}
	entries, err := statusEntries(ctx, checkout)
	if err != nil {
		return SettingsStatus{}, err
	}
	paths := []string{}
	untracked := map[string]bool{}
	for _, entry := range entries {
		if settings.Holds(entry.path) {
			paths = append(paths, entry.path)
			untracked[entry.path] = entry.untracked()
		}
	}
	if len(paths) == 0 {
		return status, nil
	}
	sort.Strings(paths)
	head, err := treeEntries(ctx, checkout, "HEAD", paths)
	if err != nil {
		return SettingsStatus{}, err
	}
	base := map[string]treeEntry{}
	if baseRef != "" {
		if status.baseCommit, err = git(ctx, checkout, "rev-parse", "--verify", baseRef+"^{commit}"); err != nil {
			return SettingsStatus{}, err
		}
		if base, err = treeEntries(ctx, checkout, status.baseCommit, paths); err != nil {
			return SettingsStatus{}, err
		}
	}
	work, irregular, err := workingEntries(ctx, checkout, paths, false, head)
	if err != nil {
		return SettingsStatus{}, err
	}
	conflicts := []string{}
	for _, file := range paths {
		change := SettingsChange{Path: file, Kind: ChangeModified, entry: work[file]}
		_, inHead := head[file]
		_, inWork := work[file]
		odd := slices.Contains(irregular, file)
		switch {
		case untracked[file] || !inHead:
			change.Kind = ChangeAdded
		case !inWork && !odd:
			change.Kind = ChangeDeleted
		}
		if baseRef != "" && !odd {
			change.OnBase = work[file] == base[file]
			change.Conflict = !change.OnBase && head[file] != base[file]
		}
		if change.Conflict {
			conflicts = append(conflicts, file)
		}
		status.Changes = append(status.Changes, change)
	}
	pending := status.Pending()
	switch {
	case baseRef == "":
		status.Reason = "There is no base branch on origin to share them to."
	case len(irregular) > 0:
		status.Reason = fmt.Sprintf("%s is not a regular file, so Inkuna does not share it; commit it yourself.", listPaths(irregular))
	case len(pending) == 0:
		status.Reason = fmt.Sprintf("%s already has these edits; update the checkout's branch to take them as merged.", baseRef)
	case len(conflicts) > 0:
		status.Reason = fmt.Sprintf("%s changed %s after the checkout's commit, so sharing the edit would undo that change. Copy the edit aside, discard it, update, and make it again.", baseRef, listPaths(conflicts))
	default:
		status.Shareable = true
	}
	if len(pending) == 0 {
		return status, nil
	}
	status.Digest = settingsDigest(pending)
	status.SharedOn, err = sharedOn(ctx, checkout, status.Digest)
	return status, err
}

// CommitSettings commits the pending changes of status on top of the base
// commit status measured, without touching the checkout: its branch, index,
// and files stay as they are, and the edits stay in effect there. It writes
// the working files as objects and refuses if any differs from what status
// measured, which is what the user confirmed. The commit is signed when the
// repository's commit.gpgSign asks for it, and is returned without being
// pushed.
func CommitSettings(ctx context.Context, checkout string, status SettingsStatus, subject, body string) (string, error) {
	pending := status.Pending()
	parent := status.baseCommit
	if len(pending) == 0 || parent == "" {
		return "", errors.New("there are no settings changes to commit on a base")
	}
	paths := make([]string, 0, len(pending))
	known := make(map[string]treeEntry, len(pending))
	for _, change := range pending {
		paths = append(paths, change.Path)
		known[change.Path] = change.entry
	}
	work, irregular, err := workingEntries(ctx, checkout, paths, true, known)
	if err != nil {
		return "", err
	}
	for _, change := range pending {
		if slices.Contains(irregular, change.Path) || work[change.Path] != change.entry {
			return "", fmt.Errorf("%s changed while it was being shared; look at it again", change.Path)
		}
	}
	// A private index builds the tree, so the checkout's own index is never
	// read or written.
	scratch, err := os.MkdirTemp("", "inkuna-settings-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(scratch) }()
	env := []string{"GIT_INDEX_FILE=" + filepath.Join(scratch, "index")}
	if _, err := gitEnv(ctx, checkout, env, "read-tree", parent); err != nil {
		return "", err
	}
	var written, removed []string
	for _, change := range pending {
		if change.entry == (treeEntry{}) {
			removed = append(removed, change.Path)
		} else {
			written = append(written, "--cacheinfo", change.entry.mode+","+change.entry.object+","+change.Path)
		}
	}
	// Removals go first, so a file that became a directory, or the other
	// way round, is gone from the index before its replacement arrives.
	if len(removed) > 0 {
		if _, err := gitEnv(ctx, checkout, env, append([]string{"update-index", "--force-remove", "--"}, removed...)...); err != nil {
			return "", err
		}
	}
	if len(written) > 0 {
		if _, err := gitEnv(ctx, checkout, env, append([]string{"update-index", "--add"}, written...)...); err != nil {
			return "", err
		}
	}
	tree, err := gitEnv(ctx, checkout, env, "write-tree")
	if err != nil {
		return "", err
	}
	args := []string{"commit-tree", tree, "-p", parent, "-m", subject}
	if strings.TrimSpace(body) != "" {
		args = append(args, "-m", body)
	}
	args = append(args, "-m", SettingsTrailer+": "+status.Digest)
	// commit-tree, unlike commit, ignores commit.gpgSign, and a repository
	// that signs its commits may refuse an unsigned one on its base.
	if signed, _ := git(ctx, checkout, "config", "--type=bool", "commit.gpgSign"); signed == "true" {
		args = append(args, "-S")
	}
	return git(ctx, checkout, args...)
}

// PushCommit pushes commit to branch on origin. Origin accepts it only as a
// fast-forward, so a branch that moved, or one that is protected, refuses
// it and nothing changes. Prompts are off, as for FetchOrigin.
func PushCommit(ctx context.Context, repository, commit, branch string) error {
	if strings.TrimSpace(branch) == "" || strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	_, err := gitEnv(ctx, repository, []string{"GIT_TERMINAL_PROMPT=0"}, "push", "--quiet", "origin", commit+":refs/heads/"+branch)
	if err != nil && signInFailed(err.Error()) {
		return fmt.Errorf("%w: %w", ErrNotSignedIn, err)
	}
	return err
}

// PublishBranch creates branch at commit, which must not exist yet, and
// pushes it to origin as its upstream. A branch whose push fails is deleted
// again, so a retry starts clean.
func PublishBranch(ctx context.Context, repository, branch, commit string) error {
	if strings.TrimSpace(branch) == "" || strings.HasPrefix(branch, "-") {
		return fmt.Errorf("invalid branch name %q", branch)
	}
	ref := "refs/heads/" + branch
	// An empty old value makes update-ref refuse a branch that exists.
	if _, err := git(ctx, repository, "update-ref", "-m", "inkuna: share settings", ref, commit, ""); err != nil {
		return err
	}
	_, err := gitEnv(ctx, repository, []string{"GIT_TERMINAL_PROMPT=0"}, "push", "--quiet", "-u", "origin", "--", branch)
	if err == nil {
		return nil
	}
	_, _ = git(context.Background(), repository, "update-ref", "-d", ref, commit)
	if signInFailed(err.Error()) {
		return fmt.Errorf("%w: %w", ErrNotSignedIn, err)
	}
	return err
}

// statusEntry is one path git status reports in a checkout.
type statusEntry struct {
	path string
	// index and work are git's X and Y status letters.
	index, work byte
}

func (entry statusEntry) untracked() bool { return entry.index == '?' }

// staged says the index holds a change of its own for the path.
func (entry statusEntry) staged() bool { return entry.index != ' ' && entry.index != '?' }

// statusEntries lists a checkout's uncommitted and untracked paths, relative
// to its root and listing every untracked file on its own. A rename shows as
// a deletion and an addition, so each path stands alone.
func statusEntries(ctx context.Context, checkout string) ([]statusEntry, error) {
	output, err := git(ctx, checkout, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	entries := []statusEntry{}
	for _, record := range strings.Split(output, "\x00") {
		if len(record) < 4 {
			continue
		}
		entries = append(entries, statusEntry{index: record[0], work: record[1], path: strings.TrimSuffix(record[3:], "/")})
	}
	return entries, nil
}

// treeEntry is a file as a tree, or a checkout, holds it.
type treeEntry struct {
	mode, object string
}

// treeEntries reads the listed paths from revision. A path the revision
// lacks is absent from the map, as is every path of an unborn HEAD.
func treeEntries(ctx context.Context, checkout, revision string, paths []string) (map[string]treeEntry, error) {
	entries := map[string]treeEntry{}
	if _, err := git(ctx, checkout, "rev-parse", "--verify", "-q", revision+"^{tree}"); err != nil {
		if revision == "HEAD" {
			return entries, nil
		}
		return nil, err
	}
	args := append([]string{"--literal-pathspecs", "ls-tree", "-z", "--full-tree", revision, "--"}, paths...)
	output, err := git(ctx, checkout, args...)
	if err != nil {
		return nil, err
	}
	for _, record := range strings.Split(output, "\x00") {
		meta, file, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			continue
		}
		entries[file] = treeEntry{mode: fields[0], object: fields[2]}
	}
	return entries, nil
}

// workingEntries hashes the listed paths as the checkout has them, as git
// add would, writing the objects only when write is set. A missing path, one
// under a directory that is now a file, or a directory, is absent from the
// map; anything else that is not a regular
// file is listed as irregular, since hashing would follow a link rather
// than record it. In a repository whose core.fileMode is false the execute
// bit means nothing, so a path keeps the mode known has for it, as git add
// would keep it.
func workingEntries(ctx context.Context, checkout string, paths []string, write bool, known map[string]treeEntry) (map[string]treeEntry, []string, error) {
	entries := map[string]treeEntry{}
	irregular := []string{}
	files := []string{}
	modes := map[string]string{}
	trustModes := true
	if value, err := git(ctx, checkout, "config", "--type=bool", "core.fileMode"); err == nil && value == "false" {
		trustModes = false
	}
	for _, file := range paths {
		info, err := os.Lstat(filepath.Join(checkout, filepath.FromSlash(file)))
		switch {
		case errors.Is(err, os.ErrNotExist), errors.Is(err, syscall.ENOTDIR), err == nil && info.IsDir():
			// A directory where a file was tracked is that file deleted;
			// git status lists what the directory holds on its own. So is a
			// file under a directory that is now a file, which Lstat
			// reports as not a directory.
			continue
		case err != nil:
			return nil, nil, err
		case !info.Mode().IsRegular():
			irregular = append(irregular, file)
			continue
		}
		switch {
		case !trustModes && known[file].mode != "":
			modes[file] = known[file].mode
		case trustModes && info.Mode().Perm()&0o100 != 0:
			modes[file] = "100755"
		default:
			modes[file] = "100644"
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		return entries, irregular, nil
	}
	args := []string{"hash-object"}
	if write {
		args = append(args, "-w")
	}
	output, err := git(ctx, checkout, append(append(args, "--"), files...)...)
	if err != nil {
		return nil, nil, err
	}
	objects := strings.Split(output, "\n")
	if len(objects) != len(files) {
		return nil, nil, fmt.Errorf("git hash-object returned %d objects for %d files", len(objects), len(files))
	}
	for index, file := range files {
		entries[file] = treeEntry{mode: modes[file], object: objects[index]}
	}
	return entries, irregular, nil
}

// settingsDigest identifies a set of changes by path and content.
func settingsDigest(changes []SettingsChange) string {
	hash := sha256.New()
	for _, change := range changes {
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%s\n", change.Path, change.entry.mode, change.entry.object)
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// sharedOn finds the local branch whose tip is a share of digest, in one
// for-each-ref however many branches there are.
func sharedOn(ctx context.Context, repository, digest string) (string, error) {
	output, err := git(ctx, repository, "for-each-ref", "--format=%(refname:short)%1f%(contents:trailers:key="+SettingsTrailer+",valueonly,separator=%x2C)", "refs/heads")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		name, values, ok := strings.Cut(line, "\x1f")
		if ok && slices.Contains(strings.Split(values, ","), digest) {
			return name, nil
		}
	}
	return "", nil
}

// overlapsAny reports whether a path is, contains, or lies inside any of
// the others: the cases in which writing one of them disturbs it.
func overlapsAny(file string, others []string) bool {
	for _, other := range others {
		if file == other || strings.HasPrefix(file, other+"/") || strings.HasPrefix(other, file+"/") {
			return true
		}
	}
	return false
}

// listPaths names a few paths for a message.
func listPaths(paths []string) string {
	const shown = 3
	if len(paths) <= shown {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(paths[:shown], ", "), len(paths)-shown)
}

// SettingsAt names the settings of a repository whose root is dir inside
// the checkout at root, plus files, such as a rules file kept outside
// .inkuna. Paths outside the checkout are left out.
func SettingsAt(root, dir string, files ...string) Settings {
	settings := Settings{Dir: ".inkuna"}
	if relative, ok := within(root, dir); ok && relative != "." {
		settings.Dir = path.Join(relative, ".inkuna")
	}
	for _, file := range files {
		if relative, ok := within(root, file); ok && relative != "." && !settings.Holds(relative) {
			settings.Files = append(settings.Files, relative)
		}
	}
	return settings
}

// within returns target relative to root, slash-separated, when it is
// inside root. Both are resolved first, since git reports a checkout by
// its resolved path and a project may name it through a link.
func within(root, target string) (string, bool) {
	// A file not written yet resolves through its nearest existing
	// directory, which may itself be reached through a link.
	resolve := func(value string) string {
		value, rest := filepath.Clean(value), ""
		for {
			if resolved, err := filepath.EvalSymlinks(value); err == nil {
				return filepath.Join(resolved, rest)
			}
			parent := filepath.Dir(value)
			if parent == value {
				return filepath.Join(value, rest)
			}
			rest = filepath.Join(filepath.Base(value), rest)
			value = parent
		}
	}
	relative, err := filepath.Rel(resolve(root), resolve(target))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(relative), true
}
