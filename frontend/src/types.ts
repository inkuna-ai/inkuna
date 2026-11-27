import type { ReactNode } from "react";

/** A screen Inkuna itself has. */
export type BuiltInView =
  | "projects"
  | "request"
  | "workflow"
  | "tasks"
  | "costs"
  | "docs"
  | "branches"
  | "messages"
  | "agents"
  | "rules"
  | "setup"
  | "runtimes"
  | "connections";
/**
 * A screen an embedding frontend adds, named by the capability Go reports for
 * it. Go namespaces every capability by its extension ("example.audit"), so
 * one never equals a built-in view's name.
 */
export type ExtensionViewID = `${string}.${string}`;
/** Any screen the sidebar can open. */
export type View = BuiltInView | ExtensionViewID;
/**
 * A screen an embedding frontend passes to renderInkuna. It is shown only
 * while Go reports the capability named by its id.
 */
export interface ExtensionView {
  /** The capability Go reports for this screen, such as "example.audit". */
  id: ExtensionViewID;
  label: string;
  /** The sidebar group it is listed in, after Inkuna's own screens. */
  group: "work" | "configure";
  /** One character beside the label, and alone in the narrow sidebar; "•" by default. */
  glyph?: string;
  render(): ReactNode;
}
/** A runtime's name. codex, claude, and copilot are built in; inkuna.yaml adds others. */
export type Runtime = string;
/** One runtimes: entry. A preset builds its own arguments; otherwise the templates apply. */
export interface RuntimeConfig {
  label?: string;
  preset?: Runtime;
  binary?: string;
  args?: string[];
  readOnlyArgs?: string[];
  writeArgs?: string[];
  stdin?: string;
  output?: string;
  versionArgs?: string[];
  secrets?: string[];
  /** Built-in adapters only: workers load the user's own CLI setup. */
  userConfig?: boolean;
  /**
   * Built-in adapters only: the user's own MCP servers, as the CLI lists
   * them, every step on this runtime is given with all their tools.
   */
  userMcp?: string[];
}
/** A declared runtime as the Runtimes view and every runtime picker see it. */
export interface RuntimeInfo {
  name: Runtime;
  label: string;
  builtIn: boolean;
  /** A built-in whose entry differs from the default, so Reset does something. */
  customized?: boolean;
  preset?: Runtime;
  /** What will be executed, after defaults and the environment. */
  binary: string;
  /** False for a template without write arguments: read-only steps only. */
  canWrite: boolean;
  config: RuntimeConfig;
  /** The environment variable currently setting the binary, if any. */
  override?: string;
}
export interface RuntimeTest {
  available: boolean;
  path?: string;
  version?: string;
  detail?: string;
}
export const builtInRuntimes: RuntimeInfo[] = [
  { name: "claude", label: "Claude Code" },
  { name: "codex", label: "Codex" },
  { name: "copilot", label: "GitHub Copilot" },
].map((runtime) => ({
  ...runtime,
  builtIn: true,
  preset: runtime.name,
  binary: runtime.name,
  canWrite: true,
  config: {},
}));
export type TaskKind =
  | "research"
  | "bugfix"
  | "implementation"
  | "review"
  | "documentation"
  | "skill-authoring";

export interface HealthCheck {
  name: string;
  status: string;
  /** What Go found and, when not ready, how to fix it. */
  detail: string;
  required: boolean;
  /** The screen that fixes the check, when Go names one. */
  action?: "connections" | "setup";
}
export interface Project {
  id: string;
  name: string;
  repository: string;
  defaultRuntime: Runtime;
  createdAt: string;
  /** When the project was removed; absent while it is connected. */
  removedAt?: string;
}
/** What connecting a folder would connect, worked out by Go before saving. */
export interface RepositoryCandidate {
  /** The repository's root, which the chosen folder is in. */
  path: string;
  /** The suggested project name. */
  name: string;
  /** Why the folder cannot be connected; absent when it can. */
  refused?: string;
  /** The removed project connecting this path brings back. */
  restores?: string;
}
/** What removing a project would do, so the person can choose first. */
export interface ProjectRemovalPreview {
  projectId: string;
  name: string;
  repository: string;
  /** Why the project cannot be removed now; absent when it can. */
  refused?: string;
  tasks: number;
  runs: number;
  /** Its runs' worktrees still on disk, which removal leaves. */
  worktrees: string[];
}
export interface ProjectRemoval {
  project: Project;
  deletedTasks: number;
}
/** How a local branch stands against its upstream; Go derives it. */
export type BranchSyncState =
  | "synced"
  | "behind"
  | "ahead"
  | "diverged"
  | "gone"
  | "local";
/** One local branch measured against its upstream and the base. */
export interface BranchSync {
  name: string;
  upstream?: string;
  state: BranchSyncState;
  ahead: number;
  behind: number;
  /** Commits on the base the branch does not have. */
  behindBase: number;
  isBase?: boolean;
  commit: string;
  committedAt: string;
  /** The checkout that has the branch checked out; main says it is the project checkout. */
  checkedOut?: string;
  main?: boolean;
  dirty?: boolean;
  /** Go would fast-forward it; reason says why a branch that is behind cannot be. */
  updatable: boolean;
  reason?: string;
  /** For a branch without an upstream: commits no other branch, tag, or remote has. */
  unique: number;
  /** Go would delete it; deleteReason says why a branch without an upstream cannot be. */
  deletable: boolean;
  deleteReason?: string;
}
/** What DeleteBranch removed; `git branch <branch> <commit>` restores it. */
export interface BranchDeletion {
  branch: string;
  commit: string;
  unique: number;
  /** The linked worktree removed along with the branch. */
  worktree?: string;
}
/** What UpdateBranch did; from equals to when there was nothing to do. */
export interface BranchUpdate {
  branch: string;
  upstream: string;
  from: string;
  to: string;
  checkedOut?: string;
  /** Uncommitted settings edits and untracked files the checkout kept. */
  kept?: number;
  /** Settings edits the upstream already had, now taken as merged. */
  settled?: string[];
}
/** A repository's local branches against what the last fetch saw. */
export interface SyncStatus {
  repository: string;
  hasOrigin: boolean;
  /** origin as host/owner/repo. */
  origin?: string;
  base?: string;
  /** What behindBase counts against: origin/<base>, else <base>. */
  baseRef?: string;
  fetchedAt?: string;
  branches: BranchSync[];
  truncated?: boolean;
}
/** One uncommitted change to an Inkuna setting in the project checkout. */
export interface SettingsChange {
  path: string;
  kind: "added" | "modified" | "deleted";
  /** The base already has the file as the checkout does. */
  onBase?: boolean;
  /** The base changed the file after the checkout's commit. */
  conflict?: boolean;
}
/** The project checkout's settings edits against the base they would go to. */
export interface SettingsStatus {
  baseRef?: string;
  changes: SettingsChange[];
  /** What a share would commit; ShareSettings takes only this digest. */
  digest?: string;
  shareable: boolean;
  reason?: string;
  /** The branch an earlier share pushed these edits on. */
  sharedOn?: string;
}
export type ShareMode = "pull-request" | "push";
/** What ShareSettings did. */
export interface SettingsShare {
  mode: ShareMode;
  base: string;
  commit: string;
  files: string[];
  branch?: string;
  /** The pull request, or the page to open one on when pullRequest is false. */
  url?: string;
  pullRequest?: boolean;
  updated?: BranchUpdate;
  note?: string;
}
/** A project's branches as Go measured them, with what a fetch or update did. */
export interface BranchSyncInfo {
  projectId: string;
  sync: SyncStatus;
  /** Settings edits uncommitted in the project checkout; absent when none. */
  settings?: SettingsStatus;
  fetchError?: string;
  update?: BranchUpdate;
  deleted?: BranchDeletion;
  shared?: SettingsShare;
}
/** How a repository stands against origin at a glance, for its project card. */
export interface SyncSummary {
  hasOrigin: boolean;
  origin?: string;
  base?: string;
  fetchedAt?: string;
  /** The branch checked out in the project checkout; absent when HEAD is detached. */
  checkout?: BranchSync;
  branches: number;
  /** Branches behind, diverged from, or gone from their upstream. */
  outOfSync: number;
  noUpstream: number;
}
/** A project's sync summary; fetched says Go fetched origin first. */
export interface ProjectSyncInfo {
  projectId: string;
  summary: SyncSummary;
  fetched?: boolean;
  fetchError?: string;
}
export interface SetupInfo {
  configPath: string;
  configExists: boolean;
  dataDir: string;
}
export interface DashboardData {
  projects: Project[];
  /** Removed projects whose history is kept, to name it in Tasks and Costs. */
  removedProjects?: Project[];
  health: HealthCheck[];
  defaults: AppDefaults;
  setup?: SetupInfo;
  pendingInputs?: number;
  activeRuns?: number;
}
export interface RoutingBand {
  minimum: number;
  maximum: number;
  model: string;
  reasoningEffort?: string;
}
export interface RoutingPolicy {
  version: number;
  bands: Record<Runtime, RoutingBand[]>;
}
export interface AppDefaults {
  maxConcurrency: number;
  maxTasks?: number;
  planningSeconds: number;
  workerSeconds: number;
  verifierSeconds: number;
  approvalSeconds: number;
  orchestrator?: Runtime;
  routingPolicy: RoutingPolicy;
}

