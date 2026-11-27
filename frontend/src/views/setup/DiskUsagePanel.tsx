import { useCallback, useEffect, useRef, useState } from "react";
import { getStorageUsage, removeKeptWorktree } from "../../bridge";
import { formatBytes, formatTime } from "../../format";
import type { KeptWorktree, StorageUsage } from "../../types";
import { Alert, Button, Metric, Metrics, Modal } from "../common";
import { LinkButton, SetupPanel } from "./shared";

const plural = (count: number, one: string) =>
  `${count} ${count === 1 ? one : `${one}s`}`;

/**
 * The space worktrees and artifacts take, and the worktrees a delete kept,
 * each removable after the same confirmation a run's worktree gets.
 */
export function DiskUsagePanel() {
  const [usage, setUsage] = useState<StorageUsage | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [confirming, setConfirming] = useState<KeptWorktree | null>(null);
  const [pending, setPending] = useState(false);
  const [removeError, setRemoveError] = useState("");
  // The ref refuses a second confirmation before the pending state renders.
  const removing = useRef(false);
  // Each measurement is numbered, so one that finishes after a later one
  // started neither replaces its result nor ends its loading state.
  const latest = useRef(0);

  // Measuring walks every worktree, so it runs when the panel opens and on
  // request rather than on every run event.
  const load = useCallback(async () => {
    const request = ++latest.current;
    setLoading(true);
    try {
      const measured = await getStorageUsage();
      if (request !== latest.current) return;
      setUsage(measured);
      setError("");
    } catch (reason) {
      if (request === latest.current) setError(String(reason));
    } finally {
      if (request === latest.current) setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
  }, [load]);

  async function remove(kept: KeptWorktree) {
    if (removing.current) return;
    removing.current = true;
    setPending(true);
    setRemoveError("");
    try {
      await removeKeptWorktree(kept.path);
      setConfirming(null);
      await load();
    } catch (reason) {
      setRemoveError(String(reason));
      setConfirming(null);
    } finally {
      removing.current = false;
      setPending(false);
    }
  }

  return (
    <SetupPanel
      title="Disk usage"
      wide
      description="What Inkuna's history takes on disk. Deleting finished tasks in the Tasks view frees it, and a worktree a delete kept is listed here until you remove it."
    >
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <Button disabled={loading} onClick={() => void load()}>
          {loading ? "Measuring…" : "Measure again"}
        </Button>
      </div>
      {error && (
        <div className="mt-4">
          <Alert flush>{error}</Alert>
        </div>
      )}
      {usage && (
        <>
          <div className="mt-4">
            <Metrics flush>
              <Metric
                value={formatBytes(usage.worktreeBytes)}
                label={`Worktrees · ${plural(usage.worktrees, "worktree")}`}
              />
              <Metric
                value={formatBytes(usage.artifactBytes)}
                label={`Artifacts · ${plural(usage.artifacts, "blob")}`}
              />
            </Metrics>
          </div>
          {(usage.problems ?? []).map((problem) => (
            <p key={problem} className="mt-2 mb-0 text-[11px] text-danger">
              {problem}
            </p>
          ))}
          <h3 className="mt-5 mb-2 text-xs font-semibold">
            Kept after their task was deleted
          </h3>
          {removeError && (
            <div className="mb-2">
              <Alert flush>{removeError}</Alert>
            </div>
          )}
          {usage.kept.length === 0 ? (
            <p className="m-0 text-xs text-muted">None.</p>
          ) : (
            <ul
              aria-label="Kept worktrees"
              className="m-0 grid list-none gap-0 p-0"
            >
              {usage.kept.map((kept) => (
                <li
                  key={kept.path}
                  className="flex min-w-0 items-start justify-between gap-3 border-b border-line py-2 text-xs"
                >
                  <div className="grid min-w-0 gap-0.5">
                    <span className="min-w-0 font-mono wrap-anywhere">
                      {kept.path}
                    </span>
                    <small className="text-[11px] wrap-anywhere text-muted">
                      {[
                        formatBytes(kept.bytes),
                        kept.projectName,
                        kept.branch,
                        `kept ${formatTime(kept.keptAt)}`,
                      ]
                        .filter(Boolean)
                        .join(" · ")}
                    </small>
                    <small className="text-[11px] wrap-anywhere text-muted">
                      {kept.title}: {kept.reason}
                    </small>
                  </div>
                  <LinkButton
                    aria-label={`Remove worktree ${kept.path}`}
                    disabled={pending || loading}
                    onClick={() => {
                      setRemoveError("");
                      setConfirming(kept);
                    }}
                  >
                    Remove…
                  </LinkButton>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {confirming && (
        <Modal
          title="Remove this worktree?"
          onClose={() => removing.current || setConfirming(null)}
        >
          <p className="m-0 text-sm wrap-anywhere text-muted">
            This deletes {confirming.path} ({formatBytes(confirming.bytes)}).
            {confirming.branch
              ? ` The branch ${confirming.branch} and its commits stay.`
              : ""}{" "}
            A worktree with uncommitted changes is kept.
          </p>
          <div className="mt-6 flex justify-end gap-2">
            <Button disabled={pending} onClick={() => setConfirming(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={pending || loading}
              onClick={() => void remove(confirming)}
            >
              Remove worktree
            </Button>
          </div>
        </Modal>
      )}
    </SetupPanel>
  );
}
