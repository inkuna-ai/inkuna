package runtime

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// shells are the programs that run a command line given to them as one
// string. Inkuna runs a step's commands directly, as argv arrays, so a shell
// would bring back everything that rules out: expansion, chaining, and a
// command nobody wrote down.
var shells = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "ash": true, "csh": true, "tcsh": true,
	"fish": true, "nu": true, "cmd": true, "powershell": true, "pwsh": true,
}

// lineCharacters are what keeps an argument from being one plain word on a
// command line: whitespace, quotes, and the characters a shell expands or
// splits on. A worker's allowed command is matched as a command line, so an
// argument with one of them would allow a different command, or several.
const lineCharacters = " \t\r\n\"'`$;&|<>()*?[]{}!\\"

// CheckCommand refuses a command Inkuna would not run for a step, from a
// rule or from the approval screen: an empty one, a whole command line
// written as its program, a shell, or an argument that turns a CLI's
// permission checks off (bypassArguments, the list runtime declarations are
// held to).
func CheckCommand(argv []string) error {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return errors.New("a command needs a program to run")
	}
	if strings.ContainsAny(argv[0], " \t\r\n") {
		return fmt.Errorf("%q is a whole command line written as one argument; give the program and each of its arguments separately", argv[0])
	}
	for index, argument := range argv {
		if strings.ContainsRune(argument, 0) {
			return fmt.Errorf("an argument of %q contains a NUL byte", strings.Join(argv, " "))
		}
		// A shell is refused as the program, and after a wrapper such as env
		// or timeout when it is handed a command line.
		if shell(argument) && (index == 0 || index+1 < len(argv) && commandLineOption(argv[index+1])) {
			return fmt.Errorf("%q runs a shell; Inkuna runs each command directly, never through a shell, so name the program the shell would run (a script runs as ./script.sh)", strings.Join(argv, " "))
		}
		if bypassArgument(argument) {
			return fmt.Errorf("%q in %q turns a CLI's permission checks off, which Inkuna does not allow", argument, strings.Join(argv, " "))
		}
	}
	return nil
}

// CheckAllowedCommand refuses a command an agent step's worker could not be
// allowed to run without asking. Besides CheckCommand's checks, every
// argument must be one plain word: the CLIs allow a shell command as an
// exact command line (Claude Code's Bash(...), Copilot's shell(...)), built
// by joining the arguments with spaces, so a space, a quote, or a character
// such as * | ; & $ would allow some other command than the one listed.
func CheckAllowedCommand(argv []string) error {
	if err := CheckCommand(argv); err != nil {
		return err
	}
	for _, argument := range argv {
		if argument == "" || strings.ContainsAny(argument, lineCharacters) || strings.HasPrefix(argument, "~") || strings.HasPrefix(argument, "#") {
			return fmt.Errorf("argument %q of %q is not one plain word: a worker is allowed the command as one exact command line, so write its arguments without spaces, quotes, or shell characters such as * | ; & $", argument, strings.Join(argv, " "))
		}
	}
	return nil
}

// shell reports whether an argument names a shell, by path or by name, with
// or without .exe.
func shell(argument string) bool {
	name := strings.ToLower(argument[strings.LastIndexAny(argument, `/\`)+1:])
	return shells[strings.TrimSuffix(name, ".exe")]
}

// commandLineOption reports whether a shell's argument makes it run the next
// one as a command line: -c and the flags combined with it (-lc, -ec),
// fish's --command, cmd's /c and /k, and PowerShell's -Command and
// -EncodedCommand, which it accepts abbreviated down to -e.
func commandLineOption(argument string) bool {
	lower := strings.ToLower(argument)
	if lower == "/c" || lower == "/k" {
		return true
	}
	flags, ok := strings.CutPrefix(lower, "-")
	return ok && flags != "" && (strings.ContainsRune(flags, 'c') || strings.HasPrefix("encodedcommand", flags))
}

// bypassArgument reports whether an argument is one of bypassArguments,
// ignoring case and a trailing =value, or is a flag set to one.
func bypassArgument(argument string) bool {
	flag, value, _ := strings.Cut(strings.ToLower(argument), "=")
	return slices.Contains(bypassArguments, flag) || slices.Contains(bypassArguments, value)
}

// AllowedCommandFor is the argv that would let a step's worker run a shell
// command line it was refused, as one of the step's commands, or false when
// no command could allow exactly that line: one with quoting, shell
// syntax, or anything else CheckAllowedCommand refuses.
func AllowedCommandFor(line string) ([]string, bool) {
	argv := strings.Fields(line)
	if CheckAllowedCommand(argv) != nil {
		return nil, false
	}
	return argv, true
}

// EditTool reports whether a tool changes files. Only a step's permission
// allows one, so a refused edit is a write the step was not allowed.
func EditTool(tool string) bool {
	return slices.Contains(claudeEditTools, tool)
}
