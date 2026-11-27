import { useCallback, useEffect, useRef, useState } from "react";
import {
  deleteRemoteAgent,
  listRemoteAgents,
  previewRemoteAgent,
  repinRemoteAgent,
  saveRemoteAgent,
  setRemoteAgentEnabled,
} from "../../bridge";
import { useInkunaEvent } from "../../events";
import { formatTime } from "../../format";
import type {
  RemoteAgent,
  RemoteAgentPreview,
  RemoteAgentRequest,
} from "../../types";
import {
  Alert,
  Button,
  Chip,
  Code,
  Disclosure,
  Field,
  Modal,
  Notice,
  inputClass,
} from "../common";

const defaultArtifactBytes = 262144;

function authLabel(auth?: string) {
  if (!auth) return "none";
  if (auth === "bearer") return "bearer token";
  return `API key in the ${auth.replace(/^header:/, "")} header`;
}

function host(url: string) {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

type Editing =
  | { mode: "connect" }
  | { mode: "settings"; agent: RemoteAgent }
  | { mode: "review"; agent: RemoteAgent };

/**
 * Remote A2A agents: connect by Agent Card URL, review what the card says and
 * what a stage would send, then pin and enable. They are read-only advisors.
 */
export function RemoteAgents({ secrets }: { secrets: string[] }) {
  const [agents, setAgents] = useState<RemoteAgent[]>([]);
  const [editing, setEditing] = useState<Editing | null>(null);
  const [removing, setRemoving] = useState<RemoteAgent | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");

  const latest = useRef(0);
  const refresh = useCallback(() => {
    const request = ++latest.current;
    listRemoteAgents()
      .then((value) => request === latest.current && setAgents(value))
      .catch(
        (reason) => request === latest.current && setError(String(reason)),
      );
  }, []);
  useEffect(refresh, [refresh]);
  useInkunaEvent("inkuna:config", refresh);

  async function act(work: () => Promise<RemoteAgent[]>, done: string) {
    if (busy) return;
    setBusy(true);
    try {
      setAgents(await work());
      setStatus(done);
      setError("");
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy(false);
      setRemoving(null);
    }
  }

  return (
    <section aria-labelledby="remote-agents-title" className="mt-10">
      <div className="flex flex-wrap items-end justify-between gap-3 border-b border-line pb-3">
        <div className="min-w-0">
          <h2 id="remote-agents-title" className="m-0 text-xl font-semibold">
            Remote advisors
          </h2>
          <p className="mt-1 mb-0 max-w-[720px] text-xs leading-relaxed text-muted">
            External A2A agents, connected by Agent Card URL. They never see the
            worktree, secrets, or other agents&apos; transcripts: a stage sends
            them a compact handoff and they answer with advice, which only a
            local agent can act on.
          </p>
        </div>
        {!editing && (
          <Button
            variant="primary"
            onClick={() => setEditing({ mode: "connect" })}
          >
            + Connect agent
          </Button>
        )}
      </div>
      {error && (
        <div className="mt-4">
          <Alert flush>{error}</Alert>
        </div>
      )}
      {status && <Notice role="status">{status}</Notice>}
      {editing?.mode === "review" ? (
        <CardReview
          agent={editing.agent}
          onClose={() => setEditing(null)}
          onRepinned={(next) => {
            setAgents(next);
            setEditing(null);
            setStatus(`Pinned the current card of ${editing.agent.name}.`);
          }}
        />
      ) : editing ? (
        <ConnectForm
          key={editing.mode === "settings" ? editing.agent.name : "new"}
          existing={editing.mode === "settings" ? editing.agent : undefined}
          taken={agents.map((agent) => agent.name)}
          secrets={secrets}
          onCancel={() => setEditing(null)}
          onSaved={(next, name) => {
            setAgents(next);
            setEditing(null);
            setStatus(`Saved ${name}.`);
          }}
        />
      ) : agents.length === 0 ? (
        <p className="mt-4 mb-0 text-sm text-muted">
          No remote agents are connected.
        </p>
      ) : (
        <div className="mt-4 grid grid-cols-[repeat(auto-fit,minmax(320px,1fr))] gap-3 max-[900px]:grid-cols-1">
          {agents.map((agent) => (
            <article
              aria-label={agent.name}
              className="flex min-w-0 flex-col border border-line bg-panel p-5"
              key={agent.name}
            >
              <div className="flex flex-wrap items-center gap-2">
                <Chip tone="accent">remote</Chip>
                <Chip>read-only</Chip>
                <Chip tone={agent.enabled ? "green" : "neutral"}>
                  {agent.enabled ? "enabled" : "disabled"}
                </Chip>
                {agent.auto && <Chip>auto staffing</Chip>}
                {agent.sendArtifacts && (
                  <Chip tone="warn">sends artifact text</Chip>
                )}
                {agent.allowInsecure && (
                  <Chip tone="warn">insecure: development only</Chip>
                )}
                {agent.shadowed && (
                  <Chip tone="warn">shadowed by a local agent</Chip>
                )}
                {!agent.pinned && <Chip tone="warn">card not reviewed</Chip>}
                {agent.secretStatus && !agent.secretStatus.available && (
                  <Chip tone="warn">secret has no value</Chip>
                )}
              </div>
              <h3 className="mt-4 mb-1 text-[17px] font-semibold break-words">
                {agent.name}
              </h3>
              <p className="m-0 text-sm leading-relaxed break-words text-muted">
                {agent.description || "No description on the card."}
              </p>
              <dl className="my-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]">
                <dt className="text-muted">Endpoint</dt>
                <dd className="m-0 min-w-0 wrap-anywhere">
                  {agent.pinned?.url ?? "—"}
                </dd>
                <dt className="text-muted">Authentication</dt>
                <dd className="m-0 min-w-0 wrap-anywhere">
                  {authLabel(agent.pinned?.auth)}
                  {agent.secret ? ` · ${agent.secret}` : ""}
                </dd>
                <dt className="text-muted">Skills</dt>
                <dd className="m-0 min-w-0 break-words">
                  {agent.skills?.join(", ") || "—"}
                </dd>
                <dt className="text-muted">Card reviewed</dt>
                <dd className="m-0 min-w-0 break-words">
                  {agent.pinned?.fetchedAt
                    ? formatTime(agent.pinned.fetchedAt)
                    : "never"}
                </dd>
              </dl>
              <div className="mt-auto flex flex-wrap justify-end gap-1.5 border-t border-line pt-3">
                <Button
                  aria-label={`${agent.enabled ? "Disable" : "Enable"} ${agent.name}`}
                  disabled={busy || (!agent.enabled && !agent.pinned)}
                  onClick={() =>
                    void act(
                      () => setRemoteAgentEnabled(agent.name, !agent.enabled),
                      agent.enabled
                        ? `${agent.name} is disabled and invisible to planning.`
                        : `${agent.name} is enabled.`,
                    )
                  }
                >
                  {agent.enabled ? "Disable" : "Enable"}
                </Button>
                <Button
                  aria-label={`Check the card of ${agent.name}`}
                  onClick={() => setEditing({ mode: "review", agent })}
                >
                  Check card
                </Button>
                <Button
                  aria-label={`Settings for ${agent.name}`}
                  onClick={() => setEditing({ mode: "settings", agent })}
                >
                  Settings
                </Button>
                <Button
                  variant="danger"
                  aria-label={`Remove ${agent.name}`}
                  onClick={() => setRemoving(agent)}
                >
                  Remove
                </Button>
              </div>
            </article>
          ))}
        </div>
      )}
      {removing && (
        <Modal
          title={`Remove ${removing.name}?`}
          onClose={() => busy || setRemoving(null)}
        >
          <p className="m-0 text-sm wrap-anywhere text-muted">
            Inkuna stops sending anything to {host(removing.cardUrl)}. Rules
            that name this agent will stop validating.
            {removing.secret
              ? ` The secret ${removing.secret} stays declared; remove it in Settings if nothing else uses it.`
              : ""}
          </p>
          <div className="mt-6 flex justify-end gap-2">
            <Button disabled={busy} onClick={() => setRemoving(null)}>
              Cancel
            </Button>
            <Button
              variant="danger"
              disabled={busy}
              onClick={() =>
                void act(
                  () => deleteRemoteAgent(removing.name),
                  `Removed ${removing.name}.`,
                )
              }
            >
              Remove agent
            </Button>
          </div>
        </Modal>
      )}
    </section>
  );
}

function Hint({ children }: { children: React.ReactNode }) {
  return (
    <span className="text-[11px] leading-relaxed text-muted">{children}</span>
  );
}

function CardSummary({ preview }: { preview: RemoteAgentPreview }) {
  const { card, pin } = preview;
  return (
    <dl className="my-3 grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-xs">
      <dt className="text-muted">Agent</dt>
      <dd className="m-0 min-w-0 wrap-anywhere">
        {card.name}
        {card.provider?.organization ? ` · ${card.provider.organization}` : ""}
        {card.version ? ` · v${card.version}` : ""}
      </dd>
      <dt className="text-muted">Description</dt>
      <dd className="m-0 min-w-0 wrap-anywhere">{card.description || "—"}</dd>
      <dt className="text-muted">Skills</dt>
      <dd className="m-0 min-w-0 wrap-anywhere">
        {card.skills?.length
          ? card.skills.map((skill) => (
              <span className="block" key={skill.id}>
                <b className="font-medium">{skill.name || skill.id}</b>
                {skill.description ? `: ${skill.description}` : ""}
              </span>
            ))
          : "—"}
      </dd>
      <dt className="text-muted">Endpoint</dt>
      <dd className="m-0 min-w-0 font-mono wrap-anywhere">{pin.url}</dd>
      <dt className="text-muted">Authentication</dt>
      <dd className="m-0 min-w-0 wrap-anywhere">{authLabel(pin.auth)}</dd>
      <dt className="text-muted">Transport</dt>
      <dd className="m-0 min-w-0 wrap-anywhere">
        A2A {pin.protocolVersion},{" "}
        {pin.streaming ? "streaming with polling as fallback" : "polling"}
      </dd>
      <dt className="text-muted">Card digest</dt>
      <dd className="m-0 min-w-0 font-mono text-[10px] wrap-anywhere">
        {card.digest}
      </dd>
    </dl>
  );
}

function ConnectForm({
  existing,
  taken,
  secrets,
  onCancel,
  onSaved,
}: {
  existing?: RemoteAgent;
  taken: string[];
  secrets: string[];
  onCancel(): void;
  onSaved(agents: RemoteAgent[], name: string): void;
}) {
  const [cardUrl, setCardUrl] = useState(existing?.cardUrl ?? "");
  const [preview, setPreview] = useState<RemoteAgentPreview | null>(null);
  const [name, setName] = useState(existing?.name ?? "");
  const [secret, setSecret] = useState(existing?.secret ?? "");
  const [auto, setAuto] = useState(existing?.auto ?? false);
  const [sendArtifacts, setSendArtifacts] = useState(
    existing?.sendArtifacts ?? false,
  );
  const [maxBytes, setMaxBytes] = useState(
    existing?.maxArtifactBytes || defaultArtifactBytes,
  );
  const [enabled, setEnabled] = useState(existing?.enabled ?? true);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  // A settings edit keeps the pin unless the card URL changes.
  const needsReview = !existing || cardUrl.trim() !== existing.cardUrl;
  const reviewed = preview !== null;
  const auth = preview?.pin.auth ?? existing?.pinned?.auth;

  async function fetchCard() {
    if (pending) return;
    setPending(true);
    try {
      const next = await previewRemoteAgent(cardUrl.trim(), existing?.name);
      setPreview(next);
      if (!existing && !name) setName(next.suggestedName);
      setError("");
    } catch (reason) {
      setPreview(null);
      setError(String(reason));
    } finally {
      setPending(false);
    }
  }

  async function save() {
    if (pending) return;
    if (!existing && taken.includes(name.trim())) {
      setError(`A remote agent named ${name.trim()} is already connected.`);
      return;
    }
    setPending(true);
    try {
      const request: RemoteAgentRequest = {
        name: name.trim(),
        cardUrl: cardUrl.trim(),
        secret,
        auto,
        sendArtifacts,
        maxArtifactBytes: sendArtifacts ? maxBytes : 0,
        enabled,
        digest: needsReview ? (preview?.card.digest ?? "") : "",
      };
      onSaved(await saveRemoteAgent(request), request.name);
    } catch (reason) {
      setError(String(reason));
    } finally {
      setPending(false);
    }
  }

  return (
    <form
      aria-label={existing ? `Settings for ${existing.name}` : "Connect agent"}
      className="mt-4 max-w-[760px] border border-line bg-panel p-5"
      onSubmit={(event) => {
        event.preventDefault();
        void (needsReview && !reviewed ? fetchCard() : save());
      }}
    >
      {error && <Alert>{error}</Alert>}
      <Field label="Agent Card URL">
        <input
          aria-label="Agent Card URL"
          className={inputClass}
          type="url"
          required
          placeholder="https://advisor.example.com/.well-known/agent-card.json"
          value={cardUrl}
          onChange={(event) => {
            setCardUrl(event.target.value);
            setPreview(null);
          }}
        />
        <Hint>
          Usually https://…/.well-known/agent-card.json. Fetching it sends no
          credential and nothing about your work.
        </Hint>
      </Field>
      {needsReview && !reviewed && (
        <div className="flex justify-end gap-2">
          <Button onClick={onCancel}>Cancel</Button>
          <Button variant="primary" type="submit" disabled={pending}>
            {pending ? "Fetching…" : "Fetch card"}
          </Button>
        </div>
      )}
      {preview && (
        <>
          <h3 className="mt-5 mb-0 text-sm font-semibold">
            What the card says
          </h3>
          <CardSummary preview={preview} />
        </>
      )}
      {(reviewed || !needsReview) && (
        <>
          <Field label="Agent name">
            <input
              aria-label="Agent name"
              className={inputClass}
              required
              disabled={Boolean(existing)}
              value={name}
              onChange={(event) => setName(event.target.value)}
            />
            <Hint>
              Rules and threads use this name. Lower-case letters, digits, and
              dashes.
            </Hint>
          </Field>
          <Field label="Token secret">
            <select
              aria-label="Token secret"
              className={inputClass}
              value={secret}
              onChange={(event) => setSecret(event.target.value)}
            >
              <option value="">None</option>
              {[...new Set([...secrets, ...(secret ? [secret] : [])])].map(
                (item) => (
                  <option key={item} value={item}>
                    {item}
                  </option>
                ),
              )}
            </select>
            <Hint>
              {auth
                ? `The card asks for a ${authLabel(auth)}. The value stays in the keychain; declare secrets in Settings.`
                : "The card asks for no credential."}
            </Hint>
          </Field>
          <fieldset className="m-0 mb-4 grid gap-2.5 border-0 p-0 text-xs">
            <legend className="mb-2 p-0 text-xs text-muted">
              What it may do
            </legend>
            <label className="flex items-start gap-2">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={auto}
                onChange={(event) => setAuto(event.target.checked)}
              />
              <span>
                Let the orchestrator pick it for read-only <code>auto</code>{" "}
                stages
                <span className="block text-muted">
                  Off, it is used only where a rule names it.
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={sendArtifacts}
                onChange={(event) => setSendArtifacts(event.target.checked)}
              />
              <span>
                Send the text of upstream artifacts (diffs, reports)
                <span className="block text-muted">
                  Off, it only sees their names, types, sizes, and digests. On,
                  your code can leave this machine.
                </span>
              </span>
            </label>
            {sendArtifacts && (
              <label className="flex items-center gap-2 pl-6">
                Up to
                <input
                  aria-label="Maximum artifact bytes"
                  className={`${inputClass} w-[120px]`}
                  type="number"
                  min={1024}
                  max={1048576}
                  step={1024}
                  value={maxBytes}
                  onChange={(event) => setMaxBytes(Number(event.target.value))}
                />
                bytes per stage
              </label>
            )}
            <label className="flex items-start gap-2">
              <input
                type="checkbox"
                className="mt-0.5"
                checked={enabled}
                onChange={(event) => setEnabled(event.target.checked)}
              />
              <span>Enabled: visible to planning</span>
            </label>
          </fieldset>
          {preview && (
            <Disclosure label="What a stage would send (sample)">
              <p className="mt-0 mb-2 text-[11px] leading-relaxed text-muted">
                One message per stage, to {host(preview.pin.url)} only. Never
                included: agent instructions, skills, allowed paths, commands,
                the repository path, secrets, or transcripts. The exact payload
                of every real request is kept in the agent&apos;s thread.
              </p>
              <Code>
                {sendArtifacts
                  ? preview.samplePayloadWithArtifacts
                  : preview.samplePayload}
              </Code>
            </Disclosure>
          )}
          <div className="mt-4 flex justify-end gap-2">
            <Button onClick={onCancel}>Cancel</Button>
            <Button variant="primary" type="submit" disabled={pending}>
              {needsReview ? "Pin card and save" : "Save"}
            </Button>
          </div>
        </>
      )}
    </form>
  );
}

function CardReview({
  agent,
  onClose,
  onRepinned,
}: {
  agent: RemoteAgent;
  onClose(): void;
  onRepinned(agents: RemoteAgent[]): void;
}) {
  const [preview, setPreview] = useState<RemoteAgentPreview | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    previewRemoteAgent("", agent.name)
      .then((value) => active && setPreview(value))
      .catch((reason) => active && setError(String(reason)));
    return () => {
      active = false;
    };
  }, [agent.name]);

  async function repin() {
    if (!preview || pending) return;
    setPending(true);
    try {
      onRepinned(await repinRemoteAgent(agent.name, preview.card.digest));
    } catch (reason) {
      setError(String(reason));
      setPending(false);
    }
  }

  const stale = preview !== null && (preview.changed || !agent.pinned);
  return (
    <div
      role="group"
      aria-label={`Card of ${agent.name}`}
      className="mt-4 max-w-[760px] border border-line bg-panel p-5"
    >
      {error && <Alert>{error}</Alert>}
      {!preview && !error && (
        <p className="m-0 text-sm text-muted">Fetching the card…</p>
      )}
      {preview && !stale && (
        <Notice className="mt-0" title="The card matches what you pinned">
          Nothing has changed since {formatTime(agent.pinned?.fetchedAt ?? "")}.
        </Notice>
      )}
      {preview && stale && (
        <>
          <Notice
            className="mt-0"
            title={
              agent.pinned
                ? "The card changed since you reviewed it"
                : "This card has not been reviewed"
            }
          >
            {agent.pinned
              ? "Runs refuse to send this agent anything until you pin the card it serves now."
              : "Pin it to let the agent be enabled."}
          </Notice>
          {preview.changes.length > 0 && (
            <table className="mt-4 w-full border-collapse text-left text-xs">
              <thead>
                <tr className="text-muted">
                  <th className="border-b border-line py-1.5 pr-3 font-normal">
                    Field
                  </th>
                  <th className="border-b border-line py-1.5 pr-3 font-normal">
                    Pinned
                  </th>
                  <th className="border-b border-line py-1.5 font-normal">
                    Now
                  </th>
                </tr>
              </thead>
              <tbody>
                {preview.changes.map((change) => (
                  <tr key={change.field} className="align-top">
                    <td className="border-b border-line py-1.5 pr-3">
                      {change.field}
                    </td>
                    <td className="border-b border-line py-1.5 pr-3 wrap-anywhere text-muted">
                      {change.before || "—"}
                    </td>
                    <td className="border-b border-line py-1.5 wrap-anywhere">
                      {change.after || "—"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
      {preview && <CardSummary preview={preview} />}
      <div className="mt-4 flex justify-end gap-2">
        <Button onClick={onClose}>Close</Button>
        {stale && (
          <Button
            variant="primary"
            disabled={pending}
            onClick={() => void repin()}
          >
            Pin this card
          </Button>
        )}
      </div>
    </div>
  );
}
