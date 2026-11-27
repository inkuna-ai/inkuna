import { render, screen } from "@testing-library/react";
import type { BranchPlan } from "../types";
import { BranchPanel } from "./BranchPanel";

const worktreePlan: BranchPlan = {
  worktree: true,
  branch: "feature/23-docs-index",
  base: "main",
  root: "/src/inkuna-worktrees",
  cleanup: "prune",
};

describe("BranchPanel", () => {
  it("shows the branch, its base, where the worktree goes, and the cleanup policy", () => {
    render(<BranchPanel plan={worktreePlan} />);
    const panel = screen.getByLabelText("Branch plan");
    expect(panel).toHaveTextContent("feature/23-docs-index from main");
    expect(panel).toHaveTextContent(
      "Worktree under /src/inkuna-worktrees, removed when the next run starts; the branch is always kept.",
    );
  });

  it("explains that a checkout run commits on the current branch", () => {
    render(
      <BranchPanel
        plan={{ worktree: false, base: "main", cleanup: "checkout" }}
      />,
    );
    expect(screen.getByLabelText("Branch plan")).toHaveTextContent(
      "Commits in the checkout on its current branch, which must be clean and not main.",
    );
  });

  it("lists proposed configuration that approval will write", () => {
    render(
      <BranchPanel
        plan={{
          ...worktreePlan,
          cleanup: "manual",
          proposed: { tracker: "github", "branch.base": "main" },
          notes: ["tracker: github (inferred from the repository)"],
        }}
      />,
    );
    const panel = screen.getByLabelText("Branch plan");
    expect(panel).toHaveTextContent(
      "Approving writes these values to .inkuna/branching.yaml",
    );
    expect(screen.getByText("tracker")).toBeInTheDocument();
    expect(screen.getByText("github")).toBeInTheDocument();
    expect(
      screen.getByText("tracker: github (inferred from the repository)"),
    ).toBeInTheDocument();
  });

  it("stays out of the way for a read-only plan that only carries a policy", () => {
    const { container, rerender } = render(
      <BranchPanel
        plan={{ worktree: true, base: "main", cleanup: "manual" }}
      />,
    );
    expect(container).toBeEmptyDOMElement();
    rerender(
      <BranchPanel plan={{ worktree: true, base: "main", cleanup: "prune" }} />,
    );
    expect(screen.getByLabelText("Branch plan")).toHaveTextContent(
      "Starting this run removes the worktrees of finished runs; their branches are kept.",
    );
  });
});
