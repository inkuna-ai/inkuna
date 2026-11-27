import { render, screen, within } from "@testing-library/react";
import type { AuthenticationStatus } from "../types";
import { ConnectionsView } from "./ConnectionsView";

const status: AuthenticationStatus = {
  github: {
    provider: "github",
    authenticated: true,
    account: "octocat",
    detail: "Authenticated through gh for github.com",
    write: {
      status: "partial",
      detail:
        "gh's token can write issues but has no project scope, which lantern need to set a GitHub Project's Status; run gh auth refresh -s project",
    },
  },
  claude: { provider: "claude", authenticated: false, detail: "Not signed in" },
  linear: {
    provider: "linear",
    authenticated: true,
    account: "Ada",
    detail: "Authenticated for this Inkuna session",
    write: {
      status: "read-only",
      detail:
        "The key can only read, so importing issues works and every write says why it cannot be made.",
    },
  },
};

const getAuthenticationStatus = vi.fn(() => Promise.resolve(status));
vi.mock("../bridge", () => ({
  getAuthenticationStatus: () => getAuthenticationStatus(),
  authenticateClaude: vi.fn(),
  authenticateGitHub: vi.fn(),
  authenticateLinear: vi.fn(),
  forgetLinearAuthentication: vi.fn(),
}));

describe("ConnectionsView", () => {
  it("says whether each tracker's credential can write, and why", async () => {
    render(
      <ConnectionsView onAuthenticationChange={() => Promise.resolve()} />,
    );
    const writes = await screen.findAllByLabelText("Tracker writes");
    expect(writes).toHaveLength(2);
    const [github, linear] = writes;
    expect(within(github).getByText("can write some")).toBeInTheDocument();
    expect(github).toHaveTextContent("gh auth refresh -s project");
    expect(within(linear).getByText("read only")).toBeInTheDocument();
    expect(linear).toHaveTextContent("importing issues works");
  });
});
