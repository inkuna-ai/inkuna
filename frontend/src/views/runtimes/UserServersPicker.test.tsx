import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { UserMCPServer } from "../../types";
import type { RuntimeDraft } from "./draft";
import { RuntimeForm } from "./RuntimeForm";

const listUserMCPServers = vi.hoisted(() => vi.fn());
const signInUserMCPServer = vi.hoisted(() => vi.fn());
vi.mock("../../bridge", () => ({ listUserMCPServers, signInUserMCPServer }));

const servers: UserMCPServer[] = [
  {
    name: "linear-server",
    source: "config",
    transport: "http",
    url: "https://mcp.linear.app/mcp",
    status: "needs-sign-in",
    signIn: true,
  },
  {
    name: "claude.ai Gmail",
    source: "claude.ai",
    transport: "http",
    url: "https://gmailmcp.googleapis.com/mcp/v1",
    status: "ready",
    signIn: true,
  },
];

function renderForm(draft: RuntimeDraft) {
  const onSave = vi.fn<(draft: RuntimeDraft) => Promise<void>>(async () => {});
  render(
    <RuntimeForm
      draft={draft}
      taken={[]}
      secrets={[]}
      onCancel={() => {}}
      onSave={onSave}
    />,
  );
  return onSave;
}

describe("UserServersPicker", () => {
  beforeEach(() => {
    listUserMCPServers.mockReset();
    signInUserMCPServer.mockReset();
  });

  it("lists the CLI's servers on request, ticks them into userMcp, and signs in", async () => {
    listUserMCPServers.mockResolvedValue(servers);
    signInUserMCPServer.mockResolvedValue(undefined);
    const onSave = renderForm({
      name: "claude",
      isNew: false,
      builtIn: true,
      config: { binary: "claude" },
    });
    // Claude Code connects to every server to list them, so nothing is
    // listed until asked.
    expect(listUserMCPServers).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "List my servers" }));
    expect(await screen.findByText("linear-server")).toBeInTheDocument();
    expect(listUserMCPServers).toHaveBeenCalledWith("claude");
    expect(screen.getByText("needs sign-in")).toBeInTheDocument();
    expect(
      screen.getByText("claude.ai · https://gmailmcp.googleapis.com/mcp/v1"),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("linear-server"));
    fireEvent.click(screen.getByRole("button", { name: "Save runtime" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].config).toEqual({
      binary: "claude",
      userMcp: ["linear-server"],
    });

    listUserMCPServers.mockResolvedValue([
      { ...servers[0], status: "ready" },
      servers[1],
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() =>
      expect(signInUserMCPServer).toHaveBeenCalledWith(
        "claude",
        "linear-server",
      ),
    );
    await waitFor(() =>
      expect(screen.queryByText("needs sign-in")).not.toBeInTheDocument(),
    );
  });

  it("keeps a ticked server the CLI no longer lists, so it can be unticked", async () => {
    listUserMCPServers.mockResolvedValue([servers[1]]);
    const onSave = renderForm({
      name: "claude",
      isNew: false,
      builtIn: true,
      config: { userMcp: ["notes"] },
    });
    expect(screen.getByLabelText("notes")).toBeChecked();
    fireEvent.click(screen.getByRole("button", { name: "List my servers" }));
    expect(await screen.findByText("not configured")).toBeInTheDocument();
    fireEvent.click(screen.getByLabelText("notes"));
    fireEvent.click(screen.getByRole("button", { name: "Save runtime" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].config).toEqual({});
  });

  it("drops another CLI's servers when a copy switches CLI, and lists only the saved one", async () => {
    const onSave = renderForm({
      name: "work",
      isNew: false,
      builtIn: false,
      config: { preset: "claude", userMcp: ["claude.ai Gmail"] },
    });
    expect(screen.getByLabelText("claude.ai Gmail")).toBeChecked();
    expect(
      screen.getByRole("button", { name: "List my servers" }),
    ).toBeEnabled();
    fireEvent.change(screen.getByLabelText("Start from"), {
      target: { value: "codex" },
    });
    // Listing asks the runtime as saved, which is still Claude Code.
    expect(screen.queryByLabelText("claude.ai Gmail")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "List my servers" }),
    ).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Save runtime" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].config).toEqual({ preset: "codex" });
  });

  it("asks to save a new runtime before listing its servers", () => {
    renderForm({
      name: "claude-work",
      isNew: true,
      builtIn: false,
      config: { preset: "claude" },
    });
    expect(
      screen.getByRole("button", { name: "List my servers" }),
    ).toBeDisabled();
    expect(
      screen.getByText("Save the runtime first to list its CLI's servers."),
    ).toBeInTheDocument();
  });
});