export const emptyDefaults: AppDefaults = {
  maxConcurrency: 0,
  planningSeconds: 0,
  workerSeconds: 0,
  verifierSeconds: 0,
  approvalSeconds: 0,
  routingPolicy: { version: 0, bands: { codex: [], claude: [], copilot: [] } },
};
export interface ComplexityAssessment {
  score: number;
  rationale: string;
  signals?: string[];
}
export interface ModelSelection {
  model: string;
  reasoningEffort?: string;
  policyVersion?: number;
  source: string;
  rationale: string;
}
export interface Budget {
  wallTimeSeconds: number;
  maxTurns?: number;
  maxOutputBytes?: number;
  maxCostCents?: number;
}
export interface WorkflowNode {
  id: string;
  title: string;
  kind: string;
  runtime?: Runtime;
  agent?: string;
  prompt?: string;
  complexity?: ComplexityAssessment;
  modelSelection?: ModelSelection;
  permission: string;
  selectedSkills?: string[];
  dependsOn?: string[];
  allowedPaths?: string[];
  commands?: string[][];
  expectedOutputs?: string[];
  budget: Budget;
  metadata?: Record<string, string>;
  review?: boolean;
  onFailure?: "fail" | "feedback";
  feedbackTarget?: string;
  maxRounds?: number;
  /** Exit codes that pause the run for a person instead of starting a
   * round; with any, a check that could not start or was stopped pauses
   * too. */
  systemExitCodes?: number[];
  /** The declared MCP servers the step is granted: its stage's and its agent's. */
  mcpServers?: string[];
  /** The user's own MCP servers the step's runtime gives it (userMcp). */
  userMcp?: string[];
  /** The kinds of tracker change an agent step may ask for in its handoff. */
  trackerKinds?: TrackerKind[];
  /** Each change approval made to the step's access, as Go recorded it. */
  accessEdits?: AccessEdit[];
  /** The access the step's stage gave it when the plan was made; approval keeps it as it is. */
  ruleAccess?: {
    permission?: string;
    commands?: string[][];
    allowedPaths?: string[];
  };
  /** The shared stage or group the step was planned from, as the plan
   * recorded it; absent for a stage written inline in the flow. */
  from?: StepOrigin;
}
/** Where a planned step came from: the shared stage or group its rule used. */
export interface StepOrigin {
  /** The shared stage; absent for a stage written inline in a group. */
  stage?: string;
  /** The groups it came through, outermost first. */
  groups?: string[];
  /** builtin, global, or repository. */
  scope: string;
  /** The library or rules file that defines it; absent for a built-in one. */
  file?: string;
  /** The fields the flow's use overrode. */
  overrides?: string[];
}
/**
 * A change approval made to what a step may do, against the plan: Go's note
 * of it, and whether it lets the step do more than the plan did.
 */
export interface AccessEdit {
  field: "permission" | "commands" | "allowedPaths";
  note: string;
  widens?: boolean;
}
/** The kinds of tracker change, in the order settings show them. */
export const trackerKinds: TrackerKind[] = [
  "status",
  "comment",
  "edit",
  "create",
];
export interface TaskSpec {
  version: number;
  id: string;
  kind: TaskKind;
  /** The task's name at a glance, and its runs' and branch's; tasks from
   * before titles have none. */
  title?: string;
  /** The details of what to do. */
  objective: string;
  source: { kind: string; reference?: string; repository?: string };
  acceptanceCriteria: string[];
  constraints?: string[];
  labels?: string[];
  repository: string;
  priority: number;
  budget: Budget;
  metadata?: Record<string, string>;
}
/** Worktree cleanup policies, plus the marker for a run in the checkout. */
export type CleanupPolicy =
  | "listener"
  | "prune"
  | "merged"
  | "manual"
  | "checkout";
/**
 * Where a run's writable steps work, derived by Go from the repository's
 * branching configuration. Approval cannot change it.
 */
export interface BranchPlan {
  /** false: writable steps commit in the checkout on its current branch. */
  worktree: boolean;
  branch?: string;
  base: string;
  /** Directory the worktree is created under. */
  root?: string;
  cleanup: CleanupPolicy | string;
  maxLength?: number;
  /** Values for missing configuration keys, written on approval. */
  proposed?: Record<string, string>;
  notes?: string[];
}
export interface WorkflowSpec {
  version: number;
  id: string;
  taskId: string;
  defaultRuntime: Runtime;
  routingPolicyVersion?: number;
  ruleName?: string;
  orchestrated?: boolean;
  maxConcurrency: number;
  steps: WorkflowNode[];
  branching?: BranchPlan;
  createdAt: string;
}
export interface WorkflowRequest {
  projectId: string;
  /** Left blank, planning writes one from the details. */
  title?: string;
  objective: string;
  acceptanceCriteria: string[];
  constraints: string[];
  labels?: string[];
  kind: TaskKind;
  sourceKind: "request" | "github" | "linear";
  sourceReference?: string;
  /** A rule name (or "default") to use instead of letting the rules match. */
  ruleName?: string;
  /** Ask the orchestrator again instead of reusing an identical plan's decision. */
  restaff?: boolean;
}
/** An agent the approval screen may put on a step. */
export interface AgentCandidate {
  name: string;
  description: string;
  permission: string;
  runtime?: Runtime;
  model?: string;
  source: string;
  taskKinds?: TaskKind[];
}
export interface Assignment {
  stageId: string;
  agent: string;
  skills?: string[];
  complexity: ComplexityAssessment;
  rationale: string;
}
export interface WorkflowPlan {
  project: Project;
  task: TaskSpec;
  workflow: WorkflowSpec;
  ruleName?: string;
  orchestrated?: boolean;
  decision?: { assignments?: Assignment[]; notes?: string };
  notes?: string[];
  events?: RunEvent[];
  candidates?: AgentCandidate[];
  /** The orchestrator calls made while planning: title, staffing, and branching. */
  usage?: UsageRecord[];
  /** When the reused staffing decision was made, for an identical plan. */
  staffingReused?: string;
  /** The MCP servers inkuna.yaml declares, without their secrets. */
  mcpServers?: MCPServer[];
}
export interface RunEvent {
  version: number;
  runId: string;
  sequence: number;
  timestamp: string;
  kind: string;
  summary: string;
  payload?: Record<string, unknown>;
}
export interface StepRun {
  id: string;
  title: string;
  kind: string;
  agent?: string;
  runtime?: Runtime;
  model?: string;
  state: string;
  a2aTaskId?: string;
  summary?: string;
  startedAt?: string;
  finishedAt?: string;
  /** The round of the run shown: the run's current round, or an earlier one
   * whose result a later round kept. */
  round?: number;
  /** The shared stage or group the step was planned from. */
  from?: StepOrigin;
}
export interface CommitInfo {
  hash: string;
  shortHash: string;
  author: string;
  date: string;
  subject: string;
  body?: string;
  stat: string;
  stepId?: string;
}
/** One commit on a run's branch (RunCommits), oldest first. */
export interface BranchCommit {
  hash: string;
  shortHash: string;
  subject: string;
  body?: string;
  author: string;
  /** Author date, RFC 3339. */
  date: string;
  /** The step that made it and its agent; an external commit has neither. */
  stepId?: string;
  agent?: string;
  /** Inkuna has no record of making it, such as an edit made by hand. */
  external?: boolean;
  /** More than one parent; its counts are against the first, as its commit scope is. */
  merge?: boolean;
  files: number;
  additions: number;
  deletions: number;
}
/**
 * Which of a run's changes to show (internal/diff.Scope): the whole branch
 * from the run's base to its tip now, one commit on it, or the changes
 * between two commits on it.
 */
