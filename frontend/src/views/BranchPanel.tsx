import type { BranchPlan } from "../types";
import { branchPanelVisible, cleanupWords } from "./branching";

const text = "mt-1.5 text-[11px] leading-relaxed wrap-anywhere text-muted";
const code = "font-mono text-ink-soft";

/**
 * Where an approved run will work, as Go planned it from the repository's
 * branching configuration.
 */
export function BranchPanel({ plan }: { plan?: BranchPlan }) {
  if (!branchPanelVisible(plan)) return null;
  const proposed = Object.entries(plan.proposed ?? {});
  const checkout = plan.cleanup === "checkout";
  return (
    <div aria-label="Branch plan">
      <h3 className="text-xs font-semibold text-ink">Branch</h3>
      {checkout ? (
        <p className={text}>
          Commits in the checkout on its current branch, which must be clean and
          not <span className={code}>{plan.base}</span>.
        </p>
      ) : plan.branch ? (
        <>
          <p className={text}>
            <span className={code}>{plan.branch}</span> from{" "}
            <span className={code}>{plan.base}</span>
          </p>
          {plan.root && (
            <p className={text}>
              Worktree under <span className={code}>{plan.root}</span>,{" "}
              {cleanupWords(plan.cleanup)}; the branch is always kept.
            </p>
          )}
        </>
      ) : (
        <p className={text}>
          Starting this run removes the worktrees of finished runs; their
          branches are kept.
        </p>
      )}
      {proposed.length > 0 && (
        <div className="mt-3 grid gap-1.5 bg-field p-3">
          <p className="m-0 text-[11px] text-ink-soft">
            This repository had no complete branching configuration. Approving
            writes these values to{" "}
            <span className={code}>.inkuna/branching.yaml</span>:
          </p>
          <dl className="m-0 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]">
            {proposed.map(([key, value]) => (
              <div className="contents" key={key}>
                <dt className="font-mono text-muted">{key}</dt>
                <dd className="m-0 font-mono wrap-anywhere text-ink-strong">
                  {value}
                </dd>
              </div>
            ))}
          </dl>
          {plan.notes?.map((note) => (
            <small className="text-[11px] wrap-anywhere text-muted" key={note}>
              {note}
            </small>
          ))}
        </div>
      )}
    </div>
  );
}
