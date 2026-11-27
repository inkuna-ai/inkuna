import { render, screen } from "@testing-library/react";
import { StageChain } from "./RuleSummary";

describe("StageChain", () => {
  it("names the MCP servers and tracker kinds an agent stage grants", () => {
    render(
      <StageChain
        stages={[
          {
            id: "research",
            kind: "agent",
            agent: "researcher",
            permission: "read-only",
            mcp: ["docs-search", "linear"],
            tracker: ["comment", "create"],
          },
          { id: "approval", kind: "approval" },
        ]}
      />,
    );
    expect(
      screen.getByText(
        "agent researcher · read-only · MCP docs-search, linear · tracker comment, create",
      ),
    ).toBeInTheDocument();
  });

  it("shows a use by what it uses and what it overrides", () => {
    render(
      <StageChain
        stages={[
          {
            use: "verify",
            id: "verify-docs",
            kind: "",
            commands: [["go", "test", "./internal/docs/..."]],
            overrides: ["commands"],
          },
          { use: "approval", id: "", kind: "" },
        ]}
      />,
    );
    expect(screen.getByText("uses verify")).toBeInTheDocument();
    expect(screen.getByText("verify-docs")).toBeInTheDocument();
    expect(screen.getByText("overrides commands")).toBeInTheDocument();
    expect(screen.getByText("uses approval")).toBeInTheDocument();
    expect(screen.getByText("approval")).toBeInTheDocument();
  });
});
