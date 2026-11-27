import { useEffect, useMemo, useRef, useState } from "react";
import {
  checkRules,
  expandRules,
  getRules,
  getStageLibrary,
  listAgents,
  listRemoteAgents,
  rulesFromYAML,
  rulesToYAML,
  saveRules,
} from "../bridge";
import type {
  CatalogResult,
  Project,
  Rule,
  RuleEdits,
  RulesDocument,
  RulesInfo,
  RulesProblem,
  StageLibrary,
} from "../types";
import {
  Alert,
  Button,
  Chip,
  Field,
  Header,
  Notice,
  Panel,
  TabPanel,
  Tabs,
  inputClass,
} from "./common";
import { CatalogAddedNotice, CatalogDialog } from "./catalog/CatalogDialog";
import { ConditionEditor } from "./rules/ConditionEditor";
import { FlowEditor } from "./rules/FlowEditor";
import { LibraryPanel } from "./rules/LibraryPanel";
import { StageChain } from "./rules/RuleSummary";
import {
  definitionNames,
  describeCondition,
  extractStage,
  move,
  newFlow,
  newRule,
  tidy,
  uniqueName,
  type Flow,
} from "./rules/model";

type Mode = "form" | "yaml";
const modes: Array<{ id: Mode; label: string }> = [
  { id: "form", label: "Form" },
  { id: "yaml", label: "YAML" },
];

const ruleHeading =
  "mt-4 mb-1.5 text-[10px] font-semibold tracking-widest text-muted uppercase";

