import { formatTime } from "../../format";
import type {
  A2AArtifact,
  A2AMessage,
  A2APart,
  FeedbackItem,
} from "../../types";
import { ArtifactLink } from "../artifacts/ArtifactViewer";
import { artifactDigest } from "../artifacts/links";
import { Code, Disclosure } from "../common";

/**
 * Inkuna marks the artifact holding what it sent to a remote agent. A remote
 * agent chooses its own artifacts' names but never their metadata, so the
 * marker, not the name, decides what is shown as having left the machine.
 */
function isOutbound(artifact: A2AArtifact) {
  return artifact.metadata?.outbound === true;
}

function prettyJSON(text: string) {
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}

/**
 * What left the machine for a remote advisor, shown before its reply. The
 * text is the request as it was sent; it is only re-indented for reading.
 */
export function OutboundPayload({ artifacts }: { artifacts: A2AArtifact[] }) {
  const sent = artifacts.filter(isOutbound);
  if (sent.length === 0) return null;
  return (
    <div className="min-w-0 border-l-2 border-warn bg-raised px-3 py-1">
      {sent.map((artifact) => {
        const text = artifact.parts.find((part) => part.kind === "text")?.text;
        const bytes = artifact.metadata?.bytes;
        return (
          <div key={artifact.artifactId} className="min-w-0">
            <small className="block pt-1.5 font-mono text-[10px] tracking-wider text-muted uppercase">
              Left this machine
            </small>
            <p className="my-1 text-xs wrap-anywhere text-ink-soft">
              {artifact.description}
              {typeof bytes === "number" ? ` (${bytes} bytes)` : ""}
            </p>
            <Disclosure label="Outbound payload">
              <Code>{prettyJSON(text ?? "")}</Code>
            </Disclosure>
          </div>
        );
      })}
    </div>
  );
}

function partsText(parts: A2AMessage["parts"]) {
  return (
    parts
      .filter((part) => part.kind === "text")
      .map((part) => part.text)
      .join("\n") || `(${parts.map((part) => part.kind).join(", ")})`
  );
}

/** Agent turns carry the accent rail; user turns are muted. */
export function MessageBubble({
  message,
  agent,
}: {
  message: A2AMessage;
  agent?: string;
}) {
  const fromUser = message.role === "user";
  return (
    <div
      className={`min-w-0 border-l-2 px-3 py-2.5 ${
        fromUser ? "border-line-strong bg-raised" : "border-accent bg-field"
      }`}
    >
      <small
        className={`block font-mono text-[10px] tracking-wider uppercase ${
          fromUser ? "text-muted" : "text-accent"
        }`}
      >
        {fromUser ? "user" : (agent ?? message.role)}
        {message.timestamp ? ` · ${formatTime(message.timestamp)}` : ""}
      </small>
      <p
        className={`mt-1.5 mb-0 text-xs leading-relaxed wrap-anywhere whitespace-pre-wrap ${
          fromUser ? "text-ink-soft" : "text-ink"
        }`}
      >
        {partsText(message.parts)}
      </p>
    </div>
  );
}

function numberField(part: A2APart, key: string) {
  const value = part.metadata?.[key];
  return typeof value === "number" ? value : undefined;
}

/**
 * What a message's artifacts carry: their text, such as a handoff's
 * summary, and every stored file as a link that opens it, never its text
 * inline. A file Inkuna did not store shows by name only.
 */