export type DiffScope =
  | { kind: "branch" }
  | { kind: "commit"; commit: string }
  | { kind: "range"; from: string; to: string };
export type DiffFileStatus =
  | "added"
  | "modified"
  | "deleted"
  | "renamed"
  | "copied";
/** One changed file, without its patch. */
export interface DiffFile {
  /** The path after the change, or before it for a deleted file. */
  path: string;
  /** Where a renamed or copied file came from. */
  oldPath?: string;
  status: DiffFileStatus;
  /** git file modes such as 100644; they differ on a mode change. */
  oldMode?: string;
  newMode?: string;
  /** git's rename or copy score, 0 to 100. */
  similarity?: number;
  binary?: boolean;
  additions: number;
  deletions: number;
  /** Lockfiles, build output, and paths the repository marks generated; collapsed by default. */
  generated?: boolean;
  /** The content's blob after the change, or before it for a deleted file; Viewed marks are kept against it. */
  blob?: string;
}
/** What a scope changed (RunChanges). Load each file's patch with a range scope of from..to. */
export interface DiffChange {
  from: string;
  to: string;
  files: DiffFile[];
  additions: number;
  deletions: number;
}
/** A piece of a line's text; a line's spans join to its text. */
export interface DiffSpan {
  text: string;
  /** A highlighting class from frontend/src/diff.css, such as tok-keyword. */
  class?: string;
  /** Part of what changed within the line. */
  changed?: boolean;
}
export interface DiffLine {
  kind: "context" | "added" | "deleted";
  /** Line numbers in each version; absent on the side the line is not in. */
  old?: number;
  new?: number;
  text: string;
  /** The line ended in a carriage return, which text leaves out. */
  crlf?: boolean;
  /** The last line of a version with no newline at its end. */
  noNewline?: boolean;
  /** Absent when the line has neither highlighting nor changes within it. */
  spans?: DiffSpan[];
}
export interface DiffHunk {
  header: string;
  section?: string;
  oldStart: number;
  oldLines: number;
  newStart: number;
  newLines: number;
  lines: DiffLine[];
}
/** One file's patch (RunFilePatch). */
export interface DiffFilePatch {
  file: DiffFile;
  from: string;
  to: string;
  language?: string;
  hunks: DiffHunk[];
  bytes: number;
  lines: number;
  /** Lines in the newer version, so context below the last hunk can be expanded; 0 when unknown. */
  newTotal?: number;
  /** Not loaded, with the reason; loadable means it can be loaded anyway. */
  tooLarge?: boolean;
  loadable?: boolean;
  reason?: string;
}
/** How a check ended, or that it has not yet. */
export type CheckStatus =
  | "queued"
  | "running"
  | "passed"
  | "failed"
  | "cancelled"
  | "skipped";
/** One command of an evaluation step (RunChecks). */
export interface Check {
  id: string;
  executionId: string;
  stepId: string;
  stepRunId: string;
  /** Its place among its step's commands. */
  position: number;
  argv: string[];
  status: CheckStatus;
  /** Set once the command exited; one that could not start or was stopped has none. */
  exitCode?: number;
  /** RFC 3339; absent until the command starts or ends. */
  startedAt?: string;
  finishedAt?: string;
  logDigest?: string;
  /** How much the command wrote; logCut says the kept log left part of it out. */
  logBytes: number;
  logCut?: boolean;
  /** Why the command could not start. */
  error?: string;
  stepTitle?: string;
  durationMs: number;
}
/** A check's log, or what it gained after an offset (CheckLog). */
export interface CheckLog {
  /** The log from byte offset to byte end; ask for more from end. */
  text: string;
  offset: number;
  end: number;
  /** text is the whole log, to replace what the view has. */
  reset: boolean;
  running: boolean;
  cut: boolean;
  bytes: number;
}
/** A run's checks, counting each step's latest attempt. */
export interface CheckTotals {
  passed: number;
  failed: number;
  running: number;
  total: number;
}
/** Unchanged lines of a file read to expand a patch's context (RunFileExcerpt). */
export interface DiffExcerpt {
  lines: DiffLine[];
  /** Lines in the newer version of the file. */
  newTotal: number;
}
/** A file marked viewed in a run's changes, at the blob it was marked on. */
export interface ViewedFile {
  path: string;
  blob: string;
}
export interface WorkflowRun {
  id: string;
  workflowId: string;
  taskId: string;
  projectId?: string;
  state: string;
  title: string;
  runtime?: Runtime;
  model?: string;
  agent?: string;
  ruleName?: string;
  orchestrated?: boolean;
  worktree?: string;
  branch?: string;
  /** The commit the run's workspace started from; its changes are measured from here. */
  baseCommit?: string;
  cleanup?: CleanupPolicy | string;
  /** Set once Inkuna removed the worktree; the branch remains. */
  worktreeRemovedAt?: string;
  workspaceNotes?: string[];
  startedAt: string;
  finishedAt?: string;
  error?: string;
  steps?: StepRun[];
  lastCommit?: CommitInfo;
  pendingInputs?: number;
  live?: boolean;
  events: RunEvent[];
  /** What every call made for the run's task cost so far, across its runs. */
  taskCost?: CallTotals;
  /** The run's checks by how they ended; absent when it has none. */
  checks?: CheckTotals;
  /** The run's current feedback round, from 1. */
  round?: number;
  /** The story the run is for in its repository's tracker; only a run's detail carries it. */
  story?: RunStory;
  /** The changes the run's agents asked of its tracker; only a run's detail carries them. */
  trackerChanges?: TrackerChange[];
  /** Why Go would refuse to delete the run's task now; only the runs list carries it, and it is absent when Go would delete it. */
  deleteRefused?: string;
  /** The pull request the run's branch was opened in, as Go's last check found it. */
  pullRequest?: LinkedPullRequest;
  /** The run this one started over from, when it is a rerun. */
  rerunOf?: string;
}
/** What deleting a task, or one of its runs, would remove and leave, as Go previews it. */
export interface TaskDeletePreview {
  taskId: string;
  projectId: string;
  title: string;
  /** Why Go would refuse the delete now; absent when it would not. */
  refused?: string;
  runs: RunDeletePreview[];
}
/** One run of a delete preview, with what it left outside history. */
export interface RunDeletePreview {
  runId: string;
  title: string;
  state: string;
  finishedAt?: string;
  /** The run's worktree while it is there; one runs share is on the first. */
  worktree?: WorktreePreview;
  /** The run's branch while it exists; one runs share is on the first. */
  branch?: BranchPreview;
}
/** A run's worktree, the space it takes, and whether a delete may remove it. */
export interface WorktreePreview {
  path: string;
  bytes: number;
  /** Whether "Also remove worktrees" would remove it. */
  removable: boolean;
  reason?: string;
}
/** A run's branch, its tip, and what deleting it would lose. */
export interface BranchPreview {
  /** The project whose repository has it; a delete confirms it under this. */
  projectId: string;
  name: string;
  /** Its tip, which a delete must confirm. */
  commit: string;
  /** Commits only this branch has, which deleting it would lose. */
  unique: number;
  deletable: boolean;
  reason?: string;
}
/** What a delete removes besides history; neither is done unless asked. */
export interface DeleteOptions {
  removeWorktrees: boolean;
  /**
   * Each branch to delete, by project id and then branch name, with the tip
   * the person confirmed. Two projects can each have a branch of one name.
   */
  deleteBranches?: Record<string, Record<string, string>>;
}
/** Go's report of a delete, one entry per task. */
export interface DeleteReport {
  tasks: TaskDeleteReport[];
}
/** What a delete removed for one task, and what it kept and why. */
export interface TaskDeleteReport {
  taskId: string;
  /** Set when only this run of the task was deleted. */
  runId?: string;
  rows: Array<{ table: string; rows: number }>;
  /** Artifacts freed, and those another task still uses, which stay. */
  blobs: number;
  blobBytes: number;
  keptBlobs: number;
  worktrees: WorktreeOutcome[];
  branches: BranchOutcome[];
  /** Tracker issues Inkuna no longer links; nothing changed in the tracker. */
  links: Array<{ tracker: string; id: string; url?: string }>;
  problems?: string[];
}
/** Whether a delete removed a run's worktree, or why it kept it. */
export interface WorktreeOutcome {
  runId: string;
  path: string;
  removed: boolean;
  /** Why it was kept. */
  reason?: string;
}
/** Whether a delete deleted a run's branch, or why it kept it. */
export interface BranchOutcome {
  runId: string;
  name: string;
  commit?: string;
  unique: number;
  deleted: boolean;
  /** Why it was kept. */
  reason?: string;
}
/** The disk space history takes, and the worktrees deletes kept. */
export interface StorageUsage {
  artifacts: number;
  artifactBytes: number;
  /** Worktrees Inkuna made that are still on disk, kept ones included. */
  worktrees: number;
  worktreeBytes: number;
  kept: KeptWorktree[];
  problems?: string[];
}
/** A worktree a delete left on disk after its run was deleted. */
export interface KeptWorktree {
  path: string;
  projectId: string;
  projectName?: string;
  branch?: string;
  taskId: string;
  runId: string;
  title: string;
  /** Why the delete kept it. */
  reason: string;
  keptAt: string;
  bytes: number;
}
/** One change an agent asked for in its handoff, as its run keeps it. */
export interface TrackerChange {
  id: string;
  round: number;
  stepId: string;
  tracker?: string;
  kind: TrackerKind | string;
  /** The issue it acts on; for a new issue, its parent, if any. */
  issue?: string;
  /** What it would do or did, as the timeline says it. */
  summary: string;
  /** All it says, such as a comment's whole body. */
  change: {
    kind: string;
    issue?: string;
    body?: string;
    status?: string;
    title?: string;
    description?: string;
    addLabels?: string[];
    removeLabels?: string[];
    estimate?: number;
    priority?: number;
    assignee?: string;
    cycle?: string;
    milestone?: string;
    parent?: string;
  };
  /** refused, pending, discarded, applying, applied, or failed. */
  state: string;
  /** Why it was refused, discarded, or failed. */
  reason?: string;
  /** What applying it made: a comment, or a new issue. */
  url?: string;
  /** Set on a failed change the run can try again. */
  retry?: boolean;
  updatedAt: string;
}
/** A run's story, and whether Go offers Mark done on the run. */
export interface RunStory {
  tracker: string;
  id: string;
  /** Set when the run has ended, has a branch, and updates.done is on for it. */
  markDone?: boolean;
}
export interface PullRequest {
  number: number;
  title: string;
  url: string;
  /** OPEN, CLOSED, or MERGED. */
  state: string;
  mergedAt?: string;
}
/** A run branch's pull request as Go recorded it, and when it last looked. */
export interface LinkedPullRequest extends PullRequest {
  checkedAt: string;
}
/** What checking the projects' pull requests found and did, as Go says it. */
export interface PullRequestCheck {
  /** Projects read now; Go skips one it read a few minutes ago unless asked. */
  checked: number;
  linked: number;
  /** Worktrees removed because their pull request merged. */
  removed: string[];
  problems?: string[];
}
/** What Mark done found and did, as Go says it. */
export interface StoryDone {
  pullRequest?: PullRequest;
  merged: boolean;
  summary: string;
  notes: string[];
}
export const terminalStates = [
  "completed",
  "failed",
  "cancelled",
  "interrupted",
];
export function isTerminal(state: string): boolean {
  return terminalStates.includes(state);
}
export interface InputRequest {
  id: string;
  executionId: string;
  stepId: string;
  a2aTaskId?: string;
  agent: string;
  askedBy: string;
  question: string;
  options?: string[];
  context?: string;
  state: string;
  answer?: string;
  createdAt: string;
  resolvedAt?: string;
  /** A run paused at a review gate: it is approved, or a reply requests
   * changes from reviewTarget. */
  review?: boolean;
  /** The step a reply sends changes to; empty when the gate can only be
   * approved. */
  reviewTarget?: string;
  /** A run paused because its checks failed in a way the writer cannot
   * fix; it is answered with one of its options only. */
  checkPause?: boolean;
  /** What a permission request asks to be allowed, one entry each. */
  permissions?: PermissionAsk[];
  /** Asked by a human-input stage: its thread links what the stage follows,
   * as a review gate's does. */
  stage?: boolean;
  runTitle?: string;
  /** Pending, but the run that asked is gone, so it cannot be answered. */
  expired?: boolean;
  /** What the user answered when the same step asked this before, as a
   * human-input stage asks again in a later round. */
  previousAnswer?: string;
}
/** One thing a permission request asks to be allowed: a command line, or a
 * tool with what the agent wanted it for. */
