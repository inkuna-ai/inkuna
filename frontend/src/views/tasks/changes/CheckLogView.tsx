import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { getCheckLog, saveCheckLog } from "../../../bridge";
import { useInkunaEvent } from "../../../events";
import { formatBytes } from "../../../format";
import type { Check } from "../../../types";
import { Button, inputClass } from "../../common";
import { InlineAlert } from "../shared";
import { isErrorLine, parseLog, type AnsiSpan } from "./ansi";

/** Every log row is one line of this height, so a row's place is its index times it. */
const rowHeight = 20;
/** Rows drawn beyond each edge of the box, so a quick scroll shows no gap. */
const overscan = 40;
/** How often a running check's log is read again when no event says it grew. */
const pollEvery = 1000;

/**
 * A check's log, with line numbers, ANSI colours, search, a jump to the
 * first line that reads as an error, and a download. While the command runs
 * the log grows in place, following its end unless the reader has scrolled
 * up.
 */
export function CheckLogView({ check }: { check: Check }) {
  const [log, setLog] = useState<{
    text: string;
    cut: boolean;
    bytes: number;
    running: boolean;
  } | null>(null);
  const [error, setError] = useState("");
  const text = useRef("");
  // Go counts the log in bytes, which a string's length does not (é is one
  // character but two bytes), so the view asks for more from the byte
  // offset Go last handed it, and -1 for the whole log again.
  const end = useRef(-1);
  const reading = useRef(false);

  const read = useCallback(
    async (from: number) => {
      if (reading.current) return;
      reading.current = true;
      try {
        const part = await getCheckLog(check.id, from);
        if (part.reset) {
          text.current = part.text;
          end.current = part.end;
        } else if (part.offset === end.current) {
          text.current += part.text;
          end.current = part.end;
        } else {
          // Not the part after what the view has: read it whole next time.
          end.current = -1;
        }
        setLog({
          text: text.current,
          cut: part.cut,
          bytes: part.bytes,
          running: part.running,
        });
        setError("");
      } catch (reason) {
        setError(String(reason));
      } finally {
        reading.current = false;
      }
    },
    [check.id],
  );
  // The whole log once at first and again whenever the check's status
  // changes, since an ended check's stored log replaces the live one.
  useEffect(() => {
    void read(-1);
  }, [read, check.status]);
  const running = log?.running ?? check.status === "running";
  const onOutput = useCallback(
    (data?: unknown) => {
      if (data === check.id) void read(end.current);
    },
    [check.id, read],
  );
  useInkunaEvent("inkuna:check", onOutput);
  useEffect(() => {
    if (!running) return;
    const timer = window.setInterval(() => void read(end.current), pollEvery);
    return () => window.clearInterval(timer);
  }, [running, read]);

  const lines = useMemo(() => parseLog(log?.text ?? ""), [log?.text]);
  const widest = useMemo(
    () => lines.reduce((most, line) => Math.max(most, line.plain.length), 0),
    [lines],
  );
  const digits = String(Math.max(lines.length, 1)).length;

  const [query, setQuery] = useState("");
  const matches = useMemo(() => {
    const wanted = query.trim().toLowerCase();
    if (!wanted) return [];
    return lines.flatMap((line, index) =>
      line.plain.toLowerCase().includes(wanted) ? [index] : [],
    );
  }, [lines, query]);
  const [match, setMatch] = useState(0);
  const firstError = useMemo(
    () => lines.findIndex((line) => isErrorLine(line.plain)),
    [lines],
  );
  const [marked, setMarked] = useState<number | null>(null);

  const box = useRef<HTMLDivElement>(null);
  const [scroll, setScroll] = useState({ top: 0, height: 480 });
  // The line is centred in the box, and the box brought on screen if the
  // page has scrolled it away.
  const reveal = useCallback((index: number) => {
    setMarked(index);
    const element = box.current;
    if (!element) return;
    element.scrollTop = Math.max(
      0,
      index * rowHeight - element.clientHeight / 2,
    );
    element.scrollIntoView?.({ block: "nearest" });
  }, []);
  const step = (direction: 1 | -1) => {
    if (matches.length === 0) return;
    const next = (match + direction + matches.length) % matches.length;
    setMatch(next);
    reveal(matches[next]);
  };

  // A running log keeps its end in view while the reader is there.
  const following = useRef(true);
  useLayoutEffect(() => {
    const element = box.current;
    if (element && running && following.current)
      element.scrollTop = element.scrollHeight;
  }, [lines.length, running]);

  const [saved, setSaved] = useState("");
  const download = () => {
    setSaved("");
    saveCheckLog(check.id).then(
      (path) => setSaved(path ? `Saved to ${path}` : ""),
      (reason: unknown) => setError(String(reason)),
    );
  };

  const first = Math.max(0, Math.floor(scroll.top / rowHeight) - overscan);
  const last = Math.min(
    lines.length,
    Math.ceil((scroll.top + scroll.height) / rowHeight) + overscan,
  );
  const wanted = query.trim().toLowerCase();
  return (
    <div className="grid min-w-0 gap-2 p-2">
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <input
          type="search"
          aria-label={`Search the log of ${check.argv.join(" ")}`}
          placeholder="Search the log…"
          value={query}
          onChange={(event) => {
            setQuery(event.target.value);
            setMatch(0);
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              step(event.shiftKey ? -1 : 1);
            }
          }}
          className={`${inputClass.replace("w-full", "w-56")} py-1 text-xs`}
        />
        {wanted && (
          <span className="text-xs text-muted">
            {matches.length === 0
              ? "No matches"
              : `${match + 1} of ${matches.length}`}
          </span>
        )}
        <Button
          className="px-2 py-1 text-xs"
          disabled={matches.length === 0}
          aria-label="Previous match"
          onClick={() => step(-1)}
        >
          ↑
        </Button>
        <Button
          className="px-2 py-1 text-xs"
          disabled={matches.length === 0}
          aria-label="Next match"
          onClick={() => step(1)}
        >
          ↓
        </Button>
        <Button
          className="px-2.5 py-1 text-xs"
          disabled={firstError < 0}
          onClick={() => reveal(firstError)}
        >
          First error
        </Button>
        <Button
          className="px-2.5 py-1 text-xs"
          disabled={running || !check.logDigest}
          onClick={download}
        >
          Download log
        </Button>
        {saved && <span className="text-xs text-muted">{saved}</span>}
      </div>
      {error && <InlineAlert>{error}</InlineAlert>}
      {log?.cut && (
        <p className="m-0 text-xs text-warn">
          The command wrote {formatBytes(log.bytes)}; the log keeps its first
          and last part, and marks where the rest was left out.
        </p>
      )}
      {log === null ? (
        <p className="m-0 text-xs text-muted">Loading the log…</p>
      ) : lines.length === 0 ? (
        <p className="m-0 text-xs text-muted">
          {running ? "No output yet." : "The command wrote nothing."}
        </p>
      ) : (
        <div
          ref={box}
          role="log"
          aria-label={`Log of ${check.argv.join(" ")}`}
          className="max-h-[60vh] overflow-auto border border-line bg-canvas font-mono text-[11px] leading-5"
          onScroll={(event) => {
            const element = event.currentTarget;
            following.current =
              element.scrollTop + element.clientHeight >=
              element.scrollHeight - rowHeight;
            setScroll({ top: element.scrollTop, height: element.clientHeight });
          }}
        >
          <div
            className="relative"
            style={{
              height: lines.length * rowHeight,
              minWidth: `calc(${widest + digits + 4}ch)`,
            }}
          >
            {lines.slice(first, last).map((line, offset) => {
              const index = first + offset;
              const current =
                marked === index ||
                (matches.length > 0 && matches[match] === index);
              return (
                <div
                  key={index}
                  data-line={index + 1}
                  className={`absolute right-0 left-0 flex h-5 whitespace-pre ${current ? "bg-accent/15" : ""}`}
                  style={{ top: index * rowHeight }}
                >
                  <span
                    className="sticky left-0 flex-none bg-canvas pr-2 pl-2 text-right text-faint select-none"
                    style={{ width: `${digits + 2}ch` }}
                  >
                    {index + 1}
                  </span>
                  <span className="pr-3">
                    <Highlighted spans={line.spans} wanted={wanted} />
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

/** A line's styled pieces, with what the search wants marked. */
function Highlighted({ spans, wanted }: { spans: AnsiSpan[]; wanted: string }) {
  return (
    <>
      {spans.map((span, index) => {
        if (!wanted)
          return (
            <span key={index} className={span.className}>
              {span.text}
            </span>
          );
        const pieces: React.ReactNode[] = [];
        const lower = span.text.toLowerCase();
        let at = 0;
        for (
          let found = lower.indexOf(wanted);
          found >= 0;
          found = lower.indexOf(wanted, at)
        ) {
          pieces.push(span.text.slice(at, found));
          pieces.push(
            <mark key={found} className="bg-warn/40 text-ink-strong">
              {span.text.slice(found, found + wanted.length)}
            </mark>,
          );
          at = found + wanted.length;
        }
        pieces.push(span.text.slice(at));
        return (
          <span key={index} className={span.className}>
            {pieces}
          </span>
        );
      })}
    </>
  );
}
