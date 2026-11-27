import { useCallback, useEffect, useRef, useState } from "react";
import {
  deleteAgent,
  getAgentLocations,
  getConfiguration,
  listAgents,
  parseAgent,
  saveAgent,
} from "../bridge";
import { pretty } from "../format";
import type {
  AgentDefinition,
  AgentInfo,
  AgentLocations,
  AgentScope,
  CatalogResult,
  Project,
} from "../types";
import { AgentForm } from "./agents/AgentForm";
import { blankDefinition, type AgentDraft } from "./agents/draft";
import { RemoteAgents } from "./agents/RemoteAgents";
import { CatalogAddedNotice, CatalogDialog } from "./catalog/CatalogDialog";
import {
  Alert,
  Button,
  Chip,
  Code,
  Disclosure,
  Field,
  Header,
  Modal,
  Notice,
  inputClass,
} from "./common";

const sourceTone: Record<string, "neutral" | "accent" | "green"> = {
  builtin: "neutral",
  global: "accent",
  repository: "green",
};

// The user and the planner are part of the harness, not editable workers.
const fixedAgents = ["human", "orchestrator"];

function fileScope(definition: AgentDefinition): AgentScope | null {
  if (definition.source === "repository") return "repository";
  if (definition.source === "global") return "global";
  return null;
}

