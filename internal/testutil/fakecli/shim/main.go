// Command shim stands in for a CLI in tests on Windows, which cannot run a
// shell script as a program. Copied to fake.exe, it runs fake.exe.sh with
// the shell named in fake.exe.shell, passing its arguments, input, output,
// and exit code through.
//
// The shell is Git for Windows' sh, whose runtime parses its own command
// line differently from how Go quotes one, so an argument holding quotes,
// such as a JSON schema, could reach the script split or merged. So the
// arguments travel in a file, NUL-separated, and the shell's command line
// names only fake.exe.run.sh, which reads them back and sources the script.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	self, err := os.Executable()
	if err != nil {
		fail(err)
	}
	shell, err := os.ReadFile(self + ".shell")
	if err != nil {
		fail(err)
	}
	arguments, err := os.CreateTemp(dirOf(self), "args-*")
	if err != nil {
		fail(err)
	}
	for _, argument := range os.Args[1:] {
		if _, err := arguments.WriteString(argument + "\x00"); err != nil {
			fail(err)
		}
	}
	if err := arguments.Close(); err != nil {
		fail(err)
	}
	defer func() { _ = os.Remove(arguments.Name()) }()
	command := exec.Command(strings.TrimSpace(string(shell)), self+".run.sh")
	command.Env = append(os.Environ(), "FAKECLI_ARGS="+arguments.Name(), "FAKECLI_SCRIPT="+self+".sh")
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = command.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		_ = os.Remove(arguments.Name())
		os.Exit(exit.ExitCode())
	}
	if err != nil {
		fail(err)
	}
}

func dirOf(path string) string {
	if index := strings.LastIndexAny(path, `\/`); index >= 0 {
		return path[:index]
	}
	return "."
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fakecli shim:", err)
	os.Exit(127)
}
