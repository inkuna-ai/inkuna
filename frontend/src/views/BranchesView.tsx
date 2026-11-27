import { useEffect, useRef, useState } from "react";
import {
  deleteBranch,
  fetchProject,
  getProjectBranches,
  openExternal,
  shareSettings,
  updateBranch,
} from "../bridge";
import { formatTime } from "../format";
import type {
  BranchSync,
  BranchSyncInfo,
  Project,
  SettingsShare,
  SettingsStatus,
  ShareMode,
} from "../types";
import {
  Alert,
  Button,
  Chip,
  Empty,
  Field,
  Header,
  Metric,
  Metrics,
  Modal,
  Notice,
  TableScroll,
  inputClass,
  tableClass,
  tdClass,
  thClass,
} from "./common";
import {
  deletionWords,
  outOfSync,
  settingsChangeWords,
  shareWords,
  syncTones,
  syncWords,
  updateWords,
} from "./branches";

/**
 * A project's local branches against origin: which are behind, ahead, or
 * diverged, a fast-forward for those Go says can take one, deletion of
 * those without an upstream, and sharing the settings edits sitting in the
 * project checkout.
 */
export function BranchesView({
  projects,
  selectedProject,
  onProjectChange,
}: {
  projects: Project[];
  selectedProject: Project | null;
  onProjectChange(project: Project | null): void;
}) {
  const projectID = selectedProject?.id ?? "";
  return (
    <section>
      <Header eyebrow="Repository" title="Branches" />
      <div className="my-4 max-w-[420px]">
        <Field label="Project" flush>
          <select
            aria-label="Branches project"
            className={inputClass}
            value={projectID}
            onChange={(event) =>
              onProjectChange(
                projects.find((project) => project.id === event.target.value) ??
                  null,
              )
            }
          >
            {!projectID && <option value="">Choose a project</option>}
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {projectID ? (
        // Keyed so a fetch for one project never lands in another's view.
        <BranchSyncPanel key={projectID} projectID={projectID} />
      ) : (
        <Empty icon="⎇" title="No project selected">
          <p>Add a project to see whether its branches are in sync.</p>
        </Empty>
      )}
    </section>
  );
}

