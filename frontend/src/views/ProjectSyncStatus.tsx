import { useEffect, useState } from "react";
import { getProjectSync } from "../bridge";
import { formatTime } from "../format";
import type { ProjectSyncInfo } from "../types";
import { syncTones, syncWords } from "./branches";
import { Button, Chip } from "./common";

/**
 * Whether a project's repository is out of sync with origin, for its card:
 * the checked-out branch against its upstream and how many branches need
 * attention. Go fetches first only when its last fetch is stale.
 */
export function ProjectSyncStatus({
  projectID,
  onOpen,
}: {
  projectID: string;
  /** Opens the Branches view for this project. */
  onOpen(): void;
}) {
  const [info, setInfo] = useState<ProjectSyncInfo | null>(null);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState("");

  // Local state first, at once; then whatever a stale fetch changes.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const local = await getProjectSync(projectID, false);
        if (cancelled || !local) return;
        setInfo(local);
        if (!local.summary.hasOrigin) return;
        setChecking(true);
        const fresh = await getProjectSync(projectID, true);
        if (!cancelled && fresh) setInfo(fresh);
      } catch (reason) {
        if (!cancelled) setError(String(reason));
      } finally {
        if (!cancelled) setChecking(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [projectID]);

  if (error) {
    return (
      <small className="mb-4 block text-xs wrap-anywhere text-muted">
        Could not read branches: {error}
      </small>
    );
  }
  if (!info) return null;
  const { summary } = info;
  const checkout = summary.checkout;
  const details = [
    !summary.hasOrigin
      ? "No origin remote"
      : checking
        ? "Checking origin…"
        : summary.fetchedAt
          ? `Fetched ${formatTime(summary.fetchedAt)}`
          : "Not fetched yet",
    summary.outOfSync > 0 && `${summary.outOfSync} out of sync`,
    summary.noUpstream > 0 && `${summary.noUpstream} without an upstream`,
  ].filter(Boolean);
  return (
    <div aria-label="Branch sync" className="mb-4 grid min-w-0 gap-1.5">
      <div className="flex min-w-0 flex-wrap items-center gap-2 text-xs">
        {checkout ? (
          <>
            <span className="min-w-0 font-mono break-words text-ink-soft">
              {checkout.name}
            </span>
            <Chip tone={syncTones[checkout.state] ?? "muted"}>
              {syncWords(checkout)}
            </Chip>
            {checkout.upstream && (
              <span className="font-mono text-[11px] text-muted">
                {checkout.upstream}
              </span>
            )}
          </>
        ) : (
          <span className="text-muted">Detached HEAD</span>
        )}
      </div>
      <small className="text-[11px] wrap-anywhere text-muted">
        {details.join(" · ")}
      </small>
      {info.fetchError && (
        <small
          className="text-[11px] wrap-anywhere text-warn"
          title={info.fetchError}
        >
          Could not fetch from origin
        </small>
      )}
      <div>
        <Button
          variant="ghost"
          className="px-0 py-0 font-mono text-[11px]"
          onClick={onOpen}
        >
          Branches →
        </Button>
      </div>
    </div>
  );
}
