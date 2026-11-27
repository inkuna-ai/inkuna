package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/agent"
	"github.com/inkuna-ai/inkuna/internal/config"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/rules"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// runSetup is the interactive wizard. It writes inkuna.yaml and stores any
// secret values in the OS keychain; values are read from the terminal and
// never echoed back or written to disk.
func runSetup(stdin io.Reader, stdout io.Writer, store secrets.Store) error {
	reader := bufio.NewReader(stdin)
	ask := func(prompt, fallback string) (string, error) {
		if fallback != "" {
			_, _ = fmt.Fprintf(stdout, "%s [%s]: ", prompt, fallback)
		} else {
			_, _ = fmt.Fprintf(stdout, "%s: ", prompt)
		}
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return fallback, nil
		}
		return line, nil
	}
	dataDir, err := defaultDataDir()
	if err != nil {
		return err
	}
	dataDir, err = ask("Data directory", dataDir)
	if err != nil {
		return err
	}
	if pinned := strings.TrimSpace(os.Getenv(config.EnvDataDir)); pinned != "" && filepath.Clean(pinned) != filepath.Clean(dataDir) {
		return fmt.Errorf("%s is set to %s; choose that directory or unset the variable so the configuration and its locator cannot diverge", config.EnvDataDir, pinned)
	}
	path := config.DefaultPath(dataDir)
	file, exists, err := config.ReadFile(path)
	if err != nil {
		return err
	}
	if exists {
		_, _ = fmt.Fprintf(stdout, "Updating existing configuration at %s\n", path)
	}
	runtime, err := ask("Orchestrator runtime (codex, claude, copilot)", string(file.Orchestrator.Runtime))
	if err != nil {
		return err
	}
	file.Orchestrator.Runtime = domain.Runtime(runtime)
	if model, err := ask("Orchestrator model override (blank keeps policy routing)", file.Orchestrator.Model); err == nil {
		file.Orchestrator.Model = model
	}
	if answer, err := ask("Maximum concurrent tasks", fmt.Sprint(file.Concurrency.MaxTasks)); err == nil {
		if _, scanErr := fmt.Sscanf(answer, "%d", &file.Concurrency.MaxTasks); scanErr != nil {
			return fmt.Errorf("maximum tasks must be a number")
		}
	}
	if answer, err := ask("Desktop notifications (yes/no)", boolWord(file.Notifications.Desktop)); err == nil {
		file.Notifications.Desktop = strings.HasPrefix(strings.ToLower(answer), "y")
	}
	for {
		repositoryPath, err := ask("Repository path to register (blank to finish)", "")
		if err != nil {
			return err
		}
		if repositoryPath == "" {
			break
		}
		absolute, err := filepath.Abs(repositoryPath)
		if err != nil {
			return err
		}
		name, err := ask("Repository name", filepath.Base(absolute))
		if err != nil {
			return err
		}
		defaultRuntime, err := ask("Default worker runtime for "+name, string(file.Orchestrator.Runtime))
		if err != nil {
			return err
		}
		replaced := false
		for index := range file.Repositories {
			if file.Repositories[index].Path == absolute {
				file.Repositories[index] = config.RepositoryConfig{Name: name, Path: absolute, DefaultRuntime: domain.Runtime(defaultRuntime)}
				replaced = true
			}
		}
		if !replaced {
			file.Repositories = append(file.Repositories, config.RepositoryConfig{Name: name, Path: absolute, DefaultRuntime: domain.Runtime(defaultRuntime)})
		}
	}
	for {
		name, err := ask("Secret name to store in the keychain (blank to finish)", "")
		if err != nil {
			return err
		}
		if name == "" {
			break
		}
		if err := secrets.ValidateName(name); err != nil {
			_, _ = fmt.Fprintln(stdout, err.Error())
			continue
		}
		value, err := readSecretValue(reader, stdout, name)
		if err != nil {
			return err
		}
		if err := store.Set(name, value); err != nil {
			return err
		}
		declared := false
		for _, secret := range file.Secrets {
			if secret.Name == name {
				declared = true
			}
		}
		if !declared {
			file.Secrets = append(file.Secrets, config.SecretRef{Name: name})
		}
		_, _ = fmt.Fprintf(stdout, "Stored %s in the keychain.\n", name)
	}
	if err := config.WriteFile(path, file); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Wrote %s\n", path)
	// A custom data directory is only discoverable if startup can find it.
	// Write a locator at the default location that names the chosen
	// directory; config.Load follows it when no environment override is set.
	if startupDir, err := defaultDataDir(); err == nil && config.DefaultPath(startupDir) != path && strings.TrimSpace(os.Getenv(config.EnvConfigFile)) == "" {
		locator, _, err := config.ReadFile(config.DefaultPath(startupDir))
		if err != nil {
			return err
		}
		locator.DataDir = dataDir
		if err := config.WriteFile(config.DefaultPath(startupDir), locator); err != nil {
			return fmt.Errorf("write locator: %w", err)
		}
		_, _ = fmt.Fprintf(stdout, "Wrote locator %s pointing at %s so startup finds this configuration without environment variables.\n", config.DefaultPath(startupDir), dataDir)
	}
	return nil
}

