package runtime

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inkuna-ai/inkuna/internal/domain"
)

// ExtractReads returns the read-type tool calls one runtime event reports,
// in the shapes recorded from each CLI (internal/runtime/testdata/reads).
// Claude reports a tool_use block per call; Codex reads through its shell,
// reported once the command completes; Copilot reports a tool execution as
// it starts. A shell command counts when it hands files to a read command.
func ExtractReads(kind domain.Runtime, payload map[string]any) []domain.FileRead {
	switch kind {
	case domain.RuntimeClaude:
		return claudeReads(payload)
	case domain.RuntimeCodex:
		return codexReads(payload)
	case domain.RuntimeCopilot:
		return copilotReads(payload)
	}
	return nil
}

func claudeReads(payload map[string]any) []domain.FileRead {
	if payload["type"] != "assistant" {
		return nil
	}
	message, _ := payload["message"].(map[string]any)
	content, _ := message["content"].([]any)
	var reads []domain.FileRead
	for _, raw := range content {
		block, _ := raw.(map[string]any)
		if block["type"] != "tool_use" {
			continue
		}
		name, _ := block["name"].(string)
		input, _ := block["input"].(map[string]any)
		switch name {
		case "Read":
			reads = appendRead(reads, name, stringField(input, "file_path"))
		case "Grep", "Glob":
			// Without a path they search the working directory, which is
			// not a doc.
			reads = appendRead(reads, name, stringField(input, "path"))
		case "Bash":
			reads = append(reads, shellReads(stringField(input, "command"), "")...)
		}
	}
	return reads
}

func codexReads(payload map[string]any) []domain.FileRead {
	if payload["type"] != "item.completed" {
		return nil
	}
	item, _ := payload["item"].(map[string]any)
	if item["type"] != "command_execution" {
		return nil
	}
	// The same command is reported when it starts; only a completed one
	// that succeeded read anything.
	if code, ok := item["exit_code"].(float64); !ok || code != 0 {
		return nil
	}
	switch command := item["command"].(type) {
	case string:
		return shellReads(command, "")
	case []any:
		argv := make([]string, 0, len(command))
		for _, arg := range command {
			text, _ := arg.(string)
			argv = append(argv, text)
		}
		directory := ""
		return commandReads(argv, nil, &directory)
	}
	return nil
}

func copilotReads(payload map[string]any) []domain.FileRead {
	if payload["type"] != "tool.execution_start" {
		return nil
	}
	data, _ := payload["data"].(map[string]any)
	name := stringField(data, "toolName")
	arguments, _ := data["arguments"].(map[string]any)
	switch name {
	case "view":
		return appendRead(nil, name, stringField(arguments, "path"))
	case "grep", "glob":
		var reads []domain.FileRead
		switch paths := arguments["paths"].(type) {
		case string:
			reads = appendRead(reads, name, paths)
		case []any:
			for _, entry := range paths {
				text, _ := entry.(string)
				reads = appendRead(reads, name, text)
			}
		}
		return appendRead(reads, name, stringField(arguments, "path"))
	case "bash":
		return shellReads(stringField(arguments, "command"), "")
	}
	return nil
}

func appendRead(reads []domain.FileRead, tool, target string) []domain.FileRead {
	if target = strings.TrimSpace(target); target == "" {
		return reads
	}
	return append(reads, domain.FileRead{Tool: tool, Paths: []string{target}})
}

func stringField(values map[string]any, key string) string {
	text, _ := values[key].(string)
	return strings.TrimSpace(text)
}

