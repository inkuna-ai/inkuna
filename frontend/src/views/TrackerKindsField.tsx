import { type TrackerKind, trackerKinds } from "../types";

/**
 * The kinds of tracker change an agent or a rule stage asks for, one box
 * each. A step gets only those the repository's tracker settings allow; Go
 * checks the rest when the agent or rules are saved.
 */
export function TrackerKindsField({
  label,
  values,
  onChange,
}: {
  label: string;
  values?: TrackerKind[];
  onChange(values: TrackerKind[]): void;
}) {
  const chosen = values ?? [];
  return (
    <fieldset className="m-0 min-w-0 border-0 p-0">
      <legend className="mb-1.5 text-xs text-ink-soft">{label}</legend>
      <div className="flex flex-wrap gap-x-4 gap-y-1.5">
        {trackerKinds.map((kind) => (
          <label key={kind} className="flex items-center gap-1.5 text-xs">
            <input
              type="checkbox"
              checked={chosen.includes(kind)}
              onChange={(event) =>
                onChange(
                  trackerKinds.filter((other) =>
                    other === kind
                      ? event.target.checked
                      : chosen.includes(other),
                  ),
                )
              }
            />
            {kind}
          </label>
        ))}
      </div>
    </fieldset>
  );
}
