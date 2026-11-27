/** A piece of a log line, with the colour and weight ANSI codes gave it. */
export interface AnsiSpan {
  text: string;
  className?: string;
}

/** One log line: its pieces, and its text with the codes taken out. */
export interface AnsiLine {
  spans: AnsiSpan[];
  plain: string;
}

interface Style {
  fg?: number;
  bg?: number;
  bold?: boolean;
  dim?: boolean;
  italic?: boolean;
  underline?: boolean;
}

// CSI sequences (colours, cursor moves), OSC sequences (titles, links),
// and the two-byte escapes; only SGR, a CSI ending in m, changes style.
const escapes =
  // eslint-disable-next-line no-control-regex
  /\x1b\[([0-9;?]*)([ -/]*[@-~])|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[@-Z\\-_]/g;

function className(style: Style): string | undefined {
  const names = [
    style.fg !== undefined && `ansi-fg-${style.fg}`,
    style.bg !== undefined && `ansi-bg-${style.bg}`,
    style.bold && "font-bold",
    style.dim && "opacity-70",
    style.italic && "italic",
    style.underline && "underline",
  ].filter(Boolean);
  return names.length ? names.join(" ") : undefined;
}

function applySGR(style: Style, parameters: string): Style {
  const codes =
    parameters === ""
      ? [0]
      : parameters.split(";").map((code) => Number(code) || 0);
  let next = { ...style };
  for (let index = 0; index < codes.length; index++) {
    const code = codes[index];
    if (code === 0) next = {};
    else if (code === 1) next.bold = true;
    else if (code === 2) next.dim = true;
    else if (code === 3) next.italic = true;
    else if (code === 4) next.underline = true;
    else if (code === 22) next.bold = next.dim = false;
    else if (code === 23) next.italic = false;
    else if (code === 24) next.underline = false;
    else if (code >= 30 && code <= 37) next.fg = code - 30;
    else if (code >= 90 && code <= 97) next.fg = code - 90 + 8;
    else if (code === 39) next.fg = undefined;
    else if (code >= 40 && code <= 47) next.bg = code - 40;
    else if (code >= 100 && code <= 107) next.bg = code - 100 + 8;
    else if (code === 49) next.bg = undefined;
    else if (code === 38 || code === 48) {
      // 256-colour and true-colour codes: the first 16 of the 256 map to
      // the named colours; the rest keep the text colour.
      if (codes[index + 1] === 5) {
        const colour = codes[index + 2];
        if (colour !== undefined && colour < 16) {
          if (code === 38) next.fg = colour;
          else next.bg = colour;
        }
        index += 2;
      } else if (codes[index + 1] === 2) index += 4;
    }
  }
  return next;
}

/**
 * Splits a log line into styled pieces. A carriage return rewrites the
 * line, as a progress bar does, so only what follows the last one shows,
 * as a terminal would show it.
 */
export function parseAnsiLine(
  line: string,
  start: Style = {},
): AnsiLine & { end: Style } {
  let text = line.endsWith("\r") ? line.slice(0, -1) : line;
  const rewritten = text.lastIndexOf("\r");
  if (rewritten >= 0) text = text.slice(rewritten + 1);
  const spans: AnsiSpan[] = [];
  let style = start;
  let at = 0;
  const push = (piece: string) => {
    if (!piece) return;
    const name = className(style);
    const last = spans[spans.length - 1];
    if (last && last.className === name) last.text += piece;
    else spans.push(name ? { text: piece, className: name } : { text: piece });
  };
  for (const match of text.matchAll(escapes)) {
    push(text.slice(at, match.index));
    if (match[2] === "m") style = applySGR(style, match[1] ?? "");
    at = match.index + match[0].length;
  }
  push(text.slice(at));
  return { spans, plain: spans.map((span) => span.text).join(""), end: style };
}

/** Splits a log into lines; a style carries over a line break as in a terminal. */
export function parseLog(text: string): AnsiLine[] {
  const raw = text.split("\n");
  if (raw[raw.length - 1] === "") raw.pop();
  const lines: AnsiLine[] = [];
  let style: Style = {};
  for (const line of raw) {
    const parsed = parseAnsiLine(line, style);
    style = parsed.end;
    lines.push({ spans: parsed.spans, plain: parsed.plain });
  }
  return lines;
}

/** Whether a line reads as an error, for jumping to the first one. */
export function isErrorLine(plain: string): boolean {
  return /\b(error|errors|fail|failed|failure|panic|fatal)\b|^\s*(FAIL|ERR!?)\b|^E\s/i.test(
    plain,
  );
}