export interface PermissionAsk {
  command?: string;
  tool?: string;
  detail?: string;
}
/**
 * What the review bar offers on a run (RunReview): Approve at a review gate,
 * and Request changes for the stages Go would send changes to. refusal is
 * Go's reason changes cannot be sent now.
 */
export interface RunReview {
  /** The round being reviewed, which a request names. */
  round: number;
  approve: boolean;
  targets: ReviewTarget[];
  /** The target the bar picks first. */
  target?: string;
  refusal?: string;
  /** Draft line comments a request for changes sends with its items. */
  drafts?: number;
  /** The tracker changes the steps before the paused gates asked for, which Approve makes. */
  trackerChanges?: TrackerChange[];
}
export interface ReviewTarget {
  id: string;
  title: string;
  agent: string;
}
/** What a person sends a run back with: an item per thing to fix. */
export interface FeedbackRequest {
  round: number;
  items: Array<{ target: string; text: string }>;
}
/**
 * What resuming a stopped run would do now (RunResume): the steps it runs
 * again and those it keeps, and what is uncommitted in its workspace, or
 * Go's reason it cannot be resumed.
 */
export interface RunResume {
  /** The round the run stopped in, which a resume names. */
  round: number;
  /** The run failed, was cancelled, or was interrupted: one Resume is for. */
  stopped: boolean;
  refusal?: string;
  rerun: ResumeStep[];
  kept: ResumeStep[];
  /** The worktree, or checkout, the run works in; empty before it made one. */
  workspace?: string;
  changes: PendingChange[];
}
/** A step as a stopped run left it. */
export interface ResumeStep {
  id: string;
  title: string;
  state: string;
}
/** A path changed since the workspace's last commit, with git's status. */
export interface PendingChange {
  status: string;
  path: string;
}
/** How a person resumes a run, and what to do with what is uncommitted. */
export interface ResumeRequest {
  round: number;
  discard: boolean;
  /** The paths the person saw, which a discard must still match. */
  changes: string[];
}
/** One round of a run as SQLite keeps it (RunRounds). */
export interface RunRound {
  round: number;
  /** The commit the round built on; empty for round 1. */
  since?: string;
  startedAt?: string;
  finishedAt?: string;
  /** Every step as the round left it; a step's round says which round's
   * run it shows. */
  steps: StepRun[];
  events: RunEvent[];
  /** Earlier events of the round left out. */
  hiddenEvents?: number;
  /** What the run was sent back into this round with, and the replies. */
  feedback: FeedbackItem[];
  /** A person started the round by resuming the run after it stopped. */
  resumed?: boolean;
  /** They discarded what the stopped steps left uncommitted. */
  discarded?: boolean;
  /** The steps whose plan they edited for the round, which it ran again. */
  edited?: string[];
}
/** Why a stopped run did not finish, and the plan changes Go suggests (RunFailure). */
export interface RunFailure {
  round: number;
  /** The run failed, was cancelled, or was interrupted, so it can be rerun, even when no step says why. */
  stopped: boolean;
  /** The run's own error, when it recorded one. */
  error?: string;
  steps: FailedStep[];
  suggestions: PlanSuggestion[];
}
/** A step of the round the run stopped in that did not finish, or was refused something. */
export interface FailedStep {
  stepId: string;
  title: string;
  state: string;
  error?: string;
  denials: Denial[];
  checks: FailedCheck[];
}
/** A tool call a step was refused, as its runtime reported it. */
export interface Denial {
  tool: string;
  command?: string;
  detail?: string;
  /** It would have changed files on a read-only step. */
  write?: boolean;
  /** The person could have allowed it for the run, and did not. */
  grantable?: boolean;
}
/** An evaluation command that failed. */
export interface FailedCheck {
  checkId: string;
  argv: string[];
  exitCode?: number;
  error?: string;
}
/** A change to a step's access Go suggests: a command to add, or a permission to give. */
export interface PlanSuggestion {
  id: string;
  stepId: string;
  text: string;
  command?: string[];
  permission?: string;
}
/** What the approval screen starts a rerun from (PlanRerun). */
export interface RerunPlan {
  runId: string;
  plan: WorkflowPlan;
  resume: RunResume;
}
/** A rerun's plan as Go rebuilt it, the steps the person edited, and what resuming under it would do. */
export interface RerunRevision {
  workflow: WorkflowPlan["workflow"];
  edited: string[];
  resume: RunResume;
}
/**
 * A rerun's access changes made to the rule its run was planned from
 * (RerunRuleEdits): the rules document with them made, unsaved, and each
 * change, or why it was left out.
 */
