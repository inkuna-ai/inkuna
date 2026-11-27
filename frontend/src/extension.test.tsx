import { act, cleanup, screen } from "@testing-library/react";
import type { Root } from "react-dom/client";
import { renderInkuna } from "./extension";

// jsdom cannot parse Tailwind's stylesheet, and no assertion needs it.
vi.mock("./index.css", () => ({}));
import type { ExtensionView } from "./types";

const view = (id: ExtensionView["id"]): ExtensionView => ({
  id,
  label: "Audit records",
  group: "work",
  render: () => <p>Audit screen</p>,
});

describe("renderInkuna", () => {
  let root: HTMLDivElement;
  let app: Root | undefined;
  beforeEach(() => {
    root = document.createElement("div");
    document.body.append(root);
  });
  afterEach(() => {
    cleanup();
    act(() => app?.unmount());
    app = undefined;
    root.remove();
  });

  it("renders the app into the root it is given", async () => {
    await act(async () => {
      app = renderInkuna(root, []);
    });
    expect(
      await screen.findByRole("navigation", { name: "Primary" }),
    ).toBeInTheDocument();
    expect(root).toContainElement(
      screen.getByRole("navigation", { name: "Primary" }),
    );
  });

  it("refuses two views with one id, since only one could ever be shown", () => {
    expect(() =>
      renderInkuna(root, [view("audit.records"), view("audit.records")]),
    ).toThrow('two extension views share the id "audit.records"');
    expect(root).toBeEmptyDOMElement();
  });
});
