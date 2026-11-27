package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	inkuna "github.com/inkuna-ai/inkuna/internal/app"
	"github.com/inkuna-ai/inkuna/internal/artifact"
	"github.com/inkuna-ai/inkuna/internal/domain"
	"github.com/inkuna-ai/inkuna/internal/proc"
	"github.com/inkuna-ai/inkuna/internal/secrets"
	"github.com/inkuna-ai/inkuna/internal/skill"
	"github.com/inkuna-ai/inkuna/pkg/extension"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		code := 1
		var failure exitError
		if errors.As(err, &failure) {
			code = failure.code
		}
		if message := err.Error(); message != "" {
			_, _ = fmt.Fprintln(os.Stderr, "inkunactl:", message)
		}
		os.Exit(code)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "help" {
		_, err := fmt.Fprintln(stdout, strings.Join([]string{
			"inkunactl install [--check] [--yes]  check what Inkuna needs, then build Inkuna and put the app and CLI in place",
			"                                     --check only reports what is needed; --yes agrees to the install's own steps",
			"inkunactl update [--check | --rollback] [--channel stable|edge] [--yes]",
			"                                     fetch the install's checkout, move it to the newest release (or origin/main on edge), build, and swap the build in",
			"                                     --check only says what changed; --rollback swaps the previous build back; --channel switches and is remembered",
			"inkunactl setup                      interactive wizard: writes inkuna.yaml, stores secrets in the keychain",
			"inkunactl init [REPOSITORY]          add .inkuna/out/ to the repository's .gitignore",
			"inkunactl config [show|example]      print the effective configuration or a starter inkuna.yaml",
			"inkunactl secret set NAME            store a secret (value read from stdin)",
			"inkunactl secret delete NAME | list  manage keychain entries",
			"inkunactl agents list REPOSITORY     list built-in, global, and repository agents",
			"inkunactl agent validate FILE        validate one agent definition",
			"inkunactl rules show [--expanded] REPOSITORY",
			"                                     print the effective rules document; --expanded resolves shared stages",
			"inkunactl rules validate FILE        validate a rules file",
			"inkunactl stages list [REPOSITORY]   list built-in, global, and repository shared stages and groups",
			"inkunactl catalog list [REPOSITORY] | catalog show NAME [--source SOURCE] [REPOSITORY]",
			"                                     list the agents, skills, rules, shared stages, and groups ready to add, or print one",
			"inkunactl catalog add NAME [--global] [--source SOURCE] [REPOSITORY]",
			"                                     copy an item, and what it needs, into .inkuna/ or the global directories",
			"inkunactl catalog sources [REPOSITORY]",
			"                                     what the definition sources shared when the app last asked them",
			"inkunactl docs list REPOSITORY       print the doc index: paths, frontmatter, and problems",
			"inkunactl docs validate [--changed-since REF] REPOSITORY",
			"                                     print doc problems; exit 1 if any, 2 if the check cannot run",
			"inkunactl doctor                     check local dependencies and authentication",
			"inkunactl version [--json]           print the version and commit this build was made from",
			"inkunactl skill validate DIR | skill run DIR ACTION | skills export ROOT TARGET REPOSITORY",
			"inkunactl artifact put STORE NAME MEDIA_TYPE | artifact read STORE DIGEST",
			"inkunactl workflow validate FILE",
			"inkunactl usage [--sections] RUN_ID  print each model call of a run: tokens, cost, and prompt bytes",
			"inkunactl usage export [--since DATE] [--format csv|jsonl]",
			"                                     write every model call with its cost, as the usage sinks receive it",
			"inkunactl diff [--commit SHA | --step ID] [--out FILE] [--open] [--split] [--include-large] RUN_ID",
			"                                     write a run's changes, commits, and checks as one HTML page",
			"inkunactl cost [--since DATE] [--csv] | cost TASK [--by step|agent|runtime|model] [--csv]",
			"                                     print what each task cost, or one task's breakdown",
			"inkunactl tasks list [--finished]    list tasks with their runs; --finished lists those a delete accepts",
			"inkunactl tasks delete [--remove-worktrees] [--delete-branches] TASK...",
			"                                     delete finished tasks and their history, after asking",
			"inkunactl branch new --type TYPE [--story ID] [--base BRANCH] [--push] DESCRIPTION...",
			"inkunactl branch init [--write] [--no-orchestrator] [REPOSITORY] | branch show [REPOSITORY]",
		}, "\n"))
		return err
	}
	keychain := secrets.Keyring{}
	switch args[0] {
	case "install":
		return runInstall(args[1:], stdin, stdout)
	case "update":
		return runUpdate(args[1:], stdin, stdout)
	case "setup":
		return runSetup(stdin, stdout, keychain)
	case "init":
		return runInit(args[1:], stdout)
	case "config":
		if len(args) > 1 && args[1] == "example" {
			return renderExampleConfig(stdout)
		}
		return runConfig(stdout)
	case "secret":
		return runSecret(args[1:], stdin, stdout, keychain)
	case "agents":
		return runAgents(args[1:], stdout)
	case "agent":
		if len(args) != 3 || args[1] != "validate" {
			return errors.New("usage: agent validate FILE")
		}
		return runAgentValidate(args[2], stdout)
	case "rules":
		return runRules(args[1:], stdout)
	case "stages":
		return runStages(args[1:], stdout)
	case "catalog":
		return runCatalog(args[1:], stdout)
	case "docs":
		return runDocs(args[1:], stdout)
	case "branch":
		return runBranch(args[1:], stdout)
	case "usage":
		return runUsage(args[1:], stdout)
	case "diff":
		return runDiff(args[1:], stdout)
	case "cost":
		return runCost(args[1:], stdout)
	case "tasks":
		return runTasks(args[1:], stdin, stdout)
	case "doctor":
		service, err := inkuna.New(filepath.Join(os.TempDir(), "inkuna-doctor"), extension.Product{}, nil)
		if err != nil {
			return err
		}
		service.Startup(context.Background())
		defer service.Shutdown(context.Background())
		return writeJSON(stdout, service.Doctor())
	case "version":
		return runVersion(args[1:], stdout)
	case "skill":
		if len(args) < 3 {
			return errors.New("skill requires validate DIR or run DIR ACTION")
		}
		pkg, err := skill.Load(args[2])
		if err != nil {
			return err
		}
		if args[1] == "validate" {
			return writeJSON(stdout, pkg)
		}
		if args[1] != "run" || len(args) != 4 {
			return errors.New("usage: skill run DIR ACTION")
		}
		return runSkill(pkg, args[3], stdin, stdout)
	case "skills":
		if len(args) != 5 || args[1] != "export" {
			return errors.New("usage: skills export ROOT TARGET REPOSITORY")
		}
		packages, err := skill.Discover(args[2])
		if err != nil {
			return err
		}
		return skill.Export(packages, args[4], args[3])
	case "artifact":
		if len(args) < 4 {
			return errors.New("artifact requires put or read")
		}
		store, err := artifact.New(args[2])
		if err != nil {
			return err
		}
		if args[1] == "put" && len(args) == 5 {
			ref, err := store.Put(args[3], args[4], stdin)
			if err != nil {
				return err
			}
			return writeJSON(stdout, ref)
		}
		if args[1] == "read" && len(args) == 4 {
			reader, err := store.Open(args[3])
			if err != nil {
				return err
			}
			defer func() { _ = reader.Close() }()
			_, err = io.Copy(stdout, reader)
			return err
		}
		return errors.New("usage: artifact put STORE NAME MEDIA_TYPE | artifact read STORE DIGEST")
	case "workflow":
		if len(args) != 3 || args[1] != "validate" {
			return errors.New("usage: workflow validate FILE")
		}
		data, err := os.ReadFile(args[2])
		if err != nil {
			return err
		}
		var workflow domain.WorkflowSpec
		if err := json.Unmarshal(data, &workflow); err != nil {
			return err
		}
		if err := domain.ValidateWorkflow(workflow); err != nil {
			return err
		}
		return writeJSON(stdout, map[string]any{"valid": true, "steps": len(workflow.Steps)})
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runSkill(pkg skill.Package, name string, stdin io.Reader, stdout io.Writer) error {
	action, ok := pkg.Manifest.Actions[name]
	if !ok {
		return fmt.Errorf("unknown skill action %q", name)
	}
	input, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(action.Timeout)*time.Second)
	defer cancel()
	script := filepath.Join(pkg.Directory, filepath.Clean(action.Script))
	command := exec.CommandContext(ctx, action.Interpreter, script)
	command.Dir = pkg.Directory
	command.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	err = command.Run()
	storeRoot := filepath.Join(pkg.Directory, ".artifacts")
	store, storeErr := artifact.New(storeRoot)
	if storeErr != nil {
		return storeErr
	}
	ref, storeErr := store.Put(name+".log", "text/plain", bytes.NewReader(output.Bytes()))
	if storeErr != nil {
		return storeErr
	}
	result := map[string]any{"ok": err == nil, "artifact": ref, "summary": strings.TrimSpace(limit(output.String(), 1024))}
	if ctx.Err() != nil {
		result["error"] = ctx.Err().Error()
	} else if err != nil {
		result["error"] = err.Error()
	}
	if writeErr := writeJSON(stdout, result); writeErr != nil {
		return writeErr
	}
	return err
}

func limit(value string, size int) string {
	if len(value) <= size {
		return value
	}
	return value[:size] + "…"
}
func execGit(ctx context.Context, directory string, args ...string) *exec.Cmd {
	command := proc.Command(ctx, "git", args...)
	command.Dir = directory
	return command
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
