import { useState } from "react";
import { Button, Notice, inputClass } from "../common";
import {
  BrowseButton,
  LinkButton,
  SetupPanel,
  TextSetting,
  type UpdateConfig,
} from "./shared";

/** Global agent directories and the data directory redirect. */
export function StoragePanel({
  directories,
  dataDir,
  effectiveDataDir,
  update,
  onError,
}: {
  directories: string[];
  dataDir: string | undefined;
  effectiveDataDir: string;
  update: UpdateConfig;
  onError(message: string): void;
}) {
  const [directory, setDirectory] = useState("");
  function add(path: string) {
    const value = path.trim();
    if (!value) return;
    update((draft) => {
      const current = draft.agents.directories ?? [];
      if (!current.includes(value)) {
        draft.agents.directories = [...current, value];
      }
    });
    setDirectory("");
  }
  return (
    <SetupPanel
      title="Agent directories & storage"
      wide
      description="Global agent directories are searched after the built-ins and before a repository's .inkuna/agents. The first one is where the Agents view saves agents for all projects."
    >
      {directories.length === 0 ? (
        <p className="mt-4 mb-0 text-xs wrap-anywhere text-muted">
          Using the default: {effectiveDataDir || "<data dir>"}/agents
        </p>
      ) : (
        <ul
          aria-label="Global agent directories"
          className="m-0 mt-4 grid list-none gap-1.5 p-0"
        >
          {directories.map((path) => (
            <li
              key={path}
              className="flex min-w-0 items-center justify-between gap-3 border-b border-line py-1.5 text-xs"
            >
              <span className="min-w-0 font-mono wrap-anywhere">{path}</span>
              <LinkButton
                aria-label={`Remove agent directory ${path}`}
                onClick={() =>
                  update((draft) => {
                    draft.agents.directories = (
                      draft.agents.directories ?? []
                    ).filter((item) => item !== path);
                  })
                }
              >
                Remove
              </LinkButton>
            </li>
          ))}
        </ul>
      )}
      <div className="mt-3 flex min-w-0 flex-wrap gap-2">
        <input
          className={`${inputClass} min-w-[180px] flex-1`}
          aria-label="Agent directory path"
          placeholder="/absolute/path/to/agents"
          value={directory}
          onChange={(event) => setDirectory(event.target.value)}
        />
        <BrowseButton
          title="Choose a global agents directory"
          onPick={add}
          onError={onError}
        />
        <Button disabled={!directory.trim()} onClick={() => add(directory)}>
          Add directory
        </Button>
      </div>
      <TextSetting
        label="Data directory redirect"
        field="dataDir"
        value={dataDir}
        placeholder={effectiveDataDir}
        onChange={(value) =>
          update((draft) => {
            draft.dataDir = value.trim() || undefined;
          })
        }
      />
      <Notice>
        The data directory holds the database, artifacts, and worktrees. Saving
        a redirect copies this configuration there and makes Inkuna use that
        folder from the next launch; nothing else is moved for you. Leave it
        blank unless you mean to relocate.
      </Notice>
    </SetupPanel>
  );
}
