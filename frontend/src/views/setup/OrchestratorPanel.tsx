import type { ConfigFile, Runtime } from "../../types";
import { inputClass } from "../common";
import { RuntimeOptions } from "../runtimes/RuntimeOptions";
import { useRuntimes } from "../runtimes/useRuntimes";
import { useOverride } from "./overrides";
import {
  CheckRow,
  NumberInput,
  Setting,
  SetupPanel,
  TextSetting,
  type UpdateConfig,
} from "./shared";

export function OrchestratorPanel({
  orchestrator,
  update,
}: {
  orchestrator: ConfigFile["orchestrator"];
  update: UpdateConfig;
}) {
  const runtimeOverride = useOverride("orchestrator.runtime");
  const runtimes = useRuntimes();
  return (
    <SetupPanel
      title="Orchestrator"
      description={
        <>
          The orchestrator reads each task, chooses an agent for every
          &quot;auto&quot; stage, and scores every agent stage for its own
          work, knowing the model each score routes to. Go validates every
          decision.
        </>
      }
    >
      <Setting label="Runtime" field="orchestrator.runtime">
        <select
          className={inputClass}
          aria-label="Orchestrator runtime"
          value={orchestrator.runtime}
          disabled={Boolean(runtimeOverride)}
          onChange={(event) =>
            update((draft) => {
              draft.orchestrator.runtime = event.target.value as Runtime | "";
            })
          }
        >
          {/* inkuna.yaml may omit the runtime; Go then defaults to Claude. */}
          <option value="">Default (Claude Code)</option>
          <RuntimeOptions runtimes={runtimes} value={orchestrator.runtime} />
        </select>
      </Setting>
      <TextSetting
        label="Model override"
        ariaLabel="Orchestrator model"
        field="orchestrator.model"
        value={orchestrator.model}
        placeholder="blank keeps policy routing"
        onChange={(value) =>
          update((draft) => {
            draft.orchestrator.model = value;
          })
        }
      />
      <TextSetting
        label="Reasoning effort"
        ariaLabel="Orchestrator reasoning effort"
        field="orchestrator.reasoningEffort"
        value={orchestrator.reasoningEffort}
        placeholder="low, medium, high, xhigh"
        onChange={(value) =>
          update((draft) => {
            draft.orchestrator.reasoningEffort = value.trim() || undefined;
          })
        }
      />
      <Setting
        label="Max turns"
        field="orchestrator.maxTurns"
        hint="0 leaves the planning call unlimited within its timeout"
      >
        <NumberInput
          aria-label="Orchestrator max turns"
          min={0}
          value={orchestrator.maxTurns ?? 0}
          onCommit={(value) =>
            update((draft) => {
              draft.orchestrator.maxTurns = value || undefined;
            })
          }
        />
      </Setting>
      <CheckRow
        aria-label="Let staffing read the repository"
        checked={orchestrator.readTools ?? false}
        onChange={(event) =>
          update((draft) => {
            draft.orchestrator.readTools = event.target.checked || undefined;
          })
        }
      >
        Let staffing read the repository with read-only tools (otherwise it has
        no tools and works from the request alone)
      </CheckRow>
      <CheckRow
        aria-label="Disable orchestrator"
        checked={orchestrator.disabled ?? false}
        onChange={(event) =>
          update((draft) => {
            draft.orchestrator.disabled = event.target.checked;
          })
        }
      >
        Disable the orchestrator (deterministic staffing only)
      </CheckRow>
    </SetupPanel>
  );
}
