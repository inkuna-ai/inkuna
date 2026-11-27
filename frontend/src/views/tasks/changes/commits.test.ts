import type { BranchCommit } from "../../../types";
import { groupCommits } from "./commits";

const commit = (
  hash: string,
  stepId?: string,
  agent?: string,
): BranchCommit => ({
  hash,
  shortHash: hash,
  subject: hash,
  author: "Inkuna <inkuna@localhost>",
  date: "2026-10-26T09:00:00Z",
  stepId,
  agent,
  external: !stepId,
  files: 1,
  additions: 1,
  deletions: 0,
});

describe("groupCommits", () => {
  it("keeps branch order and starts a group wherever the step changes", () => {
    const groups = groupCommits([
      commit("a", "research", "researcher"),
      commit("b", "implement", "implementer"),
      commit("c", "implement", "implementer"),
      commit("d"),
      commit("e", "implement", "implementer"),
      commit("f"),
      commit("g"),
    ]);
    expect(
      groups.map((group) => [
        group.external ? "external" : group.stepId,
        group.commits.map((entry) => entry.hash).join(""),
      ]),
    ).toEqual([
      ["research", "a"],
      ["implement", "bc"],
      ["external", "d"],
      ["implement", "e"],
      ["external", "fg"],
    ]);
    expect(new Set(groups.map((group) => group.key)).size).toBe(groups.length);
  });
});
