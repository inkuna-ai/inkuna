import { useEffect, useState } from "react";
import { previewRemoveProject, removeProject } from "../../bridge";
import type {
  Project,
  ProjectRemoval,
  ProjectRemovalPreview,
} from "../../types";
import { Alert, Button, Modal } from "../common";

const plural = (count: number, word: string) =>
  `${count} ${word}${count === 1 ? "" : "s"}`;

/**
 * Removes a project after saying what that does. Go decides whether it can
 * be removed now and does the removal; the dialog shows Go's preview and
 * reason, and asks whether to keep the project's history.
 */
export function RemoveProjectDialog({
  project,
  onRemoved,
  onClose,
}: {
  project: Project;
  onRemoved(removal: ProjectRemoval): void;
  onClose(): void;
}) {
  const [preview, setPreview] = useState<ProjectRemovalPreview | null>(null);
  const [loadError, setLoadError] = useState("");
  const [deleteHistory, setDeleteHistory] = useState(false);
  const [error, setError] = useState("");
  const [removing, setRemoving] = useState(false);

  useEffect(() => {
    let active = true;
    previewRemoveProject(project.id)
      .then((found) => active && setPreview(found))
      .catch((reason: unknown) => active && setLoadError(String(reason)));
    return () => {
      active = false;
    };
  }, [project.id]);

  async function confirm() {
    setError("");
    setRemoving(true);
    try {
      onRemoved(await removeProject(project.id, deleteHistory));
    } catch (reason) {
      setError(String(reason));
      setRemoving(false);
    }
  }

  const hasHistory = (preview?.tasks ?? 0) > 0;
  return (
    <Modal
      title={`Remove ${project.name}`}
      onClose={() => removing || onClose()}
    >
      {loadError ? (
        <Alert flush>{loadError}</Alert>
      ) : !preview ? (
        <p className="m-0 text-sm text-muted">Reading what removal would do…</p>
      ) : (
        <div className="grid min-w-0 gap-4 text-sm">
          {preview.refused && <Alert flush>{preview.refused}</Alert>}
          <p className="m-0 text-ink">
            {project.name} leaves Projects and New request, and its entry leaves
            inkuna.yaml. The repository at{" "}
            <span className="wrap-anywhere">{preview.repository}</span>, its
            branches, and its runs&apos; worktrees stay on disk, and nothing
            changes on GitHub or Linear.
          </p>
          {preview.worktrees.length > 0 && (
            <div className="text-xs text-muted">
              {plural(preview.worktrees.length, "worktree")} of its runs stay on
              disk:
              <ul className="m-0 mt-1.5 grid gap-1 pl-4">
                {preview.worktrees.map((path) => (
                  <li key={path} className="wrap-anywhere">
                    {path}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {hasHistory && (
            <fieldset className="m-0 grid gap-1.5 border-0 p-0 text-xs text-ink-soft">
              <legend className="mb-1.5 text-muted">
                Its history: {plural(preview.tasks, "task")},{" "}
                {plural(preview.runs, "run")}
              </legend>
              <label className="flex items-start gap-2">
                <input
                  type="radio"
                  name="project-history"
                  className="mt-0.5 accent-accent"
                  checked={!deleteHistory}
                  onChange={() => setDeleteHistory(false)}
                />
                <span>
                  Keep it. Its tasks and runs stay readable in Tasks under its
                  name, and connecting the folder again brings the project back
                  with them.
                </span>
              </label>
              <label className="flex items-start gap-2">
                <input
                  type="radio"
                  name="project-history"
                  className="mt-0.5 accent-accent"
                  checked={deleteHistory}
                  onChange={() => setDeleteHistory(true)}
                />
                <span>
                  Delete it, as deleting its tasks in Tasks does. Worktrees and
                  branches stay.
                </span>
              </label>
            </fieldset>
          )}
          {error && <Alert flush>{error}</Alert>}
        </div>
      )}
      <div className="mt-6 flex justify-end gap-2">
        <Button disabled={removing} onClick={onClose}>
          Cancel
        </Button>
        <Button
          variant="danger"
          disabled={removing || !preview || Boolean(preview.refused)}
          onClick={() => void confirm()}
        >
          {removing ? "Removing…" : "Remove project"}
        </Button>
      </div>
    </Modal>
  );
}
