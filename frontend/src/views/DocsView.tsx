import { useCallback, useEffect, useMemo, useState } from "react";
import {
  getDocContent,
  getDocGraph,
  getDocReads,
  getDocs,
  openExternal,
} from "../bridge";
import { formatTime } from "../format";
import type {
  DocContent,
  DocGraph,
  DocGraphNode,
  DocGraphScope,
  DocRead,
  DocsInfo,
  Project,
} from "../types";
import {
  Alert,
  Button,
  Chip,
  Empty,
  Field,
  Header,
  Panel,
  TabPanel,
  Tabs,
  inputClass,
} from "./common";
import { DocGraphPanel } from "./docs/DocGraph";
import { DocTree } from "./docs/DocTree";
import { backlinks, type Backlink } from "./docs/graph";
import { Markdown } from "./docs/Markdown";
import { anchorID } from "./docs/anchors";
import {
  allDocs,
  anyDoc,
  baseName,
  docStatusTones,
  facets,
  filterTree,
  findDoc,
  type DocFilter,
} from "./docs/tree";

/** Browses a project's docs: the folder tree beside the rendered doc. */
export function DocsView({
  projects,
  selectedProject,
  onProjectChange,
  onOpenRun,
}: {
  projects: Project[];
  selectedProject: Project | null;
  onProjectChange(project: Project | null): void;
  /** Shows a run in the Tasks view, from a doc's Read by list. */
  onOpenRun?(runID: string): void;
}) {
  const projectID = selectedProject?.id ?? "";
  // Reload re-reads the tree and the open doc from disk; Inkuna does not
  // watch the files.
  const [generation, setGeneration] = useState(0);
  // A doc in another project, opened from the graph or a backlink, waits
  // here while the browser remounts for that project.
  const [pending, setPending] = useState<{
    projectID: string;
    path: string;
  } | null>(null);

  return (
    <section>
      <Header
        eyebrow="Documentation"
        title="Docs"
        actions={
          <Button
            disabled={!projectID}
            onClick={() => setGeneration((value) => value + 1)}
          >
            Reload
          </Button>
        }
      />
      <div className="my-4 max-w-[420px]">
        <Field label="Project" flush>
          <select
            aria-label="Docs project"
            className={inputClass}
            value={projectID}
            onChange={(event) => {
              setPending(null);
              onProjectChange(
                projects.find((project) => project.id === event.target.value) ??
                  null,
              );
            }}
          >
            {!projectID && <option value="">Choose a project</option>}
            {projects.map((project) => (
              <option key={project.id} value={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </Field>
      </div>
      {projectID ? (
        // Keyed so nothing selected or filtered in one project carries over.
        <DocsBrowser
          key={projectID}
          projectID={projectID}
          projects={projects}
          generation={generation}
          onOpenRun={onOpenRun}
          initialDoc={
            pending?.projectID === projectID ? pending.path : undefined
          }
          onOpenElsewhere={(id, path) => {
            const project = projects.find((each) => each.id === id);
            if (!project) return;
            setPending({ projectID: id, path });
            onProjectChange(project);
          }}
        />
      ) : (
        <Empty icon="¶" title="No project selected">
          <p>Add a project to browse the docs in its repository.</p>
        </Empty>
      )}
    </section>
  );
}

type BrowserTab = "browse" | "graph";
const browserTabs: Array<{ id: BrowserTab; label: string }> = [
  { id: "browse", label: "Browse" },
  { id: "graph", label: "Graph" },
];

/**
 * Loads a doc graph once per project, scope, and reload, while enabled. The
 * result is kept when it is disabled again, so switching back shows it.
 */
function useDocGraph(
  projectID: string,
  scope: DocGraphScope,
  generation: number,
  enabled: boolean,
) {
  const key = `${projectID}\n${scope}\n${generation}`;
  const [state, setState] = useState<{
    key: string;
    graph: DocGraph | null;
    error: string;
  }>({ key: "", graph: null, error: "" });
  const loaded = state.key === key;
  useEffect(() => {
    if (!enabled || loaded) return;
    let active = true;
    getDocGraph(projectID, scope)
      .then((graph) => {
        if (active) setState({ key, graph, error: "" });
      })
      .catch((reason: unknown) => {
        if (active) setState({ key, graph: null, error: String(reason) });
      });
    return () => {
      active = false;
    };
  }, [projectID, scope, enabled, loaded, key]);
  return loaded ? state : { key, graph: null, error: "" };
}

function DocsBrowser({
  projectID,
  projects,
  generation,
  initialDoc,
  onOpenElsewhere,
  onOpenRun,
}: {
  projectID: string;
  projects: Project[];
  generation: number;
  onOpenRun?(runID: string): void;
  /** A doc to open on arrival, from another project's graph or backlinks. */
  initialDoc?: string;
  onOpenElsewhere(projectID: string, path: string): void;
}) {
  const [info, setInfo] = useState<DocsInfo | null>(null);
  const [error, setError] = useState("");
  const [filter, setFilter] = useState<DocFilter>(anyDoc);
  const [collapsed, setCollapsed] = useState<ReadonlySet<string>>(new Set());
  const [open, setOpen] = useState<{ path: string; fragment: string } | null>(
    initialDoc ? { path: initialDoc, fragment: "" } : null,
  );
  const [tab, setTab] = useState<BrowserTab>("browse");
  const [scope, setScope] = useState<DocGraphScope>("project");
  // The project graph also gives the open doc its backlinks, so it loads with
  // the tree; every project's graph loads only once it is asked for.
  const [wantAll, setWantAll] = useState(false);
  const projectGraph = useDocGraph(projectID, "project", generation, true);
  const allGraph = useDocGraph(projectID, "all", generation, wantAll);
  const shownGraph = scope === "all" ? allGraph : projectGraph;
  const projectName = useCallback(
    (id: string) => projects.find((project) => project.id === id)?.name ?? id,
    [projects],
  );
  const linkedFrom = useMemo(
    () =>
      projectGraph.graph && open
        ? backlinks(projectGraph.graph, projectID, open.path)
        : null,
    [projectGraph.graph, projectID, open],
  );
  // Docs visited before the open one, most recent last, for Back.
  const [history, setHistory] = useState<string[]>([]);

  useEffect(() => {
    let active = true;
    getDocs(projectID)
      .then((value) => {
        if (!active) return;
        setInfo(value);
        setError("");
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [projectID, generation]);

  const choices = useMemo(
    () => (info ? facets(info.tree) : { statuses: [], tags: [] }),
    [info],
  );
  const visible = useMemo(
    () => (info ? filterTree(info.tree, filter) : null),
    [info, filter],
  );

  const isDoc = useCallback(
    (path: string) => info !== null && findDoc(info.tree, path) !== undefined,
    [info],
  );

  function openDoc(path: string, fragment = "") {
    if (open && open.path !== path)
      setHistory((visited) => [...visited, open.path]);
    setOpen({ path, fragment });
    setTab("browse");
  }

  /** A doc opens here or in its own project; a GitHub repository in the browser. */
  function openNode(node: DocGraphNode) {
    if (node.kind === "external" && node.url) {
      openExternal(node.url);
      return;
    }
    if (node.kind !== "doc" || !node.path || !node.project) return;
    if (node.project === projectID) openDoc(node.path);
    else onOpenElsewhere(node.project, node.path);
  }

  if (!info) {
    return error ? (
      <Alert>{error}</Alert>
    ) : (
      <p className="text-sm text-muted">Loading docs…</p>
    );
  }
  if (allDocs(info.tree).length === 0) {
    return (
      <>
        {error && <Alert>{error}</Alert>}
        <Empty icon="¶" title="No docs yet">
          <p>
            Inkuna lists the Markdown files under{" "}
            <code className="font-mono text-xs wrap-anywhere">{info.path}</code>
            . Set <code className="font-mono text-xs">docs</code> on the
            repository in inkuna.yaml to read another directory.
          </p>
        </Empty>
      </>
    );
  }

  const shown = visible && allDocs(visible).length > 0 ? visible : null;
  return (
    <>
      {error && <Alert>{error}</Alert>}
      <Tabs
        tabs={browserTabs}
        active={tab}
        onChange={setTab}
        label="Docs"
        idPrefix="docs"
      />
      {tab === "graph" ? (
        <TabPanel idPrefix="docs" tab="graph">
          <div className="mt-4">
            <DocGraphPanel
              graph={shownGraph.graph}
              error={shownGraph.error}
              scope={scope}
              onScopeChange={(next) => {
                if (next === "all") setWantAll(true);
                setScope(next);
              }}
              projects={projects}
              projectID={projectID}
              onOpen={openNode}
            />
          </div>
        </TabPanel>
      ) : (
        <TabPanel idPrefix="docs" tab="browse">
          <div className="mt-4 grid grid-cols-[minmax(220px,300px)_minmax(0,1fr)] items-start gap-6 max-[900px]:grid-cols-1">
            <Panel className="!p-3">
              <div className="mb-3 grid grid-cols-2 gap-2">
                <Field label="Status" flush>
                  <select
                    className={inputClass}
                    value={filter.status}
                    onChange={(event) =>
                      setFilter({ ...filter, status: event.target.value })
                    }
                  >
                    <option value="">Any status</option>
                    {choices.statuses.map((status) => (
                      <option key={status} value={status}>
                        {status}
                      </option>
                    ))}
                    <option value="problems">Docs with problems</option>
                  </select>
                </Field>
                <Field label="Tag" flush>
                  <select
                    className={inputClass}
                    value={filter.tag}
                    onChange={(event) =>
                      setFilter({ ...filter, tag: event.target.value })
                    }
                  >
                    <option value="">Any tag</option>
                    {choices.tags.map((tag) => (
                      <option key={tag} value={tag}>
                        {tag}
                      </option>
                    ))}
                  </select>
                </Field>
              </div>
              {shown ? (
                <DocTree
                  tree={shown}
                  selected={open?.path ?? ""}
                  collapsed={collapsed}
                  onToggle={(path) =>
                    setCollapsed((current) => {
                      const next = new Set(current);
                      if (!next.delete(path)) next.add(path);
                      return next;
                    })
                  }
                  onSelect={(path) => openDoc(path)}
                />
              ) : (
                <p className="m-0 px-2 py-1.5 text-xs text-muted">
                  No docs match these filters.
                </p>
              )}
              <p className="mt-3 mb-0 px-2 font-mono text-[10px] wrap-anywhere text-faint">
                {info.path}
              </p>
            </Panel>
            <Panel className="!p-6">
              {open ? (
                <DocPane
                  projectID={projectID}
                  path={open.path}
                  fragment={open.fragment}
                  generation={generation}
                  isDoc={isDoc}
                  onOpenDoc={openDoc}
                  linkedFrom={linkedFrom}
                  linksError={projectGraph.error}
                  projectName={projectName}
                  onOpenNode={openNode}
                  onOpenRun={onOpenRun}
                  onBack={
                    history.length > 0
                      ? () => {
                          setOpen({
                            path: history[history.length - 1],
                            fragment: "",
                          });
                          setHistory(history.slice(0, -1));
                        }
                      : undefined
                  }
                />
              ) : (
                <p className="m-0 text-sm text-muted">
                  Choose a doc from the tree to read it.
                </p>
              )}
            </Panel>
          </div>
        </TabPanel>
      )}
    </>
  );
}

function scrollToAnchor(fragment: string) {
  let decoded = fragment;
  try {
    decoded = decodeURIComponent(fragment);
  } catch {
    // An undecodable fragment is matched as written.
  }
  document.getElementById(anchorID(decoded))?.scrollIntoView?.({
    block: "start",
  });
}

function DocPane({
  projectID,
  path,
  fragment,
  generation,
  isDoc,
  onOpenDoc,
  onBack,
  linkedFrom,
  linksError,
  projectName,
  onOpenNode,
  onOpenRun,
}: {
  projectID: string;
  path: string;
  fragment: string;
  generation: number;
  isDoc(path: string): boolean;
  onOpenDoc(path: string, fragment: string): void;
  onBack?(): void;
  /** Docs that link here, from the doc graph; null until it loads. */
  linkedFrom: Backlink[] | null;
  linksError: string;
  projectName(id: string): string;
  onOpenNode(node: DocGraphNode): void;
  onOpenRun?(runID: string): void;
}) {
  const [content, setContent] = useState<DocContent | null>(null);
  const [error, setError] = useState("");
  const [readBy, setReadBy] = useState<{
    path: string;
    reads: DocRead[];
    error: string;
  } | null>(null);
  useEffect(() => {
    let active = true;
    getDocReads(projectID, path)
      .then((reads) => {
        if (active) setReadBy({ path, reads, error: "" });
      })
      .catch((reason: unknown) => {
        if (active) setReadBy({ path, reads: [], error: String(reason) });
      });
    return () => {
      active = false;
    };
  }, [projectID, path, generation]);
  const reads = readBy?.path === path ? readBy : null;

  useEffect(() => {
    let active = true;
    setContent(null);
    setError("");
    getDocContent(projectID, path)
      .then((value) => {
        if (active) setContent(value);
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [projectID, path, generation]);

  // A link to a heading lands on it once the doc is on screen.
  useEffect(() => {
    if (content && fragment) scrollToAnchor(fragment);
  }, [content, fragment]);

  const doc = content?.doc;
  const problems = doc?.problems ?? [];
  return (
    <article aria-label={doc?.title || baseName(path)} className="min-w-0">
      <header className="mb-6 border-b border-line pb-4">
        <div className="flex min-w-0 flex-wrap items-center gap-3">
          {onBack && (
            <Button variant="ghost" className="!px-0 !py-0" onClick={onBack}>
              ← Back
            </Button>
          )}
          <span className="min-w-0 font-mono text-[11px] wrap-anywhere text-muted">
            {path}
          </span>
        </div>
        <h2 className="mt-2 mb-2 text-2xl font-semibold tracking-[-0.02em] wrap-anywhere text-ink-strong">
          {doc?.title || baseName(path)}
        </h2>
        {doc && (
          <>
            <div className="flex flex-wrap items-center gap-1.5">
              {doc.status && (
                <Chip tone={docStatusTones[doc.status] ?? "warn"}>
                  {doc.status}
                </Chip>
              )}
              {doc.tags.map((tag) => (
                <Chip key={tag} tone="accent">
                  {tag}
                </Chip>
              ))}
            </div>
            {(doc.lastUpdated || doc.author) && (
              <p className="mt-2 mb-0 text-xs text-muted">
                {[
                  doc.lastUpdated && `Updated ${doc.lastUpdated}`,
                  doc.author && `approved by ${doc.author}`,
                ]
                  .filter(Boolean)
                  .join(" · ")}
              </p>
            )}
          </>
        )}
        {problems.length > 0 && (
          <div
            role="note"
            aria-label="Problems"
            className="mt-3 border-l-2 border-warn-line bg-raised p-3 text-xs text-warn"
          >
            <b className="mb-1 block">Problems</b>
            <ul className="m-0 list-disc pl-5 wrap-anywhere">
              {problems.map((problem) => (
                <li key={problem}>{problem}</li>
              ))}
            </ul>
          </div>
        )}
      </header>
      {error && <Alert>{error}</Alert>}
      {!content && !error && (
        <p className="m-0 text-sm text-muted">Loading {path}…</p>
      )}
      {content && (
        <Markdown
          markdown={content.markdown}
          path={path}
          isDoc={isDoc}
          onOpenDoc={onOpenDoc}
          onAnchor={scrollToAnchor}
        />
      )}
      <section
        aria-label="Linked from"
        className="mt-8 border-t border-line pt-4"
      >
        <h3 className="m-0 mb-2 text-[10px] font-semibold tracking-widest text-muted uppercase">
          Linked from
        </h3>
        {linksError ? (
          <p className="m-0 text-xs wrap-anywhere text-danger">{linksError}</p>
        ) : linkedFrom === null ? (
          <p className="m-0 text-xs text-muted">Finding links…</p>
        ) : linkedFrom.length === 0 ? (
          <p className="m-0 text-xs text-muted">No doc links here.</p>
        ) : (
          <ul className="m-0 grid list-none gap-1 p-0 text-xs">
            {linkedFrom.map(({ node, count }) => (
              <li key={node.id} className="flex min-w-0 items-baseline gap-2">
                <button
                  type="button"
                  className="min-w-0 cursor-pointer border-0 bg-transparent p-0 text-left wrap-anywhere text-accent underline decoration-accent-line underline-offset-2"
                  onClick={() => onOpenNode(node)}
                >
                  {node.label}
                </button>
                <span className="min-w-0 font-mono text-[10px] wrap-anywhere text-muted">
                  {[
                    node.project !== projectID && node.project
                      ? projectName(node.project)
                      : "",
                    // An untitled doc is already labelled by its file name.
                    node.path !== node.label ? node.path : "",
                    count > 1 ? `${count} links` : "",
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section aria-label="Read by" className="mt-6 border-t border-line pt-4">
        <h3 className="m-0 mb-2 text-[10px] font-semibold tracking-widest text-muted uppercase">
          Read by
        </h3>
        {reads?.error ? (
          <p className="m-0 text-xs wrap-anywhere text-danger">{reads.error}</p>
        ) : reads === null ? (
          <p className="m-0 text-xs text-muted">Finding reads…</p>
        ) : reads.reads.length === 0 ? (
          <p className="m-0 text-xs text-muted">
            No agent has opened this doc in a recorded run.
          </p>
        ) : (
          <ul className="m-0 grid list-none gap-1 p-0 text-xs">
            {reads.reads.map((read, index) => (
              <li
                key={`${read.runId}-${read.openedAt}-${index}`}
                className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5"
              >
                {onOpenRun ? (
                  <button
                    type="button"
                    className="cursor-pointer border-0 bg-transparent p-0 font-mono text-accent underline decoration-accent-line underline-offset-2"
                    onClick={() => onOpenRun(read.runId)}
                  >
                    {read.taskId}
                  </button>
                ) : (
                  <span className="font-mono">{read.taskId}</span>
                )}
                <span className="min-w-0 wrap-anywhere text-ink-soft">
                  {read.stepId} · {read.agent} · {read.runtime}
                </span>
                <time
                  dateTime={read.openedAt}
                  className="font-mono text-[10px] text-muted"
                >
                  {formatTime(read.openedAt)}
                </time>
              </li>
            ))}
          </ul>
        )}
      </section>
    </article>
  );
}
