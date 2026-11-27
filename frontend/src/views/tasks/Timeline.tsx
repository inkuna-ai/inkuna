import type { RunEvent } from "../../types";
import type { ShownRound } from "./rounds";

const MAX_EVENTS = 500;

const dotTones: Record<string, string> = {
  error: "bg-danger",
  warning: "bg-danger",
  completion: "bg-ok",
};

/**
 * The events of the rounds shown, oldest first. With several rounds each
 * gets a heading.
 */
export function Timeline({ rounds }: { rounds: ShownRound[] }) {
  const headed = rounds.length > 1;
  if (rounds.every((round) => round.events.length === 0)) {
    return (
      <div className="mt-5 min-w-0">
        <p className="m-0 text-xs text-muted">
          Waiting for the first run event…
        </p>
      </div>
    );
  }
  return (
    <div className="mt-5 min-w-0">
      {rounds.map((round, index) => (
        <section key={round.round} aria-label={`Round ${round.round} events`}>
          {headed && (
            <h3
              className={`${index === 0 ? "mt-0" : "mt-4"} mb-3 font-mono text-[10px] font-medium tracking-wider text-muted uppercase`}
            >
              Round {round.round}
            </h3>
          )}
          <Events events={round.events} hidden={round.hiddenEvents} />
        </section>
      ))}
    </div>
  );
}

/**
 * Each row is its own three-track grid (time, rail, content). The rail is the
 * left border of zero-width segments above and below the dot, and rows have no
 * gap between them, so the line runs unbroken without any offsets.
 */
function Events({ events, hidden }: { events: RunEvent[]; hidden: number }) {
  const visibleEvents = events.slice(-MAX_EVENTS);
  const older = hidden + events.length - visibleEvents.length;
  return (
    <>
      {older > 0 && (
        <p className="mt-0 mb-4 text-xs text-muted">
          {older} older events hidden
        </p>
      )}
      {visibleEvents.length === 0 && (
        <p className="mt-0 mb-5 text-xs text-muted">No events.</p>
      )}
      {visibleEvents.map((event, index) => (
        <div
          key={`${event.runId}-${event.sequence}`}
          className="grid grid-cols-[auto_auto_minmax(0,1fr)] gap-x-3"
        >
          <time className="min-w-[72px] pt-0.5 font-mono text-[10px] leading-4 whitespace-nowrap text-faint tabular-nums">
            {new Date(event.timestamp).toLocaleTimeString([], {
              hour: "2-digit",
              minute: "2-digit",
              second: "2-digit",
            })}
          </time>
          <div aria-hidden="true" className="flex w-3 flex-col items-center">
            <span
              className={`h-1.5 w-0 border-l ${
                index === 0 ? "border-transparent" : "border-line-strong"
              }`}
            />
            <span
              className={`size-2 flex-none rounded-full ${
                dotTones[event.kind] ?? "bg-accent"
              }`}
            />
            <span className="w-0 flex-1 border-l border-line-strong" />
          </div>
          <p className="m-0 min-w-0 pb-5 text-xs leading-5 wrap-anywhere">
            {event.summary}
          </p>
        </div>
      ))}
    </>
  );
}
