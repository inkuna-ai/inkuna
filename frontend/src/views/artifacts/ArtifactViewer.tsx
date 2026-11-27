import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";
import { copyText, readArtifact } from "../../bridge";
import { formatBytes } from "../../format";
import type { ArtifactInfo } from "../../types";
import { Alert, Button, Code } from "../common";
import { Markdown } from "../docs/Markdown";

/** What a link knows about an artifact before it is opened. */
export interface ArtifactTarget {
  digest: string;
  name: string;
  kind?: string;
  mediaType?: string;
  size?: number;
  round?: number;
  stepId?: string;
}

type Open = (target: ArtifactTarget) => void;

const ArtifactViewerContext = createContext<Open | null>(null);

/**
 * Holds the one artifact drawer of the app, so a link anywhere opens it
 * over whatever view is showing and the next link replaces it.
 */
export function ArtifactViewerProvider({ children }: { children: ReactNode }) {
  const [target, setTarget] = useState<ArtifactTarget | null>(null);
  const close = useCallback(() => setTarget(null), []);
  return (
    <ArtifactViewerContext.Provider value={setTarget}>
      {children}
      {target && (
        <ArtifactDrawer key={target.digest} target={target} onClose={close} />
      )}
    </ArtifactViewerContext.Provider>
  );
}

/**
 * A link to a stored artifact: its kind, name, size, and round, or only its
 * name where a table already shows the rest. It opens the app's drawer, or
 * one of its own where no provider holds one.
 */
export function ArtifactLink({
  target,
  nameOnly = false,
}: {
  target: ArtifactTarget;
  nameOnly?: boolean;
}) {
  const open = useContext(ArtifactViewerContext);
  const [local, setLocal] = useState<ArtifactTarget | null>(null);
  const details = [
    target.size !== undefined ? formatBytes(target.size) : "",
    target.round ? `round ${target.round}` : "",
  ].filter(Boolean);
  return (
    <>
      {nameOnly ? (
        <button
          type="button"
          title={`Open ${target.name}`}
          className="cursor-pointer border-0 bg-transparent p-0 text-left wrap-anywhere text-accent hover:underline"
          onClick={() => (open ?? setLocal)(target)}
        >
          {target.name}
        </button>
      ) : (
        <button
          type="button"
          title={`Open ${target.name}`}
          className="inline-flex max-w-full min-w-0 cursor-pointer items-baseline gap-1.5 border border-line bg-field px-2 py-1 text-left font-mono text-[11px] text-accent hover:border-accent-line"
          onClick={() => (open ?? setLocal)(target)}
        >
          {target.kind && (
            <span className="shrink-0 text-[9px] tracking-wider text-muted uppercase">
              {target.kind}
            </span>
          )}
          <span className="min-w-0 wrap-anywhere">{target.name}</span>
          {details.length > 0 && (
            <span className="shrink-0 text-[10px] text-muted">
              {details.join(" · ")}
            </span>
          )}
        </button>
      )}
      {local && (
        <ArtifactDrawer
          key={local.digest}
          target={local}
          onClose={() => setLocal(null)}
        />
      )}
    </>
  );
}

function isMarkdown(mediaType = "", name = "") {
  return mediaType.includes("markdown") || /\.(md|markdown)$/i.test(name);
}

function prettyJSON(text: string) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

// The drawer renders no doc links; a stable function keeps the rendered
// Markdown from being rebuilt on every render.
const noDoc = () => false;

/**
 * One artifact, read from Go and shown beside the view it was opened from:
 * Markdown rendered, other text as it is, with a copy button. It is not
 * modal, so the conversation and its Approve stay in reach.
 */
export function ArtifactDrawer({
  target,
  onClose,
}: {
  target: ArtifactTarget;
  onClose(): void;
}) {
  const titleID = useId();
  const drawer = useRef<HTMLElement>(null);
  const [artifact, setArtifact] = useState<ArtifactInfo | null>(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState<"" | "copied" | "failed">("");

  useEffect(() => {
    let active = true;
    readArtifact(target.digest)
      .then((value) => {
        if (active) setArtifact(value);
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [target.digest]);

  // Focus moves into the drawer, Escape closes it, and the link that opened
  // it has focus again after.
  const close = useRef(onClose);
  useEffect(() => {
    close.current = onClose;
  }, [onClose]);
  useEffect(() => {
    const previous = document.activeElement;
    drawer.current?.focus();
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") close.current();
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, []);

  const name = artifact?.name ?? target.name;
  const mediaType = artifact?.mediaType ?? target.mediaType;
  const kind = artifact?.kind ?? target.kind;
  const size = artifact?.size ?? target.size;
  const meta = [
    target.round ? `round ${target.round}` : "",
    artifact?.stepId ?? target.stepId ?? "",
    kind ?? "",
    mediaType ?? "",
    size !== undefined ? formatBytes(size) : "",
  ].filter(Boolean);
  const content = artifact?.content ?? "";

  function copy() {
    copyText(content).then(
      () => setCopied("copied"),
      () => setCopied("failed"),
    );
  }

  return createPortal(
    <aside
      ref={drawer}
      role="dialog"
      aria-labelledby={titleID}
      tabIndex={-1}
      className="fixed inset-y-0 right-0 z-overlay flex w-[min(720px,100vw)] flex-col border-l border-line-strong bg-panel shadow-[-24px_0_80px_#0009] outline-none"
    >
      <header className="flex min-w-0 items-start gap-3 border-b border-line px-5 py-4">
        <div className="grid min-w-0 flex-1 gap-1">
          <h2
            id={titleID}
            className="m-0 font-mono text-sm font-medium wrap-anywhere text-ink-strong"
          >
            {name}
          </h2>
          {meta.length > 0 && (
            <small className="font-mono text-[10px] wrap-anywhere text-muted">
              {meta.join(" · ")}
            </small>
          )}
        </div>
        {artifact && !artifact.binary && (
          <Button variant="ghost" onClick={copy}>
            {copied === "copied"
              ? "Copied"
              : copied === "failed"
                ? "Copy failed"
                : "Copy"}
          </Button>
        )}
        <Button variant="ghost" aria-label="Close" onClick={onClose}>
          ✕
        </Button>
      </header>
      <div className="min-h-0 flex-1 overflow-auto px-5 py-4">
        {error && <Alert flush>{error}</Alert>}
        {!artifact && !error && (
          <p className="m-0 text-sm text-muted">Loading…</p>
        )}
        {artifact?.binary && (
          <p className="m-0 text-sm text-muted">
            This artifact is binary, so it is not shown here.
          </p>
        )}
        {artifact &&
          !artifact.binary &&
          (isMarkdown(mediaType, name) ? (
            <Markdown
              markdown={content}
              path=""
              isDoc={noDoc}
              onOpenDoc={() => {}}
              onAnchor={(fragment) =>
                drawer.current
                  ?.querySelector(`[id="${CSS.escape(fragment)}"]`)
                  ?.scrollIntoView()
              }
            />
          ) : (
            <Code tall>
              {mediaType?.includes("json") ? prettyJSON(content) : content}
            </Code>
          ))}
        {artifact?.truncated && (
          <p className="mt-3 mb-0 text-xs text-muted">
            Only the first 256 KB is shown.
          </p>
        )}
      </div>
    </aside>,
    document.body,
  );
}