export interface RuleEdits {
  projectId: string;
  ruleName: string;
  /** Where the rule is in the document's rules, as Go found it; absent for the fallback flow. */
  ruleIndex?: number;
  rules: RulesInfo;
  changes: RuleEdit[];
  leftOut: RuleEdit[];
}
/** One change to a stage's access, by the step it came from: what it is, or why it was left out. */
export interface RuleEdit {
  stepId: string;
  field: "permission" | "commands" | "allowedPaths";
  text: string;
}
/** How a person reruns a stopped run (RerunWorkflowRun). */
export interface RerunRequest {
  round: number;
  /** The approved plan, as JSON. */
  workflow: string;
  startOver: boolean;
  discard: boolean;
  changes: string[];
}
export interface AgentDefinition {
  name: string;
  description: string;
  runtime?: Runtime;
  model?: string;
  reasoningEffort?: string;
  permission: string;
  skills?: string[];
  taskKinds?: TaskKind[];
  tags?: string[];
  maxTurns?: number;
  secrets?: string[];
  instructions: string;
  source: string;
  path?: string;
  /** Declared MCP servers this agent's steps are given. */
  mcp?: string[];
  /** Kinds of tracker change this agent's steps may ask for. */
  tracker?: TrackerKind[];
  /** The file's frontmatter keys Inkuna does not read, as YAML; carried back
   * unchanged on save so another tool's keys survive an edit here. */
  extra?: string;
}
export interface AgentCard {
  name: string;
  description: string;
  url: string;
  version: string;
  protocolVersion: string;
  capabilities: Record<string, boolean>;
  defaultInputModes: string[];
  defaultOutputModes: string[];
  skills: Array<{ id: string; name: string; description: string }>;
  metadata?: Record<string, unknown>;
}
export interface AgentInfo {
  definition: AgentDefinition;
  card: AgentCard;
  /** The lower layer this definition shadows: "builtin" or "global". */
  overrides?: string;
}
/** What Inkuna remembers about a remote agent's reviewed Agent Card. */
export interface RemotePin {
  digest: string;
  url: string;
  protocolVersion: string;
  streaming?: boolean;
  /** "bearer", "header:<name>", or absent when the card asks for nothing. */
  auth?: string;
  fetchedAt?: string;
}
/** An external A2A agent connected as a read-only advisor. */
export interface RemoteAgent {
  name: string;
  cardUrl: string;
  enabled: boolean;
  secret?: string;
  auto?: boolean;
  sendArtifacts?: boolean;
  maxArtifactBytes?: number;
  /** Only ever set by hand in inkuna.yaml, for an agent under development. */
  allowInsecure?: boolean;
  description?: string;
  skills?: string[];
  pinned?: RemotePin;
  secretStatus?: SecretStatus;
  /** A local agent has the same name, so this one is never used. */
  shadowed?: boolean;
}
export interface RemoteCard extends AgentCard {
  provider?: { organization: string; url?: string };
  digest: string;
}
export interface CardChange {
  field: string;
  before: string;
  after: string;
}
/** A fetched card awaiting review. Producing one saves and sends nothing. */
export interface RemoteAgentPreview {
  card: RemoteCard;
  pin: RemotePin;
  suggestedName: string;
  samplePayload: string;
  samplePayloadWithArtifacts: string;
  changed: boolean;
  changes: CardChange[];
}
/** digest names the card the user reviewed; empty keeps the current pin. */
export interface RemoteAgentRequest {
  name: string;
  cardUrl: string;
  secret: string;
  auto: boolean;
  sendArtifacts: boolean;
  maxArtifactBytes: number;
  enabled: boolean;
  digest: string;
}
/** Where a definition file is written. */
export type AgentScope = "repository" | "global";
export interface AgentLocations {
  repository?: string;
  global?: string;
}
export interface RuleStage {
  /** Names the shared stage or group this entry stands for. Its id (the
   * name when empty) and dependsOn wire it in; the stage fields it sets
   * override a shared stage's for this use. Go expands it before planning. */
  use?: string;
  /** Empty on a use that keeps its shared stage's or group's name. */
  id: string;
  title?: string;
  kind: string;
  agent?: string;
  runtime?: Runtime;
  permission?: string;
  dependsOn?: string[];
  skills?: string[];
  allowedPaths?: string[];
  prompt?: string;
  commands?: string[][];
  optional?: boolean;
  /** Evaluation stages only: run the remaining commands after one fails. */
  continueOnFailure?: boolean;
  /** Approval stages only: pause for a person to approve or request changes. */
  review?: boolean;
  /** Evaluation stages only: "feedback" sends the failures back. */
  onFailure?: "fail" | "feedback";
  /** Where an approval or evaluation stage's feedback goes, how many
   * automatic rounds an evaluation stage's failures may start, and the exit
   * codes that pause the run for a person instead. */
  feedback?: {
    target?: string;
    maxRounds?: number;
    systemExitCodes?: number[];
  };
  /** Agent stages only: declared MCP servers the step is given. */
  mcp?: string[];
  /** Agent stages only: kinds of tracker change the step may ask for. */
  tracker?: TrackerKind[];
  /** On a use: the stage fields it sets, even to false or empty. */
  overrides?: string[];
  /** On an expanded stage: where it came from. */
  from?: RuleProvenance;
}
export interface RuleProvenance {
  stage?: string;
  groups?: string[];
  scope: string;
  /** The global library file the definition is in. */
  file?: string;
  overrides?: string[];
}
export interface RuleGroup {
  stages: RuleStage[];
}
export interface RuleCondition {
  kind?: TaskKind[];
  labels?: string[];
  source?: string[];
  keywords?: string[];
  repositories?: string[];
  complexity?: { min: number; max: number };
  minPriority?: number;
}
export interface Rule {
  name: string;
  description?: string;
  when: RuleCondition;
  then: { stages: RuleStage[] };
}
export interface RulesDocument {
  version: number;
  /** Shared stage definitions, by name, that flows and groups use. */
  stages?: Record<string, RuleStage>;
  /** Named runs of stages a flow uses as one entry. */
  groups?: Record<string, RuleGroup>;
  rules: Rule[];
  default?: { stages: RuleStage[] };
  source: string;
}
export interface RulesInfo {
  document: RulesDocument;
  exists: boolean;
  path: string;
}
/** Where a shared stage or group is defined, lowest precedence first. */
export type DefinitionScope = "builtin" | "global" | "repository";
/** A shared stage or group the edited rules can use, as Go lists it. */
export interface LibraryDefinition {
  name: string;
  scope: DefinitionScope;
  /** The library or rules file that defines it; empty for a built-in one. */
  file?: string;
  kind: "stage" | "group";
  stage?: RuleStage;
  group?: RuleGroup;
  /** A higher scope defines the same name, so no flow here uses this one. */
  overridden?: boolean;
  /** Two global files define the name, so no use resolves it. */
  ambiguous?: boolean;
  /** Go's reason it cannot be edited here; empty when it can. */
  readOnly?: string;
  /** The flows of the edited rules that use it: rule names, or "default". */
  usedBy?: string[] | null;
}
/** A stage field a use of a shared stage may override, and the stage kinds
 * it applies to (every kind when none are listed). */
export interface OverridableField {
  name: string;
  kinds?: string[] | null;
}
/** What an item of the catalog is. */
export type CatalogKind = "agent" | "skill" | "rule" | "stage" | "group";

/** One agent, skill, rule, shared stage, or group the catalog offers to add. */
export interface CatalogItem {
  name: string;
  kind: CatalogKind;
  description: string;
  /** What it relies on, directly or through what it uses, built-ins included. */
  needs: {
    agents?: string[] | null;
    stages?: string[] | null;
    skills?: string[] | null;
  };
  /** An agent's permission. */
  permission?: string;
  /** The kinds of task an agent is for, or a rule's condition names. */
  taskKinds?: string[] | null;
  path: string;
  content: string;
  /** A skill's other files, by their path in its directory. */
  files?: string[] | null;
  /** The definition source that offers it; absent for Inkuna's own catalog. */
  source?: string;
  /** Who the source shares it with, as the source names them. */
  scope?: string;
  version?: string;
}

