import { isTerminal, type BranchPlan, type WorkflowRun } from "../types";

/**
 * What a cleanup policy does to a run's worktree, in plain words. Go decides
 * and enforces the policy; this only describes it.
 */
export function cleanupWords(policy?: string): string {
  switch (policy) {
    case "listener":
      return "removed when the run finishes";
    case "prune":
      return "removed when the next run starts";
    case "merged":
      return "removed once its pull request merges";
    case "manual":
      return "kept until you remove it";
    case "checkout":
      return "in the checkout";
    default:
      return "kept until you remove it";
  }
}

/**
 * Whether a plan has anything to say about branches: a branch it will
 * create, a checkout it will commit in, configuration it will write, or
 * other runs' worktrees it will prune when it starts.
 */
export function branchPanelVisible(plan?: BranchPlan): plan is BranchPlan {
  return Boolean(
    plan &&
      (plan.branch ||
        plan.cleanup === "checkout" ||
        plan.cleanup === "prune" ||
        Object.keys(plan.proposed ?? {}).length),
  );
}

/**
 * Whether the run view offers removing the worktree: the run finished and
 * still has a worktree Inkuna created. Go checks again when asked.
 */
export function canRemoveWorktree(run: WorkflowRun): boolean {
  return (
    isTerminal(run.state) &&
    Boolean(run.worktree) &&
    !run.worktreeRemovedAt &&
    run.cleanup !== "checkout"
  );
}
