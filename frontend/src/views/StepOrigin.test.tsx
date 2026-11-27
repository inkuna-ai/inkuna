import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { StepOriginBadge } from "./StepOrigin";

const origin = {
  stage: "verify",
  groups: ["verify-and-approve"],
  scope: "global",
  file: "/stages/checks.yaml",
  overrides: ["commands"],
};

/** Puts the badge at top on a 600px-high viewport, with a 120×100 popover. */
function layout(top: { value: number }) {
  const summary = screen.getByText("shared: verify-and-approve (global)");
  summary.getBoundingClientRect = () =>
    ({ top: top.value, bottom: top.value + 20, left: 40 }) as DOMRect;
  const popover = screen.getByRole("group", { name: "Where Verify came from" });
  Object.defineProperty(popover, "offsetHeight", { value: 100 });
  Object.defineProperty(popover, "offsetWidth", { value: 120 });
  return { summary, popover };
}

describe("StepOriginBadge", () => {
  const height = window.innerHeight;
  beforeEach(() => {
    window.innerHeight = 600;
  });
  afterEach(() => {
    window.innerHeight = height;
  });

  it("places its explanation against the viewport, so a scrolling table cannot clip it", async () => {
    render(<StepOriginBadge origin={origin} step="Verify" />);
    const top = { value: 200 };
    const { summary, popover } = layout(top);
    fireEvent.click(summary);
    await waitFor(() => expect(popover).toHaveStyle({ top: "224px" }));
    expect(popover).toHaveClass("fixed");
    expect(popover).toHaveStyle({ left: "40px" });

    // Near the bottom of the window it opens above the badge.
    top.value = 540;
    fireEvent.scroll(window);
    expect(popover).toHaveStyle({ top: "436px" });
    expect(popover).toHaveTextContent("Overridden by this flow: commands.");
  });
});
