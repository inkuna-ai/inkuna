import { isErrorLine, parseAnsiLine, parseLog } from "./ansi";

describe("ANSI log lines", () => {
  it("turns colour and weight codes into classes and drops the rest", () => {
    const line = parseAnsiLine(
      "\x1b[1;31mFAIL\x1b[0m pkg \x1b[32mok\x1b[39m \x1b]8;;https://x\x07link\x1b]8;;\x07 \x1b[2Kdone",
    );
    expect(line.plain).toBe("FAIL pkg ok link done");
    expect(line.spans).toEqual([
      { text: "FAIL", className: "ansi-fg-1 font-bold" },
      { text: " pkg " },
      { text: "ok", className: "ansi-fg-2" },
      { text: " link done" },
    ]);
  });

  it("maps bright and 256-colour codes, keeping the text colour past the first 16", () => {
    expect(
      parseAnsiLine("\x1b[92ma\x1b[38;5;9mb\x1b[38;5;200mc").spans,
    ).toEqual([
      { text: "a", className: "ansi-fg-10" },
      { text: "bc", className: "ansi-fg-9" },
    ]);
  });

  it("shows only what follows the last carriage return, as a terminal would", () => {
    expect(parseAnsiLine("10%\r50%\r100% done\r").plain).toBe("100% done");
  });

  it("carries a style over a line break and splits on newlines", () => {
    const lines = parseLog("\x1b[33mwarn\nstill yellow\x1b[0m\nplain\n");
    expect(lines.map((line) => line.plain)).toEqual([
      "warn",
      "still yellow",
      "plain",
    ]);
    expect(lines[1].spans[0].className).toBe("ansi-fg-3");
    expect(lines[2].spans[0].className).toBeUndefined();
  });

  it("spots the lines that read as errors", () => {
    expect(isErrorLine("--- FAIL: TestPlan (0.01s)")).toBe(true);
    expect(isErrorLine("main.go:3: undefined: x (compile error)")).toBe(true);
    expect(isErrorLine("ok  	github.com/inkuna-ai/inkuna")).toBe(false);
  });
});
