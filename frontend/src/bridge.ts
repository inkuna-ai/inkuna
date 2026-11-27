import {
  emptyConfigFile,
  emptyDefaults,
  type AgentDefinition,
  type AgentInfo,
  type AgentLocations,
  type AgentScope,
  type ArtifactInfo,
  type AuthenticationStatus,
  type BranchPlan,
  type BranchSyncInfo,
  type CapabilitiesInfo,
  type CatalogListing,
  type CatalogResult,
  type ProjectSyncInfo,
  type BranchCommit,
  type Check,
  type CheckLog as CheckLogInfo,
  type CommentDraft,
  type DiffChange,
  type DiffExcerpt,
  type DiffFilePatch,
  type DiffScope,
  type GateStep,
  type ConfigFieldError,
  type ConfigurationInfo,
  type DashboardData,
  type DocContent as DocContentInfo,
  type DocGraph as DocGraphInfo,
  type DocGraphScope,
  type DocRead,
  type DocsInfo,
  type FeedbackRequest,
  type InputRequest,
  type Thread,
  type ThreadDetail,
  type LinearIssue,
  type ProviderAuthentication,
  type Project,
  type ProjectRemoval,
  type ProjectRemovalPreview,
  type RepositoryCandidate,
  type RemoteAgent,
  type RemoteAgentPreview,
  type DeleteOptions,
  type DeleteReport,
  type KeptWorktree,
  type StorageUsage,
  type TaskDeletePreview,
  type RemoteAgentRequest,
  builtInRuntimes,
  type RulesDocument,
  type RuleEdits,
  type RuleGroup,
  type RuleStage,
  type RulesInfo,
  type RulesProblem,
  type DefinitionUser,
  type StageLibrary as StageLibraryInfo,
  type RunComments,
  type RerunPlan,
  type RerunRequest,
  type RerunRevision,
  type ResumeRequest,
  type RunFailure,
  type RunResume,
  type RunReview,
  type RunRound,
  type RunTask,
  type Runtime,
  type RuntimeConfig,
  type RuntimeInfo,
  type RuntimeTest,
  type UserMCPServer,
  type RunUsage as RunUsageInfo,
  type ShareMode,
  type PeriodCost,
  type PullRequestCheck,
  type StepContext,
  type StoryDone,
  type TaskCost as TaskCostInfo,
  type TrackerChange,
  type TaskSpec,
  type ViewedFile,
  type WorkflowPlan,
  type WorkflowRequest,
  type WorkflowRun,
  type WorkflowSpec,
} from "./types";
import {
  AddCatalogItem,
  AddProject,
  CatalogItems,
  InspectRepository,
  PreviewCatalogItem,
  PreviewRemoveProject,
  RemoveProject,
  AgentLocations as GetAgentLocations,
  Agents,
  AnswerInput,
  ApproveReview,
  AuthenticateClaude,
  AuthenticateGitHub,
  AuthenticateLinear,
  AuthenticationStatus as GetAuthenticationStatus,
  CancelWorkflowRun,
  Capabilities,
  CheckLog,
  CheckPullRequests,
  ChooseDirectory,
  CommentTarget,
  Configuration,
  Dashboard,
  DeleteAgent,
  DeleteBranch,
  DeleteComment,
  DeleteRemoteAgent,
  DeleteRun,
  DeleteRuntime,
  DeleteSecret,
  DeleteTasks,
  DocContent,
  DocGraph,
  DocReads,
  Docs,
  FetchProject,
  ForgetLinearAuthentication,
  GateArtifacts,
  ImportLinearIssue,
  LatestWorkflowRun,
  ParseAgent,
  PlanWorkflow,
  ProjectBranches,
  ProjectSync,
  ReadArtifact,
  RemoveKeptWorktree,
  RemoveRunWorktree,
  MarkStoryDone,
  RemoveSecretDeclaration,
  RetryTrackerChange,
  ResolveThread,
  Rules,
  RequestChanges,
  RulesFromYAML,
  RulesToYAML,
  StageLibrary,
  CheckRules,
  ExpandRules,
  GlobalDefinitionUsers,
  SaveGlobalDefinition,
  RunArtifacts,
  RunChanges,
  RunChecks,
  RunComments as RunCommentsOf,
  RunCommits,
  RunContext,
  RunFileExcerpt,
  RunFilePatch,
  RunMessages,
  ResumeWorkflowRun,
  PlanRerun as PlanRerunOf,
  RerunRuleEdits as RerunRuleEditsOf,
  RerunWorkflowRun as RerunWorkflowRunOf,
  ReviseRerun as ReviseRerunOf,
  RunFailure as RunFailureOf,
  RunResume as RunResumeOf,
  RunReview as RunReviewOf,
  RunRounds,
  RunTask as RunTaskOf,
  RunUsage,
  RunViewed,
  SaveCSV,
  SaveCheckLog,
  TaskCost,
  TaskCosts,
  PreviewDeleteRun,
  PreviewDeleteTasks,
  PreviewRemoteAgent,
  RemoteAgents,
  RepinRemoteAgent,
  ReviseWorkflow,
  Runtimes,
  SaveAgent,
  SaveComment,
  SaveConfiguration,
  SaveRemoteAgent,
  SaveRules,
  SaveRuntime,
  SaveSecret,
  SetFileViewed,
  SetPlanTitle,
  SetRemoteAgentEnabled,
  ShareSettings,
  StartWorkflow,
  StorageUsage as GetStorageUsage,
  TestRuntime,
  ListUserMCPServers,
  SignInUserMCPServer,
  Thread as GetThread,
  Threads,
  UpdateBranch,
  ValidateConfiguration,
  WorkflowRun as GetWorkflowRun,
  WorkflowRuns,
} from "../wailsjs/go/app/Service";
import { BrowserOpenURL, ClipboardSetText } from "../wailsjs/runtime/runtime";

