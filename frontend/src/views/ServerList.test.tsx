import { render, screen } from "@testing-library/react";
import { ServerList } from "./ServerList";

describe("ServerList", () => {
  it("warns on a read-only step even when its only server is a tracker's", () => {
    render(
      <ServerList
        readOnly
        servers={[
          {
            name: "linear",
            url: "https://mcp.linear.app/mcp",
            tools: ["get_issue"],
            tracker: "linear",
          },
        ]}
      />,
    );
    const servers = screen.getByRole("region", { name: "MCP servers" });
    // Which tools only read is the declaration's word, so it says so.
    expect(servers).toHaveTextContent(
      "Read-only covers the worktree only: through these servers the step can change systems outside it.",
    );
    expect(servers).toHaveTextContent("linear · declared read tools");
  });
});