export function ArtifactList({ artifacts }: { artifacts: A2AArtifact[] }) {
  artifacts = artifacts.filter((artifact) => !isOutbound(artifact));
  if (artifacts.length === 0) return null;
  return (
    <div className="min-w-0">
      {artifacts.map((artifact) => {
        const files = artifact.parts.filter((part) => part.kind === "file");
        const text = artifact.parts
          .filter((part) => part.kind === "text")
          .map((part) => part.text)
          .join("\n");
        return (
          <div
            className="grid min-w-0 gap-1.5 border-t border-line py-2 text-xs"
            key={artifact.artifactId}
          >
            <b className="min-w-0 font-mono text-[10px] font-medium wrap-anywhere text-accent">
              {artifact.name ?? artifact.artifactId}
            </b>
            {(text || files.length === 0) && (
              <span className="min-w-0 wrap-anywhere whitespace-pre-wrap">
                {text || partsText(artifact.parts)}
              </span>
            )}
            {files.length > 0 && (
              <ul
                aria-label={`Files of ${artifact.name ?? artifact.artifactId}`}
                className="m-0 flex min-w-0 list-none flex-wrap gap-1.5 p-0"
              >
                {files.map((part, index) => {
                  const digest = artifactDigest(part.file?.uri);
                  const name = part.file?.name || digest || "file";
                  const kind = part.metadata?.kind;
                  return (
                    <li key={`${digest}-${index}`} className="min-w-0">
                      {digest ? (
                        <ArtifactLink
                          target={{
                            digest,
                            name,
                            mediaType: part.file?.mimeType,
                            kind: typeof kind === "string" ? kind : undefined,
                            size: numberField(part, "size"),
                            round: numberField(part, "round"),
                          }}
                        />
                      ) : (
                        <span className="font-mono text-[11px] wrap-anywhere text-muted">
                          {name}
                        </span>
                      )}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        );
      })}
    </div>
  );
}

const replyTones: Record<string, string> = {
  fixed: "text-ok",
  declined: "text-warn",
  question: "text-accent",
  "no-reply": "text-muted",
};

/**
 * What a round sent a target back with, as the message from whoever sent
 * it: the user, or a check that failed.
 */
export function FeedbackMessage({ items }: { items: FeedbackItem[] }) {
  if (items.length === 0) return null;
  const from = [...new Set(items.map((item) => item.from))].join(" and ");
  return (
    <div
      aria-label={`Feedback for round ${items[0].round}`}
      className="min-w-0 border-l-2 border-line-strong bg-raised px-3 py-2.5"
    >
      <small className="block font-mono text-[10px] tracking-wider text-muted uppercase">
        {from} · feedback for round {items[0].round}
      </small>
      <ul className="mt-1.5 mb-0 grid list-none gap-1 p-0 text-xs leading-relaxed text-ink-soft">
        {items.map((item) => (
          <li
            key={item.id}
            className="min-w-0 wrap-anywhere whitespace-pre-wrap"
          >
            <b className="mr-1.5 font-mono text-[10px] font-medium text-accent">
              {item.id}
            </b>
            <CommentPlace item={item} />
            {item.text}
            <CheckTail item={item} />
            <CommentExcerpt item={item} />
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * The target's reply on each item it was sent, once its step ended, as the
 * agent's message.
 */
export function FeedbackReplies({
  items,
  agent,
}: {
  items: FeedbackItem[];
  agent: string;
}) {
  const settled = items.filter((item) => item.status !== "open");
  if (settled.length === 0) return null;
  return (
    <div
      aria-label={`Replies for round ${settled[0].round}`}
      className="min-w-0 border-l-2 border-accent bg-field px-3 py-2.5"
    >
      <small className="block font-mono text-[10px] tracking-wider text-accent uppercase">
        {agent} · replies
      </small>
      <ul className="mt-1.5 mb-0 grid list-none gap-1 p-0 text-xs leading-relaxed text-ink">
        {settled.map((item) => (
          <li
            key={item.id}
            className="min-w-0 wrap-anywhere whitespace-pre-wrap"
          >
            <b className="mr-1.5 font-mono text-[10px] font-medium text-accent">
              {item.id}
            </b>
            <span className={replyTones[item.status] ?? "text-muted"}>
              {item.status === "no-reply" ? "no reply" : item.status}
            </span>
            {item.note ? ` — ${item.note}` : ""}
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Where a line comment is: its file and lines. */
export function CommentPlace({ item }: { item: FeedbackItem }) {
  if (!item.path || !item.line) return null;
  const end = item.endLine ?? item.line;
  const lines = end > item.line ? `${item.line}–${end}` : `${item.line}`;
  return (
    <span className="mr-1.5 font-mono text-[10px] text-ink-soft">
      {item.path}:{item.side === "old" ? `removed ${lines}` : lines}
    </span>
  );
}

/** The diff around a line comment, as its target was sent it. */
export function CommentExcerpt({ item }: { item: FeedbackItem }) {
  if (!item.excerpt) return null;
  return (
    <Disclosure label="The diff there">
      <Code>{item.excerpt}</Code>
    </Disclosure>
  );
}

/** The end of a failed check's log, as its target was sent it. */
export function CheckTail({ item }: { item: FeedbackItem }) {
  if (!item.tail) return null;
  return (
    <Disclosure label="End of the check's output">
      <Code>{item.tail}</Code>
    </Disclosure>
  );
}
