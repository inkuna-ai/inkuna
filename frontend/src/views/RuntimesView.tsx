import { useCallback, useEffect, useRef, useState } from "react";
import {
  deleteRuntime,
  getConfiguration,
  listRuntimes,
  saveRuntime,
  testRuntime,
} from "../bridge";
import { useInkunaEvent } from "../events";
import type { Runtime, RuntimeInfo, RuntimeTest } from "../types";
import { blankTemplate, type RuntimeDraft } from "./runtimes/draft";
import { RuntimeForm } from "./runtimes/RuntimeForm";
import {
  Alert,
  Button,
  Chip,
  Code,
  Disclosure,
  Header,
  Modal,
  Notice,
} from "./common";

/** What a card says after its connection test. */
type TestState = { running: boolean; result?: RuntimeTest; error?: string };

function templateLines(runtime: RuntimeInfo): string {
  const { args, readOnlyArgs, writeArgs, stdin, output } = runtime.config;
  return [
    `args:          ${(args ?? []).join(" ") || "—"}`,
    `read-only:     ${(readOnlyArgs ?? []).join(" ") || "—"}`,
    `worktree-write: ${(writeArgs ?? []).join(" ") || "— (read-only runtime)"}`,
    `stdin:         ${stdin}`,
    `output:        ${output}`,
  ].join("\n");
}