declare global {
  interface Window {
    runtime?: unknown;
    go?: {
      app?: {
        Service?: {
          Dashboard(): Promise<DashboardData>;
          AddProject(
            name: string,
            repository: string,
            runtime: Runtime,
          ): Promise<Project>;
          InspectRepository?(folder: string): Promise<RepositoryCandidate>;
          PreviewRemoveProject?(
            projectID: string,
          ): Promise<ProjectRemovalPreview>;
          RemoveProject?(
            projectID: string,
            deleteHistory: boolean,
          ): Promise<ProjectRemoval>;
          ImportLinearIssue(reference: string): Promise<LinearIssue>;
          LatestWorkflowRun(): Promise<WorkflowRun>;
          AuthenticationStatus(): Promise<AuthenticationStatus>;
          AuthenticateClaude(): Promise<ProviderAuthentication>;
          AuthenticateGitHub(): Promise<ProviderAuthentication>;
          AuthenticateLinear(apiKey: string): Promise<ProviderAuthentication>;
          ForgetLinearAuthentication(): Promise<ProviderAuthentication>;
          PlanWorkflow(request: WorkflowRequest): Promise<WorkflowPlan>;
          StartWorkflow(workflowJSON: string): Promise<WorkflowRun>;
          ReviseWorkflow?(workflowJSON: string): Promise<WorkflowSpec>;
          WorkflowRun(runID: string): Promise<WorkflowRun>;
          CancelWorkflowRun(runID: string): Promise<WorkflowRun>;
          RemoveRunWorktree?(runID: string): Promise<WorkflowRun>;
          PreviewDeleteTasks?(taskIDs: string[]): Promise<TaskDeletePreview[]>;
          PreviewDeleteRun?(runID: string): Promise<TaskDeletePreview>;
          DeleteTasks?(
            taskIDs: string[],
            options: DeleteOptions,
          ): Promise<DeleteReport>;
          DeleteRun?(
            runID: string,
            options: DeleteOptions,
          ): Promise<DeleteReport>;
          StorageUsage?(): Promise<StorageUsage>;
          CheckPullRequests?(force: boolean): Promise<PullRequestCheck>;
          RemoveKeptWorktree?(path: string): Promise<void>;
          MarkStoryDone?(runID: string): Promise<StoryDone>;
          RetryTrackerChange?(
            runID: string,
            changeID: string,
          ): Promise<TrackerChange>;
          WorkflowRuns?(): Promise<WorkflowRun[]>;
          RunContext?(runID: string, stepID: string): Promise<StepContext>;
          RunCommits?(runID: string): Promise<BranchCommit[]>;
          RunChecks?(runID: string): Promise<Check[]>;
          CheckLog?(checkID: string, from: number): Promise<CheckLogInfo>;
          SaveCheckLog?(checkID: string): Promise<string>;
          RunChanges?(runID: string, scope: DiffScope): Promise<DiffChange>;
          RunFilePatch?(
            runID: string,
            scope: DiffScope,
            path: string,
            loadAnyway: boolean,
          ): Promise<DiffFilePatch>;
          RunFileExcerpt?(
            runID: string,
            scope: DiffScope,
            path: string,
            oldStart: number,
            newStart: number,
            count: number,
          ): Promise<DiffExcerpt>;
          RunViewed?(runID: string): Promise<ViewedFile[]>;
          SetFileViewed?(
            runID: string,
            path: string,
            blob: string,
            viewed: boolean,
          ): Promise<void>;
          RunArtifacts?(runID: string): Promise<ArtifactInfo[]>;
          ReadArtifact?(digest: string): Promise<ArtifactInfo>;
          RunMessages?(runID: string): Promise<RunTask[]>;
          GateArtifacts?(runID: string, gateID: string): Promise<GateStep[]>;
          RunRounds?(runID: string): Promise<RunRound[]>;
          RunReview?(runID: string): Promise<RunReview>;
          RunResume?(runID: string): Promise<RunResume>;
          ResumeWorkflowRun?(
            runID: string,
            request: ResumeRequest,
          ): Promise<WorkflowRun>;
          RunFailure?(runID: string): Promise<RunFailure>;
          PlanRerun?(runID: string): Promise<RerunPlan>;
          ReviseRerun?(
            runID: string,
            workflowJSON: string,
          ): Promise<RerunRevision>;
          RerunWorkflowRun?(
            runID: string,
            request: RerunRequest,
          ): Promise<WorkflowRun>;
          RerunRuleEdits?(
            runID: string,
            workflowJSON: string,
          ): Promise<RuleEdits>;
          RunTask?(runID: string): Promise<TaskSpec>;
          SetPlanTitle?(
            workflowID: string,
            title: string,
          ): Promise<{ task: TaskSpec; branching?: BranchPlan }>;
          RunComments?(runID: string, to: string): Promise<RunComments>;
          SaveComment?(runID: string, draft: CommentDraft): Promise<void>;
          DeleteComment?(runID: string, id: string): Promise<void>;
          ResolveThread?(
            runID: string,
            id: string,
            resolved: boolean,
          ): Promise<void>;
          CommentTarget?(
            runID: string,
            path: string,
            side: string,
            line: number,
          ): Promise<string>;
          RequestChanges?(
            runID: string,
            request: FeedbackRequest,
          ): Promise<WorkflowRun>;
          RunUsage?(runID: string): Promise<RunUsageInfo>;
          TaskCost?(taskID: string): Promise<TaskCostInfo>;
          TaskCosts?(
            projectID: string,
            from: string,
            to: string,
          ): Promise<PeriodCost>;
          SaveCSV?(defaultName: string, content: string): Promise<string>;
          Inbox?(): Promise<InputRequest[]>;
          Threads?(): Promise<Thread[]>;
          Thread?(taskID: string): Promise<ThreadDetail>;
          AnswerInput?(inputID: string, answer: string): Promise<InputRequest>;
          ApproveReview?(runID: string): Promise<WorkflowRun>;
          Agents?(projectID: string): Promise<AgentInfo[]>;
          AgentLocations?(projectID: string): Promise<AgentLocations>;
          ParseAgent?(content: string): Promise<AgentDefinition>;
          SaveAgent?(
            projectID: string,
            scope: string,
            originalName: string,
            definitionJSON: string,
          ): Promise<AgentInfo>;
          DeleteAgent?(
            projectID: string,
            scope: string,
            name: string,
          ): Promise<void>;
          RemoveSecretDeclaration?(name: string): Promise<ConfigurationInfo>;
          ChooseDirectory?(title: string): Promise<string>;
          Docs?(projectID: string): Promise<DocsInfo>;
          DocContent?(projectID: string, path: string): Promise<DocContentInfo>;
          DocGraph?(projectID: string, scope: string): Promise<DocGraphInfo>;
          DocReads?(projectID: string, path: string): Promise<DocRead[]>;
          ProjectBranches?(projectID: string): Promise<BranchSyncInfo>;
          FetchProject?(projectID: string): Promise<BranchSyncInfo>;
          DeleteBranch?(
            projectID: string,
            branch: string,
            commit: string,
          ): Promise<BranchSyncInfo>;
          ProjectSync?(
            projectID: string,
            fetchIfStale: boolean,
          ): Promise<ProjectSyncInfo>;
          UpdateBranch?(
            projectID: string,
            branch: string,
          ): Promise<BranchSyncInfo>;
          ShareSettings?(
            projectID: string,
            mode: string,
            digest: string,
          ): Promise<BranchSyncInfo>;
          Rules?(projectID: string): Promise<RulesInfo>;
          SaveRules?(
            projectID: string,
            documentJSON: string,
          ): Promise<RulesInfo>;
          RulesToYAML?(
            projectID: string,
            documentJSON: string,
          ): Promise<string>;
          RulesFromYAML?(
            projectID: string,
            content: string,
          ): Promise<RulesDocument>;
          StageLibrary?(
            projectID: string,
            documentJSON: string,
          ): Promise<StageLibraryInfo>;
          CheckRules?(
            projectID: string,
            documentJSON: string,
          ): Promise<RulesProblem>;
          ExpandRules?(
            projectID: string,
            documentJSON: string,
          ): Promise<RulesDocument>;
          CatalogItems?(projectID: string): Promise<CatalogListing>;
          PreviewCatalogItem?(
            projectID: string,
            name: string,
            source: string,
            global: boolean,
          ): Promise<CatalogResult>;
          AddCatalogItem?(
            projectID: string,
            name: string,
            source: string,
            global: boolean,
          ): Promise<CatalogResult>;
          GlobalDefinitionUsers?(
            file: string,
            name: string,
          ): Promise<DefinitionUser[]>;
          SaveGlobalDefinition?(
            file: string,
            name: string,
            definitionJSON: string,
          ): Promise<void>;
          Capabilities?(): Promise<CapabilitiesInfo>;
          Configuration?(): Promise<ConfigurationInfo>;
          SaveConfiguration?(fileJSON: string): Promise<ConfigurationInfo>;
          ValidateConfiguration?(fileJSON: string): Promise<ConfigFieldError[]>;
          Runtimes?(): Promise<RuntimeInfo[]>;
          SaveRuntime?(
            name: string,
            runtimeJSON: string,
          ): Promise<RuntimeInfo[]>;
          DeleteRuntime?(name: string): Promise<RuntimeInfo[]>;
          TestRuntime?(name: string): Promise<RuntimeTest>;
          ListUserMCPServers?(name: string): Promise<UserMCPServer[]>;
          SignInUserMCPServer?(runtime: string, server: string): Promise<void>;
          RemoteAgents?(): Promise<RemoteAgent[]>;
          PreviewRemoteAgent?(
            cardURL: string,
            existingName: string,
          ): Promise<RemoteAgentPreview>;
          SaveRemoteAgent?(requestJSON: string): Promise<RemoteAgent[]>;
          RepinRemoteAgent?(
            name: string,
            reviewedDigest: string,
          ): Promise<RemoteAgent[]>;
          SetRemoteAgentEnabled?(
            name: string,
            enabled: boolean,
          ): Promise<RemoteAgent[]>;
          DeleteRemoteAgent?(name: string): Promise<RemoteAgent[]>;
          SaveSecret?(name: string, value: string): Promise<ConfigurationInfo>;
          DeleteSecret?(name: string): Promise<ConfigurationInfo>;
        };
      };
    };
  }
}

