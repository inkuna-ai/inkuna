import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { answerInput, approveReview, getThread, listThreads } from "../bridge";
import { useInkunaEvent } from "../events";
import { formatTime } from "../format";
import type {
  A2AMessage,
  InputRequest,
  PermissionAsk,
  Thread,
  ThreadDetail,
} from "../types";
import {
  Alert,
  Button,
  Chip,
  Code,
  Empty,
  Header,
  Notice,
  StateChip,
  inputClass,
} from "./common";
import {
  ArtifactList,
  FeedbackMessage,
  FeedbackReplies,
  MessageBubble,
  OutboundPayload,
} from "./messages/Conversation";
import { GateLinks } from "./messages/GateLinks";
import { commandLines } from "./messages/commands";

function messageText(message: A2AMessage) {
  return message.parts
    .filter((part) => part.kind === "text")
    .map((part) => part.text)
    .join("\n");
}

function awaitingReply(thread: Thread) {
  return Boolean(thread.pendingInput && !thread.pendingInput.expired);
}

/** Threads of one run stay together, in the order the backend ranked them. */
function groupByRun(threads: Thread[]) {
  const groups: Array<{ key: string; title: string; threads: Thread[] }> = [];
  for (const thread of threads) {
    const key = thread.runId ?? thread.contextId;
    let group = groups.find((candidate) => candidate.key === key);
    if (!group) {
      group = { key, title: thread.runTitle ?? "Earlier run", threads: [] };
      groups.push(group);
    }
    group.threads.push(thread);
  }
  return groups;
}

export function MessagesView({
  selectedThreadID,
  onSelectThread,
  onChanged,
}: {
  selectedThreadID: string | null;
  onSelectThread(taskID: string | null): void;
  onChanged(): void;
}) {
  const [threads, setThreads] = useState<Thread[]>([]);
  const [detail, setDetail] = useState<ThreadDetail | null>(null);
  const [error, setError] = useState("");

  // A slow response for a thread the user already left must not replace the
  // conversation on screen.
  const requested = useRef<string | null>(null);
  const refresh = useCallback(async () => {
    const target = selectedThreadID;
    requested.current = target;
    try {
      setThreads(await listThreads());
      if (target) {
        const next = await getThread(target);
        if (requested.current === target) setDetail(next);
      }
      setError("");
    } catch (reason) {
      setError(String(reason));
    }
  }, [selectedThreadID]);

  useEffect(() => {
    void refresh();
    return () => {
      requested.current = null;
    };
  }, [refresh]);
  useInkunaEvent("inkuna:messages", refresh);
  useInkunaEvent("inkuna:inbox", refresh);

  // Without the desktop runtime there are no events, so poll instead.
  useEffect(() => {
    if (typeof window !== "undefined" && window.runtime) return;
    const interval = window.setInterval(() => void refresh(), 2000);
    return () => window.clearInterval(interval);
  }, [refresh]);

  // Open the first thread that needs a reply, or the most recent one.
  useEffect(() => {
    if (selectedThreadID || threads.length === 0) return;
    onSelectThread((threads.find(awaitingReply) ?? threads[0]).taskId);
  }, [selectedThreadID, threads, onSelectThread]);

  const groups = useMemo(() => groupByRun(threads), [threads]);
  const current = detail?.taskId === selectedThreadID ? detail : null;

  return (
    <section>
      <Header eyebrow="Conversations" title="Messages" />
      {error && <Alert>{error}</Alert>}
      {threads.length === 0 && !error ? (
        <Empty icon="✉" title="No conversations yet">
          <p>
            Every agent gets a thread here once a run starts. When an agent
            needs a decision, reply in its thread.
          </p>
        </Empty>
      ) : (
        <div className="grid grid-cols-[minmax(240px,320px)_minmax(0,1fr)] items-start gap-4 max-[900px]:grid-cols-1">
          <nav
            aria-label="Threads"
            className="grid min-w-0 content-start gap-4 overflow-y-auto p-1 min-[901px]:sticky min-[901px]:top-4 min-[901px]:max-h-[calc(100vh-2rem)] max-[900px]:max-h-[40vh]"
          >
            {groups.map((group) => (
              <div
                key={group.key}
                role="group"
                aria-label={group.title}
                className="grid min-w-0 gap-1.5"
              >
                <h2
                  className="m-0 truncate px-1 text-[11px] font-semibold text-ink-soft"
                  title={group.title}
                >
                  {group.title}
                </h2>
                {group.threads.map((thread) => (
                  <ThreadRow
                    key={thread.taskId}
                    thread={thread}
                    current={thread.taskId === selectedThreadID}
                    onSelect={() => onSelectThread(thread.taskId)}
                  />
                ))}
              </div>
            ))}
          </nav>
          <div className="min-w-0">
            {current ? (
              <ThreadPane
                key={current.taskId}
                thread={current}
                onAnswered={async () => {
                  await refresh();
                  onChanged();
                }}
              />
            ) : (
              <Empty title="Select a thread">
                <p>Pick a conversation to read it in full.</p>
              </Empty>
            )}
          </div>
        </div>
      )}
    </section>
  );
}