export function AgentsView({
  projects,
  selectedProject,
  onProjectChange,
  onDirtyChange,
}: {
  projects: Project[];
  selectedProject: Project | null;
  onProjectChange(project: Project | null): void;
  /** Says whether an agent open in the form has edits not yet saved. */
  onDirtyChange?(dirty: boolean): void;
}) {
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  const [locations, setLocations] = useState<AgentLocations>({});
  const [secrets, setSecrets] = useState<string[]>([]);
  const [draft, setDraft] = useState<AgentDraft | null>(null);
  const [removing, setRemoving] = useState<AgentInfo | null>(null);
  // The ref refuses a second confirmation before the pending state renders.
  const deleting = useRef(false);
  const [deletePending, setDeletePending] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [catalogOpen, setCatalogOpen] = useState(false);
  const [added, setAdded] = useState<CatalogResult | null>(null);
  const filePicker = useRef<HTMLInputElement>(null);
  const projectID = selectedProject?.id ?? "";

  // Only the newest list request may update the view; an older one can belong
  // to another project or predate a save or delete.
  const latestList = useRef(0);
  const refresh = useCallback(async () => {
    const request = ++latestList.current;
    try {
      const next = await listAgents(projectID);
      if (request !== latestList.current) return;
      setAgents(next);
      setError("");
    } catch (reason) {
      if (request !== latestList.current) return;
      // The list on screen may belong to another project.
      setAgents([]);
      setError(String(reason));
    }
  }, [projectID]);

  useEffect(() => {
    let active = true;
    setDraft(null);
    // A pending delete names an agent in the project it was opened for.
    setRemoving(null);
    setStatus("");
    setAdded(null);
    setCatalogOpen(false);
    // Until this project's locations load, nothing can be created from the
    // previous project's.
    setLocations({});
    void refresh();
    getAgentLocations(projectID)
      .then((value) => active && setLocations(value))
      .catch(() => active && setLocations({}));
    getConfiguration()
      .then(
        (value) =>
          active &&
          setSecrets((value.file.secrets ?? []).map((secret) => secret.name)),
      )
      .catch(() => active && setSecrets([]));
    return () => {
      active = false;
    };
  }, [projectID, refresh]);

  const defaultScope: AgentScope = locations.repository
    ? "repository"
    : "global";
  const canCreate = Boolean(locations.repository || locations.global);

  function open(
    definition: AgentDefinition,
    originalName: string,
    scope: AgentScope,
    scopeLocked: boolean,
  ) {
    setStatus("");
    setAdded(null);
    setDraft({ originalName, scope, scopeLocked, definition });
  }

  async function importFile(file: File | undefined) {
    if (!file) return;
    try {
      const definition = await parseAgent(await file.text());
      open(definition, "", defaultScope, false);
      setError("");
    } catch (reason) {
      setError(`${file.name}: ${String(reason)}`);
    }
  }

  async function save(next: AgentDraft) {
    const saved = await saveAgent(
      projectID,
      next.scope,
      next.originalName,
      next.definition,
    );
    await refresh();
    setDraft(null);
    setStatus(`Saved ${saved.definition.path ?? saved.definition.name}.`);
  }

  async function remove(agent: AgentInfo) {
    const scope = fileScope(agent.definition);
    if (!scope || deleting.current) return;
    deleting.current = true;
    setDeletePending(true);
    try {
      await deleteAgent(projectID, scope, agent.definition.name);
      await refresh();
      setStatus(
        agent.overrides
          ? `Deleted the ${scope} override; the ${agent.overrides} ${agent.definition.name} applies again.`
          : `Deleted ${agent.definition.name}.`,
      );
    } catch (reason) {
      setError(String(reason));
    } finally {
      deleting.current = false;
      setDeletePending(false);
      setRemoving(null);
    }
  }

  return (
    <section>
      <Header
        eyebrow="Registry"
        title="Agents"
        actions={
          !draft && (
            <>
              <input
                ref={filePicker}
                type="file"
                accept=".md,text/markdown"
                aria-label="Agent file to import"
                className="sr-only"
                onChange={(event) => {
                  void importFile(event.target.files?.[0]);
                  event.target.value = "";
                }}
              />
              <Button
                disabled={!canCreate}
                onClick={() => filePicker.current?.click()}
              >
                Import file…
              </Button>
              <Button
                disabled={!canCreate}
                onClick={() => setCatalogOpen(true)}
              >
                Add from catalog…
              </Button>
              <Button
                variant="primary"
                disabled={!canCreate}
                onClick={() => open(blankDefinition(), "", defaultScope, false)}
              >
                + New agent
              </Button>
            </>
          )
        }
      />
      {error && <Alert>{error}</Alert>}
      {status && <Notice role="status">{status}</Notice>}
      {added && <CatalogAddedNotice result={added} />}
      <div className="my-4 max-w-[420px]">
        <Field label="Project" flush>
          <select
            aria-label="Agents project"
            className={inputClass}
            value={projectID}
            disabled={draft !== null || removing !== null}
            onChange={(event) =>
              onProjectChange(
                projects.find((project) => project.id === event.target.value) ??
                  null,
              )
            }
          >
            <option value="">Built-in and global only</option>
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {draft ? (
        <AgentForm
          key={`${draft.originalName}:${draft.definition.name}`}
          draft={draft}
          locations={locations}
          secrets={secrets}
          onCancel={() => setDraft(null)}
          onSave={save}
          onDirtyChange={onDirtyChange}
        />
      ) : (
        <div className="grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-3 max-[900px]:grid-cols-1">
          {agents.map((agent) => {
            const { definition, card } = agent;
            const scope = fileScope(definition);
            const fixed = fixedAgents.includes(definition.name);
            return (
              <article
                aria-label={definition.name}
                className="flex min-w-0 flex-col border border-line bg-panel p-5"
                key={definition.name}
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Chip tone={sourceTone[definition.source] ?? "neutral"}>
                    {definition.source}
                  </Chip>
                  {agent.overrides && (
                    <Chip tone="warn">overrides {agent.overrides}</Chip>
                  )}
                  <Chip>{definition.permission}</Chip>
                  {definition.runtime && <Chip>{definition.runtime}</Chip>}
                  {definition.model && (
                    <Chip tone="accent">{definition.model}</Chip>
                  )}
                </div>
                <h3 className="mt-4 mb-1 text-[17px] font-semibold break-words">
                  {definition.name}
                </h3>
                <p className="m-0 text-sm leading-relaxed break-words text-muted">
                  {definition.description}
                </p>
                <dl className="my-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]">
                  <dt className="text-muted">Task kinds</dt>
                  <dd className="m-0 min-w-0 break-words">
                    {definition.taskKinds?.join(", ") || "any"}
                  </dd>
                  <dt className="text-muted">Skills</dt>
                  <dd className="m-0 min-w-0 break-words">
                    {definition.skills?.join(", ") || "—"}
                  </dd>
                  <dt className="text-muted">Tags</dt>
                  <dd className="m-0 min-w-0 break-words">
                    {definition.tags?.join(", ") || "—"}
                  </dd>
                </dl>
                <Disclosure label="Instructions">
                  <Code>{definition.instructions}</Code>
                </Disclosure>
                <Disclosure label="A2A agent card">
                  <Code>{pretty(card)}</Code>
                </Disclosure>
                {!fixed && (
                  <div className="mt-auto flex flex-wrap justify-end gap-1.5 border-t border-line pt-3">
                    {scope ? (
                      <>
                        <Button
                          aria-label={`Edit ${definition.name}`}
                          onClick={() =>
                            open(definition, definition.name, scope, true)
                          }
                        >
                          Edit
                        </Button>
                        <Button
                          aria-label={`Duplicate ${definition.name}`}
                          onClick={() =>
                            open(
                              {
                                ...definition,
                                name: `${definition.name}-copy`,
                              },
                              "",
                              scope,
                              false,
                            )
                          }
                        >
                          Duplicate
                        </Button>
                        <Button
                          variant="danger"
                          aria-label={`Delete ${definition.name}`}
                          onClick={() => setRemoving(agent)}
                        >
                          Delete
                        </Button>
                      </>
                    ) : (
                      <Button
                        aria-label={`Customize ${definition.name}`}
                        disabled={!canCreate}
                        title="Save a copy with the same name to override the built-in, or rename it to add a new agent"
                        onClick={() =>
                          open(definition, "", defaultScope, false)
                        }
                      >
                        Customize
                      </Button>
                    )}
                  </div>
                )}
              </article>
            );
          })}
        </div>
      )}
      {!draft && <RemoteAgents secrets={secrets} />}
      {catalogOpen && (
        <CatalogDialog
          title="Add an agent from the catalog"
          kinds={["agent"]}
          projectID={locations.repository ? projectID : ""}
          allowGlobal
          onClose={() => setCatalogOpen(false)}
          onAdded={(result) => {
            setCatalogOpen(false);
            setStatus("");
            setAdded(result);
            void refresh();
          }}
        />
      )}
      {removing && (
        <Modal
          title={`Delete ${removing.definition.name}?`}
          onClose={() => deleting.current || setRemoving(null)}
        >
          <p className="m-0 text-sm wrap-anywhere text-muted">
            This removes {removing.definition.path ?? "the definition file"}.
            {removing.overrides
              ? ` The ${removing.overrides} agent with the same name applies again.`
              : " Rules that name this agent will stop validating."}
          </p>
          <div className="mt-6 flex justify-end gap-2">
            <Button disabled={deletePending} onClick={() => setRemoving(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={deletePending}
              onClick={() => void remove(removing)}
            >
              Delete agent
            </Button>
          </div>
        </Modal>
      )}
    </section>
  );
}
