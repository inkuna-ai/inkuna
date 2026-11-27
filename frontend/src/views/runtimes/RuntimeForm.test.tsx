import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { RuntimeDraft } from "./draft";
import { RuntimeForm } from "./RuntimeForm";

function renderForm(draft: RuntimeDraft) {
  const onSave = vi.fn<(draft: RuntimeDraft) => Promise<void>>(
    async () => {},
  );
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

describe("RuntimeForm", () => {
  it("lets a built-in adapter load the user's own CLI setup, off by default", async () => {
    const onSave = renderForm({
      name: "codex",
      isNew: false,
      builtIn: true,
      config: { binary: "codex" },
    });
    const own = screen.getByLabelText("Load my own CLI setup in workers");
    expect(own).not.toBeChecked();
    fireEvent.click(own);
    fireEvent.click(screen.getByRole("button", { name: "Save runtime" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(1));
    expect(onSave.mock.calls[0][0].config).toEqual({
      binary: "codex",
      userConfig: true,
    });

    // Unticking leaves the field out, as inkuna.yaml does when it is off.
    fireEvent.click(own);
    fireEvent.click(screen.getByRole("button", { name: "Save runtime" }));
    await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2));
    expect(onSave.mock.calls[1][0].config).toEqual({ binary: "codex" });
  });

  it("keeps the choice across adapters and drops it for a template", () => {
    renderForm({ name: "work", isNew: true, builtIn: false, config: {} });
    expect(
      screen.queryByLabelText("Load my own CLI setup in workers"),
    ).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Start from"), {
      target: { value: "claude" },
    });
    fireEvent.click(screen.getByLabelText("Load my own CLI setup in workers"));
    fireEvent.change(screen.getByLabelText("Start from"), {
      target: { value: "copilot" },
    });
    expect(
      screen.getByLabelText("Load my own CLI setup in workers"),
    ).toBeChecked();
    fireEvent.change(screen.getByLabelText("Start from"), {
      target: { value: "" },
    });
    expect(
      screen.queryByLabelText("Load my own CLI setup in workers"),
    ).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Start from"), {
      target: { value: "codex" },
    });
    expect(
      screen.getByLabelText("Load my own CLI setup in workers"),
    ).not.toBeChecked();
  });
});