const fallback: DashboardData = {
  projects: [],
  health: [],
  defaults: emptyDefaults,
};

function service() {
  return window.go?.app?.Service;
}

export async function getDashboard(): Promise<DashboardData> {
  if (service()?.Dashboard)
    return Dashboard() as unknown as Promise<DashboardData>;
  return fallback;
}

export async function addProject(
  name: string,
  repository: string,
  runtime: Runtime,
): Promise<Project> {
  if (service()?.AddProject)
    return AddProject(name, repository, runtime) as Promise<Project>;
  return {
    id: `preview-${Date.now()}`,
    name,
    repository,
    defaultRuntime: runtime,
    createdAt: new Date().toISOString(),
  };
}

const projectsUnavailable =
  "Connecting and removing projects is available in the Inkuna desktop app";

/** Asks Go what connecting a folder would connect, or why it cannot. */
export async function inspectRepository(
  folder: string,
): Promise<RepositoryCandidate> {
  if (!service()?.InspectRepository) throw new Error(projectsUnavailable);
  return InspectRepository(folder) as unknown as Promise<RepositoryCandidate>;
}

/** Asks Go whether a project can be removed now, and what it has. */
export async function previewRemoveProject(
  projectID: string,
): Promise<ProjectRemovalPreview> {
  if (!service()?.PreviewRemoveProject) throw new Error(projectsUnavailable);
  const preview = (await PreviewRemoveProject(
    projectID,
  )) as unknown as ProjectRemovalPreview;
  return { ...preview, worktrees: list(preview.worktrees) };
}

/** Removes a project, keeping its history unless deleteHistory says not to. */
export async function removeProject(
  projectID: string,
  deleteHistory: boolean,
): Promise<ProjectRemoval> {
  if (!service()?.RemoveProject) throw new Error(projectsUnavailable);
  return RemoveProject(
    projectID,
    deleteHistory,
  ) as unknown as Promise<ProjectRemoval>;
}

export async function importLinearIssue(
  reference: string,
): Promise<LinearIssue> {
  if (!service()?.ImportLinearIssue)
    throw new Error("Linear import is available in the Inkuna desktop app");
  return ImportLinearIssue(reference) as Promise<LinearIssue>;
}

const unavailableAuthentication: AuthenticationStatus = {
  github: {
    provider: "github",
    authenticated: false,
    detail: "Authentication is available in the Inkuna desktop app",
  },
  claude: {
    provider: "claude",
    authenticated: false,
    detail: "Authentication is available in the Inkuna desktop app",
  },
  linear: {
    provider: "linear",
    authenticated: false,
    detail: "Authentication is available in the Inkuna desktop app",
  },
};

export async function getAuthenticationStatus(): Promise<AuthenticationStatus> {
  if (!service()?.AuthenticationStatus) return unavailableAuthentication;
  return GetAuthenticationStatus() as Promise<AuthenticationStatus>;
}

export async function authenticateGitHub(): Promise<ProviderAuthentication> {
  if (!service()?.AuthenticateGitHub)
    throw new Error(
      "GitHub authentication is available in the Inkuna desktop app",
    );
  return AuthenticateGitHub() as Promise<ProviderAuthentication>;
}

export async function authenticateClaude(): Promise<ProviderAuthentication> {
  if (!service()?.AuthenticateClaude)
    throw new Error(
      "Claude Code authentication is available in the Inkuna desktop app",
    );
  return AuthenticateClaude() as Promise<ProviderAuthentication>;
}

export async function authenticateLinear(
  apiKey: string,
): Promise<ProviderAuthentication> {
  if (!service()?.AuthenticateLinear)
    throw new Error(
      "Linear authentication is available in the Inkuna desktop app",
    );
  return AuthenticateLinear(apiKey) as Promise<ProviderAuthentication>;
}

export async function forgetLinearAuthentication(): Promise<ProviderAuthentication> {
  if (!service()?.ForgetLinearAuthentication)
    throw new Error(
      "Linear authentication is available in the Inkuna desktop app",
    );
  return ForgetLinearAuthentication() as Promise<ProviderAuthentication>;
}

/**
 * Changes a planned task's title before approval; Go names the planned
 * branch again and returns both, or rejects with its reason.
 */
export async function setPlanTitle(
  workflowID: string,
  title: string,
): Promise<{ task: TaskSpec; branching?: BranchPlan }> {
  if (!service()?.SetPlanTitle)
    throw new Error("Workflow planning is available in the Inkuna desktop app");
  return SetPlanTitle(workflowID, title) as unknown as Promise<{
    task: TaskSpec;
    branching?: BranchPlan;
  }>;
}

/** The task a run carries out: its title and the details behind it. */
export async function getRunTask(runID: string): Promise<TaskSpec> {
  if (!service()?.RunTask)
    throw new Error("Task details are available in the Inkuna desktop app");
  return RunTaskOf(runID) as unknown as Promise<TaskSpec>;
}

export async function planWorkflow(
  request: WorkflowRequest,
): Promise<WorkflowPlan> {
  if (!service()?.PlanWorkflow)
    throw new Error("Workflow planning is available in the Inkuna desktop app");
  return normalizeWorkflowPlan(await PlanWorkflow(request));
}

export async function startWorkflow(
  workflowJSON: string,
): Promise<WorkflowRun> {
  if (!service()?.StartWorkflow)
    throw new Error(
      "Workflow execution is available in the Inkuna desktop app",
    );
  return normalizeWorkflowRun(await StartWorkflow(workflowJSON));
}

/**
 * Rebuilds a planned workflow around the approval screen's edits (agent,
 * runtime, and complexity per agent step). Go derives the rest, so what comes
 * back is exactly what StartWorkflow will accept.
 */
export async function reviseWorkflow(
  workflowJSON: string,
): Promise<WorkflowSpec> {
  if (!service()?.ReviseWorkflow)
    throw new Error("Workflow editing is available in the Inkuna desktop app");
  const spec = recordValue(await ReviseWorkflow(workflowJSON), "workflow");
  if (!Array.isArray(spec.steps))
    throw new Error("Inkuna returned a workflow without steps");
  return spec as unknown as WorkflowSpec;
}

export async function getWorkflowRun(runID: string): Promise<WorkflowRun> {
  if (!service()?.WorkflowRun)
    throw new Error("Runs are available in the Inkuna desktop app");
  return normalizeWorkflowRun(await GetWorkflowRun(runID));
}

export async function getLatestWorkflowRun(): Promise<WorkflowRun> {
  if (!service()?.LatestWorkflowRun)
    throw new Error("Runs are available in the Inkuna desktop app");
  return normalizeWorkflowRun(await LatestWorkflowRun());
}

export async function cancelWorkflowRun(runID: string): Promise<WorkflowRun> {
  if (!service()?.CancelWorkflowRun)
    throw new Error("Run cancellation is available in the Inkuna desktop app");
  return normalizeWorkflowRun(await CancelWorkflowRun(runID));
}

/**
 * Removes a finished run's worktree. Go checks the run is finished, the
 * worktree is Inkuna's and clean, and keeps the branch; its reason comes
 * back as the error.
 */
export async function removeRunWorktree(runID: string): Promise<WorkflowRun> {
  if (!service()?.RemoveRunWorktree)
    throw new Error("Worktree cleanup is available in the Inkuna desktop app");
  return normalizeWorkflowRun(await RemoveRunWorktree(runID));
}

/**
 * Asks Go whether an ended run's pull request merged, and when it did, to
 * attach it to the run's story and move the story to Done, as far as the
 * tracker has not already. Go's reason comes back as the error.
 */
