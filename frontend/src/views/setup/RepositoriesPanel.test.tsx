import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import type {
  ConfigFile,
  RepositoryConfig,
  RepositoryTracker,
} from "../../types";
import { emptyConfigFile } from "../../types";
import { RepositoriesPanel } from "./RepositoriesPanel";

vi.mock("../../bridge", () => ({
  listRuntimes: vi.fn().mockResolvedValue([]),
  chooseDirectory: vi.fn(),
}));
vi.mock("../../events", () => ({ useInkunaEvent: () => {} }));

const lantern: RepositoryConfig = {
  name: "lantern",
  path: "/src/lantern",
  defaultRuntime: "",
};

// What Go read of lantern: tracker.yaml allows comments and new issues, and
// the result comment; this machine has switched nothing off yet.
const lanternTracker: RepositoryTracker = {
  path: "/src/lantern",
  name: "lantern",
  tracker: "github",
  file: "/src/lantern/.inkuna/tracker.yaml",
  exists: true,
  project: "acme/3",
  ceiling: {
    writes: ["comment", "create"],
    updates: { start: false, result: true, done: false },
  },
  effective: {
    writes: ["comment", "create"],
    updates: { start: false, result: true, done: false },
  },
};

// The panel inside a draft the way SetupView keeps one, so each edit can be
// read back from what would be saved.
function Harness({
  initial = [lantern],
  trackers,
  onChange,
}: {
  initial?: RepositoryConfig[];
  trackers: RepositoryTracker[];
  onChange(file: ConfigFile): void;
}) {
  const [file, setFile] = useState<ConfigFile>({
    ...emptyConfigFile,
    repositories: initial,
  });
  return (
    <RepositoriesPanel
      repositories={file.repositories}
      trackers={trackers}
      onError={() => {}}
      update={(mutate) =>
        setFile((current) => {
          const draft: ConfigFile = JSON.parse(JSON.stringify(current));
          mutate(draft);
          onChange(draft);
          return draft;
        })
      }
    />
  );
}

function check(group: string, label: RegExp) {
  return within(screen.getByRole("group", { name: group })).getByRole(
    "checkbox",
    { name: label },
  );
}

describe("RepositoriesPanel tracker writes", () => {
  it("shows what Go says each saved repository's runs may write", () => {
    render(
      <Harness
        trackers={[
          lanternTracker,
          {
            ...lanternTracker,
            path: "/src/bakery",
            exists: false,
            ceiling: {
              writes: [],
              updates: { start: false, result: false, done: false },
            },
            effective: {
              writes: [],
              updates: { start: false, result: false, done: false },
            },
            note: "the repository has no .inkuna/tracker.yaml, so nothing is written",
          },
        ]}
        onChange={() => {}}
      />,
    );
    const table = screen.getByRole("table", { name: "Declared repositories" });
    expect(within(table).getByText("comment, create")).toBeInTheDocument();
    expect(within(table).getByText("result")).toBeInTheDocument();
    expect(within(table).getByText("project acme/3")).toBeInTheDocument();
  });

  it("switches kinds and updates off on this machine, and back", () => {
    let saved: ConfigFile = emptyConfigFile;
    render(
      <Harness
        trackers={[lanternTracker]}
        onChange={(file) => (saved = file)}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Edit repository lantern" }),
    );
    // What tracker.yaml does not allow is marked; the switch cannot raise it.
    expect(
      check("Changes agents may ask for", /Change status/).parentElement,
    ).toHaveTextContent("off in tracker.yaml");
    expect(
      check("Changes agents may ask for", /Comment/).parentElement,
    ).not.toHaveTextContent("off in tracker.yaml");

    fireEvent.click(check("Changes agents may ask for", /Comment/));
    fireEvent.click(check("Updates Inkuna makes", /Comment the result/));
    fireEvent.click(screen.getByRole("button", { name: "Update repository" }));
    expect(saved.repositories[0].tracker).toEqual({
      writes: ["status", "edit", "create"],
      updates: { result: false },
    });

    fireEvent.click(
      screen.getByRole("button", { name: "Edit repository lantern" }),
    );
    expect(check("Changes agents may ask for", /Comment/)).not.toBeChecked();
    fireEvent.click(check("Changes agents may ask for", /Comment/));
    fireEvent.click(check("Updates Inkuna makes", /Comment the result/));
    fireEvent.click(screen.getByRole("button", { name: "Update repository" }));
    // Everything back on is no override at all.
    expect(saved.repositories[0].tracker).toBeUndefined();
  });

  it("shows no ceiling for a path Go has not read", () => {
    render(<Harness trackers={[lanternTracker]} onChange={() => {}} />);
    fireEvent.click(
      screen.getByRole("button", { name: "Edit repository lantern" }),
    );
    expect(
      check("Changes agents may ask for", /Change status/).parentElement,
    ).toHaveTextContent("off in tracker.yaml");
    // The override goes with the new path, whose tracker.yaml is unknown
    // until it is saved, so the old path's ceiling is not shown for it.
    fireEvent.change(screen.getByLabelText("Repository path"), {
      target: { value: "/src/lantern-next" },
    });
    expect(
      check("Changes agents may ask for", /Change status/).parentElement,
    ).not.toHaveTextContent("off in tracker.yaml");
  });

  it("keeps a row's override when its path is added again", () => {
    let saved: ConfigFile = emptyConfigFile;
    render(
      <Harness
        initial={[{ ...lantern, tracker: { writes: [] } }]}
        trackers={[lanternTracker]}
        onChange={(file) => (saved = file)}
      />,
    );
    fireEvent.change(screen.getByLabelText("Repository name"), {
      target: { value: "lantern" },
    });
    fireEvent.change(screen.getByLabelText("Repository path"), {
      target: { value: "/src/lantern" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add repository" }));
    expect(saved.repositories).toHaveLength(1);
    expect(saved.repositories[0].tracker).toEqual({ writes: [] });
  });

  it("turns every kind off as an empty list, not a missing one", () => {
    let saved: ConfigFile = emptyConfigFile;
    render(
      <Harness
        trackers={[lanternTracker]}
        onChange={(file) => (saved = file)}
      />,
    );
    fireEvent.click(
      screen.getByRole("button", { name: "Edit repository lantern" }),
    );
    for (const label of [/Change status/, /Comment/, /Edit fields/, /Create/]) {
      fireEvent.click(check("Changes agents may ask for", label));
    }
    fireEvent.click(screen.getByRole("button", { name: "Update repository" }));
    expect(saved.repositories[0].tracker).toEqual({ writes: [] });
  });
});
