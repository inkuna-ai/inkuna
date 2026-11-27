import { useState } from "react";
import type { DashboardData, Project } from "../types";
import { ProjectSyncStatus } from "./ProjectSyncStatus";
import { AddProjectDialog } from "./projects/AddProjectDialog";
import { RemoveProjectDialog } from "./projects/RemoveProjectDialog";
import {
  Button,
  Empty,
  Header,
  Metric,
  Metrics,
  SectionTitle,
  StateChip,
} from "./common";

const healthDotTones: Record<string, string> = {
  ready: "bg-ok",
  missing: "bg-faint",
  error: "bg-danger",
};

/** Small status dot for a dependency health row; unknown statuses read as missing. */
function HealthDot({ status }: { status: string }) {
  return (
    <span
      aria-hidden="true"
      className={`mt-[5px] size-[7px] flex-none rounded-full ${healthDotTones[status] ?? "bg-faint"}`}
    />
  );
}

export function Projects({
  data,
  onChange,
  onCompose,
  onBranches,
  onConnections,
  onSetup,
}: {
  data: DashboardData;
  onChange(value: DashboardData): void;
  onCompose(project: Project): void;
  /** Opens the Branches view for the project. */
  onBranches(project: Project): void;
  onConnections(): void;
  onSetup(): void;
}) {
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<Project | null>(null);
  const others = (projects: Project[] | undefined, id: string) =>
    (projects ?? []).filter((project) => project.id !== id);
  function added(project: Project) {
    // A project connected again leaves the removed list it was on.
    onChange({
      ...data,
      projects: [project, ...others(data.projects, project.id)],
      removedProjects: others(data.removedProjects, project.id),
    });
    setAdding(false);
  }
  const needsSetup = data.setup && !data.setup.configExists;
  return (
    <section>
      <Header
        eyebrow="Workspace"
        title="Projects"
        actions={
          <Button variant="primary" onClick={() => setAdding(true)}>
            + Add project
          </Button>
        }
      />
      {needsSetup && (
        <div
          role="status"
          className="mb-7 flex items-center justify-between gap-4 border border-accent-line bg-raised p-5 max-[600px]:flex-col max-[600px]:items-start"
        >
          <div className="min-w-0">
            <b className="text-ink-strong">Inkuna is not configured yet</b>
            <p className="mt-1.5 text-xs leading-relaxed wrap-anywhere text-muted">
              Create inkuna.yaml at{" "}
              {data.setup?.configPath || "the data directory"} to choose the
              orchestrator, register repositories, and store keys in the
              keychain.
            </p>
          </div>
          <Button variant="primary" className="flex-none" onClick={onSetup}>
            Open Setup →
          </Button>
        </div>
      )}
      <Metrics>
        <Metric
          value={String(data.projects.length)}
          label="Connected projects"
        />
        <Metric
          value={String(
            data.health.filter((check) => check.status === "ready").length,
          )}
          label="Healthy dependencies"
        />
        <Metric value={String(data.activeRuns ?? 0)} label="Active tasks" />
        <Metric
          value={String(data.pendingInputs ?? 0)}
          label="Awaiting your input"
        />
      </Metrics>
      <SectionTitle title="Your workspaces" aside="Local repositories only" />
      {data.projects.length === 0 ? (
        <Empty icon="◇" title="Bring in a repository">
          <p>
            Inkuna profiles it locally and keeps credentials with your existing
            CLIs.
          </p>
          <Button variant="primary" onClick={() => setAdding(true)}>
            Add your first project
          </Button>
        </Empty>
      ) : (
        <div className="grid grid-cols-[repeat(auto-fit,minmax(240px,1fr))] gap-3">
          {data.projects.map((project) => (
            <article
              className="flex min-w-0 flex-col border border-line bg-panel p-5"
              key={project.id}
            >
              <div>
                <StateChip state="ready" />
              </div>
              <h3 className="mt-4 mb-1 text-[17px] font-semibold break-words">
                {project.name}
              </h3>
              <p className="mb-4 text-sm wrap-anywhere text-muted">
                {project.repository}
              </p>
              <ProjectSyncStatus
                projectID={project.id}
                onOpen={() => onBranches(project)}
              />
              <div className="mt-auto flex items-center justify-between gap-3 border-t border-line pt-4">
                <span className="min-w-0 font-mono text-[11px] break-words">
                  {project.defaultRuntime}
                </span>
                <div className="flex flex-none items-center gap-3">
                  <Button
                    variant="ghost"
                    className="px-0 py-0 font-mono text-[11px] text-muted"
                    aria-label={`Remove ${project.name}`}
                    onClick={() => setRemoving(project)}
                  >
                    Remove…
                  </Button>
                  <Button
                    variant="ghost"
                    className="px-0 py-0 font-mono text-[11px]"
                    onClick={() => onCompose(project)}
                  >
                    New request →
                  </Button>
                </div>
              </div>
            </article>
          ))}
        </div>
      )}
      <SectionTitle
        title="Dependency health"
        aside="Authentication stays in each CLI"
      />
      <div className="grid grid-cols-[repeat(auto-fit,minmax(240px,1fr))] gap-3">
        {data.health.map((check) => (
          <div
            className="flex min-w-0 items-start gap-2 border border-line p-4"
            key={check.name}
          >
            <HealthDot status={check.status} />
            <div className="min-w-0 flex-1">
              <b className="block font-mono text-[13px] font-medium break-words">
                {check.name}
              </b>
              <small className="mt-1.5 block text-xs text-muted">
                {check.status}
                {check.required ? " · required" : " · optional"}
              </small>
              {check.detail && (
                <p className="mt-1.5 text-xs break-words text-muted">
                  {check.detail}
                </p>
              )}
              {/* Go names the screen that fixes the check; the view knows
                  none of the checks. */}
              {check.action === "connections" && (
                <Button
                  variant="ghost"
                  className="mt-2 px-0 py-0 text-xs"
                  onClick={onConnections}
                >
                  Connect →
                </Button>
              )}
              {check.action === "setup" && (
                <Button
                  variant="ghost"
                  className="mt-2 px-0 py-0 text-xs"
                  onClick={onSetup}
                >
                  Set up →
                </Button>
              )}
            </div>
          </div>
        ))}
      </div>
      {adding && (
        <AddProjectDialog
          defaultRuntime={data.defaults.orchestrator ?? "codex"}
          onAdded={added}
          onClose={() => setAdding(false)}
        />
      )}
      {removing && (
        <RemoveProjectDialog
          project={removing}
          onRemoved={(removal) => {
            onChange({
              ...data,
              projects: others(data.projects, removal.project.id),
              removedProjects: [
                removal.project,
                ...others(data.removedProjects, removal.project.id),
              ],
            });
            setRemoving(null);
          }}
          onClose={() => setRemoving(null)}
        />
      )}
    </section>
  );
}