/** What this build does with one definition a source shares. */
export interface SourceDefinitionReport {
  kind: string;
  name: string;
  mode: string;
  scope?: string;
  version?: string;
  status: "offered" | "fails" | "not applied" | "refuses";
  /** Go's reason, for any definition that is not offered. */
  reason?: string;
}

/** One definition source's snapshot for the project, as Go read it. */
export interface SourceReport {
  source: string;
  version?: string;
  fetchedAt?: string;
  /** The source could not be reached, so its last snapshot is shown. */
  cached?: boolean;
  unreachable?: string;
  definitions: SourceDefinitionReport[] | null;
}

/** A catalog item with where the project and this machine already have it. */
export interface CatalogEntry {
  item: CatalogItem;
  repository?: string;
  global?: string;
  /** Go's reason the item cannot be added globally, such as a rule. */
  globalRefusal?: string;
}

export interface CatalogListing {
  items: CatalogEntry[] | null;
  /** Why what the project has could not be read; adding to it is refused. */
  repositoryProblem?: string;
  globalProblem?: string;
  /** The project's definition sources, when the build has any. */
  sources?: SourceReport[] | null;
}

/** What an add from the catalog did, or what its preview says it would. */
export interface CatalogResult {
  item: string;
  /** The item first, then each agent, shared stage, and group it brings. */
  added: Array<{ kind: CatalogKind; name: string; path: string }> | null;
  notes?: string[] | null;
}

export interface StageLibrary {
  definitions: LibraryDefinition[] | null;
  problems: LibraryProblem[] | null;
  overridable: OverridableField[] | null;
}
/** Where Go found a mistake in edited rules; message is empty when valid. */
export interface RulesProblem {
  message: string;
  /** The flow entry: rule is -1 for the default flow, entry -1 for the
   * flow as a whole. */
  flow?: { rule: number; entry: number };
  /** The repository's own shared stage or group the mistake is in. */
  definition?: string;
}
/** A registered repository whose saved rules use a global definition. */
export interface DefinitionUser {
  project: string;
  repository: string;
  flows?: string[] | null;
  /** Why its rules could not be read; they may use the definition. */
  problem?: string;
}
/** A sort of tracker change agents may ask for. */
export type TrackerKind = "status" | "comment" | "edit" | "create";
/** One of the updates Inkuna makes to a run's story on its own. */
export type TrackerUpdate = "start" | "result" | "done";
/**
 * Lowers, on this machine, what a repository's .inkuna/tracker.yaml lets
 * Inkuna write; it can switch things off, never on. An absent writes list
 * keeps the repository's kinds, an empty one allows none, and an update is
 * off only when false.
 */
export interface TrackerOverride {
  writes?: TrackerKind[] | null;
  updates?: Partial<Record<TrackerUpdate, boolean | null>>;
}
export interface RepositoryConfig {
  name: string;
  path: string;
  defaultRuntime: Runtime | "";
  rules?: string;
  docs?: string;
  tracker?: TrackerOverride | null;
}
/** What Inkuna may write: kinds agents ask for and its own updates. */
export interface TrackerAllowance {
  writes: TrackerKind[] | null;
  updates: Record<TrackerUpdate, boolean>;
}
/** One declared repository's tracker settings, as Go resolved them. */
export interface RepositoryTracker {
  path: string;
  name: string;
  tracker?: string;
  file: string;
  exists: boolean;
  project?: string;
  ceiling: TrackerAllowance;
  effective: TrackerAllowance;
  note?: string;
  error?: string;
}
export interface SecretRef {
  name: string;
  description?: string;
  required?: boolean;
}
export interface ConfigFile {
  version: number;
  dataDir?: string;
  orchestrator: {
    runtime: Runtime | "";
    model?: string;
    reasoningEffort?: string;
    maxTurns?: number;
    readTools?: boolean;
    disabled?: boolean;
  };
  runtimes: Record<Runtime, RuntimeConfig>;
  concurrency: { maxTasks?: number; maxWorkers?: number };
  timeouts: Record<string, string | undefined>;
  notifications: { desktop: boolean };
  secrets: SecretRef[];
  repositories: RepositoryConfig[];
  agents: { directories?: string[] };
  /** Global stage library directories every repository's rules can use. */
  stages: { directories?: string[] };
  linear: { endpoint?: string };
  github: { host?: string; protocol?: string; binary?: string };
  /** Bands replace the built-in ones for the runtimes they name. */
  routing: { bands?: Partial<Record<Runtime, RoutingBand[]>> };
  /** Byte budgets for a worker's context envelope; unset ones take Go's defaults. */
  budgets?: Partial<Record<string, number>>;
  /** Model prices that add to or replace the built-in table. */
  pricing?: PricingOverrides;
  /** MCP servers this machine offers workers; stages and agents grant them by name. */
  mcpServers?: MCPServerConfig[];
}
/** An MCP server inkuna.yaml declares. Env and bearer name declared secrets, never values. */
export interface MCPServerConfig {
  name: string;
  description?: string;
  command?: string[];
  url?: string;
  env?: Record<string, string>;
  bearer?: string;
  tools?: string[];
  tracker?: string;
}
/** An MCP server as a step is granted it: no secrets. Empty tools means all of them. */
export interface MCPServer {
  name: string;
  description?: string;
  command?: string[];
  url?: string;
  tools?: string[];
  tracker?: string;
}
/** What a CLI says of one of the user's own MCP servers. */
export type UserServerStatus =
  | "ready"
  | "unknown"
  | "needs-sign-in"
  | "failed"
  | "missing"
  | "disabled";
/**
 * One of the user's own MCP servers as their CLI lists it, never its
 * configuration's secrets.
 */
export interface UserMCPServer {
  name: string;
  /** Where the CLI found it: its configuration, a claude.ai connector, a plugin. */
  source?: string;
  transport?: string;
  url?: string;
  command?: string;
  status: UserServerStatus | string;
  detail?: string;
  /** The CLI can sign in to it itself. */
  signIn?: boolean;
}
/**
 * A model's prices in US dollars per million tokens, as inkuna.yaml gives
 * them. Input and output are required; Go fills a cache rate left out.
 */
