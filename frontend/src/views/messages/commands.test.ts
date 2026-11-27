import { describe, expect, it } from "vitest";
import { commandLines } from "./commands";

describe("commandLines", () => {
  it("puts each chained command on its own line, keeping its operator", () => {
    expect(
      commandLines(
        `grep -n "a\\|b" -r lib | head -40; grep -n x 2>/dev/null | head -120 || find ../proto -name 'care*.proto'`,
      ),
    ).toEqual([
      `grep -n "a\\|b" -r lib | head -40;`,
      "grep -n x 2>/dev/null | head -120 ||",
      "find ../proto -name 'care*.proto'",
    ]);
  });

  it("keeps quotes, substitutions, and escapes together", () => {
    expect(
      commandLines(
        `FL=$(dirname $(readlink -f $(which flutter))); echo "a; b" 'c && d' e\\;f && ls`,
      ),
    ).toEqual([
      "FL=$(dirname $(readlink -f $(which flutter)));",
      `echo "a; b" 'c && d' e\\;f &&`,
      "ls",
    ]);
  });

  it("leaves a pipe, a background job, and a simple command alone", () => {
    expect(commandLines("ls .inkuna 2>&1 | head &")).toEqual([
      "ls .inkuna 2>&1 | head &",
    ]);
    expect(commandLines("  go test ./...  ")).toEqual(["go test ./..."]);
  });

  it("keeps a comment, and any ; in it, with the command it follows", () => {
    expect(commandLines("echo ready # note; rm -rf ./cache")).toEqual([
      "echo ready # note; rm -rf ./cache",
    ]);
    expect(commandLines("ls # a; b\npwd; echo $# ${#x} a#b; id")).toEqual([
      "ls # a; b",
      "pwd;",
      "echo $# ${#x} a#b;",
      "id",
    ]);
    expect(commandLines("echo $(ls # ) x\n); date")).toEqual([
      "echo $(ls # ) x\n);",
      "date",
    ]);
  });

  it("does not take a # after an escaped or quoted blank for a comment", () => {
    expect(commandLines("echo a\\ #b; pwd")).toEqual(["echo a\\ #b;", "pwd"]);
    expect(commandLines(`echo "a "#b; pwd`)).toEqual([`echo "a "#b;`, "pwd"]);
  });

  it("follows a line continuation into a comment, or into the word it joins", () => {
    expect(commandLines("echo a \\\n# note; rm x")).toEqual([
      "echo a \\\n# note; rm x",
    ]);
    expect(commandLines("echo a\\\n#b; pwd")).toEqual(["echo a\\\n#b;", "pwd"]);
  });

  it("splits on line breaks and drops empty commands", () => {
    expect(commandLines("cd lib\n\nls;;")).toEqual(["cd lib", "ls;", ";"]);
  });
});
