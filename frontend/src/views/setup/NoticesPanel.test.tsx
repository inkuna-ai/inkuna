import { fireEvent, render, screen } from "@testing-library/react";
import { NoticesPanel } from "./NoticesPanel";
import { loadNotices } from "./notices";

// The page carries the generated file itself, Go's and npm's packages both.
test("the notices the page loads are the generated file", async () => {
  const notices = await loadNotices();
  expect(notices.startsWith("THIRD-PARTY NOTICES\n")).toBe(true);
  expect(notices).toContain("\ngithub.com/wailsapp/wails/v2 ");
  expect(notices).toContain("\nmermaid ");
});

test("the notices load when asked for", async () => {
  const load = vi.fn().mockResolvedValue("THIRD-PARTY NOTICES\n\nreact 19.1.1");
  render(<NoticesPanel load={load} />);
  expect(load).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", { name: "Show notices" }));

  expect(await screen.findByText(/react 19\.1\.1/)).toBeInTheDocument();
  expect(load).toHaveBeenCalledTimes(1);
  expect(screen.queryByRole("button", { name: "Show notices" })).toBeNull();
});

test("a notice that fails to load says so and can be asked for again", async () => {
  const load = vi
    .fn()
    .mockRejectedValueOnce(new Error("chunk missing"))
    .mockResolvedValueOnce("THIRD-PARTY NOTICES");
  render(<NoticesPanel load={load} />);

  fireEvent.click(screen.getByRole("button", { name: "Show notices" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Could not load the notices: Error: chunk missing",
  );

  fireEvent.click(screen.getByRole("button", { name: "Show notices" }));
  expect(await screen.findByText("THIRD-PARTY NOTICES")).toBeInTheDocument();
  expect(screen.queryByRole("alert")).toBeNull();
});
