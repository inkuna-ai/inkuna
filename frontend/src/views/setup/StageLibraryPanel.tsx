import { useState } from "react";
import type { LibraryProblem } from "../../types";
import { Button, Chip, Notice, inputClass } from "../common";
import { useOverride } from "./overrides";
import {
  BrowseButton,
  FieldErrors,
  LinkButton,
  SetupPanel,
  type UpdateConfig,
} from "./shared";

/**
 * Global stage library directories, and the problems Go found in their
 * files. The problems describe the saved configuration and never block a
 * save: the files are edited outside inkuna.yaml.
 */
export function StageLibraryPanel({
  directories,
  effective,
  problems,
  update,
  onError,
}: {
  directories: string[];
  /** The directories Go reads now, after the environment and the default. */
  effective: string[];
  problems: LibraryProblem[];
  update: UpdateConfig;
  onError(message: string): void;
}) {
  const [directory, setDirectory] = useState("");
  const override = useOverride("stages.directories");
  function add(path: string) {
    const value = path.trim();
    if (!value) return;
    update((draft) => {
      const current = draft.stages.directories ?? [];
      if (!current.includes(value)) {
        draft.stages.directories = [...current, value];
      }
    });
    setDirectory("");
  }
  return (
    <SetupPanel
      title="Stage library"
      wide
      description="Shared stages and groups every repository's rules can use. Each *.yaml file holds stages: and groups: in the rules schema. A definition here replaces a built-in one with the same name, and a repository's own replaces it."
    >
      {override && (
        <p className="mt-4 mb-0 flex flex-wrap items-center gap-2 text-xs text-muted">
          <Chip tone="warn">
            overridden by ${override.variable}={override.value}
          </Chip>
          Inkuna reads {effective.join(", ")}.
        </p>
      )}
      {directories.length === 0 ? (
        !override && (
          <p className="mt-4 mb-0 text-xs wrap-anywhere text-muted">
            Using the default: {effective[0] ?? "<data dir>/stages"}
          </p>
        )
      ) : (
        <ul
          aria-label="Stage library directories"
          className="m-0 mt-4 grid list-none gap-1.5 p-0"
        >
          {directories.map((path) => (
            <li
              key={path}
              className="flex min-w-0 items-center justify-between gap-3 border-b border-line py-1.5 text-xs"
            >
              <span className="min-w-0 font-mono wrap-anywhere">{path}</span>
              <LinkButton
                aria-label={`Remove stage library directory ${path}`}
                onClick={() =>
                  update((draft) => {
                    draft.stages.directories = (
                      draft.stages.directories ?? []
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
          aria-label="Stage library directory path"
          placeholder="/absolute/path/to/stages"
          value={directory}
          onChange={(event) => setDirectory(event.target.value)}
        />
        <BrowseButton
          title="Choose a stage library directory"
          onPick={add}
          onError={onError}
        />
        <Button disabled={!directory.trim()} onClick={() => add(directory)}>
          Add directory
        </Button>
      </div>
      <FieldErrors field="stages.directories" />
      {problems.length > 0 && (
        <Notice title="Library files with problems" role="status">
          <p className="m-0">
            Plans that use what these files define are refused until they are
            fixed. Saving settings is not.
          </p>
          <ul className="m-0 mt-2 grid list-none gap-1.5 p-0">
            {problems.map((problem) => (
              <li
                key={`${problem.file}\n${problem.message}`}
                className="wrap-anywhere text-warn"
              >
                <span className="font-mono">{problem.file}</span>:{" "}
                {problem.message}
              </li>
            ))}
          </ul>
        </Notice>
      )}
    </SetupPanel>
  );
}