function ThreadRow({
  thread,
  current,
  onSelect,
}: {
  thread: Thread;
  current: boolean;
  onSelect(): void;
}) {
  return (
    <button
      type="button"
      aria-current={current ? "true" : undefined}
      className={`grid w-full min-w-0 cursor-pointer gap-1 rounded-md border border-l-2 p-3 text-left ${
        current
          ? "border-accent bg-raised"
          : "border-line bg-panel hover:border-line-strong"
      }`}
      onClick={onSelect}
    >
      <span className="flex min-w-0 flex-wrap items-center gap-1.5">
        <b className="min-w-0 truncate text-[13px] font-semibold text-ink-strong">
          {thread.agent}
        </b>
        {awaitingReply(thread) ? (
          <Chip tone="accent">needs reply</Chip>
        ) : thread.pendingInput?.expired ? (
          <Chip tone="muted">expired</Chip>
        ) : (
          <StateChip state={thread.state} />
        )}
      </span>
      {thread.preview && (
        <small className="line-clamp-2 min-w-0 text-[11px] leading-snug wrap-anywhere text-muted">
          {thread.preview}
        </small>
      )}
      <small className="text-[10px] text-faint">
        {formatTime(thread.updatedAt)} · {thread.messageCount}{" "}
        {thread.messageCount === 1 ? "message" : "messages"}
      </small>
    </button>
  );
}

type Entry =
  | { kind: "message"; at: string; index: number }
  | { kind: "question"; at: string; index: number };

function ThreadPane({
  thread,
  onAnswered,
}: {
  thread: ThreadDetail;
  onAnswered(): Promise<void>;
}) {
  // Questions are shown where they were asked, between the messages.
  const entries = useMemo(() => {
    // The agent's own copy of a question is dropped; the question card, which
    // also records the answer, stands in for it.
    const questions = new Set(thread.inputs.map((input) => input.question));
    const list: Entry[] = [
      ...thread.messages
        .map((message, index) => ({
          kind: "message" as const,
          at: message.timestamp ?? "",
          index,
        }))
        .filter(({ index }) => {
          const message = thread.messages[index];
          return !(
            message.role === "agent" &&
            questions.has(messageText(message).trim())
          );
        }),
      ...thread.inputs.map((input, index) => ({
        kind: "question" as const,
        at: input.createdAt,
        index,
      })),
    ];
    return list.sort((a, b) => a.at.localeCompare(b.at));
  }, [thread.messages, thread.inputs]);
  const pending = thread.pendingInput;
  // A review gate's thread links what the gate follows before its Approve,
  // and a human-input stage's what it asks about before the reply.
  const gate = thread.inputs.find((input) => input.review || input.stage);

  return (
    <article
      aria-label={`Thread with ${thread.agent}`}
      className="grid min-w-0 gap-3 border border-line bg-panel p-5"
    >
      <header className="flex min-w-0 flex-wrap items-center gap-1.5 border-b border-line pb-3">
        <h2 className="m-0 mr-1 min-w-0 text-lg font-semibold wrap-anywhere">
          {thread.agent}
        </h2>
        <StateChip state={thread.state} />
        {thread.stepId && <Chip>step {thread.stepId}</Chip>}
        {thread.runTitle && (
          <small className="min-w-0 basis-full text-xs wrap-anywhere text-muted">
            {thread.runTitle}
          </small>
        )}
      </header>
      {entries.length === 0 && (
        <p className="m-0 text-sm text-muted">No messages yet.</p>
      )}
      {/* A round's feedback is inside the first message's prompt; it is
          shown first as what the person or check sent. */}
      <FeedbackMessage items={thread.feedback ?? []} />
      {entries.map((entry, position) =>
        entry.kind === "message" ? (
          <Fragment key={thread.messages[entry.index].messageId}>
            <MessageBubble
              message={thread.messages[entry.index]}
              agent={thread.agent}
            />
            {/* What a remote advisor was sent sits between the request and
                anything the agent said back. */}
            {position === 0 && <OutboundPayload artifacts={thread.artifacts} />}
          </Fragment>
        ) : (
          <Question
            key={thread.inputs[entry.index].id}
            input={thread.inputs[entry.index]}
          />
        ),
      )}
      <FeedbackReplies items={thread.feedback ?? []} agent={thread.agent} />
      <ArtifactList artifacts={thread.artifacts} />
      {gate && (
        <GateLinks
          runId={gate.executionId}
          gateId={gate.stepId}
          answering={!gate.review}
        />
      )}
      {pending && !pending.expired && (
        <Reply input={pending} onAnswered={onAnswered} />
      )}
      {pending?.expired && (
        <Notice title="This question can no longer be answered">
          The run that asked it is not active any more. Start the task again if
          the decision is still needed.
        </Notice>
      )}
      {!pending && (
        <p className="m-0 border-t border-line pt-3 text-[11px] text-muted">
          You can reply when {thread.agent} asks a question. This thread is kept
          as a record.
        </p>
      )}
    </article>
  );
}

