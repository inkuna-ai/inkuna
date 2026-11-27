import { useState } from "react";
import type { RepositoryConfig, RepositoryTracker, Runtime } from "../../types";
import {
  Button,
  Chip,
  TableScroll,
  inputClass,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import { RuntimeOptions } from "../runtimes/RuntimeOptions";
import { useRuntimes } from "../runtimes/useRuntimes";
import {
  FieldErrors,
  LinkButton,
  SetupPanel,
  formGridClass,
  type UpdateConfig,
  BrowseButton,
} from "./shared";
import { TrackerOverrideFields, TrackerSummary } from "./TrackerOverrideFields";

const emptyRepository: RepositoryConfig = {
  name: "",
  path: "",
  defaultRuntime: "",
};

export function RepositoriesPanel({
  repositories,
  trackers = [],
  update,
  onError,
}: {
  repositories: RepositoryConfig[];
  /** Go's reading of each saved repository's tracker settings. */
  trackers?: RepositoryTracker[];
  update: UpdateConfig;
  onError(message: string): void;
}) {
  const runtimes = useRuntimes();
  const [repo, setRepo] = useState<RepositoryConfig>(emptyRepository);
  // The path of the declared repository loaded into the form, if any.
  const [editing, setEditing] = useState<string | null>(null);

  function add() {
    const name = repo.name.trim();
    const path = repo.path.trim();
    update((draft) => {
      // The row being edited is the source even when its path was retyped;
      // otherwise re-adding a declared path edits that row.
      const source = editing ?? path;
      const existing = draft.repositories.find((item) => item.path === source);
      const next = {
        ...existing,
        name,
        path,
        defaultRuntime: repo.defaultRuntime,
      };
      // A rules or docs path typed here replaces the override; clearing it
      // while editing a loaded row removes the override.
      if (repo.rules?.trim()) next.rules = repo.rules.trim();
      else if (editing !== null) delete next.rules;
      if (repo.docs?.trim()) next.docs = repo.docs.trim();
      else if (editing !== null) delete next.docs;
      // The form holds the whole override, so it replaces the row's;
      // re-adding a declared path keeps that row's, as rules and docs are.
      if (repo.tracker) next.tracker = repo.tracker;
      else if (editing !== null) delete next.tracker;
      // Drop the source row and any row already at the new path, so a retyped
      // path neither leaves the old entry behind nor declares a path twice.
      draft.repositories = [
        ...draft.repositories.filter(
          (item) => item.path !== source && item.path !== path,
        ),
        next,
      ];
    });
    setRepo(emptyRepository);
    setEditing(null);
  }

  return (
    <SetupPanel
      title="Repositories"
      wide
      description="Declared repositories are registered as projects on save."
    >
      <FieldErrors field="repositories" />
      {repositories.length === 0 ? (
        <p className="mt-4 mb-0 text-xs text-muted">
          No repositories declared.
        </p>
      ) : (
        <TableScroll>
          <table className={tableClass} aria-label="Declared repositories">
            <thead>
              <tr>
                <th className={thClass}>Name</th>
                <th className={thClass}>Path</th>
                <th className={thClass}>Runtime</th>
                <th className={thClass}>Rules file</th>
                <th className={thClass}>Docs directory</th>
                <th className={thClass}>Tracker writes</th>
                <th className={thClass}></th>
              </tr>
            </thead>
            <tbody>
              {repositories.map((repository) => (
                <tr key={repository.path}>
                  <td className={tdClass}>
                    <b className="font-semibold wrap-anywhere text-ink-strong">
                      {repository.name}
                    </b>
                  </td>
                  <td className={tdClass}>
                    <span className="font-mono wrap-anywhere text-muted">
                      {repository.path}
                    </span>
                  </td>
                  <td className={tdClass}>
                    <Chip>
                      {repository.defaultRuntime || "orchestrator runtime"}
                    </Chip>
                  </td>
                  <td className={tdClass}>
                    <span className="font-mono wrap-anywhere text-muted">
                      {repository.rules || ".inkuna/rules.yaml"}
                    </span>
                  </td>
                  <td className={tdClass}>
                    <span className="font-mono wrap-anywhere text-muted">
                      {repository.docs || "docs"}
                    </span>
                  </td>
                  <td className={`${tdClass} text-[11px]`}>
                    <TrackerSummary
                      view={trackers.find(
                        (view) => view.path === repository.path,
                      )}
                    />
                  </td>
                  <td className={`${tdClass} text-right whitespace-nowrap`}>
                    <LinkButton
                      aria-label={`Edit repository ${repository.name}`}
                      onClick={() => {
                        setRepo({ rules: "", docs: "", ...repository });
                        setEditing(repository.path);
                      }}
                    >
                      Edit
                    </LinkButton>{" "}
                    <LinkButton
                      aria-label={`Remove repository ${repository.name}`}
                      onClick={() =>
                        update((draft) => {
                          draft.repositories = draft.repositories.filter(
                            (item) => item.path !== repository.path,
                          );
                        })
                      }
                    >
                      Remove
                    </LinkButton>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      <div className={formGridClass}>
        <input
          className={inputClass}
          aria-label="Repository name"
          placeholder="name"
          value={repo.name}
          onChange={(event) => setRepo({ ...repo, name: event.target.value })}
        />
        <input
          className={inputClass}
          aria-label="Repository path"
          placeholder="/absolute/path"
          value={repo.path}
          onChange={(event) => setRepo({ ...repo, path: event.target.value })}
        />
        <select
          className={inputClass}
          aria-label="Repository runtime"
          value={repo.defaultRuntime}
          onChange={(event) =>
            setRepo({
              ...repo,
              defaultRuntime: event.target.value as Runtime | "",
            })
          }
        >
          <option value="">inherit</option>
          <RuntimeOptions runtimes={runtimes} value={repo.defaultRuntime} />
        </select>
        <input
          className={inputClass}
          aria-label="Repository rules file"
          placeholder="rules override (optional)"
          value={repo.rules ?? ""}
          onChange={(event) => setRepo({ ...repo, rules: event.target.value })}
        />
        <input
          className={inputClass}
          aria-label="Repository docs directory"
          placeholder="docs directory (optional, default docs)"
          value={repo.docs ?? ""}
          onChange={(event) => setRepo({ ...repo, docs: event.target.value })}
        />
        <BrowseButton
          title="Choose a repository"
          onPick={(path) =>
            setRepo((current) => ({
              ...current,
              path,
              name: current.name || (path.split(/[\\/]/).pop() ?? ""),
            }))
          }
          onError={onError}
        />
        <TrackerOverrideFields
          value={repo.tracker}
          ceiling={
            trackers.find((view) => view.path === repo.path.trim())?.ceiling
          }
          onChange={(tracker) => setRepo({ ...repo, tracker })}
        />
        <Button disabled={!repo.name.trim() || !repo.path.trim()} onClick={add}>
          {editing ? "Update repository" : "Add repository"}
        </Button>
      </div>
    </SetupPanel>
  );
}
