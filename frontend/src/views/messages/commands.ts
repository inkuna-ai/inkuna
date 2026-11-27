/**
 * Splits a shell command line into the commands it chains with `;`, `&&`,
 * `||`, or a line break, each keeping the operator that ends it, so a long
 * compound command reads one command per line. Quotes, backslash escapes,
 * comments, and anything inside parentheses or `$(…)` stay together. It only lays the
 * text out: every character but the whitespace around a split is kept, and
 * what an approval allows is the command Go stored, unchanged.
 */
export function commandLines(command: string): string[] {
  const lines: string[] = [];
  let current = "";
  let quote = "";
  let depth = 0;
  // Whether the next character starts a word. Only an unquoted, unescaped
  // blank or operator ends one: in `a\ #b` the # is inside the word.
  let wordStart = true;
  const flush = () => {
    const line = current.trim();
    if (line) lines.push(line);
    current = "";
  };
  for (let index = 0; index < command.length; index++) {
    const char = command[index];
    const next = command[index + 1] ?? "";
    const startsWord: boolean = wordStart;
    wordStart = false;
    // Single quotes take everything literally, backslashes included.
    if (char === "\\" && quote !== "'") {
      // A backslash before a line break joins the lines, as if neither
      // were there, so a word that was about to start still does.
      if (next === "\n") wordStart = startsWord;
      current += char + next;
      index++;
      continue;
    }
    if (quote) {
      current += char;
      if (char === quote) quote = "";
      continue;
    }
    if (char === "'" || char === '"' || char === "`") {
      quote = char;
      current += char;
      continue;
    }
    // A # that starts a word comments out the rest of the line: what
    // follows a ; in it never runs, so it must not read as its own command.
    if (char === "#" && startsWord) {
      const end = command.indexOf("\n", index);
      const stop = end === -1 ? command.length : end;
      current += command.slice(index, stop);
      index = stop - 1;
      continue;
    }
    wordStart = /[\s;&|()]/.test(char);
    if (char === "(") depth++;
    if (char === ")") depth = Math.max(0, depth - 1);
    if (depth === 0) {
      if (char === "\n") {
        flush();
        continue;
      }
      if (char === ";") {
        current += char;
        flush();
        continue;
      }
      if ((char === "&" || char === "|") && next === char) {
        current += char + next;
        index++;
        flush();
        continue;
      }
    }
    current += char;
  }
  flush();
  return lines;
}
