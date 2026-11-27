import type { WorkflowRun } from "../../types";
import { deletableTasks, everyTask, filterRuns, filtering } from "./history";

const run = (fields: Partial<WorkflowRun>): WorkflowRun => ({
  id: "run",
  workflowId: "workflow",
  taskId: "task",
  projectId: "inkuna",
  state: "completed",
  title: "Run",
  startedAt: "2026-11-01T10:00:00Z",
  finishedAt: "2026-11-01T11:00:00Z",
  events: [],
  ...fields,
});

const now = Date.parse("2026-11-05T12:00:00Z");
const runs = [
  run({
    id: "done",
    taskId: "a",
    finishedAt: "2026-11-05T09:00:00Z",
    pullRequest: {
      number: 12,
      title: "Fix",
      url: "https://github.com/acme/app/pull/12",
      state: "MERGED",
      mergedAt: "2026-11-05T10:00:00Z",
      checkedAt: "2026-11-05T11:00:00Z",
    },
  }),
  run({ id: "failed", taskId: "b", state: "failed" }),
  run({
    id: "old",
    taskId: "c",
    state: "cancelled",
    projectId: "docs",
    startedAt: "2026-09-01T10:00:00Z",
    finishedAt: "2026-09-01T10:30:00Z",
  }),
  run({
    id: "running",
    taskId: "d",
    state: "running",
    startedAt: "2026-10-01T10:00:00Z",
    finishedAt: undefined,
    deleteRefused: "task d cannot be deleted: run running is running",
  }),
];
const ids = (list: WorkflowRun[]) => list.map((item) => item.id);

describe("task filters", () => {
  it("shows every run until a filter narrows it", () => {
    expect(ids(filterRuns(runs, everyTask, now))).toEqual(ids(runs));
    expect(filtering(everyTask)).toBe(false);
    expect(filtering({ ...everyTask, age: "7d" })).toBe(true);
  });

  it("filters by how a run ended, or that it has not", () => {
    const by = (state: typeof everyTask.state) =>
      ids(filterRuns(runs, { ...everyTask, state }, now));
    expect(by("failed")).toEqual(["failed"]);
    expect(by("cancelled")).toEqual(["old"]);
    expect(by("active")).toEqual(["running"]);
    expect(by("merged")).toEqual(["done"]);
  });

  it("filters by project", () => {
    expect(
      ids(filterRuns(runs, { ...everyTask, project: "docs" }, now)),
    ).toEqual(["old"]);
  });

  it("keeps runs older than the age, by when they ended or else started", () => {
    expect(ids(filterRuns(runs, { ...everyTask, age: "1d" }, now))).toEqual([
      "failed",
      "old",
      "running",
    ]);
    expect(ids(filterRuns(runs, { ...everyTask, age: "30d" }, now))).toEqual([
      "old",
      "running",
    ]);
    expect(ids(filterRuns(runs, { ...everyTask, age: "90d" }, now))).toEqual(
      [],
    );
  });

  it("offers each task Go would delete once, and none it refuses", () => {
    const second = run({ id: "done-2", taskId: "a" });
    const refusedSibling = run({
      id: "sibling",
      taskId: "d",
      state: "completed",
    });
    expect(deletableTasks([...runs, second, refusedSibling])).toEqual([
      "a",
      "b",
      "c",
    ]);
  });
});
