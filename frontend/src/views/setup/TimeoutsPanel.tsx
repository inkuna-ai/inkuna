import type { ConfigFile } from "../../types";
import { SetupPanel, TextSetting, type UpdateConfig } from "./shared";

const timeouts: Array<{ key: string; label: string }> = [
  { key: "planning", label: "Planning (orchestrator)" },
  { key: "worker", label: "Worker step" },
  { key: "verifier", label: "Verifier step" },
  { key: "approval", label: "Waiting for approval" },
  { key: "health", label: "Health check per tool" },
  { key: "auth", label: "Interactive sign-in" },
  { key: "linear", label: "Linear request" },
];

export function TimeoutsPanel({
  values,
  defaults,
  update,
}: {
  values: ConfigFile["timeouts"];
  defaults: ConfigFile["timeouts"] | undefined;
  update: UpdateConfig;
}) {
  return (
    <SetupPanel
      title="Timeouts"
      description="Durations such as 90s, 5m, or 1h. Blank uses the default shown."
    >
      <div className="grid min-w-0 grid-cols-[repeat(auto-fit,minmax(180px,1fr))] gap-x-4">
        {timeouts.map(({ key, label }) => (
          <TextSetting
            key={key}
            label={label}
            field={`timeouts.${key}`}
            value={values[key]}
            placeholder={defaults?.[key]}
            onChange={(value) =>
              update((draft) => {
                draft.timeouts[key] = value.trim() || undefined;
              })
            }
          />
        ))}
      </div>
    </SetupPanel>
  );
}
