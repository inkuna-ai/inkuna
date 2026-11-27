import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import type { ConfigFile, ConfigOverride, LibraryProblem } from "../../types";
import { emptyConfigFile } from "../../types";
import { OverridesContext } from "./overrides";
import { StageLibraryPanel } from "./StageLibraryPanel";

vi.mock("../../bridge", () => ({ chooseDirectory: vi.fn() }));

// The panel inside a draft the way SetupView keeps one, so each edit can be
// read back from what would be saved.
function Harness({
  problems = [],
  overrides = [],
  onChange,
}: {
  problems?: LibraryProblem[];
  overrides?: ConfigOverride[];
  onChange(file: ConfigFile): void;
}) {
  const [file, setFile] = useState<ConfigFile>(emptyConfigFile);
  return (
    <OverridesContext.Provider
      value={new Map(overrides.map((entry) => [entry.field, entry]))}
    >
      <StageLibraryPanel
        directories={file.stages.directories ?? []}
        effective={["/data/stages"]}
        problems={problems}
        update={(mutate) =>
          setFile((current) => {
            const draft: ConfigFile = JSON.parse(JSON.stringify(current));
            mutate(draft);
            onChange(draft);
            return draft;
          })
        }
        onError={() => {}}
      />
    </OverridesContext.Provider>
  );
}

test("adds and removes stage library directories in the draft", () => {
  let saved: ConfigFile = emptyConfigFile;
  render(<Harness onChange={(file) => (saved = file)} />);
  expect(screen.getByText(/Using the default: \/data\/stages/)).toBeTruthy();
  fireEvent.change(screen.getByLabelText("Stage library directory path"), {
    target: { value: " /team/stages " },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add directory" }));
  expect(saved.stages.directories).toEqual(["/team/stages"]);
  const list = screen.getByRole("list", { name: "Stage library directories" });
  expect(within(list).getByText("/team/stages")).toBeTruthy();
  fireEvent.click(
    screen.getByRole("button", {
      name: "Remove stage library directory /team/stages",
    }),
  );
  expect(saved.stages.directories).toEqual([]);
});

test("shows library problems without stopping a save, and the override", () => {
  render(
    <Harness
      onChange={() => {}}
      problems={[
        {
          file: "/team/stages/checks.yaml",
          message: "parse stage library: field command not found",
        },
      ]}
      overrides={[
        {
          field: "stages.directories",
          variable: "INKUNA_STAGE_DIRECTORIES",
          value: "/env/stages",
        },
      ]}
    />,
  );
  const status = screen.getByRole("status");
  expect(within(status).getByText("/team/stages/checks.yaml")).toBeTruthy();
  expect(status.textContent).toContain("field command not found");
  expect(status.textContent).toContain("Saving settings is not.");
  expect(
    screen.getByText("overridden by $INKUNA_STAGE_DIRECTORIES=/env/stages"),
  ).toBeTruthy();
  expect(screen.queryByText(/Using the default/)).toBeNull();
});
