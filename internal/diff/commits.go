package diff

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Commit is one commit on a run's branch.
type Commit struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"shortHash"`
	Subject   string `json:"subject"`
	Body      string `json:"body,omitempty"`
	Author    string `json:"author"`
	// Date is the author date, RFC 3339.
	Date string `json:"date"`
	// Merge marks a commit with more than one parent. Its counts, like its
	// commit scope, are against its first parent.
	Merge bool `json:"merge,omitempty"`
	// Files, Additions, and Deletions measure what the commit changed, as
	// its commit scope shows it.
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

// commitFormat starts each commit with a record separator and writes its
// fields apart with unit separators. The body comes last but one, since it
// is the only field that can hold newlines, and the --shortstat line git
// writes after it falls into the last.
const commitFormat = "--format=%x1e%H%x1f%h%x1f%P%x1f%an <%ae>%x1f%aI%x1f%s%x1f%b%x1f"

// shortstat reads git's " 3 files changed, 10 insertions(+), 2 deletions(-)",
// where either count is left out when it is zero.
var shortstat = regexp.MustCompile(`(\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?`)

// Commits lists the commits on the run's branch, oldest first: those from
// where it leaves its base to its tip, which are the ones a commit scope
// accepts. Once the branch is gone, the run's recorded commits stand in.
func Commits(ctx context.Context, repository string, run Run) ([]Commit, error) {
	base, err := baseOf(ctx, repository, run)
	if err != nil {
		return nil, err
	}
	tip, err := branchTip(ctx, repository, run.Branch)
	if err != nil {
		return nil, err
	}
	// A user's log.showSignature would write gpg's output among the
	// commits. The counts take the same diff flags as the Changes view and
	// measure a merge against its first parent, so each commit's counts
	// match what its commit scope shows.
	args := append([]string{"-c", "log.showSignature=false", "log", "--shortstat", "--diff-merges=first-parent", commitFormat}, diffFlags...)
	if tip == "" {
		hashes := make([]string, 0, len(run.Commits))
		for _, recorded := range run.Commits {
			if full, err := commitHash(ctx, repository, recorded); err == nil {
				hashes = append(hashes, full)
			}
		}
		if len(hashes) == 0 {
			return []Commit{}, nil
		}
		args = append(append(args, "--no-walk=unsorted"), hashes...)
	} else {
		floor, err := floorOf(ctx, repository, run.Branch, base, tip)
		if err != nil {
			return nil, err
		}
		args = append(args, "--reverse", "--topo-order", floor+".."+tip)
	}
	output, err := git(ctx, repository, nil, listLimit, append(args, "--")...)
	if err != nil {
		return nil, overLimit(err, "list the branch's commits")
	}
	commits := []Commit{}
	for _, record := range strings.Split(string(output), "\x1e")[1:] {
		fields := strings.Split(record, "\x1f")
		if len(fields) != 8 {
			return nil, fmt.Errorf("parse git log: unexpected record %q", record)
		}
		commit := Commit{Hash: fields[0], ShortHash: fields[1], Merge: len(strings.Fields(fields[2])) > 1, Author: fields[3], Date: fields[4], Subject: fields[5], Body: strings.TrimSpace(fields[6])}
		if counts := shortstat.FindStringSubmatch(fields[7]); counts != nil {
			commit.Files, _ = strconv.Atoi(counts[1])
			commit.Additions, _ = strconv.Atoi(counts[2])
			commit.Deletions, _ = strconv.Atoi(counts[3])
		}
		commits = append(commits, commit)
	}
	return commits, nil
}
