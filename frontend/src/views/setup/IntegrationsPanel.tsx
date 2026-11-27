import type { ConfigFile } from "../../types";
import { inputClass } from "../common";
import { useOverride } from "./overrides";
import { Setting, SetupPanel, TextSetting, type UpdateConfig } from "./shared";

export function IntegrationsPanel({
  linear,
  github,
  defaults,
  update,
}: {
  linear: ConfigFile["linear"];
  github: ConfigFile["github"];
  defaults: ConfigFile | undefined;
  update: UpdateConfig;
}) {
  const protocolOverride = useOverride("github.protocol");
  return (
    <SetupPanel
      title="Linear & GitHub"
      description="Linear and GitHub issues are intake sources, written to only as far as a repository's .inkuna/tracker.yaml allows. Other GitHub writes stay behind their own approval. Sign-in lives under Connections."
    >
      <TextSetting
        label="Linear GraphQL endpoint"
        field="linear.endpoint"
        value={linear.endpoint}
        placeholder={defaults?.linear.endpoint}
        onChange={(value) =>
          update((draft) => {
            draft.linear.endpoint = value.trim() || undefined;
          })
        }
      />
      <TextSetting
        label="GitHub host"
        field="github.host"
        value={github.host}
        placeholder={defaults?.github.host}
        onChange={(value) =>
          update((draft) => {
            draft.github.host = value.trim() || undefined;
          })
        }
      />
      <Setting label="Git protocol" field="github.protocol">
        <select
          className={inputClass}
          value={github.protocol ?? ""}
          disabled={Boolean(protocolOverride)}
          onChange={(event) =>
            update((draft) => {
              draft.github.protocol = event.target.value || undefined;
            })
          }
        >
          <option value="">
            Default ({defaults?.github.protocol ?? "https"})
          </option>
          <option value="https">https</option>
          <option value="ssh">ssh</option>
        </select>
      </Setting>
      <TextSetting
        label="GitHub CLI binary"
        field="github.binary"
        value={github.binary}
        placeholder={defaults?.github.binary}
        onChange={(value) =>
          update((draft) => {
            draft.github.binary = value.trim() || undefined;
          })
        }
      />
    </SetupPanel>
  );
}
