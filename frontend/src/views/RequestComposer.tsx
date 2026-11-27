import { useEffect, useState } from "react";
import { importLinearIssue, planWorkflow } from "../bridge";
import type {
  LinearIssue,
  Project,
  WorkflowPlan,
  WorkflowRequest,
} from "../types";
import { useRules } from "./rules/useRules";
import {
  Alert,
  Button,
  Eyebrow,
  Field,
  Header,
  Notice,
  TabPanel,
  Tabs,
  inputClass,
} from "./common";

type Source = "request" | "github" | "linear";

const sourceTabs: Array<{ id: Source; label: string }> = [
  { id: "request", label: "Direct request" },
  { id: "linear", label: "Linear issue" },
  { id: "github", label: "GitHub issue" },
];

const textareaClass = `${inputClass} resize-y leading-relaxed`;

function SourceTag({ children }: { children: string }) {
  return (
    <span
      title={children}
      className="max-w-full truncate rounded-full border border-ok-line px-2 py-1 font-mono text-[9px] leading-none text-ok"
    >
      {children}
    </span>
  );
}

export function RequestComposer({
  projects,
  selectedProject,
  onProjectChange,
  onContinue,
  onDirtyChange,
}: {
  projects: Project[];
  selectedProject: Project | null;
  onProjectChange(project: Project | null): void;
  onContinue(plan: WorkflowPlan, request: WorkflowRequest): void;
  /** Says whether the request has text not yet planned. */
  onDirtyChange?(dirty: boolean): void;
}) {
  const [source, setSource] = useState<Source>("request");
  const [ruleName, setRuleName] = useState("");
  const rules = useRules(selectedProject?.id);
  const [title, setTitle] = useState("");
  const [objective, setObjective] = useState("");
  const [acceptanceCriteria, setAcceptanceCriteria] = useState("");
  const [constraints, setConstraints] = useState("");
  const [labels, setLabels] = useState("");
  const [linearReference, setLinearReference] = useState("");
  const [linearIssue, setLinearIssue] = useState<LinearIssue | null>(null);
  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState("");
  const [planning, setPlanning] = useState(false);
  const [planningError, setPlanningError] = useState("");
  // What the person wrote, or imported, and has not planned yet.
  const dirty = [
    title,
    objective,
    acceptanceCriteria,
    constraints,
    labels,
    linearReference,
  ].some((text) => text.trim() !== "");
  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);
  async function pullLinearIssue() {
    setImporting(true);
    setImportError("");
    try {
      const issue = await importLinearIssue(linearReference);
      setLinearIssue(issue);
      // The issue's key names the branch's story from its URL, so the
      // title is the issue's own.
      setTitle(issue.title);
      setObjective(issue.description ?? "");
      if (issue.labels?.length) setLabels(issue.labels.join(", "));
    } catch (reason) {
      setImportError(String(reason));
    } finally {
      setImporting(false);
    }
  }
  async function generateWorkflow() {
    if (!selectedProject) return;
    setPlanning(true);
    setPlanningError("");
    try {
      const request: WorkflowRequest = {
        projectId: selectedProject.id,
        title: title.trim() || undefined,
        objective: objective.trim(),
        acceptanceCriteria: acceptanceCriteria
          .split("\n")
          .map((value) => value.trim())
          .filter(Boolean),
        constraints: constraints
          .split("\n")
          .map((value) => value.trim())
          .filter(Boolean),
        labels: labels
          .split(",")
          .map((value) => value.trim())
          .filter(Boolean),
        kind: linearIssue?.suggestedKind || "implementation",
        sourceKind: source,
        sourceReference:
          source === "linear"
            ? linearIssue?.url || linearReference.trim()
            : source === "github"
              ? objective.trim()
              : undefined,
        ruleName: ruleName || undefined,
      };
      const plan = await planWorkflow(request);
      onContinue(plan, request);
    } catch (reason) {
      setPlanningError(String(reason));
    } finally {
      setPlanning(false);
    }
  }
  return (
    <section>
      <Header eyebrow="Compose" title="Turn intent into a workflow" />
      <div className="max-w-[900px] min-w-0">
        <Field label="Project">
          <select
            className={inputClass}
            aria-label="Project"
            value={selectedProject?.id ?? ""}
            onChange={(event) => {
              // A rule chosen for one project may not exist in the next.
              setRuleName("");
              onProjectChange(
                projects.find((project) => project.id === event.target.value) ??
                  null,
              );
            }}
          >
            <option value="">Select a local project</option>
            {projects.map((project) => (
              <option value={project.id} key={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </Field>
        <Tabs
          label="Request source"
          tabs={sourceTabs}
          active={source}
          onChange={setSource}
          idPrefix="request-source"
        />
        <TabPanel idPrefix="request-source" tab={source}>
          {source === "linear" && (
            <div>
              <Field label="Linear issue key or URL">
                <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-2">
                  <input
                    className={inputClass}
                    value={linearReference}
                    onChange={(event) => setLinearReference(event.target.value)}
                    placeholder="ENG-123 or https://linear.app/…"
                  />
                  <Button
                    variant="primary"
                    disabled={!linearReference.trim() || importing}
                    onClick={pullLinearIssue}
                  >
                    {importing ? "Importing…" : "Import"}
                  </Button>
                </div>
              </Field>
              {importError && <Alert>{importError}</Alert>}
              {linearIssue && (
                <div className="flex flex-wrap items-center justify-between gap-x-5 gap-y-3 border border-ok-line bg-panel p-4">
                  <div className="min-w-0">
                    <Eyebrow>Imported read-only</Eyebrow>
                    <h3 className="mt-1.5 mb-0 text-sm font-semibold wrap-anywhere">
                      {linearIssue.identifier} · {linearIssue.title}
                    </h3>
                  </div>
                  <div className="flex min-w-0 flex-wrap gap-1.5">
                    <SourceTag>{linearIssue.team}</SourceTag>
                    <SourceTag>{linearIssue.state}</SourceTag>
                    <SourceTag>{linearIssue.suggestedKind}</SourceTag>
                    {linearIssue.project && (
                      <SourceTag>{linearIssue.project}</SourceTag>
                    )}
                  </div>
                </div>
              )}
            </div>
          )}
          <Field label="Title">
            <input
              className={inputClass}
              aria-label="Title"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder="A short name for the task, like an issue title. Left blank, the orchestrator writes one from the details."
            />
          </Field>
          <Field label="Details">
            <textarea
              className={`${textareaClass} min-h-40`}
              value={objective}
              onChange={(e) => setObjective(e.target.value)}
              placeholder={
                source === "request"
                  ? "Describe the outcome you want…"
                  : source === "linear"
                    ? "Import a Linear issue above…"
                    : "Paste a GitHub issue URL…"
              }
            />
          </Field>
          <div className="grid grid-cols-[repeat(2,minmax(0,1fr))] gap-x-4 max-[900px]:grid-cols-1">
            <Field label="Acceptance criteria">
              <textarea
                className={`${textareaClass} min-h-[110px]`}
                value={acceptanceCriteria}
                onChange={(event) => setAcceptanceCriteria(event.target.value)}
                placeholder={
                  "One observable result per line\nTests cover failure paths"
                }
              />
            </Field>
            <Field label="Constraints">
              <textarea
                className={`${textareaClass} min-h-[110px]`}
                value={constraints}
                onChange={(event) => setConstraints(event.target.value)}
                placeholder={"Allowed paths, compatibility, limits…"}
              />
            </Field>
          </div>
          <Field label="Labels">
            <input
              className={inputClass}
              aria-label="Labels"
              value={labels}
              onChange={(event) => setLabels(event.target.value)}
              placeholder="security, hotfix (comma separated; a label naming a rule, like bugfix, picks that flow)"
            />
          </Field>
          {rules && (
            <Field label="Workflow">
              <select
                className={inputClass}
                aria-label="Workflow rule"
                value={ruleName}
                onChange={(event) => setRuleName(event.target.value)}
              >
                <option value="">Let the rules decide</option>
                {rules.rules.map((rule) => (
                  <option value={rule.name} key={rule.name}>
                    {rule.name}
                    {rule.description ? ` · ${rule.description}` : ""}
                  </option>
                ))}
                {!rules.rules.some((rule) => rule.name === "default") && (
                  <option value="default">default · the fallback flow</option>
                )}
              </select>
            </Field>
          )}
          <Notice
            title={
              source === "linear"
                ? "Linear is the source; GitHub is the projection"
                : "Rules pick the flow, the orchestrator staffs it"
            }
          >
            <p className="m-0">
              {source === "linear"
                ? "Inkuna reads the initial story from Linear and executes the approved plan locally. GitHub writes stay behind their own approval boundary."
                : "The repository's rules choose the stages; a label naming a rule, or the Workflow picker, chooses one directly. The orchestrator assigns agents and skills, and every step can be re-staffed before approval."}
            </p>
          </Notice>
        </TabPanel>
        {planningError && (
          <div className="mt-4">
            <Alert>{planningError}</Alert>
          </div>
        )}
        <div className="mt-5 flex justify-end gap-2">
          <Button
            variant="primary"
            disabled={
              (!title.trim() && !objective.trim()) ||
              !selectedProject ||
              planning
            }
            onClick={generateWorkflow}
          >
            {planning ? "Planning…" : "Generate workflow →"}
          </Button>
        </div>
      </div>
    </section>
  );
}