export function RulesView({
  projects,
  selectedProject,
  onProjectChange,
  onDirtyChange,
  proposal,
}: {
  projects: Project[];
  selectedProject: Project | null;
  onProjectChange(project: Project | null): void;
  onDirtyChange?(dirty: boolean): void;
  /**
   * A rerun's access changes Go made to its rule, shown as unsaved edits of
   * the project's rules for the person to review and save, or discard.
   */
  proposal?: RuleEdits | null;
}) {
  const [info, setInfo] = useState<RulesInfo | null>(null);
  const [draft, setDraft] = useState<RulesDocument | null>(null);
  const [agents, setAgents] = useState<string[]>([]);
  const [remoteAgents, setRemoteAgents] = useState<string[]>([]);
  const [mode, setMode] = useState<Mode>("form");
  const [yaml, setYaml] = useState("");
  const [editing, setEditing] = useState<number | "default" | null>(null);
  const [dirty, setDirty] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState("");
  const [pendingProject, setPendingProject] = useState<Project | null>(null);
  // The proposal shown, until a save or discard; each is shown once, so
  // switching projects and back does not make it again.
  const [proposed, setProposed] = useState<RuleEdits | null>(null);
  const shownProposal = useRef<RuleEdits | null>(null);
  // Go's listing of the shared stages and groups the draft can use, and
  // where the last refused save went wrong.
  const [library, setLibrary] = useState<StageLibrary | null>(null);
  const [libraryError, setLibraryError] = useState("");
  const [libraryVersion, setLibraryVersion] = useState(0);
  const [problem, setProblem] = useState<RulesProblem | null>(null);
  // Which of the catalog's kinds are being added from, and what the last
  // add did; an add can bring agents, so the agents are listed again.
  const [catalog, setCatalog] = useState<"rules" | "definitions" | null>(null);
  const [added, setAdded] = useState<CatalogResult | null>(null);
  const [agentsVersion, setAgentsVersion] = useState(0);
  // Whether the draft holds edits made while an add's rules were read
  // again, which hide what was added; it ends with that draft.
  const [editsOverAdd, setEditsOverAdd] = useState(false);
  const projectID = selectedProject?.id ?? "";
  // The project shown and whether its draft is edited, as of the last
  // render, for a read that returns after the person may have moved on.
  const current = useRef({ projectID, dirty });
  useEffect(() => {
    current.current = { projectID, dirty };
  });

  useEffect(() => onDirtyChange?.(dirty), [dirty, onDirtyChange]);
  useEffect(() => () => onDirtyChange?.(false), [onDirtyChange]);

  function load(value: RulesInfo) {
    setInfo(value);
    setDraft(value.document);
    setMode("form");
    setYaml("");
    setEditing(null);
    setDirty(false);
    setProposed(null);
    setProblem(null);
    setEditsOverAdd(false);
  }

  // The library follows the draft, since the draft's own definitions take
  // precedence and its flows say where each is used; a pause in typing
  // asks Go once.
  useEffect(() => {
    if (!projectID || !draft) return;
    let active = true;
    const timer = setTimeout(() => {
      getStageLibrary(projectID, tidy(draft))
        .then((value) => {
          if (!active) return;
          setLibrary(value);
          setLibraryError("");
        })
        .catch((reason: unknown) => {
          if (!active) return;
          setLibrary(null);
          setLibraryError(String(reason));
        });
    }, 250);
    return () => {
      active = false;
      clearTimeout(timer);
    };
  }, [projectID, draft, libraryVersion]);

  /** After Go refuses the draft, asks it where, so the entry is shown. */
  async function locate(document: RulesDocument) {
    try {
      const found = await checkRules(projectID, tidy(document));
      if (!found.message) return;
      setProblem(found);
      if (found.flow)
        setEditing(found.flow.rule < 0 ? "default" : found.flow.rule);
    } catch {
      // The reason is already shown; only the pointer to it is missing.
    }
  }

  useEffect(() => {
    setInfo(null);
    setDraft(null);
    setSaved("");
    setAdded(null);
    setCatalog(null);
    // Another project's library must not show while this one's loads.
    setLibrary(null);
    setLibraryError("");
    if (!projectID) return;
    let active = true;
    getRules(projectID)
      .then((value) => {
        if (active) {
          load(value);
          setError("");
          if (
            proposal &&
            proposal.projectId === projectID &&
            shownProposal.current !== proposal
          ) {
            shownProposal.current = proposal;
            setDraft(proposal.rules.document);
            setDirty(proposal.changes.length > 0);
            setProposed(proposal);
            setEditing(proposal.ruleIndex ?? "default");
          }
        }
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [projectID, proposal]);

  useEffect(() => {
    if (!projectID) return;
    let active = true;
    // Remote advisors a rule can name: enabled, reviewed, and not hidden
    // behind a local agent of the same name.
    Promise.all([listAgents(projectID), listRemoteAgents().catch(() => [])])
      .then(([local, remote]) => {
        if (!active) return;
        const names = local
          .map((agent) => agent.definition.name)
          .filter((name) => name !== "orchestrator");
        const advisors = remote
          .filter((agent) => agent.enabled && agent.pinned)
          .map((agent) => agent.name)
          .filter((name) => !names.includes(name));
        setAgents([...names, ...advisors]);
        setRemoteAgents(advisors);
      })
      .catch(() => {
        if (active) {
          setAgents([]);
          setRemoteAgents([]);
        }
      });
    return () => {
      active = false;
    };
  }, [projectID, agentsVersion]);

  function edit(next: RulesDocument) {
    setDraft(next);
    setDirty(true);
    setSaved("");
    setAdded(null);
    setProblem(null);
  }

  /** Go wrote what the add brought, so the rules and agents are read again. */
  async function catalogAdded(result: CatalogResult, global: boolean) {
    setCatalog(null);
    setSaved("");
    setAdded(result);
    setLibraryVersion((version) => version + 1);
    // A global stage or group can bring global agents, which steps pick too.
    setAgentsVersion((version) => version + 1);
    if (global) return;
    const from = projectID;
    try {
      const value = await getRules(from);
      // Loading replaces the draft, so it is done only for the project the
      // add went to, and only over a draft nobody edited meanwhile.
      if (current.current.projectID !== from) return;
      if (current.current.dirty) {
        // The edits stay, but Discard goes back to the file as the add left
        // it, not as it was before.
        setInfo(value);
        setEditsOverAdd(true);
        return;
      }
      load(value);
      setError("");
    } catch (reason) {
      if (current.current.projectID !== from) return;
      setError(
        `Added, but the rules could not be read again: ${String(reason)}`,
      );
    }
  }
  /** Extracts a flow's inline stage to a shared stage; rule is the rule's
   * index, or "default" for the fallback flow. */
  function extract(
    rule: number | "default",
    flow: Flow,
    index: number,
    name: string,
  ) {
    if (!draft) return;
    const next = extractStage(draft, flow, index, name);
    edit(
      rule === "default"
        ? { ...next.document, default: next.flow }
        : {
            ...next.document,
            rules: next.document.rules.map((item, position) =>
              position === rule ? { ...item, then: next.flow } : item,
            ),
          },
    );
  }
  /** A flow as planning would see it, from Go. */
  async function previewFlow(rule: number | "default") {
    if (!draft) return [];
    const expanded = await expandRules(projectID, tidy(draft));
    return rule === "default"
      ? (expanded.default?.stages ?? [])
      : (expanded.rules[rule]?.then.stages ?? []);
  }
  /** The refused entry of a flow, when Go's last refusal was in it. */
  function flowProblem(rule: number | "default") {
    const flow = problem?.flow;
    if (!flow || flow.entry < 0) return null;
    if ((rule === "default" ? -1 : rule) !== flow.rule) return null;
    return { entry: flow.entry, message: problem.message };
  }
  function updateRule(index: number, rule: Rule) {
    if (!draft) return;
    edit({
      ...draft,
      rules: draft.rules.map((item, position) =>
        position === index ? rule : item,
      ),
    });
  }

  /** Both directions validate in Go, so a broken document cannot switch. */
  async function switchMode(next: Mode) {
    if (next === mode || !draft) return;
    setBusy(true);
    setError("");
    try {
      if (next === "yaml") {
        setYaml(await rulesToYAML(projectID, tidy(draft)));
      } else {
        setDraft(await rulesFromYAML(projectID, yaml));
      }
      setMode(next);
      setProblem(null);
    } catch (reason) {
      setError(String(reason));
      if (next === "yaml") await locate(draft);
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    if (!draft) return;
    setBusy(true);
    setError("");
    try {
      const document =
        mode === "yaml" ? await rulesFromYAML(projectID, yaml) : draft;
      const next = await saveRules(projectID, tidy(document));
      const kept = mode;
      load(next);
      setSaved(`Saved to ${next.path}. The next plan uses these rules.`);
      if (kept === "yaml") {
        // The file is already written. A failure to re-render it must not
        // hide that, so it is reported on its own and the form is shown.
        try {
          setYaml(await rulesToYAML(projectID, tidy(next.document)));
          setMode("yaml");
        } catch (reason) {
          setError(
            `Saved, but the YAML view could not be refreshed: ${String(reason)}`,
          );
        }
      }
    } catch (reason) {
      setError(String(reason));
      if (mode === "form") await locate(draft);
    } finally {
      setBusy(false);
    }
  }

  const names = useMemo(
    () => (draft?.rules ?? []).map((rule) => rule.name),
    [draft],
  );
  const takenNames = useMemo(
    () => (draft ? definitionNames(library, draft) : []),
    [library, draft],
  );

  return (
    <section>
      <Header
        eyebrow="Flow engine"
        title="Rules"
        actions={
          draft && (
            <>
              {dirty && <Chip tone="warn">unsaved changes</Chip>}
              <Button
                disabled={!dirty || busy}
                onClick={() => info && load(info)}
              >
                Discard
              </Button>
              <Button
                variant="primary"
                disabled={busy || (!dirty && info?.exists)}
                onClick={() => void save()}
              >
                {busy ? "Working…" : "Save rules"}
              </Button>
            </>
          )
        }
      />
      {error && <Alert>{error}</Alert>}
      {editsOverAdd && (
        <Alert>
          Added, but the rules were edited while they were read again, so the
          edits are kept. Saving them would drop what was added; discard them
          to see it.
        </Alert>
      )}
      {saved && <Notice role="status">{saved}</Notice>}
      {added && <CatalogAddedNotice result={added} />}
      {proposed && <ProposalNotice proposal={proposed} />}
      <div className="my-4 max-w-[420px]">
        <Field label="Project" flush>
          <select
            aria-label="Rules project"
            className={inputClass}
            value={projectID}
            onChange={(event) => {
              const next =
                projects.find((project) => project.id === event.target.value) ??
                null;
              if (dirty) setPendingProject(next);
              else onProjectChange(next);
            }}
          >
            {projects.length === 0 && <option value="">No projects</option>}
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {pendingProject && (
        <Notice title="Discard unsaved rule changes?">
          Switching to {pendingProject.name} drops the edits you have not saved.
          <span className="mt-3 flex flex-wrap gap-2">
            <Button
              variant="danger"
              onClick={() => {
                setDirty(false);
                onProjectChange(pendingProject);
                setPendingProject(null);
              }}
            >
              Discard and switch
            </Button>
            <Button onClick={() => setPendingProject(null)}>
              Keep editing
            </Button>
          </span>
        </Notice>
      )}
      {info && draft && (
        <>
          <div className="mb-4 flex flex-wrap items-center gap-2">
            <Chip tone={info.exists ? "green" : "neutral"}>
              {info.exists ? "repository rules" : "built-in rules"}
            </Chip>
            <span className="min-w-0 text-[11px] wrap-anywhere text-muted">
              {info.exists
                ? info.path
                : `No ${info.path} yet. Saving creates it from what you see here.`}
            </span>
          </div>
          <Tabs
            tabs={modes}
            active={mode}
            onChange={(next) => void switchMode(next)}
            label="Editor mode"
            idPrefix="rules-editor"
          />
          <TabPanel idPrefix="rules-editor" tab={mode}>
            {mode === "yaml" ? (
              <Field label="rules.yaml: validated when you save or switch back to the form">
                <textarea
                  aria-label="Rules YAML"
                  spellCheck={false}
                  className={`${inputClass} min-h-[480px] resize-y font-mono text-xs leading-relaxed`}
                  value={yaml}
                  onChange={(event) => {
                    setYaml(event.target.value);
                    setDirty(true);
                    setSaved("");
                  }}
                />
              </Field>
            ) : (
              <div className="mt-4 grid grid-cols-[minmax(0,1fr)] gap-3">
                <p className="m-0 text-xs text-muted">
                  Rules are checked top to bottom; the first match decides the
                  flow.
                </p>
                {draft.rules.map((rule, index) => (
                  <Panel key={index}>
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <Chip tone="accent">#{index + 1}</Chip>
                      <h3 className="m-0 min-w-0 text-[15px] font-semibold break-words">
                        {rule.name || "(unnamed rule)"}
                      </h3>
                      <span className="ml-auto flex flex-wrap gap-1.5">
                        <Button
                          aria-label={`Move rule ${rule.name} up`}
                          disabled={index === 0}
                          onClick={() => {
                            edit({
                              ...draft,
                              rules: move(draft.rules, index, index - 1),
                            });
                            setEditing(null);
                          }}
                        >
                          ↑
                        </Button>
                        <Button
                          aria-label={`Move rule ${rule.name} down`}
                          disabled={index === draft.rules.length - 1}
                          onClick={() => {
                            edit({
                              ...draft,
                              rules: move(draft.rules, index, index + 1),
                            });
                            setEditing(null);
                          }}
                        >
                          ↓
                        </Button>
                        <Button
                          aria-label={`Duplicate rule ${rule.name}`}
                          onClick={() => {
                            const copy = structuredClone(rule);
                            copy.name = uniqueName(`${rule.name}-copy`, names);
                            const rules = [...draft.rules];
                            rules.splice(index + 1, 0, copy);
                            edit({ ...draft, rules });
                            setEditing(index + 1);
                          }}
                        >
                          Duplicate
                        </Button>
                        <Button
                          variant="danger"
                          aria-label={`Delete rule ${rule.name}`}
                          onClick={() => {
                            edit({
                              ...draft,
                              rules: draft.rules.filter(
                                (_, position) => position !== index,
                              ),
                            });
                            setEditing(null);
                          }}
                        >
                          Delete
                        </Button>
                        <Button
                          aria-label={`${editing === index ? "Close" : "Edit"} rule ${rule.name}`}
                          aria-expanded={editing === index}
                          onClick={() =>
                            setEditing(editing === index ? null : index)
                          }
                        >
                          {editing === index ? "Done" : "Edit"}
                        </Button>
                      </span>
                    </div>
                    {editing === index ? (
                      <>
                        <div className="grid min-w-0 gap-x-4 min-[901px]:grid-cols-2">
                          <Field label="Rule name">
                            <input
                              className={inputClass}
                              value={rule.name}
                              onChange={(event) =>
                                updateRule(index, {
                                  ...rule,
                                  name: event.target.value,
                                })
                              }
                            />
                          </Field>
                          <Field label="Description (optional)">
                            <input
                              className={inputClass}
                              value={rule.description ?? ""}
                              onChange={(event) =>
                                updateRule(index, {
                                  ...rule,
                                  description: event.target.value,
                                })
                              }
                            />
                          </Field>
                        </div>
                        <h4 className={ruleHeading}>When</h4>
                        <ConditionEditor
                          when={rule.when}
                          onChange={(when) =>
                            updateRule(index, { ...rule, when })
                          }
                        />
                        <h4 className={ruleHeading}>Then</h4>
                        <FlowEditor
                          label={`Stages of ${rule.name}`}
                          flow={rule.then}
                          agents={agents}
                          remoteAgents={remoteAgents}
                          library={library}
                          takenNames={takenNames}
                          problem={flowProblem(index)}
                          onExtract={(stage, name) =>
                            extract(index, rule.then, stage, name)
                          }
                          onPreview={() => previewFlow(index)}
                          previewOf={draft}
                          onChange={(then) =>
                            updateRule(index, { ...rule, then })
                          }
                        />
                      </>
                    ) : (
                      <>
                        {rule.description && (
                          <p className="mt-2 mb-0 text-sm break-words text-muted">
                            {rule.description}
                          </p>
                        )}
                        <h4 className={ruleHeading}>When</h4>
                        <ul className="m-0 list-disc pl-[18px] text-xs leading-relaxed break-words">
                          {describeCondition(rule.when).map((item) => (
                            <li key={item}>{item}</li>
                          ))}
                        </ul>
                        <h4 className={ruleHeading}>Then</h4>
                        <StageChain stages={rule.then.stages} />
                      </>
                    )}
                  </Panel>
                ))}
                <div className="flex flex-wrap gap-2">
                  <Button
                    onClick={() => {
                      edit({
                        ...draft,
                        rules: [...draft.rules, newRule(names)],
                      });
                      setEditing(draft.rules.length);
                    }}
                  >
                    + Add rule
                  </Button>
                  <Button onClick={() => setCatalog("rules")}>
                    Add rule from catalog…
                  </Button>
                </div>
                <Panel>
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    <Chip>default</Chip>
                    <h3 className="m-0 min-w-0 text-[15px] font-semibold break-words">
                      Fallback flow
                    </h3>
                    <Button
                      className="ml-auto"
                      aria-label={`${editing === "default" ? "Close" : "Edit"} fallback flow`}
                      aria-expanded={editing === "default"}
                      onClick={() =>
                        setEditing(editing === "default" ? null : "default")
                      }
                    >
                      {editing === "default" ? "Done" : "Edit"}
                    </Button>
                  </div>
                  <p className="mt-2 mb-4 text-sm text-muted">
                    Used when no rule above matches. Every flow ends with an
                    approval so nothing lands unreviewed.
                  </p>
                  {editing === "default" ? (
                    <FlowEditor
                      label="Stages of the fallback flow"
                      flow={draft.default ?? newFlow()}
                      agents={agents}
                      remoteAgents={remoteAgents}
                      library={library}
                      takenNames={takenNames}
                      problem={flowProblem("default")}
                      onExtract={(stage, name) =>
                        extract(
                          "default",
                          draft.default ?? newFlow(),
                          stage,
                          name,
                        )
                      }
                      onPreview={() => previewFlow("default")}
                      previewOf={draft}
                      onChange={(flow) => edit({ ...draft, default: flow })}
                    />
                  ) : (
                    <StageChain stages={(draft.default ?? newFlow()).stages} />
                  )}
                </Panel>
                <LibraryPanel
                  library={library}
                  loadError={libraryError}
                  document={draft}
                  agents={agents}
                  remoteAgents={remoteAgents}
                  onChange={edit}
                  onLibraryChanged={() =>
                    setLibraryVersion((version) => version + 1)
                  }
                  onAddFromCatalog={() => setCatalog("definitions")}
                  problem={
                    problem?.definition
                      ? {
                          definition: problem.definition,
                          message: problem.message,
                        }
                      : null
                  }
                />
              </div>
            )}
          </TabPanel>
        </>
      )}
      {catalog && (
        <CatalogDialog
          title={
            catalog === "rules"
              ? "Add a rule from the catalog"
              : "Add a shared stage or group from the catalog"
          }
          kinds={catalog === "rules" ? ["rule"] : ["stage", "group"]}
          projectID={projectID}
          allowGlobal={catalog === "definitions"}
          repositoryBlocked={
            dirty
              ? "Save or discard your rule changes first: adding to this repository writes its rules file, and a later save of these edits would drop what was added."
              : ""
          }
          onClose={() => setCatalog(null)}
          onAdded={(result, global) => void catalogAdded(result, global)}
        />
      )}
    </section>
  );
}

/**
 * What a rerun's changes did to its rule, as Go said: each change made to
 * the draft below, and each left out with why. Nothing is saved until the
 * person saves the rules.
 */
function ProposalNotice({ proposal }: { proposal: RuleEdits }) {
  const rule =
    proposal.ruleIndex === undefined
      ? "the fallback flow"
      : `rule ${proposal.ruleName}`;
  return (
    <Notice
      role="region"
      aria-label="Changes from a rerun"
      title={`Changes from a rerun, made to ${rule}`}
    >
      <p className="m-0">
        {proposal.changes.length > 0
          ? "They are not saved. Review them below, then Save rules to start every plan from the rule with them, or Discard to drop them. The rerun is still on the Workflow screen."
          : "None of the rerun's changes could be made to the rule, so there is nothing to save. The rerun is still on the Workflow screen."}
      </p>
      {proposal.changes.length > 0 && (
        <ul
          aria-label="Changes made"
          className="mt-2 mb-0 list-disc pl-[18px] wrap-anywhere"
        >
          {proposal.changes.map((change) => (
            <li key={`${change.stepId}:${change.field}`}>{change.text}</li>
          ))}
        </ul>
      )}
      {proposal.leftOut.length > 0 && (
        <>
          <b className="mt-3 block text-ink">Left out</b>
          <ul
            aria-label="Changes left out"
            className="mt-1 mb-0 list-disc pl-[18px] wrap-anywhere"
          >
            {proposal.leftOut.map((change) => (
              <li key={`${change.stepId}:${change.field}`}>{change.text}</li>
            ))}
          </ul>
        </>
      )}
    </Notice>
  );
}