export async function markStoryDone(runID: string): Promise<StoryDone> {
  if (!service()?.MarkStoryDone)
    throw new Error("Mark done is available in the Inkuna desktop app");
  const done = recordValue(await MarkStoryDone(runID), "Mark done result");
  return {
    ...(done as unknown as StoryDone),
    merged: done.merged === true,
    summary: String(done.summary ?? ""),
    notes: Array.isArray(done.notes) ? done.notes.map(String) : [],
  };
}

/**
 * Asks Go to try a failed tracker change again; it returns how the change
 * stands now, and Go's reason comes back as the error.
 */
export async function retryTrackerChange(
  runID: string,
  changeID: string,
): Promise<TrackerChange> {
  if (!service()?.RetryTrackerChange)
    throw new Error("Tracker changes are available in the Inkuna desktop app");
  return (await RetryTrackerChange(
    runID,
    changeID,
  )) as unknown as TrackerChange;
}

const historyUnavailable =
  "Deleting history is available in the Inkuna desktop app";

// Go writes an empty list as null.
const list = <T>(value: T[] | null | undefined): T[] => value ?? [];

function normalizePreview(preview: TaskDeletePreview): TaskDeletePreview {
  return { ...preview, runs: list(preview.runs) };
}

function normalizeReport(report: DeleteReport): DeleteReport {
  return {
    tasks: list(report?.tasks).map((task) => ({
      ...task,
      rows: list(task.rows),
      worktrees: list(task.worktrees),
      branches: list(task.branches),
      links: list(task.links),
      problems: list(task.problems),
    })),
  };
}

/**
 * Asks Go what deleting the tasks would remove and leave, and whether it
 * would refuse any of them now.
 */
export async function previewDeleteTasks(
  taskIDs: string[],
): Promise<TaskDeletePreview[]> {
  if (!service()?.PreviewDeleteTasks) throw new Error(historyUnavailable);
  const previews = (await PreviewDeleteTasks(
    taskIDs,
  )) as unknown as TaskDeletePreview[];
  return list(previews).map(normalizePreview);
}

/** Asks Go what deleting one run of a task, and no more, would remove. */
export async function previewDeleteRun(
  runID: string,
): Promise<TaskDeletePreview> {
  if (!service()?.PreviewDeleteRun) throw new Error(historyUnavailable);
  return normalizePreview(
    (await PreviewDeleteRun(runID)) as unknown as TaskDeletePreview,
  );
}

/**
 * Deletes finished tasks; Go refuses the whole call, deleting nothing, when
 * one cannot go, and its reason comes back as the error.
 */
export async function deleteTasks(
  taskIDs: string[],
  options: DeleteOptions,
): Promise<DeleteReport> {
  if (!service()?.DeleteTasks) throw new Error(historyUnavailable);
  return normalizeReport(
    (await DeleteTasks(taskIDs, options)) as unknown as DeleteReport,
  );
}

/** Deletes one finished run of a task that has others. */
export async function deleteRun(
  runID: string,
  options: DeleteOptions,
): Promise<DeleteReport> {
  if (!service()?.DeleteRun) throw new Error(historyUnavailable);
  return normalizeReport(
    (await DeleteRun(runID, options)) as unknown as DeleteReport,
  );
}

/** The disk space history takes, and the worktrees deletes kept. */
export async function getStorageUsage(): Promise<StorageUsage> {
  if (!service()?.StorageUsage)
    throw new Error("Disk usage is available in the Inkuna desktop app");
  const usage = (await GetStorageUsage()) as unknown as StorageUsage;
  return {
    ...usage,
    kept: list<KeptWorktree>(usage.kept),
    problems: list(usage.problems),
  };
}

/**
 * Removes a worktree a delete kept; Go removes only a clean one and keeps
 * its branch, and its reason comes back as the error.
 */
export async function removeKeptWorktree(path: string): Promise<void> {
  if (!service()?.RemoveKeptWorktree)
    throw new Error("Worktree cleanup is available in the Inkuna desktop app");
  await RemoveKeptWorktree(path);
}

/**
 * Asks Go to find the pull request each run branch was opened in. Go skips
 * a project it read a few minutes ago unless force is set, and under
 * `worktrees.cleanup: merged` removes merged runs' clean worktrees. Outside
 * the desktop app there is nothing to check.
 */
export async function checkPullRequests(
  force = false,
): Promise<PullRequestCheck> {
  if (!service()?.CheckPullRequests)
    return { checked: 0, linked: 0, removed: [], problems: [] };
  const check = (await CheckPullRequests(force)) as unknown as PullRequestCheck;
  return {
    ...check,
    removed: list<string>(check?.removed),
    problems: list<string>(check?.problems),
  };
}

export async function listWorkflowRuns(): Promise<WorkflowRun[]> {
  if (!service()?.WorkflowRuns) return [];
  const runs = (await WorkflowRuns()) as unknown;
  if (!Array.isArray(runs)) return [];
  return runs.map((run) => normalizeWorkflowRun(run));
}

export async function getRunContext(
  runID: string,
  stepID: string,
): Promise<StepContext> {
  if (!service()?.RunContext)
    throw new Error("Step context is available in the Inkuna desktop app");
  return RunContext(runID, stepID) as unknown as Promise<StepContext>;
}

// Go writes a time that is not set as the year 1.
const setTime = (value?: string) =>
  value && !value.startsWith("0001-") ? value : undefined;

/** The checks of a run's evaluation steps, in the order they were recorded. */
export async function getRunChecks(runID: string): Promise<Check[]> {
  if (!service()?.RunChecks) return [];
  const checks = (await RunChecks(runID)) as unknown as Check[];
  return (checks ?? []).map((check) => ({
    ...check,
    startedAt: setTime(check.startedAt),
    finishedAt: setTime(check.finishedAt),
  }));
}

/** A check's log: while it runs, what it gained after from; once it ends, all of it. */
export async function getCheckLog(
  checkID: string,
  from: number,
): Promise<CheckLogInfo> {
  if (!service()?.CheckLog)
    throw new Error("Check logs are available in the Inkuna desktop app");
  return CheckLog(checkID, from) as unknown as Promise<CheckLogInfo>;
}

/** Saves a finished check's log where the user picks; "" when they cancel. */
export async function saveCheckLog(checkID: string): Promise<string> {
  if (!service()?.SaveCheckLog)
    throw new Error("Saving a log is available in the Inkuna desktop app");
  return SaveCheckLog(checkID);
}

/** The commits on a run's branch, oldest first, each with its step if one made it. */
export async function getRunCommits(runID: string): Promise<BranchCommit[]> {
  if (!service()?.RunCommits)
    throw new Error("Commits are available in the Inkuna desktop app");
  return RunCommits(runID) as unknown as Promise<BranchCommit[]>;
}

/** The files a run changed in scope, without their patches. */
export async function getRunChanges(
  runID: string,
  scope: DiffScope,
): Promise<DiffChange> {
  if (!service()?.RunChanges)
    throw new Error("Changes are available in the Inkuna desktop app");
  return RunChanges(runID, scope as never) as unknown as Promise<DiffChange>;
}

/** One file's patch; loadAnyway loads a too-large one up to the hard cap. */
export async function getRunFilePatch(
  runID: string,
  scope: DiffScope,
  path: string,
  loadAnyway = false,
): Promise<DiffFilePatch> {
  if (!service()?.RunFilePatch)
    throw new Error("Changes are available in the Inkuna desktop app");
  return RunFilePatch(
    runID,
    scope as never,
    path,
    loadAnyway,
  ) as unknown as Promise<DiffFilePatch>;
}

/**
 * Unchanged lines of a file in a run's change, to expand a patch's context:
 * count lines from newStart in the newer version, numbered from oldStart in
 * the older one.
 */
export async function getRunFileExcerpt(
  runID: string,
  scope: DiffScope,
  path: string,
  oldStart: number,
  newStart: number,
  count: number,
): Promise<DiffExcerpt> {
  if (!service()?.RunFileExcerpt)
    throw new Error("Changes are available in the Inkuna desktop app");
  return RunFileExcerpt(
    runID,
    scope as never,
    path,
    oldStart,
    newStart,
    count,
  ) as unknown as Promise<DiffExcerpt>;
}

