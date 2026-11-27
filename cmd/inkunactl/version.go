package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/inkuna-ai/inkuna/internal/storage"
	"github.com/inkuna-ai/inkuna/internal/version"
)

const versionUsage = "usage: inkunactl version [--json]"

// buildReport is what inkunactl version --json prints: the build's version
// and commit, and the newest database migration it knows, which a rollback
// compares with the database before restoring an older build.
type buildReport struct {
	version.Info
	Migration string `json:"migration"`
}

// runVersion prints the version and commit this build was made from. --json
// is for the install and update commands, which compare it with the latest
// release.
func runVersion(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	asJSON := flags.Bool("json", false, "print the version and commit as JSON")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w\n%s", err, versionUsage)
	}
	if flags.NArg() != 0 {
		return errors.New(versionUsage)
	}
	info := version.Current()
	if *asJSON {
		return writeJSON(stdout, buildReport{Info: info, Migration: storage.LatestMigration()})
	}
	_, err := fmt.Fprintf(stdout, "version   %s\ncommit    %s\nmigration %s\n", info.Version, info.Commit, storage.LatestMigration())
	return err
}
