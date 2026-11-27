import type { BranchCommit } from "../../../types";

/**
 * A run of commits on the branch made by one step, or by no step Inkuna
 * knows of. #49's rounds will split a step's run further.
 */
export interface CommitGroup {
  key: string;
  stepId?: string;
  agent?: string;
  external: boolean;
  commits: BranchCommit[];
}

/**
 * Groups commits in branch order, starting a group wherever the step that
 * made them changes, so the list still reads as the branch's history.
 */
export function groupCommits(commits: BranchCommit[]): CommitGroup[] {
  const groups: CommitGroup[] = [];
  for (const commit of commits) {
    const external = commit.external === true;
    const last = groups[groups.length - 1];
    if (
      last &&
      last.external === external &&
      last.stepId === commit.stepId &&
      last.agent === commit.agent
    )
      last.commits.push(commit);
    else
      groups.push({
        key: `${groups.length}-${commit.hash}`,
        stepId: commit.stepId,
        agent: commit.agent,
        external,
        commits: [commit],
      });
  }
  return groups;
}