/** The files marked viewed in a run's changes, each at the blob it was marked on. */
export async function getRunViewed(runID: string): Promise<ViewedFile[]> {
  if (!service()?.RunViewed) return [];
  return RunViewed(runID) as unknown as Promise<ViewedFile[]>;
}

/** Marks a file viewed at its blob, or clears the mark; Go's reason comes back as the error. */
export async function setFileViewed(
  runID: string,
  path: string,
  blob: string,
  viewed: boolean,
): Promise<void> {
  if (!service()?.SetFileViewed)
    throw new Error("Viewed marks are available in the Inkuna desktop app");
  await SetFileViewed(runID, path, blob, viewed);
}

export async function getRunArtifacts(runID: string): Promise<ArtifactInfo[]> {
  if (!service()?.RunArtifacts) return [];
  return RunArtifacts(runID) as unknown as Promise<ArtifactInfo[]>;
}

export async function readArtifact(digest: string): Promise<ArtifactInfo> {
  if (!service()?.ReadArtifact)
    throw new Error("Artifacts are available in the Inkuna desktop app");
  return ReadArtifact(digest) as unknown as Promise<ArtifactInfo>;
}

/**
 * What every agent step before a review gate left to read before approving:
 * its document and the files it declared. Go walks the plan.
 */
export async function getGateArtifacts(
  runID: string,
  gateID: string,
): Promise<GateStep[]> {
  if (!service()?.GateArtifacts) return [];
  return GateArtifacts(runID, gateID) as unknown as Promise<GateStep[]>;
}

export async function getRunMessages(runID: string): Promise<RunTask[]> {
  if (!service()?.RunMessages) return [];
  const tasks = (await RunMessages(runID)) as unknown;
  return Array.isArray(tasks) ? (tasks as RunTask[]) : [];
}

/** A run's rounds as SQLite keeps them, oldest first. */
export async function getRunRounds(runID: string): Promise<RunRound[]> {
  if (!service()?.RunRounds) return [];
  const rounds = (await RunRounds(runID)) as unknown;
  return Array.isArray(rounds) ? (rounds as RunRound[]) : [];
}

/** What each of a run's model calls consumed; empty outside the desktop app. */
export async function getRunUsage(runID: string): Promise<RunUsageInfo> {
  const noCost = { reportedCents: 0, estimatedCents: 0, unknownCalls: 0 };
  if (!service()?.RunUsage) return { calls: [], total: {}, cost: noCost };
  const usage = (await RunUsage(runID)) as unknown as RunUsageInfo;
  return {
    calls: usage.calls ?? [],
    total: usage.total ?? {},
    cost: usage.cost ?? noCost,
  };
}

/**
 * What a task cost and where, across its planning and runs; throws outside
 * the desktop app, where nothing was recorded.
 */
export async function getTaskCost(taskID: string): Promise<TaskCostInfo> {
  if (!service()?.TaskCost)
    throw new Error("Task cost is available in the Inkuna desktop app");
  const cost = (await TaskCost(taskID)) as unknown as TaskCostInfo;
  return {
    ...cost,
    planning: {
      total: cost.planning?.total ?? noTotals(),
      calls: cost.planning?.calls ?? [],
    },
    runs: (cost.runs ?? []).map((run) => ({
      ...run,
      steps: (run.steps ?? []).map((step) => ({
        ...step,
        calls: step.calls ?? [],
      })),
    })),
    byAgent: cost.byAgent ?? [],
    byRuntime: cost.byRuntime ?? [],
    byModel: cost.byModel ?? [],
    byStep: cost.byStep ?? [],
  };
}

/**
 * What each task cost over a period, the most expensive first: one
 * project's tasks, or every project's with an empty projectID. from and to
 * are RFC 3339 times, either empty for an open side.
 */
export async function getTaskCosts(
  projectID: string,
  from: string,
  to: string,
): Promise<PeriodCost> {
  if (!service()?.TaskCosts) return { tasks: [], total: noTotals() };
  const cost = (await TaskCosts(projectID, from, to)) as unknown as PeriodCost;
  return { tasks: cost.tasks ?? [], total: cost.total ?? noTotals() };
}

/**
 * Asks where to save a CSV the view built and writes it; resolves to the
 * path written, or "" when the user cancelled.
 */
export async function saveCSV(
  defaultName: string,
  content: string,
): Promise<string> {
  if (!service()?.SaveCSV)
    throw new Error("Exporting is available in the Inkuna desktop app");
  return SaveCSV(defaultName, content);
}

function noTotals() {
  return {
    usage: {},
    cost: { reportedCents: 0, estimatedCents: 0, unknownCalls: 0 },
    bestCents: 0,
    subscriptionCents: 0,
    calls: 0,
    durationMs: 0,
  };
}

export async function listThreads(): Promise<Thread[]> {
  if (!service()?.Threads) return [];
  const threads = (await Threads()) as unknown;
  return Array.isArray(threads) ? (threads as Thread[]) : [];
}

export async function getThread(taskID: string): Promise<ThreadDetail> {
  if (!service()?.Thread)
    throw new Error("Messages are available in the Inkuna desktop app");
  const detail = (await GetThread(taskID)) as unknown as ThreadDetail;
  return {
    ...detail,
    messages: Array.isArray(detail.messages) ? detail.messages : [],
    artifacts: Array.isArray(detail.artifacts) ? detail.artifacts : [],
    inputs: Array.isArray(detail.inputs) ? detail.inputs : [],
  };
}

export async function answerInput(
  inputID: string,
  answer: string,
): Promise<InputRequest> {
  if (!service()?.AnswerInput)
    throw new Error("Replies are available in the Inkuna desktop app");
  return AnswerInput(inputID, answer) as unknown as Promise<InputRequest>;
}

export async function approveReview(runID: string): Promise<WorkflowRun> {
  if (!service()?.ApproveReview)
    throw new Error("Reviews are available in the Inkuna desktop app");
  return ApproveReview(runID) as unknown as Promise<WorkflowRun>;
}

/** What the review bar may offer on a run now, with Go's reason when not. */
export async function getRunReview(runID: string): Promise<RunReview> {
  if (!service()?.RunReview)
    throw new Error("Reviews are available in the Inkuna desktop app");
  const review = (await RunReviewOf(runID)) as unknown as RunReview;
  return {
    ...review,
    targets: review.targets ?? [],
    trackerChanges: review.trackerChanges ?? [],
  };
}

const resumeUnavailable =
  "Resuming runs is available in the Inkuna desktop app";

/** What resuming a stopped run would do now, with Go's reason when not. */
export async function getRunResume(runID: string): Promise<RunResume> {
  if (!service()?.RunResume) throw new Error(resumeUnavailable);
  return normalizeResume((await RunResumeOf(runID)) as unknown as RunResume);
}

/** A resume view with Go's empty lists, which arrive as null, made empty arrays. */
function normalizeResume(resume: RunResume): RunResume {
  return {
    ...resume,
    rerun: resume.rerun ?? [],
    kept: resume.kept ?? [],
    changes: resume.changes ?? [],
  };
}

const rerunUnavailable =
  "Rerunning runs is available in the Inkuna desktop app";

/** Why a stopped run did not finish, and the plan changes Go suggests. */
export async function getRunFailure(runID: string): Promise<RunFailure> {
  if (!service()?.RunFailure) throw new Error(rerunUnavailable);
  const failure = (await RunFailureOf(runID)) as unknown as RunFailure;
  return {
    ...failure,
    steps: (failure.steps ?? []).map((step) => ({
      ...step,
      denials: step.denials ?? [],
      checks: step.checks ?? [],
    })),
    suggestions: failure.suggestions ?? [],
  };
}

/** The plan a stopped run ran under, for the approval screen to edit. */
export async function planRerun(runID: string): Promise<RerunPlan> {
  if (!service()?.PlanRerun) throw new Error(rerunUnavailable);
  const rerun = recordValue(await PlanRerunOf(runID), "rerun");
  return {
    runId: String(rerun.runId),
    plan: normalizeWorkflowPlan(rerun.plan),
    resume: normalizeResume(rerun.resume as RunResume),
  };
}

