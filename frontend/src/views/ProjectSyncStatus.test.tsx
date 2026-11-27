import { fireEvent, render, screen } from "@testing-library/react";
import type { BranchSync, ProjectSyncInfo, SyncSummary } from "../types";
import { ProjectSyncStatus } from "./ProjectSyncStatus";

const getProjectSync = vi.hoisted(() => vi.fn());
vi.mock("../bridge", () => ({ getProjectSync }));

const main = (fields: Partial<BranchSync>): BranchSync => ({
  name: "main",
  upstream: "origin/main",
  state: "synced",
  ahead: 0,
  behind: 0,
  behindBase: 0,
  commit: "abc1234",
  committedAt: "2026-10-20T09:00:00Z",
  updatable: false,
  unique: 0,
  deletable: false,
  main: true,
  ...fields,
});

const sync = (
  summary: Partial<SyncSummary>,
  fields: Partial<ProjectSyncInfo> = {},
): ProjectSyncInfo => ({
  projectId: "project",
  summary: {
    hasOrigin: true,
    origin: "github.com/inkuna-ai/inkuna",
    base: "main",
    fetchedAt: "2026-10-20T10:00:00Z",
    checkout: main({}),
    branches: 4,
    outOfSync: 0,
    noUpstream: 0,
    ...summary,
  },
  ...fields,
});

beforeEach(() => getProjectSync.mockReset());

describe("ProjectSyncStatus", () => {
  it("shows local state, then what a stale fetch found", async () => {
    getProjectSync.mockImplementation((_id: string, fetchIfStale: boolean) =>
      Promise.resolve(
        fetchIfStale
          ? sync(
              {
                checkout: main({ state: "behind", behind: 3 }),
                outOfSync: 2,
                noUpstream: 5,
              },
              { fetched: true },
            )
          : sync({}),
      ),
    );
    const onOpen = vi.fn();
    render(<ProjectSyncStatus projectID="project" onOpen={onOpen} />);

    expect(await screen.findByText("3 behind")).toBeInTheDocument();
    expect(screen.getByText("origin/main")).toBeInTheDocument();
    expect(
      screen.getByText(/2 out of sync · 5 without an upstream/),
    ).toBeInTheDocument();
    expect(getProjectSync.mock.calls).toEqual([
      ["project", false],
      ["project", true],
    ]);
    fireEvent.click(screen.getByRole("button", { name: "Branches →" }));
    expect(onOpen).toHaveBeenCalled();
  });

  it("does not fetch a repository without origin", async () => {
    getProjectSync.mockResolvedValue(
      sync({
        hasOrigin: false,
        origin: undefined,
        fetchedAt: undefined,
        checkout: main({ upstream: undefined, state: "local" }),
      }),
    );
    render(<ProjectSyncStatus projectID="project" onOpen={() => {}} />);

    expect(await screen.findByText("No origin remote")).toBeInTheDocument();
    expect(screen.getByText("local only")).toBeInTheDocument();
    expect(getProjectSync).toHaveBeenCalledTimes(1);
  });

  it("says when the fetch failed and keeps the local state", async () => {
    getProjectSync.mockImplementation((_id: string, fetchIfStale: boolean) =>
      Promise.resolve(
        fetchIfStale
          ? sync(
              { checkout: undefined },
              { fetchError: "Git could not sign in to GitHub." },
            )
          : sync({ checkout: undefined }),
      ),
    );
    render(<ProjectSyncStatus projectID="project" onOpen={() => {}} />);

    expect(
      await screen.findByText("Could not fetch from origin"),
    ).toHaveAttribute("title", "Git could not sign in to GitHub.");
    expect(screen.getByText("Detached HEAD")).toBeInTheDocument();
  });

  it("shows nothing outside the desktop app", async () => {
    getProjectSync.mockResolvedValue(null);
    const { container } = render(
      <ProjectSyncStatus projectID="project" onOpen={() => {}} />,
    );
    await vi.waitFor(() => expect(getProjectSync).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});