export interface PricingRates {
  input?: number;
  cacheWrite?: number;
  longCacheWrite?: number;
  cacheRead?: number;
  output?: number;
}
/** inkuna.yaml's pricing block. */
export interface PricingOverrides {
  models?: Record<string, PricingRates>;
  /** US dollars per Copilot premium request. */
  premiumRequest?: number;
}
/** A model's resolved prices, with the page they were read from and when. */
export interface ModelPrice {
  provider?: string;
  input: number;
  cacheWrite: number;
  longCacheWrite: number;
  cacheRead: number;
  output: number;
  source: string;
  asOf?: string;
  note?: string;
}
/** The pricing table; its version names the built-in prices. */
export interface PricingTable {
  version: number;
  models: Record<string, ModelPrice>;
  premiumRequest?: number;
}
export interface SecretStatus {
  name: string;
  source: string;
  available: boolean;
  detail?: string;
}
/** An inkuna.yaml field an environment variable currently wins over. */
export interface ConfigOverride {
  field: string;
  variable: string;
  value: string;
}
export interface ConfigurationInfo {
  path: string;
  exists: boolean;
  dataDir: string;
  file: ConfigFile;
  secrets: SecretStatus[];
  /** What an omitted field resolves to; used for placeholders. */
  defaults?: ConfigFile;
  overrides?: ConfigOverride[];
  /** The built-in bands a runtime without its own keeps using. */
  routingDefaults?: RoutingPolicy;
  /** The built-in prices inkuna.yaml's add to or replace. */
  pricingDefaults?: PricingTable;
  /** Set by a save that changed the data directory the next launch uses. */
  restartRequired?: boolean;
  /** Each saved repository's tracker ceiling and what this machine leaves. */
  trackers?: RepositoryTracker[] | null;
  /** The stage library directories Go reads, after the environment and the default. */
  stageDirectories?: string[] | null;
  /** Mistakes in the global stage library files; they never block a save. */
  libraryProblems?: LibraryProblem[] | null;
  /** The version and commit this app was built from; "dev" when unstamped. */
  build?: BuildInfo;
}
/** What this build is and what its extensions add, from Go's Capabilities. */
export interface CapabilitiesInfo {
  product: ProductInfo;
  /** Only what extensions add; Inkuna's own screens are never gated. */
  capabilities: Capability[] | null;
}
/** The app's name and version; inkuna is set when it is Inkuna's own. */
export interface ProductInfo {
  name: string;
  version: string;
  inkuna: boolean;
}
/** A feature an extension registered, named by the extension. */
export interface Capability {
  id: string;
  label: string;
  extension: string;
}
/** Which source a build came from, as `inkunactl version --json` prints it. */
export interface BuildInfo {
  version: string;
  commit: string;
}
/** A mistake Go found in a global stage library file. */
export interface LibraryProblem {
  file: string;
  message: string;
}
/** A validation problem, keyed by the dotted path of the control it is about. */
export interface ConfigFieldError {
  field: string;
  message: string;
}
export const emptyConfigFile: ConfigFile = {
  version: 1,
  orchestrator: { runtime: "claude" },
  runtimes: {
    codex: { binary: "codex" },
    claude: { binary: "claude" },
    copilot: { binary: "copilot" },
  },
  concurrency: { maxTasks: 3, maxWorkers: 2 },
  timeouts: {},
  notifications: { desktop: true },
  secrets: [],
  repositories: [],
  agents: {},
  stages: {},
  linear: {},
  github: {},
  routing: {},
};
export interface ContextEnvelope {
  version: number;
  taskId: string;
  stepId: string;
  runtime: Runtime;
  agent?: string;
  agentInstructions?: string;
  humanResponses?: Array<{ question: string; answer: string }>;
  peerMessages?: Array<{
    from: string;
    to: string;
    subject?: string;
    content: string;
  }>;
  objective: string;
  acceptanceCriteria: string[];
  /** The step's stage in the repository's rules; a peer has neither. */
  stageTitle?: string;
  stagePrompt?: string;
  /** The limits the user set on the whole task. */
  constraints?: string[];
  /** The repository's doc index as delivered: frontmatter only, never bodies. */
  docs?: DocBrief[];
  /** The MCP servers the step was given, as declared when it started. */
  mcpServers?: MCPServer[];
  /** The user's own MCP servers its runtime opts in to, as the CLI said of each. */
  userMcpServers?: UserMCPServer[];
  /** What the step may ask of its repository's tracker, when anything. */
  tracker?: { tracker: string; kinds: TrackerKind[]; story?: string };
  metadata?: Record<string, string>;
  [key: string]: unknown;
}
/** One doc in an envelope's index; the path is relative to the repository. */
export interface DocBrief {
  path: string;
  title?: string;
  tags?: string[];
  status?: string;
  lastUpdated?: string;
  author?: string;
  problems?: string[];
}
/** One doc in a project's index; the path is relative to the docs root. */
export interface DocEntry {
  path: string;
  title: string;
  lastUpdated: string;
  author: string;
  tags: string[];
  status: string;
  /** Go's reasons the doc breaks the contract or has broken links. */
  problems?: string[];
  links?: DocLink[];
}
/** A link a doc makes: to a doc under the docs root, another file of the repository, or a web URL. */
export interface DocLink {
  kind: "doc" | "file" | "url";
  path?: string;
  url?: string;
}
export type DocGraphScope = "project" | "all";
/** A doc, a registered project's repository, or a GitHub repository that is not registered. */
export interface DocGraphNode {
  id: string;
  kind: "doc" | "project" | "external";
  label: string;
  project?: string;
  path?: string;
  status?: string;
  problems?: number;
  url?: string;
}
/** Every link from one doc to one target, counted. */
export interface DocGraphEdge {
  from: string;
  to: string;
  count: number;
}
export interface DocGraph {
  nodes: DocGraphNode[];
  edges: DocGraphEdge[];
  /** Registered projects whose docs could not be indexed and are left out. */
  warnings?: string[];
}
/** A docs folder as Go arranges it, folders and docs each sorted by name. */
export interface DocFolder {
  name: string;
  path: string;
  folders: DocFolder[];
  docs: DocEntry[];
}
export interface DocsInfo {
  /** The resolved docs root on disk. */
  path: string;
  tree: DocFolder;
}
/** One doc to render: its index entry and the Markdown after the frontmatter. */
export interface DocContent {
  doc: DocEntry;
  markdown: string;
}
export interface Handoff {
  version: number;
  status: string;
  summary: string;
  changes?: Array<{ path: string; summary: string }>;
  verification?: string[];
  decisions?: string[];
  risks?: string[];
  artifacts?: Array<{
    digest: string;
    mediaType: string;
    size: number;
    name?: string;
  }>;
  /** What the turn that produced this handoff consumed. */
  usage?: Usage;
  /** A round's target's reply on each feedback item it was sent. */
  addressed?: Array<{ item: string; status: string; note?: string }>;
}
/**
 * What a model call, or a sum of calls, consumed. Input is split three
 * ways that never overlap: read fresh, written to the prompt cache, and
 * read from it.
 */
export interface Usage {
  inputTokens?: number;
  cacheWriteTokens?: number;
  /** The part of cacheWriteTokens written to the one-hour cache. */
  longCacheWriteTokens?: number;
  cacheReadTokens?: number;
  outputTokens?: number;
  /** The cost the runtime itself reported, in cents; absent when it reported none. */
  reportedCents?: number;
  /** Copilot premium requests: its requests weighted by each model's multiplier. */
  premiumRequests?: number;
  turns?: number;
}
/** Where a call's cost came from; figures of different sources are never summed as one. */
export type CostSource = "reported" | "estimated" | "unknown";
/**
 * What a call cost, in cents, and where the figure came from. estimatedCents
 * is the pricing table's figure, kept beside a reported cost.
 */
export interface Cost {
  cents: number;
  source: CostSource | string;
  estimatedCents?: number;
  pricingVersion?: number;
}
/** Calls' costs summed by source; calls nothing priced are counted, never valued. */
export interface CostTotal {
  reportedCents: number;
  estimatedCents: number;
  unknownCalls: number;
}
/** The size of one part of a prompt as it was sent; name tells skills apart. */
export interface PromptSection {
  section: string;
  name?: string;
  bytes: number;
  /** A budget cut the part this section sends. */
  truncated?: boolean;
}
export type CallKind = "worker" | "peer" | "orchestrator";
/**
 * One model call: a worker's turn, a peer's answer, or an orchestrator call
 * made while planning, which has no executionId. A call still running has
 * the outcome "running" and no usage yet.
 */
export interface UsageRecord {
  id: number;
  taskId: string;
  executionId?: string;
  stepId: string;
  callId: string;
  turn: number;
  /** The feedback round of the step run that made the call; absent outside one. */
  round?: number;
  kind: CallKind | string;
  agent: string;
  runtime: Runtime;
  /** The model Inkuna asked for; absent when the runtime chose. */
  model?: string;
  /** The model the runtime's stream says it used. */
  modelUsed?: string;
  /** How the runtime was signed in, where its stream says. */
  authMode?: "subscription" | "api-key" | string;
  usage: Usage;
  /** Absent on a call still running. */
  cost?: Cost;
  promptBytes: number;
  sections?: PromptSection[];
  /**
   * The artifact holding a worker or peer call's prompt as its runtime
   * received it; absent for orchestrator calls and for calls made before
   * Inkuna kept prompts.
   */
  promptDigest?: string;
  outcome: string;
  startedAt: string;
  finishedAt: string;
}
/** Every call a run paid for, planning first, and Go's sum of them. */
export interface RunUsage {
  calls: UsageRecord[];
  total: Usage;
  cost: CostTotal;
}
/**
 * What a group of calls consumed, cost, and took, as Go summed it. bestCents
 * is each call's reported cost, else its estimate; with unknown calls it is a
 * floor. subscriptionCents is the reported part a subscription login
 * reported: API-equivalent, not a bill. durationMs adds up each call's time.
 */