// shellReads finds, best effort, the files a shell command reads: the
// arguments of cat-like commands, the files after a sed script, the paths
// after an rg or grep pattern, and input redirections. Paths count from
// directory, the one the shell starts in ("" for the step's working
// directory). A cd moves where later paths count from, until the subshell
// it ran in ends: parentheses, a nested shell's -c argument, a pipeline, or
// a background job. Paths it cannot know without running the shell
// (variables, globs, command substitution, or a cd whose effect differs
// between shells) are left out rather than guessed.
func shellReads(command, directory string) []domain.FileRead {
	var reads []domain.FileRead
	// enclosing holds the directory each open parenthesis was entered from.
	var enclosing []string
	var segment []shellToken
	before := ""
	run := func(after string) {
		if len(segment) == 0 {
			return
		}
		words, inputs := redirections(segment)
		segment = nil
		moved := directory
		reads = append(reads, commandReads(words, inputs, &moved)...)
		switch {
		case isPipe(after) || after == "&":
			// A command feeding a pipe, or sent to the background, runs in
			// a subshell of its own.
		case isPipe(before):
			// zsh runs the last command of a pipeline in the current
			// shell; bash runs it in a subshell.
			if moved != directory {
				directory = unknownDirectory
			}
		default:
			directory = moved
		}
	}
	for _, token := range tokenize(command) {
		// Redirections stay with their command.
		if !token.operator || strings.ContainsAny(token.text, "<>") {
			segment = append(segment, token)
			continue
		}
		run(token.text)
		switch token.text {
		case "(":
			enclosing = append(enclosing, directory)
		case ")":
			if len(enclosing) > 0 {
				directory = enclosing[len(enclosing)-1]
				enclosing = enclosing[:len(enclosing)-1]
			}
		}
		before = token.text
	}
	run("")
	return reads
}

// unknownDirectory stands for a directory only the shell knows, after a cd
// to a variable, home, or the previous directory.
const unknownDirectory = "\x00"

func isPipe(operator string) bool {
	return operator == "|" || operator == "|&"
}

type shellToken struct {
	text     string
	operator bool
}

// tokenize splits a command into words and operators, removing quotes.
func tokenize(command string) []shellToken {
	var tokens []shellToken
	var word strings.Builder
	started := false
	flush := func() {
		if started {
			tokens = append(tokens, shellToken{text: word.String()})
		}
		word.Reset()
		started = false
	}
	runes := []rune(command)
	for index := 0; index < len(runes); index++ {
		character := runes[index]
		switch {
		case character == '\'':
			started = true
			for index++; index < len(runes) && runes[index] != '\''; index++ {
				word.WriteRune(runes[index])
			}
		case character == '"':
			started = true
			for index++; index < len(runes) && runes[index] != '"'; index++ {
				if runes[index] == '\\' && index+1 < len(runes) {
					index++
				}
				word.WriteRune(runes[index])
			}
		case character == '\\' && index+1 < len(runes):
			started = true
			index++
			word.WriteRune(runes[index])
		case character == ' ' || character == '\t':
			flush()
		case strings.ContainsRune(";&|()\n<>", character):
			// A file descriptor before a redirection belongs to it.
			if (character == '>' || character == '<') && started && isDigits(word.String()) {
				word.Reset()
				started = false
			}
			flush()
			operator := string(character)
			if index+1 < len(runes) && strings.ContainsRune("&|>", runes[index+1]) && character != ';' && character != '(' && character != ')' && character != '\n' {
				index++
				operator += string(runes[index])
			}
			tokens = append(tokens, shellToken{text: operator, operator: true})
		default:
			started = true
			word.WriteRune(character)
		}
	}
	flush()
	return tokens
}

