import type {
  ConfigFile,
  RoutingBand,
  RoutingPolicy,
  Runtime,
} from "../../types";
import { Button, Chip, inputClass } from "../common";
import { useRuntimes } from "../runtimes/useRuntimes";
import {
  FieldErrors,
  LinkButton,
  NumberInput,
  SetupPanel,
  type UpdateConfig,
} from "./shared";

/**
 * Which model each complexity score gets inside a runtime. A runtime keeps the
 * built-in bands until it is customized; Go checks that custom bands cover
 * 1 to 10 without gaps or overlaps.
 */
export function RoutingPanel({
  routing,
  builtIn,
  update,
}: {
  routing: ConfigFile["routing"];
  builtIn: RoutingPolicy | undefined;
  update: UpdateConfig;
}) {
  const runtimes = useRuntimes();
  function setBands(id: Runtime, bands: RoutingBand[] | undefined) {
    update((draft) => {
      const next = { ...(draft.routing.bands ?? {}) };
      if (bands) next[id] = bands;
      else delete next[id];
      draft.routing.bands = Object.keys(next).length > 0 ? next : undefined;
    });
  }
  return (
    <SetupPanel
      title="Model routing"
      description="Planners score each agent step from 1 to 10; the band holding the score picks the model inside the step's runtime. Explicit model overrides on an agent or a step still win."
    >
      <FieldErrors field="routing.bands" />
      {runtimes.map(({ name: id, label }) => {
        const custom = routing.bands?.[id];
        const bands = custom ?? builtIn?.bands[id] ?? [];
        const patch = (index: number, next: Partial<RoutingBand>) =>
          setBands(
            id,
            bands.map((band, position) =>
              position === index ? { ...band, ...next } : band,
            ),
          );
        return (
          <div
            key={id}
            role="group"
            aria-label={`${label} routing`}
            className="mt-4 border-t border-line pt-3 first:border-t-0"
          >
            <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
              <span className="flex min-w-0 flex-wrap items-center gap-2 text-xs text-ink-soft">
                {label}
                <Chip tone={custom ? "accent" : "neutral"}>
                  {custom
                    ? "custom"
                    : bands.length > 0
                      ? "built-in"
                      : "CLI default"}
                </Chip>
              </span>
              {custom ? (
                <LinkButton
                  aria-label={`Use built-in bands for ${label}`}
                  onClick={() => setBands(id, undefined)}
                >
                  Use built-in
                </LinkButton>
              ) : (
                <LinkButton
                  aria-label={`Customize bands for ${label}`}
                  onClick={() =>
                    setBands(
                      id,
                      // An added runtime has no built-in bands to start from.
                      bands.length > 0
                        ? bands.map((band) => ({ ...band }))
                        : [{ minimum: 1, maximum: 10, model: "" }],
                    )
                  }
                >
                  Customize
                </LinkButton>
              )}
            </div>
            {bands.length === 0 && (
              <p className="mt-2 mb-0 text-xs text-muted">
                No bands: the CLI picks its own default model.
              </p>
            )}
            <ul className="m-0 mt-2 grid list-none gap-2 p-0">
              {bands.map((band, index) => (
                <li
                  key={index}
                  className="grid min-w-0 grid-cols-[64px_64px_minmax(0,1fr)_auto] items-center gap-2"
                >
                  {custom ? (
                    <>
                      <NumberInput
                        aria-label={`${label} band ${index + 1} minimum`}
                        min={1}
                        value={band.minimum}
                        onCommit={(minimum) => patch(index, { minimum })}
                      />
                      <NumberInput
                        aria-label={`${label} band ${index + 1} maximum`}
                        min={1}
                        value={band.maximum}
                        onCommit={(maximum) => patch(index, { maximum })}
                      />
                      <input
                        className={inputClass}
                        aria-label={`${label} band ${index + 1} model`}
                        placeholder="model"
                        value={band.model}
                        onChange={(event) =>
                          patch(index, { model: event.target.value })
                        }
                      />
                      <LinkButton
                        aria-label={`Remove ${label} band ${index + 1}`}
                        onClick={() =>
                          setBands(
                            id,
                            bands.filter((_, position) => position !== index),
                          )
                        }
                      >
                        Remove
                      </LinkButton>
                      <input
                        className={`${inputClass} col-start-3`}
                        aria-label={`${label} band ${index + 1} reasoning effort`}
                        placeholder="effort (optional)"
                        value={band.reasoningEffort ?? ""}
                        onChange={(event) =>
                          patch(index, {
                            reasoningEffort:
                              event.target.value.trim() || undefined,
                          })
                        }
                      />
                    </>
                  ) : (
                    <span className="col-span-full text-xs wrap-anywhere text-muted">
                      {band.minimum}–{band.maximum}{" "}
                      <span className="font-mono text-ink">{band.model}</span>
                      {band.reasoningEffort && ` · ${band.reasoningEffort}`}
                    </span>
                  )}
                </li>
              ))}
            </ul>
            {custom && (
              <div className="mt-2">
                <Button
                  aria-label={`Add a ${label} band`}
                  onClick={() => {
                    const last = bands[bands.length - 1];
                    const minimum = Math.min((last?.maximum ?? 0) + 1, 10);
                    setBands(id, [
                      ...bands,
                      { minimum, maximum: 10, model: "" },
                    ]);
                  }}
                >
                  Add band
                </Button>
              </div>
            )}
            <FieldErrors field={`routing.bands.${id}`} />
          </div>
        );
      })}
    </SetupPanel>
  );
}
