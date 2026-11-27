import { useEffect, useState } from "react";
import {
  authenticateClaude,
  authenticateGitHub,
  authenticateLinear,
  forgetLinearAuthentication,
  getAuthenticationStatus,
} from "../bridge";
import type { AuthenticationStatus, WriteAccess } from "../types";
import {
  Alert,
  Button,
  Chip,
  Eyebrow,
  Header,
  Panel,
  inputClass,
} from "./common";

const emptyAuthentication: AuthenticationStatus = {
  github: { provider: "github", authenticated: false, detail: "Checking…" },
  claude: { provider: "claude", authenticated: false, detail: "Checking…" },
  linear: { provider: "linear", authenticated: false, detail: "Checking…" },
};

const writeLabels: Record<
  WriteAccess["status"],
  { label: string; tone: "green" | "warn" | "muted" }
> = {
  "can-write": { label: "can write", tone: "green" },
  partial: { label: "can write some", tone: "warn" },
  "read-only": { label: "read only", tone: "warn" },
  unknown: { label: "writes unknown", tone: "muted" },
};

/** Whether the credential can write to the tracker, as Go found it. */
function WriteAccessLine({ write }: { write: WriteAccess }) {
  const shown = writeLabels[write.status] ?? writeLabels.unknown;
  return (
    <div role="group" className="mt-4 grid gap-1.5" aria-label="Tracker writes">
      <span>
        <Chip tone={shown.tone}>{shown.label}</Chip>
      </span>
      <p className="m-0 text-xs leading-relaxed break-words text-muted">
        {write.detail}
      </p>
    </div>
  );
}

/**
 * One provider card. The status text and actions sit at fixed ends of a flex
 * column so cards in a row line up whatever the detail length.
 */
function AuthCard({
  eyebrow,
  title,
  authenticated,
  detail,
  account,
  write,
  footnote,
  children,
}: {
  eyebrow: string;
  title: string;
  authenticated: boolean;
  detail: string;
  account?: string;
  write?: WriteAccess | null;
  footnote: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Panel className="flex flex-col">
      <div className="flex items-start justify-between gap-4">
        <div className="min-w-0">
          <Eyebrow>{eyebrow}</Eyebrow>
          <h2 className="mt-1 mb-0 text-[15px] font-semibold break-words">
            {title}
          </h2>
        </div>
        <span className="flex-none">
          <Chip tone={authenticated ? "green" : "muted"}>
            {authenticated ? "connected" : "not connected"}
          </Chip>
        </span>
      </div>
      <p className="mt-4 mb-0 text-xs leading-relaxed break-words text-muted">
        {detail}
      </p>
      {account && (
        <b className="mt-4 block text-sm font-semibold wrap-anywhere text-ok">
          {account}
        </b>
      )}
      {write && <WriteAccessLine write={write} />}
      <div className="mt-auto pt-5">
        {children}
        <small className="mt-4 block text-[11px] leading-relaxed text-muted">
          {footnote}
        </small>
      </div>
    </Panel>
  );
}

export function ConnectionsView({
  onAuthenticationChange,
}: {
  onAuthenticationChange(): Promise<void>;
}) {
  const [status, setStatus] = useState(emptyAuthentication);
  const [linearKey, setLinearKey] = useState("");
  const [busy, setBusy] = useState<
    "status" | "github" | "claude" | "linear" | ""
  >("status");
  const [error, setError] = useState("");

  useEffect(() => {
    getAuthenticationStatus()
      .then(setStatus)
      .catch((reason: unknown) => {
        setError(String(reason));
      })
      .finally(() => setBusy(""));
  }, []);

  async function connectGitHub() {
    setBusy("github");
    setError("");
    try {
      const github = await authenticateGitHub();
      setStatus((current) => ({ ...current, github }));
      await onAuthenticationChange();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy("");
    }
  }

  async function connectClaude() {
    setBusy("claude");
    setError("");
    try {
      const claude = await authenticateClaude();
      setStatus((current) => ({ ...current, claude }));
      await onAuthenticationChange();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy("");
    }
  }

  async function connectLinear(event: React.FormEvent) {
    event.preventDefault();
    const apiKey = linearKey;
    setLinearKey("");
    setBusy("linear");
    setError("");
    try {
      const linear = await authenticateLinear(apiKey);
      setStatus((current) => ({ ...current, linear }));
      await onAuthenticationChange();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy("");
    }
  }

  async function forgetLinear() {
    setBusy("linear");
    setError("");
    try {
      const linear = await forgetLinearAuthentication();
      setLinearKey("");
      setStatus((current) => ({ ...current, linear }));
      await onAuthenticationChange();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy("");
    }
  }

  return (
    <section>
      <Header eyebrow="Credentials stay with providers" title="Connections" />
      {error && <Alert>{error}</Alert>}
      <div className="grid grid-cols-[repeat(auto-fit,minmax(280px,1fr))] gap-4">
        <AuthCard
          eyebrow="GitHub CLI"
          title="GitHub"
          authenticated={status.github.authenticated}
          detail={status.github.detail}
          account={status.github.account}
          write={status.github.write}
          footnote={
            <>
              Inkuna opens the gh browser flow. GitHub credentials remain in
              gh&apos;s credential store.
            </>
          }
        >
          <Button
            variant="primary"
            disabled={busy !== "" || status.github.authenticated}
            onClick={() => void connectGitHub()}
          >
            {busy === "github"
              ? "Complete sign-in in browser…"
              : "Connect GitHub"}
          </Button>
        </AuthCard>

        <AuthCard
          eyebrow="Claude Code CLI"
          title="Claude"
          authenticated={status.claude.authenticated}
          detail={status.claude.detail}
          footnote={
            <>
              Inkuna opens Claude Code&apos;s browser login. Authentication
              remains in Claude Code&apos;s credential store.
            </>
          }
        >
          <Button
            variant="primary"
            disabled={busy !== "" || status.claude.authenticated}
            onClick={() => void connectClaude()}
          >
            {busy === "claude"
              ? "Complete sign-in in browser…"
              : "Connect Claude"}
          </Button>
        </AuthCard>

        <AuthCard
          eyebrow="Personal API key"
          title="Linear"
          authenticated={status.linear.authenticated}
          detail={status.linear.detail}
          account={status.linear.account}
          write={status.linear.write}
          footnote={
            <>
              The key is verified with Linear and held in memory for this
              session. It is never saved by Inkuna. Inkuna learns what it may
              write by asking Linear to change an issue that does not exist.
            </>
          }
        >
          <form
            className="grid gap-4"
            onSubmit={(event) => void connectLinear(event)}
          >
            <label className="grid gap-1.5 text-xs text-ink-soft">
              Linear API key
              <input
                className={inputClass}
                type="password"
                autoComplete="off"
                spellCheck={false}
                value={linearKey}
                onChange={(event) => setLinearKey(event.target.value)}
                placeholder="lin_api_…"
              />
            </label>
            <div className="flex flex-wrap justify-end gap-2">
              {status.linear.authenticated && (
                <Button
                  disabled={busy !== ""}
                  onClick={() => void forgetLinear()}
                >
                  Forget session key
                </Button>
              )}
              <Button
                type="submit"
                variant="primary"
                disabled={busy !== "" || !linearKey.trim()}
              >
                {busy === "linear" ? "Verifying…" : "Connect Linear"}
              </Button>
            </div>
          </form>
        </AuthCard>
      </div>
    </section>
  );
}