function BranchSyncPanel({ projectID }: { projectID: string }) {
  const [view, setView] = useState<BranchSyncInfo | null>(null);
  const [error, setError] = useState("");
  const [fetching, setFetching] = useState(false);
  const [updating, setUpdating] = useState("");
  const [message, setMessage] = useState("");
  const [onlyOutOfSync, setOnlyOutOfSync] = useState(false);
  const [confirming, setConfirming] = useState<BranchSync | null>(null);
  const [deleting, setDeleting] = useState(false);
  // The ref refuses a second confirmation before the pending state renders.
  const deletingRef = useRef(false);
  const [sharingMode, setSharingMode] = useState<ShareMode | null>(null);
  const [sharing, setSharing] = useState(false);
  const sharingRef = useRef(false);
  const [lastShare, setLastShare] = useState<SettingsShare | null>(null);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  async function fetchOrigin() {
    setFetching(true);
    setError("");
    try {
      const fetched = await fetchProject(projectID);
      if (alive.current) setView(fetched);
    } catch (reason) {
      if (alive.current) setError(String(reason));
    } finally {
      if (alive.current) setFetching(false);
    }
  }

  // Local state first, at once; then a fetch, since without one the view
  // cannot know what origin has.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const local = await getProjectBranches(projectID);
        if (cancelled) return;
        setView(local);
        if (!local.sync.hasOrigin) return;
        setFetching(true);
        const fetched = await fetchProject(projectID);
        if (!cancelled) setView(fetched);
      } catch (reason) {
        if (!cancelled) setError(String(reason));
      } finally {
        if (!cancelled) setFetching(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [projectID]);

  async function share(mode: ShareMode, digest: string) {
    if (sharingRef.current) return;
    sharingRef.current = true;
    setSharing(true);
    setError("");
    setMessage("");
    setLastShare(null);
    try {
      // Go commits only what this digest names: what the user confirmed.
      const shared = await shareSettings(projectID, mode, digest);
      if (!alive.current) return;
      setView(shared);
      if (shared.shared) {
        setMessage(shareWords(shared.shared));
        setLastShare(shared.shared);
      }
    } catch (reason) {
      if (alive.current) setError(String(reason));
    } finally {
      sharingRef.current = false;
      if (alive.current) {
        setSharing(false);
        setSharingMode(null);
      }
    }
  }

  async function update(branch: string) {
    setUpdating(branch);
    setError("");
    setMessage("");
    setLastShare(null);
    try {
      const updated = await updateBranch(projectID, branch);
      if (!alive.current) return;
      setView(updated);
      if (updated.update) setMessage(updateWords(updated.update));
    } catch (reason) {
      if (alive.current) setError(String(reason));
    } finally {
      if (alive.current) setUpdating("");
    }
  }

  async function remove(branch: BranchSync) {
    if (deletingRef.current) return;
    deletingRef.current = true;
    setDeleting(true);
    setError("");
    setMessage("");
    setLastShare(null);
    try {
      // Go deletes it only if it still points where the user saw it.
      const remaining = await deleteBranch(
        projectID,
        branch.name,
        branch.commit,
      );
      if (!alive.current) return;
      setView(remaining);
      if (remaining.deleted) setMessage(deletionWords(remaining.deleted));
    } catch (reason) {
      if (alive.current) setError(String(reason));
    } finally {
      deletingRef.current = false;
      if (alive.current) {
        setDeleting(false);
        setConfirming(null);
      }
    }
  }

  if (!view) {
    return error ? (
      <Alert>{error}</Alert>
    ) : (
      <p className="text-sm text-muted">Reading branches…</p>
    );
  }
  const info = view.sync;
  const shown = onlyOutOfSync ? info.branches.filter(outOfSync) : info.branches;
  const behind = info.branches.filter(
    (branch) => branch.state === "behind" || branch.state === "diverged",
  ).length;
  const behindBase = info.branches.filter(
    (branch) => !branch.isBase && branch.behindBase > 0,
  ).length;
  const busy = fetching || updating !== "" || deleting || sharing;
  const base = info.base || "the base";
  const settings = view.settings;
  return (
    <div aria-label="Branch sync">
      <div className="mb-5 flex flex-wrap items-center justify-between gap-3">
        <p className="m-0 min-w-0 text-xs wrap-anywhere text-muted">
          {info.hasOrigin ? (
            <>
              Origin{" "}
              <span className="font-mono text-ink-soft">
                {info.origin || "origin"}
              </span>
              {" · "}
              {fetching
                ? "Fetching…"
                : info.fetchedAt
                  ? `Fetched ${formatTime(info.fetchedAt)}`
                  : "Not fetched yet"}
            </>
          ) : (
            "No origin remote"
          )}
          {info.baseRef && (
            <>
              {" · "}Base{" "}
              <span className="font-mono text-ink-soft">{info.baseRef}</span>
            </>
          )}
        </p>
        <div className="flex flex-wrap items-center gap-4">
          <label className="flex items-center gap-2 text-xs text-ink-soft">
            <input
              type="checkbox"
              checked={onlyOutOfSync}
              onChange={(event) => setOnlyOutOfSync(event.target.checked)}
            />
            Out of sync only
          </label>
          <Button
            disabled={!info.hasOrigin || busy}
            onClick={() => void fetchOrigin()}
          >
            {fetching ? "Fetching…" : "Fetch origin"}
          </Button>
        </div>
      </div>
      {error && <Alert>{error}</Alert>}
      {view.fetchError && <Alert>{view.fetchError}</Alert>}
      {message && (
        <Notice role="status" className="mt-0 mb-4">
          {message}
          {lastShare?.url && (
            <Button
              variant="ghost"
              className="ml-1 p-0 text-xs"
              onClick={() => openExternal(lastShare.url!)}
            >
              {lastShare.pullRequest
                ? "View the pull request"
                : "Open a pull request on GitHub"}
            </Button>
          )}
        </Notice>
      )}
      {!info.hasOrigin && (
        <Notice className="mt-0 mb-4">
          This repository has no origin remote, so there is nothing to fetch
          from. Its branches are compared with the base locally.
        </Notice>
      )}
      {settings && (
        <SettingsPanel
          settings={settings}
          base={base}
          busy={busy}
          onShare={setSharingMode}
        />
      )}
      <Metrics>
        <Metric value={String(info.branches.length)} label="Local branches" />
        <Metric value={String(behind)} label="Behind their upstream" />
        <Metric
          value={String(
            info.branches.filter((branch) => branch.updatable).length,
          )}
          label="Can fast-forward"
        />
        <Metric
          value={String(behindBase)}
          label={`Behind ${info.baseRef || "the base"}`}
        />
      </Metrics>
      {shown.length === 0 ? (
        <Empty icon="⎇" title="Nothing out of sync">
          <p>Every branch is at its upstream or tracks none.</p>
        </Empty>
      ) : (
        <TableScroll flush>
          <table className={tableClass}>
            <thead>
              <tr>
                <th className={thClass}>Branch</th>
                <th className={thClass}>Upstream</th>
                <th className={thClass}>Base</th>
                <th className={thClass}>Checked out</th>
                <th className={thClass}>
                  <span className="sr-only">Actions</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {shown.map((branch) => (
                <BranchRow
                  key={branch.name}
                  branch={branch}
                  baseRef={info.baseRef}
                  busy={busy}
                  updating={updating === branch.name}
                  onUpdate={() => void update(branch.name)}
                  onDelete={() => setConfirming(branch)}
                />
              ))}
            </tbody>
          </table>
        </TableScroll>
      )}
      {info.truncated && (
        <small className="mt-3 block text-[11px] text-muted">
          Showing the {info.branches.length} most recently committed branches.
        </small>
      )}
      {confirming && (
        <Modal
          title={`Delete ${confirming.name}?`}
          onClose={() => deletingRef.current || setConfirming(null)}
        >
          <div className="grid gap-3 text-sm wrap-anywhere text-muted">
            <p className="m-0">
              It points at{" "}
              <span className="font-mono text-ink-soft">
                {confirming.commit}
              </span>
              {confirming.state === "gone"
                ? ", and its upstream was deleted on the remote."
                : " and tracks no remote branch."}
            </p>
            <p className="m-0">
              {confirming.unique > 0
                ? `${confirming.unique} commit(s) exist only on this branch. After deleting it, git can still restore them by hash until it collects unreachable commits.`
                : "Every commit on it is also on another branch, a tag, or origin."}
            </p>
            {confirming.checkedOut && (
              <p className="m-0">
                Its worktree at {confirming.checkedOut} is removed first; it has
                no uncommitted changes.
              </p>
            )}
          </div>
          <div className="mt-6 flex justify-end gap-2">
            <Button disabled={deleting} onClick={() => setConfirming(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deleting}
              onClick={() => void remove(confirming)}
            >
              {deleting ? "Deleting…" : "Delete branch"}
            </Button>
          </div>
        </Modal>
      )}
      {sharingMode && settings?.digest && (
        <ShareModal
          mode={sharingMode}
          settings={settings}
          base={base}
          sharing={sharing}
          onCancel={() => sharingRef.current || setSharingMode(null)}
          onConfirm={() => void share(sharingMode, settings.digest!)}
        />
      )}
    </div>
  );
}

/**
 * The settings edits sitting uncommitted in the project checkout, which
 * Inkuna plans from as they are. They no longer hold the base back; sharing
 * puts them on origin so the base gets them too.
 */
function SettingsPanel({
  settings,
  base,
  busy,
  onShare,
}: {
  settings: SettingsStatus;
  base: string;
  busy: boolean;
  onShare(mode: ShareMode): void;
}) {
  const blocked = !settings.shareable || busy;
  return (
    <section
      aria-label="Settings edits"
      className="mb-5 rounded-md border border-line bg-raised p-4"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h3 className="m-0 text-sm font-bold text-ink-strong">
            Settings edits in the project checkout
          </h3>
          <p className="m-0 mt-1 text-xs text-muted">
            Inkuna plans from these as they are, and they do not stop {base}{" "}
            from updating. Share them so origin has them too.
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="primary"
            className="px-2.5 py-1 text-xs"
            disabled={blocked || Boolean(settings.sharedOn)}
            onClick={() => onShare("pull-request")}
          >
            Open pull request
          </Button>
          <Button
            className="px-2.5 py-1 text-xs"
            disabled={blocked}
            onClick={() => onShare("push")}
          >
            Push to {base}
          </Button>
        </div>
      </div>
      <ul className="m-0 mt-3 grid list-none gap-1.5 p-0">
        {settings.changes.map((change) => (
          <li
            key={change.path}
            className="flex flex-wrap items-center gap-2 text-xs"
          >
            <span className="font-mono wrap-anywhere text-ink-soft">
              {change.path}
            </span>
            <Chip
              tone={
                change.conflict ? "error" : change.onBase ? "green" : "muted"
              }
            >
              {settingsChangeWords(change)}
            </Chip>
          </li>
        ))}
      </ul>
      {settings.sharedOn && (
        <small className="mt-3 block text-muted">
          Shared on <span className="font-mono">{settings.sharedOn}</span>; once
          it merges, update {base} to take the edits as merged.
        </small>
      )}
      {!settings.shareable && settings.reason && (
        <small className="mt-3 block text-muted">{settings.reason}</small>
      )}
    </section>
  );
}

function ShareModal({
  mode,
  settings,
  base,
  sharing,
  onCancel,
  onConfirm,
}: {
  mode: ShareMode;
  settings: SettingsStatus;
  base: string;
  sharing: boolean;
  onCancel(): void;
  onConfirm(): void;
}) {
  const files = settings.changes.filter((change) => !change.onBase);
  const onto = settings.baseRef || `origin/${base}`;
  const pull = mode === "pull-request";
  return (
    <Modal
      title={
        pull
          ? "Open a pull request for these settings?"
          : `Push these settings to ${base}?`
      }
      onClose={onCancel}
    >
      <div className="grid gap-3 text-sm wrap-anywhere text-muted">
        <ul className="m-0 grid list-none gap-1 p-0">
          {files.map((change) => (
            <li key={change.path} className="font-mono text-xs text-ink-soft">
              {change.path} <span className="text-muted">({change.kind})</span>
            </li>
          ))}
        </ul>
        <p className="m-0">
          Inkuna fetches origin and commits these files, as the checkout has
          them, on top of {onto}.{" "}
          {pull
            ? `It pushes that commit on a new branch and opens a pull request into ${base}.`
            : `It pushes that commit straight to ${base} on origin; a protected ${base}, or one that moved, refuses it and nothing changes. Then ${base} is fast-forwarded here.`}
        </p>
        <p className="m-0">
          {pull
            ? "The checkout keeps the edits until the pull request merges and you update."
            : "The checkout is not switched; its edits become committed ones."}
        </p>
      </div>
      <div className="mt-6 flex justify-end gap-2">
        <Button disabled={sharing} onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="primary" disabled={sharing} onClick={onConfirm}>
          {sharing
            ? "Sharing…"
            : pull
              ? "Open pull request"
              : `Push to ${base}`}
        </Button>
      </div>
    </Modal>
  );
}

function BranchRow({
  branch,
  baseRef,
  busy,
  updating,
  onUpdate,
  onDelete,
}: {
  branch: BranchSync;
  baseRef?: string;
  busy: boolean;
  updating: boolean;
  onUpdate(): void;
  onDelete(): void;
}) {
  return (
    <tr aria-label={branch.name}>
      <td className={tdClass}>
        <span className="font-mono text-xs text-ink-strong">{branch.name}</span>
        {branch.isBase && (
          <span className="ml-2">
            <Chip tone="accent">base</Chip>
          </span>
        )}
        <small className="mt-1 block text-muted">
          <span className="font-mono">{branch.commit}</span> ·{" "}
          {formatTime(branch.committedAt)}
        </small>
      </td>
      <td className={tdClass}>
        <Chip tone={syncTones[branch.state] ?? "muted"}>
          {syncWords(branch)}
        </Chip>
        {branch.upstream && (
          <small className="mt-1 block font-mono text-muted">
            {branch.upstream}
          </small>
        )}
      </td>
      <td className={tdClass}>
        {branch.isBase || !baseRef
          ? "—"
          : branch.behindBase > 0
            ? `${branch.behindBase} behind ${baseRef}`
            : "up to date"}
      </td>
      <td className={`${tdClass} wrap-anywhere`}>
        {branch.main
          ? "Project checkout"
          : (branch.checkedOut ?? <span className="text-muted">—</span>)}
        {branch.dirty && (
          <small className="mt-1 block text-warn">uncommitted changes</small>
        )}
      </td>
      <td className={tdClass}>
        {branch.updatable ? (
          <Button
            aria-label={`Update ${branch.name}`}
            className="px-2.5 py-1 text-xs"
            disabled={busy}
            onClick={onUpdate}
          >
            {updating ? "Updating…" : "Update"}
          </Button>
        ) : branch.deletable ? (
          <Button
            variant="danger"
            aria-label={`Delete ${branch.name}`}
            className="px-2.5 py-1 text-xs"
            disabled={busy}
            onClick={onDelete}
          >
            Delete
          </Button>
        ) : (
          (branch.reason || branch.deleteReason) && (
            <small className="block max-w-[260px] text-muted">
              {branch.reason || branch.deleteReason}
            </small>
          )
        )}
      </td>
    </tr>
  );
}
