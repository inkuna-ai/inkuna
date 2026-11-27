package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/catalog"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/sources"
)

const catalogUsage = "usage: catalog list [REPOSITORY] | catalog show NAME [--source SOURCE] [REPOSITORY] | catalog add NAME [--global] [--source SOURCE] [REPOSITORY] | catalog sources [REPOSITORY]"

// catalogSummary is an item as `catalog list` prints it: without its
// content, which `catalog show` prints.
type catalogSummary struct {
	Name        string        `json:"name"`
	Kind        catalog.Kind  `json:"kind"`
	Description string        `json:"description"`
	Needs       catalog.Needs `json:"needs"`
	Source      string        `json:"source,omitempty"`
	Scope       string        `json:"scope,omitempty"`
}

// loadCatalog is the catalog the commands use; tests give one of their own,
// since what ships changes.
var loadCatalog = catalog.Builtin

// catalogArgs are a catalog command's arguments.
type catalogArgs struct {
	global     bool
	source     string
	positional []string
}

func parseCatalogArgs(args []string) (catalogArgs, error) {
	var parsed catalogArgs
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--global":
			parsed.global = true
		case arg == "--source":
			if index+1 == len(args) || strings.TrimSpace(args[index+1]) == "" {
				return catalogArgs{}, fmt.Errorf("--source needs a source's name; %s", catalogUsage)
			}
			index++
			parsed.source = args[index]
		case strings.HasPrefix(arg, "-"):
			return catalogArgs{}, fmt.Errorf("unknown flag %q; %s", arg, catalogUsage)
		default:
			parsed.positional = append(parsed.positional, arg)
		}
	}
	return parsed, nil
}

// runCatalog lists the catalog, prints one item, or adds one to a
// repository or, with --global, to this machine's global directories. A
// repository's catalog adds what its definition sources offered the app
// when it last asked them: inkunactl has no extensions to ask, so it reads
// the snapshots the app kept in its data directory.
func runCatalog(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(catalogUsage)
	}
	parsed, err := parseCatalogArgs(args[1:])
	if err != nil {
		return err
	}
	shipped, err := loadCatalog()
	if err != nil {
		return err
	}
	// The repository is the last argument, after the item's name for the
	// commands that take one.
	named := args[0] == "show" || args[0] == "add"
	positional := parsed.positional
	if named {
		if len(positional) == 0 {
			return errors.New(catalogUsage)
		}
		positional = positional[1:]
	}
	if len(positional) > 1 || (parsed.global && args[0] != "add") || (parsed.source != "" && !named) {
		return errors.New(catalogUsage)
	}
	if parsed.global && len(positional) == 1 {
		// A repository with --global would be ignored, so it is refused
		// rather than leave the user thinking it was used.
		return errors.New(catalogUsage)
	}
	repository := "."
	if len(positional) == 1 {
		repository = positional[0]
	}
	if repository, err = filepath.Abs(repository); err != nil {
		return fmt.Errorf("resolve repository: %w", err)
	}
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	reading, err := cachedSources(configuration.DataDir, repository)
	if err != nil {
		return err
	}
	switch args[0] {
	case "list":
		summaries := []catalogSummary{}
		for _, offered := range append([]catalog.Catalog{shipped}, sourceCatalogs(reading)...) {
			for _, item := range offered.Items() {
				summaries = append(summaries, catalogSummary{Name: item.Name, Kind: item.Kind, Description: item.Description, Needs: item.Needs, Source: item.Source, Scope: item.Scope})
			}
		}
		return writeJSON(stdout, summaries)
	case "sources":
		reports := reading.Reports
		if reports == nil {
			reports = []sources.Report{}
		}
		return writeJSON(stdout, reports)
	case "show":
		offered, err := findCatalog(shipped, reading, parsed.positional[0], parsed.source)
		if err != nil {
			return err
		}
		item, _ := offered.Get(parsed.positional[0])
		return writeJSON(stdout, item)
	case "add":
		offered, err := findCatalog(shipped, reading, parsed.positional[0], parsed.source)
		if err != nil {
			return err
		}
		target := catalog.Target{Global: parsed.global, AgentDirectories: configuration.AgentDirectories, StageDirectories: configuration.StageDirectories}
		if !parsed.global {
			target.RulesPath = rulesPath(configuration.File, repository)
			target.AgentDirectory = filepath.Join(repository, ".inkuna", "agents")
			target.SkillDirectory = filepath.Join(repository, ".inkuna", "skills")
		}
		result, err := offered.Add(parsed.positional[0], target)
		if err != nil {
			return err
		}
		return writeJSON(stdout, result)
	default:
		return errors.New(catalogUsage)
	}
}

// cachedSources reads the snapshots the app last kept for repository.
func cachedSources(dataDir, repository string) (sources.Reading, error) {
	entries, err := sources.ForRepository(dataDir, repository)
	if err != nil {
		return sources.Reading{}, err
	}
	states := make([]sources.State, 0, len(entries))
	for _, entry := range entries {
		states = append(states, sources.State{Source: entry.Source, Held: true, Cached: true, Snapshot: entry.Snapshot, FetchedAt: entry.FetchedAt})
	}
	return sources.Read(states), nil
}

func sourceCatalogs(reading sources.Reading) []catalog.Catalog {
	catalogs := make([]catalog.Catalog, 0, len(reading.Catalogs))
	for _, offered := range reading.Catalogs {
		catalogs = append(catalogs, offered.Catalog)
	}
	return catalogs
}

// findCatalog is the catalog that has name: the one source names, or the
// built-in catalog, or else the one source that offers it.
func findCatalog(shipped catalog.Catalog, reading sources.Reading, name, source string) (catalog.Catalog, error) {
	if source != "" {
		for _, offered := range reading.Catalogs {
			if offered.Source != source {
				continue
			}
			if _, ok := offered.Catalog.Get(name); !ok {
				return catalog.Catalog{}, fmt.Errorf("definition source %q offers no item %q here; `inkunactl catalog list` lists them", source, name)
			}
			return offered.Catalog, nil
		}
		return catalog.Catalog{}, fmt.Errorf("definition source %q offers nothing here that the app has kept; open the project in the app to ask it", source)
	}
	if _, ok := shipped.Get(name); ok {
		return shipped, nil
	}
	var found []sources.Catalog
	for _, offered := range reading.Catalogs {
		if _, ok := offered.Catalog.Get(name); ok {
			found = append(found, offered)
		}
	}
	switch len(found) {
	case 0:
		return catalog.Catalog{}, fmt.Errorf("the catalog has no item %q; `inkunactl catalog list` lists them", name)
	case 1:
		return found[0].Catalog, nil
	}
	names := make([]string, len(found))
	for index, offered := range found {
		names[index] = offered.Source
	}
	return catalog.Catalog{}, fmt.Errorf("definition sources %s each offer %q; choose one with --source", strings.Join(names, " and "), name)
}

// rulesPath is the rules file planning reads for repository: the first
// non-blank rules override inkuna.yaml declares for it, resolved against the
// repository unless absolute, else .inkuna/rules.yaml. The app resolves it
// the same way, so an add lands where the next plan looks.
func rulesPath(file config.File, repository string) string {
	for _, entry := range file.Repositories {
		if filepath.Clean(entry.Path) != filepath.Clean(repository) {
			continue
		}
		declared := strings.TrimSpace(entry.Rules)
		switch {
		case declared == "":
			continue
		case filepath.IsAbs(declared):
			return declared
		default:
			return filepath.Join(repository, declared)
		}
	}
	return filepath.Join(repository, filepath.FromSlash(rules.RelativePath))
}
