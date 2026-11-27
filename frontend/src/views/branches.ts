import type {
  BranchDeletion,
  BranchSync,
  BranchSyncState,
  BranchUpdate,
  SettingsChange,
  SettingsShare,
} from "../types";

/** The chip tone for each sync state Go reports. */
export const syncTones: Record<
  BranchSyncState,
  "green" | "warn" | "accent" | "error" | "muted"
> = {
  synced: "green",
  behind: "warn",
  ahead: "accent",
  diverged: "error",
  gone: "muted",
  local: "muted",
};

/** A branch's standing against its upstream, in words. */
export function syncWords(branch: BranchSync): string {
  switch (branch.state) {
    case "behind":
      return `${branch.behind} behind`;
    case "ahead":
      return `${branch.ahead} ahead`;
    case "diverged":
      return `${branch.ahead} ahead, ${branch.behind} behind`;
    case "gone":
      return "upstream gone";
    case "local":
      return "local only";
    default:
      return "in sync";
  }
}

/**
 * Whether a branch is out of step with its upstream, which is what the
 * "Out of sync only" filter keeps. Falling behind the base is shown on its
 * own, since nearly every run branch does.
 */
export function outOfSync(branch: BranchSync): boolean {
  return (
    branch.state === "behind" ||
    branch.state === "diverged" ||
    branch.state === "gone"
  );
}

/** What an update did, as Go reported it. */
export function updateWords(update: BranchUpdate): string {
  if (update.from === update.to)
    return `${update.branch} was already at ${update.upstream}.`;
  const checkout = update.checkedOut
    ? `, and its checkout at ${update.checkedOut}`
    : "";
  const settled = update.settled?.length
    ? ` ${update.upstream} already had ${update.settled.join(", ")}, so ${update.settled.length === 1 ? "it is" : "they are"} no longer uncommitted.`
    : "";
  const kept = update.kept
    ? ` ${update.kept} uncommitted setting(s) or untracked file(s) stayed as they were.`
    : "";
  return `Fast-forwarded ${update.branch} to ${update.upstream} (${update.from.slice(0, 7)} → ${update.to.slice(0, 7)})${checkout}.${settled}${kept}`;
}

/** A settings change's kind, with what Go knows about the base's copy. */
export function settingsChangeWords(change: SettingsChange): string {
  if (change.onBase) return `${change.kind}, already on the base`;
  if (change.conflict) return `${change.kind}, also changed on the base`;
  return change.kind;
}

/** What a share did, as Go reported it. */
export function shareWords(share: SettingsShare): string {
  const files =
    share.files.length === 1 ? share.files[0] : `${share.files.length} files`;
  const short = share.commit.slice(0, 7);
  if (share.mode === "push") {
    const updated = share.updated ? ` ${updateWords(share.updated)}` : "";
    return `Pushed ${files} to ${share.base} on origin (${short}).${updated}${share.note ? ` ${share.note}` : ""}`;
  }
  const opened = share.pullRequest
    ? ` Opened a pull request into ${share.base}.`
    : "";
  return `Pushed ${files} on ${share.branch} (${short}).${opened}${share.note ? ` ${share.note}` : ""} Once it merges, update ${share.base} to take the edits as merged.`;
}

/** What a deletion removed, and how to bring the branch back. */
export function deletionWords(deleted: BranchDeletion): string {
  const short = deleted.commit.slice(0, 7);
  const worktree = deleted.worktree
    ? ` and its worktree at ${deleted.worktree}`
    : "";
  return `Deleted ${deleted.branch} (was ${short})${worktree}. Restore it with git branch ${deleted.branch} ${short}.`;
}