/** Rebuilds a rerun's plan around the approval screen's edits, in Go. */
export async function reviseRerun(
  runID: string,
  workflowJSON: string,
): Promise<RerunRevision> {
  if (!service()?.ReviseRerun) throw new Error(rerunUnavailable);
  const revision = recordValue(
    await ReviseRerunOf(runID, workflowJSON),
    "rerun revision",
  );
  const workflow = recordValue(revision.workflow, "workflow");
  if (!Array.isArray(workflow.steps))
    throw new Error("Inkuna returned a workflow without steps");
  return {
    workflow: workflow as unknown as RerunRevision["workflow"],
    edited: Array.isArray(revision.edited) ? (revision.edited as string[]) : [],
    resume: normalizeResume(revision.resume as RunResume),
  };
}

/** Reruns a stopped run under the approved plan; rejects with Go's reason. */
export async function rerunWorkflowRun(
  runID: string,
  request: RerunRequest,
): Promise<WorkflowRun> {
  if (!service()?.RerunWorkflowRun) throw new Error(rerunUnavailable);
  return normalizeWorkflowRun(
    await RerunWorkflowRunOf(runID, request as never),
  );
}

/**
 * A rerun's access changes made to the rule its run was planned from, for
 * the Rules editor to show unsaved; rejects with Go's reason.
 */
export async function rerunRuleEdits(
  runID: string,
  workflowJSON: string,
): Promise<RuleEdits> {
  if (!service()?.RerunRuleEdits) throw new Error(rerunUnavailable);
  const edits = (await RerunRuleEditsOf(
    runID,
    workflowJSON,
  )) as unknown as RuleEdits;
  return {
    ...edits,
    changes: edits.changes ?? [],
    leftOut: edits.leftOut ?? [],
  };
}

/** Resumes a stopped run where it stopped; rejects with Go's reason. */
export async function resumeWorkflowRun(
  runID: string,
  request: ResumeRequest,
): Promise<WorkflowRun> {
  if (!service()?.ResumeWorkflowRun) throw new Error(resumeUnavailable);
  return normalizeWorkflowRun(await ResumeWorkflowRun(runID, request as never));
}

const commentsUnavailable = "Comments are available in the Inkuna desktop app";

/**
 * A run's comment threads placed on its changes as of commit to (the tip
 * when empty), and whether comments can be written now.
 */
export async function getRunComments(
  runID: string,
  to: string,
): Promise<RunComments> {
  if (!service()?.RunComments) return { threads: [], targets: [] };
  const comments = (await RunCommentsOf(runID, to)) as unknown as RunComments;
  return {
    ...comments,
    threads: (comments.threads ?? []).map((thread) => ({
      ...thread,
      comments: thread.comments ?? [],
    })),
    targets: comments.targets ?? [],
  };
}

/** Saves a draft comment; rejects with Go's reason. */
export async function saveComment(
  runID: string,
  draft: CommentDraft,
): Promise<void> {
  if (!service()?.SaveComment) throw new Error(commentsUnavailable);
  await SaveComment(runID, draft as never);
}

/** Deletes a draft comment; rejects with Go's reason. */
export async function deleteComment(runID: string, id: string): Promise<void> {
  if (!service()?.DeleteComment) throw new Error(commentsUnavailable);
  await DeleteComment(runID, id);
}

/** Resolves a comment thread, or reopens it. */
export async function resolveThread(
  runID: string,
  id: string,
  resolved: boolean,
): Promise<void> {
  if (!service()?.ResolveThread) throw new Error(commentsUnavailable);
  await ResolveThread(runID, id, resolved);
}

/** The stage a comment on a line goes to unless the person picks another. */
export async function getCommentTarget(
  runID: string,
  path: string,
  side: string,
  line: number,
): Promise<string> {
  if (!service()?.CommentTarget) return "";
  return CommentTarget(runID, path, side, line);
}

/** Sends a run back with what to fix; rejects with Go's reason. */
export async function requestChanges(
  runID: string,
  request: FeedbackRequest,
): Promise<WorkflowRun> {
  if (!service()?.RequestChanges)
    throw new Error("Reviews are available in the Inkuna desktop app");
  return RequestChanges(
    runID,
    request as never,
  ) as unknown as Promise<WorkflowRun>;
}

export async function listAgents(projectID: string): Promise<AgentInfo[]> {
  if (!service()?.Agents) return [];
  const agents = (await Agents(projectID)) as unknown;
  return Array.isArray(agents) ? (agents as AgentInfo[]) : [];
}

const agentsUnavailable =
  "Agent editing is available in the Inkuna desktop app";

export async function getAgentLocations(
  projectID: string,
): Promise<AgentLocations> {
  if (!service()?.AgentLocations) return {};
  return GetAgentLocations(projectID) as unknown as Promise<AgentLocations>;
}

/** Validates the text of an agent file for import. Nothing is saved. */
export async function parseAgent(content: string): Promise<AgentDefinition> {
  if (!service()?.ParseAgent) throw new Error(agentsUnavailable);
  return ParseAgent(content) as unknown as Promise<AgentDefinition>;
}

/** Writes <scope>/<name>.md; a changed name removes originalName's file. */
export async function saveAgent(
  projectID: string,
  scope: AgentScope,
  originalName: string,
  definition: AgentDefinition,
): Promise<AgentInfo> {
  if (!service()?.SaveAgent) throw new Error(agentsUnavailable);
  return SaveAgent(
    projectID,
    scope,
    originalName,
    JSON.stringify(definition),
  ) as unknown as Promise<AgentInfo>;
}

export async function deleteAgent(
  projectID: string,
  scope: AgentScope,
  name: string,
): Promise<void> {
  if (!service()?.DeleteAgent) throw new Error(agentsUnavailable);
  await DeleteAgent(projectID, scope, name);
}

const branchesUnavailable =
  "Branch sync is available in the Inkuna desktop app";

function normalizeBranchSync(value: unknown): BranchSyncInfo {
  const info = value as BranchSyncInfo;
  const branches = info.sync?.branches;
  return {
    ...info,
    sync: { ...info.sync, branches: Array.isArray(branches) ? branches : [] },
  };
}

/** A project's branches against what the last fetch saw; no network. */
export async function getProjectBranches(
  projectID: string,
): Promise<BranchSyncInfo> {
  if (!service()?.ProjectBranches) throw new Error(branchesUnavailable);
  return normalizeBranchSync(await ProjectBranches(projectID));
}

/** Fetches origin, then measures again; a failed fetch is in fetchError. */
export async function fetchProject(projectID: string): Promise<BranchSyncInfo> {
  if (!service()?.FetchProject) throw new Error(branchesUnavailable);
  return normalizeBranchSync(await FetchProject(projectID));
}

/** Fast-forwards one branch to its upstream; rejects with Go's reason. */
export async function updateBranch(
  projectID: string,
  branch: string,
): Promise<BranchSyncInfo> {
  if (!service()?.UpdateBranch) throw new Error(branchesUnavailable);
  return normalizeBranchSync(await UpdateBranch(projectID, branch));
}

/**
 * Puts the settings edits in the project checkout on origin, as a pull
 * request or a push to the base. Go commits only the digest the user
 * confirmed and rejects with its reason otherwise.
 */
export async function shareSettings(
  projectID: string,
  mode: ShareMode,
  digest: string,
): Promise<BranchSyncInfo> {
  if (!service()?.ShareSettings) throw new Error(branchesUnavailable);
  return normalizeBranchSync(await ShareSettings(projectID, mode, digest));
}

/**
 * Deletes a branch without an upstream, as it stood at commit when the user
 * confirmed; rejects with Go's reason.
 */
export async function deleteBranch(
  projectID: string,
  branch: string,
  commit: string,
): Promise<BranchSyncInfo> {
  if (!service()?.DeleteBranch) throw new Error(branchesUnavailable);
  return normalizeBranchSync(await DeleteBranch(projectID, branch, commit));
}