func isDigits(text string) bool {
	if text == "" {
		return false
	}
	for _, character := range text {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// redirections separates a command's words from its input redirections;
// output redirection targets are written, not read, and are dropped.
func redirections(tokens []shellToken) (words, inputs []string) {
	for index := 0; index < len(tokens); index++ {
		token := tokens[index]
		if !token.operator {
			words = append(words, token.text)
			continue
		}
		if index+1 < len(tokens) && !tokens[index+1].operator {
			if token.text == "<" {
				inputs = append(inputs, tokens[index+1].text)
			}
			index++
		}
	}
	return words, inputs
}

var assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// readers lists the commands whose file arguments are read, by how they
// take them; flagValues are the options that consume the next word.
var readers = map[string]struct {
	firstIsPattern bool
	flagValues     []string
}{
	"cat": {}, "less": {}, "more": {}, "nl": {}, "bat": {}, "tac": {},
	"head":  {flagValues: []string{"-n", "-c"}},
	"tail":  {flagValues: []string{"-n", "-c"}},
	"sed":   {firstIsPattern: true, flagValues: []string{"-e", "-f", "--expression", "--file", "-l"}},
	"grep":  {firstIsPattern: true, flagValues: grepFlagValues},
	"egrep": {firstIsPattern: true, flagValues: grepFlagValues},
	"fgrep": {firstIsPattern: true, flagValues: grepFlagValues},
	"rg":    {firstIsPattern: true, flagValues: grepFlagValues},
}

var grepFlagValues = []string{
	"-e", "-f", "-m", "-A", "-B", "-C", "-g", "-t", "-T", "-d", "-D", "-j",
	"--regexp", "--file", "--max-count", "--context", "--after-context", "--before-context",
	"--glob", "--iglob", "--type", "--type-not", "--max-depth", "--threads",
}

// commandReads reads one simple command's words.
func commandReads(words, inputs []string, directory *string) []domain.FileRead {
	for len(words) > 0 && (assignment.MatchString(words[0]) || words[0] == "command" || words[0] == "exec" || words[0] == "time" || words[0] == "env" || words[0] == "nohup") {
		words = words[1:]
	}
	if len(words) == 0 {
		return nil
	}
	program := path.Base(words[0])
	args := words[1:]
	switch program {
	case "sh", "bash", "zsh", "dash":
		for index, arg := range args {
			if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "c") && index+1 < len(args) {
				// The nested shell starts where this one is, and a cd in it
				// ends with it.
				return shellReads(args[index+1], *directory)
			}
		}
		return nil
	case "cd":
		target := "~"
		if len(args) > 0 {
			target = args[0]
		}
		switch {
		case unknowable(target) || target == "-" || strings.HasPrefix(target, "~"):
			*directory = unknownDirectory
		case shellAbs(target):
			*directory = target
		case *directory != unknownDirectory:
			*directory = path.Join(*directory, target)
		}
		return nil
	}
	if *directory == unknownDirectory {
		return nil
	}
	var files []string
	if rule, ok := readers[program]; ok {
		files = readerFiles(program, args, rule.firstIsPattern, rule.flagValues)
	}
	files = append(files, inputs...)
	var paths []string
	for _, file := range files {
		if file == "-" || unknowable(file) {
			continue
		}
		if *directory != "" && !shellAbs(file) {
			file = path.Join(*directory, file)
		}
		paths = append(paths, file)
	}
	if len(paths) == 0 {
		return nil
	}
	return []domain.FileRead{{Tool: "shell:" + program, Paths: paths}}
}

// readerFiles picks the file arguments out of a read command's arguments.
func readerFiles(program string, args []string, firstIsPattern bool, flagValues []string) []string {
	var operands []string
	patternGiven := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			operands = append(operands, args[index+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			operands = append(operands, arg)
			continue
		}
		if program == "sed" && (arg == "-i" || strings.HasPrefix(arg, "-i") || strings.HasPrefix(arg, "--in-place")) {
			return nil // An edit, not a read.
		}
		if program == "rg" && arg == "--files" {
			patternGiven = true
		}
		for _, flag := range flagValues {
			if arg == flag {
				if flag == "-e" || flag == "-f" || flag == "--regexp" || flag == "--file" || flag == "--expression" {
					patternGiven = true
				}
				index++
				break
			}
			if strings.HasPrefix(arg, flag+"=") && (flag == "--regexp" || flag == "--file" || flag == "--expression") {
				patternGiven = true
			}
		}
	}
	if firstIsPattern && !patternGiven && len(operands) > 0 {
		operands = operands[1:]
	}
	return operands
}

// unknowable reports a word whose value only the shell knows: a variable, a
// glob, or a command substitution.
func unknowable(word string) bool {
	return strings.ContainsAny(word, "$`*?[{")
}

// shellAbs reports whether a path a shell command names is absolute: from
// the root, or from a drive, as Git Bash on Windows also takes. Paths in a
// command are joined as the shell does, with forward slashes on every
// platform.
func shellAbs(name string) bool {
	return path.IsAbs(name) || filepath.IsAbs(name)
}