export interface CallTotals {
  usage: Usage;
  cost: CostTotal;
  bestCents: number;
  subscriptionCents: number;
  calls: number;
  durationMs: number;
}
/** One step of a run: its turns and its peers' answers, in the order they ended. */
export interface StepCost {
  stepId: string;
  agent: string;
  runtime: Runtime;
  /** The model that priced the step's latest turn; empty when the runtime chose and did not say. */
  model: string;
  startedAt: string;
  total: CallTotals;
  calls: UsageRecord[];
}
/** One run of a task and its steps, in the order they started. */
export interface RunCost {
  executionId: string;
  state: string;
  startedAt: string;
  finishedAt: string;
  total: CallTotals;
  steps: StepCost[];
}
/** Calls that share an agent, runtime, model, or step id; most expensive first. */
export interface CostGroup {
  key: string;
  total: CallTotals;
}
/**
 * What a task cost and where: planning, each run with its steps and calls,
 * and the same calls grouped. Agent, runtime, and model groups include
 * planning; step groups leave it out.
 */
export interface TaskCost {
  taskId: string;
  total: CallTotals;
  planning: { total: CallTotals; calls: UsageRecord[] };
  runs: RunCost[];
  byAgent: CostGroup[];
  byRuntime: CostGroup[];
  byModel: CostGroup[];
  byStep: CostGroup[];
}
/** One task's calls that ended in a period. */
export interface TaskCostSummary {
  taskId: string;
  /** The task's title, or one derived from its objective. */
  title?: string;
  projectId: string;
  objective: string;
  kind: string;
  state: string;
  /** The task's latest run, in the period or not. */
  latestRunId?: string;
  lastCallAt: string;
  total: CallTotals;
}
/** What tasks cost over a period, the most expensive first, and their sum. */
export interface PeriodCost {
  tasks: TaskCostSummary[];
  total: CallTotals;
}
export interface StepContext {
  stepId: string;
  agent: string;
  envelope: ContextEnvelope;
  handoff?: Handoff;
  /**
   * What a remote advisor was sent. A local worker's prompts are kept per
   * call, under each usage record's promptDigest.
   */
  prompt: string;
  /** The docs the step's agent opened, in order. */
  docReads?: StepDocRead[];
  /** The step's model calls, its turns and its peers' answers, in order. */
  usage?: UsageRecord[];
  /** What each round sent the step back with, oldest first, with its replies. */
  feedback?: FeedbackRound[];
}
/** The feedback one round sent a step, item by item. */
export interface FeedbackRound {
  round: number;
  items: FeedbackItem[];
}
/**
 * One thing a round sent a step back to fix. Status is open while the step
 * runs, then its reply (fixed, declined, or question, with a note) or
 * no-reply.
 */
export interface FeedbackItem {
  executionId: string;
  round: number;
  id: string;
  target: string;
  from: string;
  text: string;
  status: string;
  note?: string;
  createdAt: string;
  /** The failed check an item from a check reports. */
  checkId?: string;
  /** The end of that check's log, as the target was sent it. */
  tail?: string;
  /** The line comment an item was sent as, where its lines were, and the
   * excerpt of the diff around them the target was sent. */
  commentId?: string;
  path?: string;
  side?: DiffSide;
  line?: number;
  endLine?: number;
  commit?: string;
  excerpt?: string;
}
/** A side of a diff: the branch's lines, or lines it removed. */
export type DiffSide = "new" | "old";
/**
 * What a person wrote in a comment thread. A draft until sent with a
 * request for changes; then round and item name the feedback item it went
 * as, and status and note are the target's reply on it.
 */
export interface LineComment {
  id: string;
  threadId: string;
  body: string;
  target: string;
  state: "draft" | "sent";
  round?: number;
  item?: string;
  status?: string;
  note?: string;
  excerpt?: string;
  createdAt: string;
  updatedAt: string;
}
/**
 * Comments on lines of a run's branch changes. line and endLine are where
 * the lines are in the version the threads were placed on; an outdated
 * thread's lines changed since it was written, and an unplaced one was
 * written on a newer version than that.
 */
export interface CommentThread {
  id: string;
  path: string;
  side: DiffSide;
  line: number;
  endLine: number;
  round: number;
  state: "open" | "resolved";
  resolvedBy?: "human" | "agent";
  outdated?: boolean;
  outdatedRound?: number;
  unplaced?: boolean;
  comments: LineComment[];
}
/**
 * A run's threads placed on its changes as of commit to (RunComments), the
 * stages a comment can be for, and Go's reason no comment can be written
 * now.
 */
export interface RunComments {
  threads: CommentThread[];
  targets: ReviewTarget[];
  refusal?: string;
  to?: string;
}
/**
 * A comment to save: id edits a draft, threadId replies in a thread, and
 * neither starts a thread on lines of path as of commit.
 */
export interface CommentDraft {
  id?: string;
  threadId?: string;
  path?: string;
  side?: DiffSide;
  line?: number;
  endLine?: number;
  commit?: string;
  body: string;
  target: string;
}
/**
 * An agent opened a doc, or searched a directory of docs, during a task.
 * docPath is under the docs root, docsDir in the repository as the step had
 * it configured; a searched directory ends in "/", and the docs root itself
 * is "/".
 */
export interface DocRead {
  taskId: string;
  stepId: string;
  runId: string;
  projectId: string;
  agent: string;
  runtime: Runtime;
  docsDir: string;
  docPath: string;
  tool: string;
  openedAt: string;
}
/** A step's read, marked with whether its envelope's doc index listed the doc. */
export interface StepDocRead extends DocRead {
  delivered: boolean;
}
export interface ArtifactInfo {
  digest: string;
  taskId: string;
  executionId?: string;
  stepId?: string;
  agent?: string;
  kind: string;
  name: string;
  mediaType: string;
  size: number;
  description?: string;
  createdAt: string;
  content?: string;
  truncated?: boolean;
  binary?: boolean;
}
export interface A2APart {
  kind: string;
  text?: string;
  file?: { name?: string; mimeType?: string; uri?: string };
  data?: Record<string, unknown>;
  /** On a file part Inkuna stored: its kind, size, and round. */
  metadata?: Record<string, unknown>;
}
export interface A2AMessage {
  messageId: string;
  role: string;
  parts: A2APart[];
  timestamp: string;
}
export interface A2ATask {
  id: string;
  contextId: string;
  agent: string;
  status: { state: string; message?: A2AMessage; timestamp: string };
  history?: A2AMessage[];
  artifacts?: A2AArtifact[];
  /** A review gate's task carries review, executionId, and stepId. */
  metadata?: Record<string, unknown>;
}
/** A stored blob as a handoff refers to it. */
export interface ArtifactRef {
  digest: string;
  mediaType: string;
  size: number;
  name?: string;
  /** handoff, output, document, diff, archive, ... */
  kind?: string;
}
/** An agent step before a review gate, and what it left to read. */
export interface GateStep {
  stepId: string;
  title: string;
  agent: string;
  round: number;
  artifacts: ArtifactRef[];
}
export interface A2AArtifact {
  artifactId: string;
  name?: string;
  description?: string;
  parts: A2APart[];
  metadata?: Record<string, unknown>;
}
/** One agent conversation; an A2A task. Replying answers pendingInput. */
export interface Thread {
  taskId: string;
  contextId: string;
  agent: string;
  state: string;
  messageCount: number;
  updatedAt: string;
  runId?: string;
  runTitle?: string;
  preview?: string;
  pendingInput?: InputRequest;
}
export interface ThreadDetail extends Thread {
  stepId?: string;
  messages: A2AMessage[];
  artifacts: A2AArtifact[];
  inputs: InputRequest[];
  /** What a round sent the step back with, and its replies, when the thread
   * is a round's target's task. */
  feedback?: FeedbackItem[];
}
/** An A2A task of a run's conversation, with a round's feedback for it. */
export interface RunTask {
  task: A2ATask;
  feedback?: FeedbackItem[];
}
export interface LinearIssue {
  id: string;
  identifier: string;
  title: string;
  description: string;
  url: string;
  priority: number;
  dueDate?: string;
  team: string;
  state: string;
  project?: string;
  parent?: string;
  labels: string[];
  suggestedKind: TaskKind;
}

/** Whether a tracker's credential can write, and why. */
export interface WriteAccess {
  status: "can-write" | "partial" | "read-only" | "unknown";
  detail: string;
}

export interface ProviderAuthentication {
  provider: "github" | "claude" | "linear";
  authenticated: boolean;
  account?: string;
  detail: string;
  write?: WriteAccess | null;
}

export interface AuthenticationStatus {
  github: ProviderAuthentication;
  claude: ProviderAuthentication;
  linear: ProviderAuthentication;
}