/**
 * A project's sync summary, fetching origin first when Go finds the last
 * fetch stale; null outside the desktop app.
 */
export async function getProjectSync(
  projectID: string,
  fetchIfStale: boolean,
): Promise<ProjectSyncInfo | null> {
  if (!service()?.ProjectSync) return null;
  return (await ProjectSync(
    projectID,
    fetchIfStale,
  )) as unknown as ProjectSyncInfo;
}

const docsUnavailable = "Docs are available in the Inkuna desktop app";

export async function getDocs(projectID: string): Promise<DocsInfo> {
  if (!service()?.Docs) throw new Error(docsUnavailable);
  return Docs(projectID) as unknown as Promise<DocsInfo>;
}

/** Reads one doc by its path under the docs root; Go refuses any other path. */
export async function getDocContent(
  projectID: string,
  path: string,
): Promise<DocContentInfo> {
  if (!service()?.DocContent) throw new Error(docsUnavailable);
  return DocContent(projectID, path) as unknown as Promise<DocContentInfo>;
}

/** Links between docs, built by Go from every registered project's index. */
export async function getDocGraph(
  projectID: string,
  scope: DocGraphScope,
): Promise<DocGraphInfo> {
  if (!service()?.DocGraph) throw new Error(docsUnavailable);
  return DocGraph(projectID, scope) as unknown as Promise<DocGraphInfo>;
}

/** Who read one doc, newest first; empty outside the desktop app. */
export async function getDocReads(
  projectID: string,
  path: string,
): Promise<DocRead[]> {
  if (!service()?.DocReads) return [];
  const reads = (await DocReads(projectID, path)) as unknown;
  return Array.isArray(reads) ? (reads as DocRead[]) : [];
}

/**
 * Opens a link in the system browser. The webview must never navigate away
 * from the app, so outside the desktop runtime the link opens in a new tab.
 */
/** Copies text to the clipboard: the app's own in the desktop app, else the browser's. */
export async function copyText(text: string): Promise<void> {
  if (window.runtime) {
    if (!(await ClipboardSetText(text)))
      throw new Error("The clipboard did not take the text");
    return;
  }
  if (!navigator.clipboard)
    throw new Error("Copying is available in the Inkuna desktop app");
  await navigator.clipboard.writeText(text);
}

export function openExternal(url: string) {
  if (window.runtime) {
    BrowserOpenURL(url);
    return;
  }
  window.open(url, "_blank", "noopener,noreferrer");
}

export async function getRules(projectID: string): Promise<RulesInfo> {
  if (!service()?.Rules)
    throw new Error("Rules are available in the Inkuna desktop app");
  return Rules(projectID) as unknown as Promise<RulesInfo>;
}

const rulesUnavailable = "Rule editing is available in the Inkuna desktop app";

/** Validates and writes the project's rules file; Go is the authority. */
export async function saveRules(
  projectID: string,
  document: RulesDocument,
): Promise<RulesInfo> {
  if (!service()?.SaveRules) throw new Error(rulesUnavailable);
  return SaveRules(
    projectID,
    JSON.stringify(document),
  ) as unknown as Promise<RulesInfo>;
}

/** Renders the document as YAML; rejects with the reason when it is invalid. */
export async function rulesToYAML(
  projectID: string,
  document: RulesDocument,
): Promise<string> {
  if (!service()?.RulesToYAML) throw new Error(rulesUnavailable);
  return RulesToYAML(projectID, JSON.stringify(document));
}

export async function rulesFromYAML(
  projectID: string,
  content: string,
): Promise<RulesDocument> {
  if (!service()?.RulesFromYAML) throw new Error(rulesUnavailable);
  return RulesFromYAML(projectID, content) as unknown as Promise<RulesDocument>;
}

/**
 * The shared stages and groups the edited rules can use, with where each
 * is used. The document need not be valid yet.
 */
export async function getStageLibrary(
  projectID: string,
  document: RulesDocument,
): Promise<StageLibraryInfo> {
  if (!service()?.StageLibrary) throw new Error(rulesUnavailable);
  return StageLibrary(
    projectID,
    JSON.stringify(document),
  ) as unknown as Promise<StageLibraryInfo>;
}

/** Where Go finds the first mistake in edited rules, as a save would. */
export async function checkRules(
  projectID: string,
  document: RulesDocument,
): Promise<RulesProblem> {
  if (!service()?.CheckRules) throw new Error(rulesUnavailable);
  return CheckRules(
    projectID,
    JSON.stringify(document),
  ) as unknown as Promise<RulesProblem>;
}

/** The edited rules as planning sees them; rejects when they would not save. */
export async function expandRules(
  projectID: string,
  document: RulesDocument,
): Promise<RulesDocument> {
  if (!service()?.ExpandRules) throw new Error(rulesUnavailable);
  return ExpandRules(
    projectID,
    JSON.stringify(document),
  ) as unknown as Promise<RulesDocument>;
}

/** The registered repositories whose saved rules use a global definition. */
export async function globalDefinitionUsers(
  file: string,
  name: string,
): Promise<DefinitionUser[]> {
  if (!service()?.GlobalDefinitionUsers) throw new Error(rulesUnavailable);
  return GlobalDefinitionUsers(file, name) as unknown as Promise<
    DefinitionUser[]
  >;
}

/** Saves a global definition to its library file; Go refuses a built-in one. */
export async function saveGlobalDefinition(
  file: string,
  name: string,
  definition: { stage: RuleStage } | { group: RuleGroup },
): Promise<void> {
  if (!service()?.SaveGlobalDefinition) throw new Error(rulesUnavailable);
  return SaveGlobalDefinition(file, name, JSON.stringify(definition));
}

const catalogUnavailable = "The catalog is available in the Inkuna desktop app";

/**
 * The catalog's items, with where the project, when one is given, and this
 * machine's global directories already have each.
 */
export async function listCatalog(projectID: string): Promise<CatalogListing> {
  if (!service()?.CatalogItems) return { items: [] };
  const listing = (await CatalogItems(projectID)) as unknown as CatalogListing;
  return { ...listing, items: listing.items ?? [] };
}

/**
 * What adding a catalog item would do, as Go checks it; nothing is written.
 * The source is the definition source offering it, "" for Inkuna's own.
 */
export async function previewCatalogItem(
  projectID: string,
  name: string,
  source: string,
  global: boolean,
): Promise<CatalogResult> {
  if (!service()?.PreviewCatalogItem) throw new Error(catalogUnavailable);
  return PreviewCatalogItem(
    projectID,
    name,
    source,
    global,
  ) as unknown as Promise<CatalogResult>;
}

/** Copies a catalog item, and what it brings, into the project or globally. */
export async function addCatalogItem(
  projectID: string,
  name: string,
  source: string,
  global: boolean,
): Promise<CatalogResult> {
  if (!service()?.AddCatalogItem) throw new Error(catalogUnavailable);
  return AddCatalogItem(
    projectID,
    name,
    source,
    global,
  ) as unknown as Promise<CatalogResult>;
}

const previewCapabilities: CapabilitiesInfo = {
  product: { name: "Inkuna", version: "dev", inkuna: true },
  capabilities: [],
};

/** The product and every capability the build's extensions registered. */
export async function getCapabilities(): Promise<CapabilitiesInfo> {
  if (!service()?.Capabilities) return previewCapabilities;
  return Capabilities() as unknown as Promise<CapabilitiesInfo>;
}

const previewConfiguration: ConfigurationInfo = {
  path: "",
  exists: false,
  dataDir: "",
  file: emptyConfigFile,
  secrets: [],
};

export async function getConfiguration(): Promise<ConfigurationInfo> {
  if (!service()?.Configuration) return previewConfiguration;
  return Configuration() as unknown as Promise<ConfigurationInfo>;
}