function Question({ input }: { input: InputRequest }) {
  return (
    <div className="grid min-w-0 gap-2 border border-accent-line bg-raised p-3">
      <small className="font-mono text-[10px] tracking-wider text-accent uppercase">
        {input.askedBy || input.agent} asked · {formatTime(input.createdAt)}
      </small>
      <p className="m-0 text-sm font-semibold wrap-anywhere whitespace-pre-wrap">
        {input.question}
      </p>
      {(input.permissions?.length ?? 0) > 0 && (
        <PermissionList asks={input.permissions!} />
      )}
      {input.context && (
        <p className="m-0 text-xs leading-relaxed wrap-anywhere whitespace-pre-wrap text-ink-soft">
          {input.context}
        </p>
      )}
      {input.state === "answered" && (
        <small className="text-[11px] wrap-anywhere text-ok">
          Answered{input.resolvedAt ? ` ${formatTime(input.resolvedAt)}` : ""}:{" "}
          {input.answer}
        </small>
      )}
      {input.state === "canceled" && (
        <small className="text-[11px] text-muted">
          Cancelled before it was answered
        </small>
      )}
    </div>
  );
}

/**
 * What a permission request asks to be allowed, numbered, each command in
 * its own block with one chained command per line, so the person can see
 * where each ends before allowing them.
 */
function PermissionList({ asks }: { asks: PermissionAsk[] }) {
  return (
    <ol
      aria-label="Asks to be allowed"
      className="m-0 grid min-w-0 list-none gap-2 p-0"
    >
      {asks.map((ask, index) => (
        <li key={index} className="grid min-w-0 gap-1">
          <small className="text-[11px] text-muted">
            {index + 1}. {ask.command ? "Run" : `Use ${ask.tool}`}
          </small>
          {ask.command ? (
            <Code>{commandLines(ask.command).join("\n")}</Code>
          ) : (
            ask.detail && <Code>{ask.detail}</Code>
          )}
        </li>
      ))}
    </ol>
  );
}

function Reply({
  input,
  onAnswered,
}: {
  input: InputRequest;
  onAnswered(): Promise<void>;
}) {
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  // A review gate is approved here, and a reply requests changes from its
  // target; one with no target can only be approved, as its question says.
  // A check pause takes one of its choices, which Go holds it to.
  const noReply = (input.review && !input.reviewTarget) || input.checkPause;

  async function act(action: () => Promise<unknown>) {
    setSending(true);
    setError("");
    try {
      await action();
      setText("");
      await onAnswered();
    } catch (reason) {
      setError(String(reason));
    } finally {
      setSending(false);
    }
  }
  const send = (value: string) => act(() => answerInput(input.id, value));

  return (
    <form
      className="grid min-w-0 gap-3 border-t border-line pt-3"
      onSubmit={(event) => {
        event.preventDefault();
        void send(text);
      }}
    >
      {error && <Alert flush>{error}</Alert>}
      {input.review && (
        <div className="flex flex-wrap gap-2" role="group" aria-label="Review">
          <Button
            variant="primary"
            disabled={sending}
            onClick={() => void act(() => approveReview(input.executionId))}
          >
            Approve
          </Button>
        </div>
      )}
      {((input.options?.length ?? 0) > 0 || input.previousAnswer) && (
        <div
          className="flex flex-wrap gap-2"
          role="group"
          aria-label={input.checkPause ? "Choices" : "Suggested answers"}
        >
          {/* A later round asks again; the answer given before is a click
              away. */}
          {input.previousAnswer && (
            <Button
              disabled={sending}
              title={input.previousAnswer}
              onClick={() => void send(input.previousAnswer!)}
            >
              Answer as before: {truncate(input.previousAnswer, 60)}
            </Button>
          )}
          {(input.options ?? []).map((option) => (
            <Button
              key={option}
              disabled={sending}
              onClick={() => void send(option)}
            >
              {option}
            </Button>
          ))}
        </div>
      )}
      {!noReply && (
        <>
          <label className="grid min-w-0 gap-1.5 text-xs text-ink-soft">
            {input.review
              ? `Request changes from ${input.reviewTarget}`
              : `Reply to ${input.askedBy || input.agent}`}
            <textarea
              className={`${inputClass} min-h-24 resize-y`}
              value={text}
              placeholder={
                input.review
                  ? "What should change…"
                  : "Type guidance for the agent…"
              }
              onChange={(event) => setText(event.target.value)}
            />
          </label>
          <div className="flex justify-end">
            <Button
              type="submit"
              variant={input.review ? "default" : "primary"}
              disabled={sending || !text.trim()}
            >
              {sending
                ? "Sending…"
                : input.review
                  ? "Request changes"
                  : "Send reply"}
            </Button>
          </div>
        </>
      )}
    </form>
  );
}

function truncate(text: string, limit: number) {
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}