// readSecretValue reads a secret without echo when a terminal is attached.
// Visible input is only used when stdin is a pipe or file, never for an
// interactive terminal, so a value cannot end up in scrollback.
func readSecretValue(reader *bufio.Reader, stdout io.Writer, name string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		_, _ = fmt.Fprintf(stdout, "Value for %s (not echoed): ", name)
		raw, err := term.ReadPassword(fd)
		_, _ = fmt.Fprintln(stdout)
		if err != nil {
			return "", fmt.Errorf("read %s without echo: %w", name, err)
		}
		value := strings.TrimSpace(string(raw))
		if value == "" {
			return "", errors.New("secret value is required")
		}
		return value, nil
	}
	_, _ = fmt.Fprintf(stdout, "Value for %s: ", name)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return "", errors.New("secret value is required")
	}
	return value, nil
}

func defaultDataDir() (string, error) {
	if value := strings.TrimSpace(os.Getenv(config.EnvDataDir)); value != "" {
		return value, nil
	}
	return config.DefaultDataDir()
}

func boolWord(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

// runConfig prints the effective configuration without secret values.
func runConfig(stdout io.Writer) error {
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	resolver := secrets.NewResolver(secrets.Keyring{})
	return writeJSON(stdout, map[string]any{
		"path": configuration.ConfigPath, "exists": configuration.ConfigExists, "dataDir": configuration.DataDir,
		"file": configuration.File, "secrets": resolver.Statuses(configuration.SecretNames()),
	})
}

// runSecret manages keychain entries. Values are read from stdin so they
// never appear in shell history or process listings.
func runSecret(args []string, stdin io.Reader, stdout io.Writer, store secrets.Store) error {
	if len(args) == 0 {
		return errors.New("usage: secret set NAME | secret delete NAME | secret list")
	}
	switch args[0] {
	case "set":
		if len(args) != 2 {
			return errors.New("usage: secret set NAME (value on stdin)")
		}
		if err := secrets.ValidateName(args[1]); err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(stdin, 64<<10))
		if err != nil {
			return err
		}
		if err := store.Set(args[1], strings.TrimSpace(string(data))); err != nil {
			return err
		}
		configuration, err := config.Load()
		if err == nil {
			declared := false
			for _, secret := range configuration.File.Secrets {
				declared = declared || secret.Name == args[1]
			}
			if !declared {
				file := configuration.File
				file.Secrets = append(file.Secrets, config.SecretRef{Name: args[1]})
				if err := config.WriteFile(configuration.ConfigPath, file); err != nil {
					return err
				}
			}
		}
		return writeJSON(stdout, map[string]any{"stored": args[1]})
	case "delete":
		if len(args) != 2 {
			return errors.New("usage: secret delete NAME")
		}
		if err := store.Delete(args[1]); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"deleted": args[1]})
	case "list":
		configuration, err := config.Load()
		if err != nil {
			return err
		}
		return writeJSON(stdout, secrets.NewResolver(store).Statuses(configuration.SecretNames()))
	default:
		return fmt.Errorf("unknown secret command %q", args[0])
	}
}

