import { useRef, useState } from "react";
import { addProject, chooseDirectory, inspectRepository } from "../../bridge";
import type { Project, RepositoryCandidate, Runtime } from "../../types";
import { Alert, Button, Field, Modal, inputClass } from "../common";
import { RuntimeOptions } from "../runtimes/RuntimeOptions";
import { useRuntimes } from "../runtimes/useRuntimes";

/**
 * Connects a repository as a project. Go works out what a folder would
 * connect (its repository's root, a suggested name, or why it cannot), and
 * saves the project into inkuna.yaml; the form only shows what Go said.
 */
export function AddProjectDialog({
  defaultRuntime,
  onAdded,
  onClose,
}: {
  /** The runtime the form starts on: the orchestrator's. */
  defaultRuntime: Runtime;
  onAdded(project: Project): void;
  onClose(): void;
}) {
  const runtimes = useRuntimes();
  const [folder, setFolder] = useState("");
  const [candidate, setCandidate] = useState<RepositoryCandidate | null>(null);
  const [name, setName] = useState("");
  // Whether the person typed a name, read when an inspection answers, so a
  // name typed while it was pending is never replaced.
  const named = useRef(false);
  // Only the newest inspection may fill the form. Editing the folder starts
  // a new one, so an answer for an earlier folder is dropped.
  const inspection = useRef(0);
  const [runtime, setRuntime] = useState<Runtime>(defaultRuntime);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);

  async function inspect(path: string) {
    const request = ++inspection.current;
    setError("");
    if (!path.trim()) {
      setCandidate(null);
      return;
    }
    try {
      const found = await inspectRepository(path);
      if (request !== inspection.current) return;
      setCandidate(found);
      if (!named.current && !found.refused) setName(found.name);
    } catch (reason) {
      if (request !== inspection.current) return;
      setCandidate(null);
      setError(String(reason));
    }
  }

  async function choose() {
    setError("");
    try {
      const chosen = await chooseDirectory("Choose a repository");
      if (!chosen) return;
      setFolder(chosen);
      await inspect(chosen);
    } catch (reason) {
      setError(String(reason));
    }
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError("");
    setSaving(true);
    try {
      onAdded(await addProject(name, folder, runtime));
    } catch (reason) {
      setError(String(reason));
    } finally {
      setSaving(false);
    }
  }

  const refused = candidate?.refused;
  return (
    <Modal title="Add project" onClose={onClose}>
      <form onSubmit={(event) => void submit(event)}>
        {error && <Alert>{error}</Alert>}
        <Field label="Repository">
          <div className="flex gap-2">
            <input
              required
              aria-label="Repository folder"
              className={inputClass}
              value={folder}
              onChange={(event) => {
                inspection.current++;
                setFolder(event.target.value);
                setCandidate(null);
              }}
              onBlur={() => void inspect(folder)}
              placeholder="A folder in a local git repository"
            />
            <Button className="flex-none" onClick={() => void choose()}>
              Choose folder…
            </Button>
          </div>
        </Field>
        {refused && <Alert>{refused}</Alert>}
        {candidate && !refused && (
          <p
            role="status"
            className="-mt-2 mb-4 text-xs wrap-anywhere text-muted"
          >
            Connects {candidate.path}
            {candidate.restores &&
              `, bringing back ${candidate.restores} and its history`}
            .
          </p>
        )}
        <Field label="Name">
          <input
            className={inputClass}
            value={name}
            placeholder={candidate?.name}
            onChange={(event) => {
              setName(event.target.value);
              named.current = true;
            }}
          />
        </Field>
        <Field label="Default runtime">
          <select
            className={inputClass}
            value={runtime}
            onChange={(event) => setRuntime(event.target.value as Runtime)}
          >
            <RuntimeOptions runtimes={runtimes} value={runtime} />
          </select>
        </Field>
        <div className="mt-6 flex justify-end gap-2">
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            variant="primary"
            disabled={saving || Boolean(refused)}
          >
            {saving ? "Adding…" : "Add project"}
          </Button>
        </div>
      </form>
    </Modal>
  );
}
