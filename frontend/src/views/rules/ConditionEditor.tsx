import type { RuleCondition, TaskKind } from "../../types";
import { Field, inputClass } from "../common";
import { TagInput } from "../TagInput";
import { taskKinds } from "./model";

const numberClass = `${inputClass} max-w-24`;

/** Every filled predicate must hold; an empty one matches anything. */
export function ConditionEditor({
  when,
  onChange,
}: {
  when: RuleCondition;
  onChange(when: RuleCondition): void;
}) {
  const kinds = when.kind ?? [];
  function toggleKind(kind: TaskKind) {
    onChange({
      ...when,
      kind: kinds.includes(kind)
        ? kinds.filter((item) => item !== kind)
        : [...kinds, kind],
    });
  }
  function setComplexity(edge: "min" | "max", value: string) {
    if (value === "" && edge === "min") {
      onChange({ ...when, complexity: undefined });
      return;
    }
    const current = when.complexity ?? { min: 1, max: 10 };
    onChange({
      ...when,
      complexity: { ...current, [edge]: Number(value) || current[edge] },
    });
  }
  return (
    <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-2">
      <fieldset className="col-span-full m-0 min-w-0 border-0 p-0">
        <legend className="mb-1.5 text-xs text-ink-soft">
          Task kinds (none selected matches every kind)
        </legend>
        <div className="flex flex-wrap gap-x-4 gap-y-1.5">
          {taskKinds.map((kind) => (
            <label key={kind} className="flex items-center gap-1.5 text-xs">
              <input
                type="checkbox"
                checked={kinds.includes(kind)}
                onChange={() => toggleKind(kind)}
              />
              {kind}
            </label>
          ))}
        </div>
      </fieldset>
      <Field label="Labels (any of, comma separated)">
        <TagInput
          label="Labels"
          values={when.labels}
          placeholder="bug, regression"
          onChange={(labels) => onChange({ ...when, labels })}
        />
      </Field>
      <Field label="Objective mentions (any of)">
        <TagInput
          label="Keywords"
          values={when.keywords}
          placeholder="crash, panic"
          onChange={(keywords) => onChange({ ...when, keywords })}
        />
      </Field>
      <Field label="Source (any of)">
        <TagInput
          label="Sources"
          values={when.source}
          placeholder="request, linear"
          onChange={(source) => onChange({ ...when, source })}
        />
      </Field>
      <Field label="Repositories (any of)">
        <TagInput
          label="Repositories"
          values={when.repositories}
          placeholder="inkuna"
          onChange={(repositories) => onChange({ ...when, repositories })}
        />
      </Field>
      <div className="my-4 grid min-w-0 gap-1.5 text-xs text-ink-soft">
        Complexity range (1–10, blank for any)
        <div className="flex items-center gap-2">
          <input
            aria-label="Minimum complexity"
            type="number"
            min={1}
            max={10}
            className={numberClass}
            value={when.complexity?.min ?? ""}
            onChange={(event) => setComplexity("min", event.target.value)}
          />
          <span aria-hidden="true">–</span>
          <input
            aria-label="Maximum complexity"
            type="number"
            min={1}
            max={10}
            className={numberClass}
            value={when.complexity?.max ?? ""}
            onChange={(event) => setComplexity("max", event.target.value)}
          />
        </div>
      </div>
      <Field label="Minimum priority (blank for any)">
        <input
          aria-label="Minimum priority"
          type="number"
          min={0}
          className={numberClass}
          value={when.minPriority || ""}
          onChange={(event) =>
            onChange({
              ...when,
              minPriority: Number(event.target.value) || undefined,
            })
          }
        />
      </Field>
    </div>
  );
}