// runAgents lists the merged agent registry for a repository.
func runAgents(args []string, stdout io.Writer) error {
	if len(args) != 2 || args[0] != "list" {
		return errors.New("usage: agents list REPOSITORY")
	}
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	registry, err := agent.Build(args[1], configuration.AgentDirectories)
	if err != nil {
		return err
	}
	return writeJSON(stdout, registry.All())
}

// runRules validates and prints a repository's rules document. With
// --expanded, show prints each flow as planning sees it: its uses of shared
// stages and groups resolved into plain stages, each saying where it came
// from.
func runRules(args []string, stdout io.Writer) error {
	usage := errors.New("usage: rules show [--expanded] REPOSITORY | rules validate FILE")
	expanded := false
	if len(args) > 0 && args[0] == "show" {
		positional := []string{"show"}
		for _, arg := range args[1:] {
			if arg == "--expanded" {
				expanded = true
				continue
			}
			positional = append(positional, arg)
		}
		args = positional
	}
	if len(args) != 2 || (args[0] != "show" && args[0] != "validate") {
		return usage
	}
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	library := rules.LoadLibrary(configuration.StageDirectories)
	path := args[1]
	if args[0] == "show" {
		path = filepath.Join(args[1], filepath.FromSlash(rules.RelativePath))
	}
	document, exists, err := rules.LoadPathWith(path, library)
	if err != nil {
		return err
	}
	if args[0] == "validate" && !exists {
		return fmt.Errorf("rules file %s does not exist", args[1])
	}
	if expanded {
		if document, err = document.Expand(); err != nil {
			return fmt.Errorf("expand rules: %w", err)
		}
	}
	return writeJSON(stdout, map[string]any{"exists": exists, "source": document.Source, "rules": len(document.Rules), "document": document})
}

// runStages lists the shared stages and groups a repository's rules may use,
// or with no repository those every repository may: each definition with its
// scope and file, marked when a higher scope overrides it, and the problems
// found in the global library files.
func runStages(args []string, stdout io.Writer) error {
	if len(args) < 1 || len(args) > 2 || args[0] != "list" {
		return errors.New("usage: stages list [REPOSITORY]")
	}
	configuration, err := config.Load()
	if err != nil {
		return err
	}
	library := rules.LoadLibrary(configuration.StageDirectories)
	problems := append([]rules.LibraryProblem{}, library.Problems...)
	var document *rules.Document
	if len(args) == 2 {
		// Rules that do not load, often because of a broken library file, are
		// one more problem: the library is still worth listing to find it.
		path := filepath.Join(args[1], filepath.FromSlash(rules.RelativePath))
		loaded, exists, err := rules.LoadPathWith(path, library)
		switch {
		case err != nil:
			problems = append(problems, rules.LibraryProblem{File: path, Message: err.Error()})
		case exists:
			document = &loaded
		}
	}
	return writeJSON(stdout, map[string]any{"directories": configuration.StageDirectories, "definitions": library.Effective(document), "problems": problems})
}

// runAgentValidate checks a single agent definition file.
func runAgentValidate(path string, stdout io.Writer) error {
	definition, err := agent.Load(path, agent.SourceRepository)
	if err != nil {
		return err
	}
	return writeJSON(stdout, definition)
}

// renderExampleConfig prints a starter inkuna.yaml.
func renderExampleConfig(stdout io.Writer) error {
	file := config.DefaultFile()
	file.Repositories = []config.RepositoryConfig{{Name: "example", Path: "/absolute/path/to/repository", DefaultRuntime: domain.RuntimeClaude}}
	file.Secrets = append(file.Secrets, config.SecretRef{Name: "ANTHROPIC_API_KEY", Description: "Optional API key exported to Claude workers"})
	file.Runtimes[domain.RuntimeClaude] = config.RuntimeConfig{Binary: "claude", Secrets: []string{"ANTHROPIC_API_KEY"}}
	data, err := yaml.Marshal(file)
	if err != nil {
		return err
	}
	_, err = stdout.Write(data)
	return err
}
