import type {
  RepositoryTracker,
  TrackerAllowance,
  TrackerKind,
  TrackerOverride,
  TrackerUpdate,
} from "../../types";

const kinds: { kind: TrackerKind; label: string }[] = [
  { kind: "status", label: "Change status" },
  { kind: "comment", label: "Comment" },
  { kind: "edit", label: "Edit fields" },
  { kind: "create", label: "Create issues" },
];

const updates: { update: TrackerUpdate; label: string }[] = [
  { update: "start", label: "In Progress when a run starts" },
  { update: "result", label: "Comment the result when a run ends" },
  { update: "done", label: "Offer Mark done" },
];

/** An override that says nothing is no override at all. */
function tidy(override: TrackerOverride): TrackerOverride | undefined {
  const next: TrackerOverride = {};
  if (override.writes) next.writes = override.writes;
  const kept = Object.entries(override.updates ?? {}).filter(
    ([, value]) => value === false,
  );
  if (kept.length > 0) next.updates = Object.fromEntries(kept);
  return next.writes || next.updates ? next : undefined;
}

/**
 * This machine's switches for a repository's tracker writes. Each one can
 * only turn off what the repository's tracker.yaml allows; Go works out what
 * is left, and the repository table shows it once saved.
 */
export function TrackerOverrideFields({
  value,
  ceiling,
  onChange,
}: {
  value: TrackerOverride | null | undefined;
  /** What tracker.yaml allows, when Go has read it for this repository. */
  ceiling?: TrackerAllowance;
  onChange(value: TrackerOverride | undefined): void;
}) {
  const writes = value?.writes ?? null;
  const allowedHere = (kind: TrackerKind) =>
    writes === null || writes.includes(kind);

  function toggleKind(kind: TrackerKind, on: boolean) {
    const next = kinds
      .map((entry) => entry.kind)
      .filter((candidate) =>
        candidate === kind ? on : allowedHere(candidate),
      );
    onChange(
      tidy({
        ...value,
        writes: next.length === kinds.length ? null : next,
      }),
    );
  }

  function toggleUpdate(update: TrackerUpdate, on: boolean) {
    const nextUpdates = { ...(value?.updates ?? {}) };
    if (on) delete nextUpdates[update];
    else nextUpdates[update] = false;
    onChange(tidy({ ...value, updates: nextUpdates }));
  }

  const notAllowed = (allowed: boolean) =>
    ceiling && !allowed ? (
      <span className="text-muted"> (off in tracker.yaml)</span>
    ) : null;

  return (
    <fieldset className="col-span-full m-0 grid gap-3 rounded-md border border-line p-3">
      <legend className="px-1 text-xs text-ink-soft">
        Tracker writes on this machine
      </legend>
      <p className="m-0 text-[11px] leading-relaxed text-muted">
        These switches can turn off what the repository&apos;s
        .inkuna/tracker.yaml allows, never turn on what it does not.
      </p>
      <div className="grid grid-cols-[repeat(auto-fit,minmax(220px,1fr))] gap-x-4">
        <div role="group" aria-label="Changes agents may ask for">
          <span className="text-[11px] text-muted">Agents may ask to</span>
          {kinds.map(({ kind, label }) => (
            <label
              key={kind}
              className="mt-1.5 flex items-start gap-2 text-xs text-ink-soft"
            >
              <input
                type="checkbox"
                className="mt-0.5 flex-none accent-accent"
                checked={allowedHere(kind)}
                onChange={(event) => toggleKind(kind, event.target.checked)}
              />
              <span>
                {label}
                {notAllowed(ceiling?.writes?.includes(kind) ?? false)}
              </span>
            </label>
          ))}
        </div>
        <div role="group" aria-label="Updates Inkuna makes">
          <span className="text-[11px] text-muted">Inkuna updates</span>
          {updates.map(({ update, label }) => (
            <label
              key={update}
              className="mt-1.5 flex items-start gap-2 text-xs text-ink-soft"
            >
              <input
                type="checkbox"
                className="mt-0.5 flex-none accent-accent"
                checked={value?.updates?.[update] !== false}
                onChange={(event) => toggleUpdate(update, event.target.checked)}
              />
              <span>
                {label}
                {notAllowed(ceiling?.updates?.[update] ?? false)}
              </span>
            </label>
          ))}
        </div>
      </div>
    </fieldset>
  );
}

/** Says what a saved repository's runs may write, as Go resolved it. */
export function TrackerSummary({ view }: { view?: RepositoryTracker }) {
  if (!view) {
    return <span className="text-muted">read when saved</span>;
  }
  if (view.error) {
    return <span className="wrap-anywhere text-danger">{view.error}</span>;
  }
  const writes = view.effective.writes ?? [];
  const on = updates
    .filter(({ update }) => view.effective.updates?.[update])
    .map(({ update }) => update);
  if (writes.length === 0 && on.length === 0) {
    return (
      <span className="wrap-anywhere text-muted">
        {view.note || "nothing is written"}
      </span>
    );
  }
  return (
    <span className="grid gap-0.5 text-muted">
      {writes.length > 0 && (
        <span>
          agents may ask: <b className="text-ink-soft">{writes.join(", ")}</b>
        </span>
      )}
      {on.length > 0 && (
        <span>
          Inkuna updates: <b className="text-ink-soft">{on.join(", ")}</b>
        </span>
      )}
      {view.project && <span>project {view.project}</span>}
    </span>
  );
}
