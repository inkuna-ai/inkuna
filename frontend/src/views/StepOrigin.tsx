import { useEffect, useRef, useState } from "react";
import type { StepOrigin } from "../types";
import { originLabel, scopeName } from "./origin";

/**
 * The badge of a step planned from a shared stage or group, which opens on
 * what the plan recorded: the definitions it came through, where they are
 * defined, and the fields the flow overrode. It is the approved plan's, so
 * a later edit to a library does not change it.
 */
export function StepOriginBadge({
  origin,
  step,
}: {
  origin?: StepOrigin | null;
  /** The step's title, naming the popover. */
  step: string;
}) {
  const summary = useRef<HTMLElement>(null);
  const popover = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [place, setPlace] = useState<{ top: number; left: number } | null>(
    null,
  );
  // The step tables scroll sideways, which clips anything positioned
  // inside them, so the open explanation is placed against the viewport,
  // below the badge or above it when there is no room, and follows the
  // badge as the page or a table scrolls.
  useEffect(() => {
    if (!open) return;
    const measure = () => {
      const badge = summary.current?.getBoundingClientRect();
      if (!badge) return;
      const height = popover.current?.offsetHeight ?? 0;
      const width = popover.current?.offsetWidth ?? 0;
      const below = badge.bottom + 4;
      const top =
        below + height > window.innerHeight && badge.top - 4 - height >= 0
          ? badge.top - 4 - height
          : below;
      const left = Math.max(
        8,
        Math.min(badge.left, window.innerWidth - width - 8),
      );
      setPlace({ top, left });
    };
    const frame = requestAnimationFrame(measure);
    window.addEventListener("scroll", measure, true);
    window.addEventListener("resize", measure);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("scroll", measure, true);
      window.removeEventListener("resize", measure);
    };
  }, [open]);
  if (!origin) return null;
  const through = [
    ...(origin.stage ? [`shared stage ${origin.stage}`] : []),
    ...[...(origin.groups ?? [])].reverse().map((group) => `group ${group}`),
  ].join(", in ");
  return (
    <details
      className="inline-block align-middle"
      onToggle={(event) => {
        const shown = event.currentTarget.open;
        setOpen(shown);
        if (!shown) setPlace(null);
      }}
    >
      <summary
        ref={summary}
        className="inline-flex cursor-pointer list-none rounded-full border border-accent-line px-2 py-0.5 font-mono text-[10px] whitespace-nowrap text-accent [&::-webkit-details-marker]:hidden"
      >
        {originLabel(origin)}
      </summary>
      <div
        ref={popover}
        role="group"
        aria-label={`Where ${step} came from`}
        style={place ?? undefined}
        // Hidden until placed, so it never shows where it would be clipped.
        className={`fixed z-30 ${place ? "" : "invisible"} grid w-max max-w-[320px] gap-1.5 border border-line-strong bg-raised p-3 text-[11px] leading-relaxed whitespace-normal text-ink-soft shadow-lg`}
      >
        <span className="wrap-anywhere">
          From {through || "a shared definition"}.
        </span>
        <span className="wrap-anywhere">
          {origin.file ? (
            <>
              Defined in{" "}
              <span className="font-mono text-ink">{origin.file}</span> (
              {scopeName(origin.scope)}).
            </>
          ) : (
            "Built into Inkuna."
          )}
        </span>
        <span className="wrap-anywhere">
          {origin.overrides?.length
            ? `Overridden by this flow: ${origin.overrides.join(", ")}.`
            : "This flow overrides none of its fields."}
        </span>
      </div>
    </details>
  );
}
