import type { ConfigFile } from "../../types";
import { Chip } from "../common";
import { useOverride } from "./overrides";
import {
  CheckRow,
  NumberInput,
  Setting,
  SetupPanel,
  type UpdateConfig,
} from "./shared";

export function ConcurrencyPanel({
  concurrency,
  notifications,
  update,
}: {
  concurrency: ConfigFile["concurrency"];
  notifications: ConfigFile["notifications"];
  update: UpdateConfig;
}) {
  const tasksOverride = useOverride("concurrency.maxTasks");
  const workersOverride = useOverride("concurrency.maxWorkers");
  const desktopOverride = useOverride("notifications.desktop");
  return (
    <SetupPanel title="Concurrency & notifications">
      <Setting label="Maximum concurrent tasks" field="concurrency.maxTasks">
        <NumberInput
          aria-label="Max tasks"
          disabled={Boolean(tasksOverride)}
          min={1}
          value={concurrency.maxTasks ?? 3}
          onCommit={(value) =>
            update((draft) => {
              draft.concurrency.maxTasks = value;
            })
          }
        />
      </Setting>
      <Setting label="Maximum workers per task" field="concurrency.maxWorkers">
        <NumberInput
          aria-label="Max workers"
          disabled={Boolean(workersOverride)}
          min={1}
          value={concurrency.maxWorkers ?? 2}
          onCommit={(value) =>
            update((draft) => {
              draft.concurrency.maxWorkers = value;
            })
          }
        />
      </Setting>
      <CheckRow
        aria-label="Desktop notifications"
        checked={notifications.desktop}
        disabled={Boolean(desktopOverride)}
        onChange={(event) =>
          update((draft) => {
            draft.notifications.desktop = event.target.checked;
          })
        }
      >
        Desktop notifications when input is needed or a task finishes
        {desktopOverride && (
          <>
            {" "}
            <Chip tone="warn">
              overridden by ${desktopOverride.variable}={desktopOverride.value}
            </Chip>
          </>
        )}
      </CheckRow>
    </SetupPanel>
  );
}