export async function saveConfiguration(
  fileJSON: string,
): Promise<ConfigurationInfo> {
  if (!service()?.SaveConfiguration)
    throw new Error("Setup is available in the Inkuna desktop app");
  return SaveConfiguration(fileJSON) as unknown as Promise<ConfigurationInfo>;
}

/**
 * Every problem a save would be refused for, keyed by field. Outside the
 * desktop app there is nothing to ask, and the save itself explains why.
 */
export async function validateConfiguration(
  fileJSON: string,
): Promise<ConfigFieldError[]> {
  if (!service()?.ValidateConfiguration) return [];
  return (await ValidateConfiguration(fileJSON)) ?? [];
}

const runtimesUnavailable = "Runtimes are managed by the Inkuna desktop app";

/** Every declared runtime, built-ins first; the preview knows the built-ins. */
export async function listRuntimes(): Promise<RuntimeInfo[]> {
  if (!service()?.Runtimes) return builtInRuntimes;
  return ((await Runtimes()) ?? []) as unknown as RuntimeInfo[];
}

export async function saveRuntime(
  name: Runtime,
  config: RuntimeConfig,
): Promise<RuntimeInfo[]> {
  if (!service()?.SaveRuntime) throw new Error(runtimesUnavailable);
  return (await SaveRuntime(
    name,
    JSON.stringify(config),
  )) as unknown as RuntimeInfo[];
}

/** Removes an added runtime; a built-in goes back to its defaults. */
export async function deleteRuntime(name: Runtime): Promise<RuntimeInfo[]> {
  if (!service()?.DeleteRuntime) throw new Error(runtimesUnavailable);
  return (await DeleteRuntime(name)) as unknown as RuntimeInfo[];
}

/** Resolves the binary and runs the version probe. */
export async function testRuntime(name: Runtime): Promise<RuntimeTest> {
  if (!service()?.TestRuntime) throw new Error(runtimesUnavailable);
  return TestRuntime(name);
}

/**
 * The MCP servers a runtime's CLI finds in the user's own setup, with what it
 * says of each. Claude Code connects to every server to list them.
 */
export async function listUserMCPServers(
  name: Runtime,
): Promise<UserMCPServer[]> {
  if (!service()?.ListUserMCPServers) throw new Error(runtimesUnavailable);
  return (await ListUserMCPServers(name)) as unknown as UserMCPServer[];
}

/** Runs the CLI's own sign-in for one of the user's servers, in the browser. */
export async function signInUserMCPServer(
  runtime: Runtime,
  server: string,
): Promise<void> {
  if (!service()?.SignInUserMCPServer) throw new Error(runtimesUnavailable);
  return SignInUserMCPServer(runtime, server);
}

const remoteUnavailable =
  "Remote agents are connected from the Inkuna desktop app";

/** Every connected remote agent, enabled or not. */
export async function listRemoteAgents(): Promise<RemoteAgent[]> {
  if (!service()?.RemoteAgents) return [];
  return ((await RemoteAgents()) ?? []) as unknown as RemoteAgent[];
}

/**
 * Fetches a card for review. With an existing agent's name it also reports
 * what changed since that agent was pinned; nothing is saved or sent.
 */
export async function previewRemoteAgent(
  cardURL: string,
  existingName = "",
): Promise<RemoteAgentPreview> {
  if (!service()?.PreviewRemoteAgent) throw new Error(remoteUnavailable);
  return (await PreviewRemoteAgent(
    cardURL,
    existingName,
  )) as unknown as RemoteAgentPreview;
}

export async function saveRemoteAgent(
  request: RemoteAgentRequest,
): Promise<RemoteAgent[]> {
  if (!service()?.SaveRemoteAgent) throw new Error(remoteUnavailable);
  return ((await SaveRemoteAgent(JSON.stringify(request))) ??
    []) as unknown as RemoteAgent[];
}

export async function repinRemoteAgent(
  name: string,
  reviewedDigest: string,
): Promise<RemoteAgent[]> {
  if (!service()?.RepinRemoteAgent) throw new Error(remoteUnavailable);
  return ((await RepinRemoteAgent(name, reviewedDigest)) ??
    []) as unknown as RemoteAgent[];
}

export async function setRemoteAgentEnabled(
  name: string,
  enabled: boolean,
): Promise<RemoteAgent[]> {
  if (!service()?.SetRemoteAgentEnabled) throw new Error(remoteUnavailable);
  return ((await SetRemoteAgentEnabled(name, enabled)) ??
    []) as unknown as RemoteAgent[];
}

export async function deleteRemoteAgent(name: string): Promise<RemoteAgent[]> {
  if (!service()?.DeleteRemoteAgent) throw new Error(remoteUnavailable);
  return ((await DeleteRemoteAgent(name)) ?? []) as unknown as RemoteAgent[];
}

export async function saveSecret(
  name: string,
  value: string,
): Promise<ConfigurationInfo> {
  if (!service()?.SaveSecret)
    throw new Error("Secrets are stored by the Inkuna desktop app");
  return SaveSecret(name, value) as unknown as Promise<ConfigurationInfo>;
}

/** Forgets a secret's value, its declaration, and its runtime exports. */
export async function removeSecretDeclaration(
  name: string,
): Promise<ConfigurationInfo> {
  if (!service()?.RemoveSecretDeclaration)
    throw new Error("Secrets are available in the Inkuna desktop app");
  return RemoveSecretDeclaration(name) as unknown as Promise<ConfigurationInfo>;
}

/** Native folder picker; resolves to "" when the user cancels. */
export async function chooseDirectory(title: string): Promise<string> {
  if (!service()?.ChooseDirectory)
    throw new Error(
      "The folder picker is available in the Inkuna desktop app; type the path instead",
    );
  return ChooseDirectory(title);
}

export async function deleteSecret(name: string): Promise<ConfigurationInfo> {
  if (!service()?.DeleteSecret)
    throw new Error("Secrets are stored by the Inkuna desktop app");
  return DeleteSecret(name) as unknown as Promise<ConfigurationInfo>;
}

function normalizeWorkflowPlan(value: unknown): WorkflowPlan {
  const plan = recordValue(value, "workflow plan");
  const workflow = recordValue(plan.workflow, "workflow specification");
  const task = recordValue(plan.task, "workflow task");
  const project = recordValue(plan.project, "workflow project");
  if (!Array.isArray(workflow.steps)) {
    throw new Error("Inkuna returned a workflow without steps");
  }
  return {
    ...(plan as unknown as WorkflowPlan),
    project: project as unknown as WorkflowPlan["project"],
    task: task as unknown as WorkflowPlan["task"],
    workflow: {
      ...(workflow as unknown as WorkflowPlan["workflow"]),
      steps: workflow.steps.map((step) => {
        const current = recordValue(step, "workflow step");
        return current as unknown as WorkflowPlan["workflow"]["steps"][number];
      }),
    },
  };
}

function normalizeWorkflowRun(value: unknown): WorkflowRun {
  const run = recordValue(value, "workflow run");
  if (typeof run.id !== "string" || typeof run.state !== "string") {
    throw new Error("Inkuna returned an invalid workflow run");
  }
  const events = Array.isArray(run.events)
    ? run.events.map((event) => {
        const current = recordValue(event, "run event");
        return {
          ...current,
          runId: String(current.runId ?? run.id),
          sequence: Number(current.sequence ?? 0),
          timestamp: String(current.timestamp ?? run.startedAt ?? ""),
          kind: String(current.kind ?? "message"),
          summary: String(current.summary ?? "Run event"),
        } as WorkflowRun["events"][number];
      })
    : [];
  const steps = Array.isArray(run.steps)
    ? (run.steps as WorkflowRun["steps"])
    : [];
  return { ...(run as unknown as WorkflowRun), events, steps };
}

function recordValue(value: unknown, label: string): Record<string, unknown> {
  if (typeof value === "string") {
    try {
      value = JSON.parse(value) as unknown;
    } catch {
      throw new Error(`Inkuna returned invalid ${label} JSON`);
    }
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`Inkuna returned an invalid ${label}`);
  }
  return value as Record<string, unknown>;
}