export function RuntimesView() {
  const [runtimes, setRuntimes] = useState<RuntimeInfo[]>([]);
  const [secrets, setSecrets] = useState<string[]>([]);
  const [draft, setDraft] = useState<RuntimeDraft | null>(null);
  const [removing, setRemoving] = useState<RuntimeInfo | null>(null);
  const [tests, setTests] = useState<Record<Runtime, TestState>>({});
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  // The ref refuses a second confirmation before the pending state renders.
  const deleting = useRef(false);
  const [deletePending, setDeletePending] = useState(false);

  // Only the newest list request may update the view; configuration events
  // can start one while another is in flight.
  const latestList = useRef(0);
  const load = useCallback(() => {
    let active = true;
    const request = ++latestList.current;
    listRuntimes()
      .then((value) => {
        if (!active || request !== latestList.current) return;
        setRuntimes(value);
        setError("");
      })
      .catch(
        (reason: unknown) =>
          active && request === latestList.current && setError(String(reason)),
      );
    getConfiguration()
      .then(
        (value) =>
          active &&
          request === latestList.current &&
          setSecrets((value.file.secrets ?? []).map((secret) => secret.name)),
      )
      .catch(() => active && request === latestList.current && setSecrets([]));
    return () => {
      active = false;
    };
  }, []);
  useEffect(load, [load]);
  // Settings can change binaries' secrets and declarations too.
  useInkunaEvent("inkuna:config", load);

  // A test result describes the declaration it ran against. Each runtime has
  // a generation that moves on when its declaration changes, so a probe still
  // in flight cannot put a result for the old declaration back.
  const testGenerations = useRef<Record<Runtime, number>>({});
  function forgetTest(name: Runtime) {
    testGenerations.current[name] = (testGenerations.current[name] ?? 0) + 1;
    setTests((current) => {
      const rest = { ...current };
      delete rest[name];
      return rest;
    });
  }

  async function save(next: RuntimeDraft) {
    const saved = await saveRuntime(next.name.trim(), next.config);
    // A load still in flight predates this save.
    latestList.current++;
    setRuntimes(saved);
    setDraft(null);
    forgetTest(next.name.trim());
    setStatus(`Saved ${next.name.trim()} to inkuna.yaml.`);
  }

  async function remove(runtime: RuntimeInfo) {
    if (deleting.current) return;
    deleting.current = true;
    setDeletePending(true);
    try {
      const remaining = await deleteRuntime(runtime.name);
      latestList.current++;
      setRuntimes(remaining);
      forgetTest(runtime.name);
      setStatus(
        runtime.builtIn
          ? `${runtime.label} is back to its defaults.`
          : `Removed ${runtime.label}.`,
      );
      setError("");
    } catch (reason) {
      setError(String(reason));
    } finally {
      deleting.current = false;
      setDeletePending(false);
      setRemoving(null);
    }
  }

  async function test(name: Runtime) {
    const generation = testGenerations.current[name] ?? 0;
    const current = () => (testGenerations.current[name] ?? 0) === generation;
    setTests((tests) => ({ ...tests, [name]: { running: true } }));
    try {
      const result = await testRuntime(name);
      if (!current()) return;
      setTests((tests) => ({ ...tests, [name]: { running: false, result } }));
    } catch (reason) {
      if (!current()) return;
      setTests((tests) => ({
        ...tests,
        [name]: { running: false, error: String(reason) },
      }));
    }
  }

  return (
    <section>
      <Header
        eyebrow="Registry"
        title="Runtimes"
        actions={
          !draft && (
            <Button
              variant="primary"
              onClick={() => {
                setStatus("");
                setDraft({
                  name: "",
                  isNew: true,
                  builtIn: false,
                  config: blankTemplate,
                });
              }}
            >
              + Add runtime
            </Button>
          )
        }
      />
      {error && <Alert>{error}</Alert>}
      {status && <Notice role="status">{status}</Notice>}
      {draft ? (
        <RuntimeForm
          key={draft.name || "new"}
          draft={draft}
          taken={runtimes.map((runtime) => runtime.name)}
          secrets={secrets}
          onCancel={() => setDraft(null)}
          onSave={save}
        />
      ) : (
        <>
          <p className="mt-0 mb-4 max-w-[760px] text-sm leading-relaxed text-muted">
            A runtime is a headless CLI that agents run on. Claude Code, Codex,
            and Copilot are built in. Add another copy of one, or describe a
            different CLI with an argument template. Inkuna starts it without a
            shell and with only the secrets ticked for it.
          </p>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-3 max-[900px]:grid-cols-1">
            {runtimes.map((runtime) => {
              const state = tests[runtime.name];
              const template = !runtime.preset;
              return (
                <article
                  key={runtime.name}
                  aria-label={runtime.label}
                  className="flex min-w-0 flex-col border border-line bg-panel p-5"
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <Chip tone={runtime.builtIn ? "neutral" : "green"}>
                      {runtime.builtIn ? "built-in" : "added"}
                    </Chip>
                    <Chip tone="accent">
                      {template ? "template" : `${runtime.preset} adapter`}
                    </Chip>
                    <Chip tone={runtime.canWrite ? "neutral" : "warn"}>
                      {runtime.canWrite ? "read and write" : "read-only"}
                    </Chip>
                    {runtime.config.userConfig && (
                      <Chip tone="warn">loads your CLI setup</Chip>
                    )}
                    {(runtime.config.userMcp?.length ?? 0) > 0 && (
                      <Chip tone="warn">
                        your MCP servers: {runtime.config.userMcp?.join(", ")}
                      </Chip>
                    )}
                  </div>
                  <h3 className="mt-4 mb-1 text-[17px] font-semibold break-words">
                    {runtime.label}
                  </h3>
                  <dl className="my-2 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]">
                    <dt className="text-muted">Name</dt>
                    <dd className="m-0 min-w-0 font-mono break-words">
                      {runtime.name}
                    </dd>
                    <dt className="text-muted">Binary</dt>
                    <dd className="m-0 min-w-0 font-mono wrap-anywhere">
                      {runtime.binary}
                      {runtime.override && (
                        <>
                          {" "}
                          <Chip tone="warn">set by ${runtime.override}</Chip>
                        </>
                      )}
                    </dd>
                    <dt className="text-muted">Secrets</dt>
                    <dd className="m-0 min-w-0 break-words">
                      {runtime.config.secrets?.join(", ") || "none exported"}
                    </dd>
                  </dl>
                  {template && (
                    <Disclosure label="Argument template">
                      <Code>{templateLines(runtime)}</Code>
                    </Disclosure>
                  )}
                  {state && !state.running && (
                    <p
                      aria-live="polite"
                      className={`mt-2 mb-0 text-xs wrap-anywhere ${state.result?.available ? "text-ok" : "text-danger"}`}
                    >
                      {state.error ??
                        (state.result?.available
                          ? `Found ${state.result.path}: ${state.result.version || "no version output"}`
                          : state.result?.detail)}
                    </p>
                  )}
                  <div className="mt-auto flex flex-wrap justify-end gap-1.5 border-t border-line pt-3">
                    <Button
                      aria-label={`Test ${runtime.label}`}
                      disabled={state?.running}
                      onClick={() => void test(runtime.name)}
                    >
                      {state?.running ? "Testing…" : "Test connection"}
                    </Button>
                    <Button
                      aria-label={`Edit ${runtime.label}`}
                      onClick={() => {
                        setStatus("");
                        setDraft({
                          name: runtime.name,
                          isNew: false,
                          builtIn: runtime.builtIn,
                          config: runtime.config,
                        });
                      }}
                    >
                      Edit
                    </Button>
                    {(!runtime.builtIn || runtime.customized) && (
                      <Button
                        variant="danger"
                        aria-label={`${runtime.builtIn ? "Reset" : "Remove"} ${runtime.label}`}
                        onClick={() => setRemoving(runtime)}
                      >
                        {runtime.builtIn ? "Reset" : "Remove"}
                      </Button>
                    )}
                  </div>
                </article>
              );
            })}
          </div>
        </>
      )}
      {removing && (
        <Modal
          title={`${removing.builtIn ? "Reset" : "Remove"} ${removing.label}?`}
          onClose={() => deleting.current || setRemoving(null)}
        >
          <p className="m-0 text-sm wrap-anywhere text-muted">
            {removing.builtIn
              ? "This removes its entry from inkuna.yaml: the binary and exported secrets go back to their defaults."
              : "This removes it from inkuna.yaml together with its routing bands. The save is refused while the orchestrator or a repository still uses it; agents, rules, and projects that name it will stop planning until they are changed."}
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
              {removing.builtIn ? "Reset runtime" : "Remove runtime"}
            </Button>
          </div>
        </Modal>
      )}
    </section>
  );
}
