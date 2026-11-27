import { useEffect, useState } from "react";
import { getRunMessages } from "../../bridge";
import type { RunTask, WorkflowRun } from "../../types";
import { Button, Chip, StateChip } from "../common";
import {
  ArtifactList,
  FeedbackMessage,
  FeedbackReplies,
  MessageBubble,
} from "../messages/Conversation";
import { GateLinks } from "../messages/GateLinks";
import { InlineAlert, TabBody } from "./shared";

export function MessagesTab({
  run,
  onOpenThread,
}: {
  run: WorkflowRun;
  onOpenThread?(taskID: string): void;
}) {
  const [tasks, setTasks] = useState<RunTask[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    getRunMessages(run.id)
      .then((value) => {
        if (active) {
          setTasks(value);
          setError("");
        }
      })
      .catch((reason: unknown) => {
        // Keep the conversation already on screen; only report the failure.
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [run.id, run.state, run.events?.length]);
  return (
    <TabBody>
      {error && <InlineAlert>{error}</InlineAlert>}
      {tasks.length === 0 && !error && (
        <p className="m-0 text-sm text-muted">
          No A2A tasks have been exchanged yet.
        </p>
      )}
      {tasks.map(({ task, feedback = [] }) => (
        <article
          className="grid min-w-0 gap-3 border border-line p-3.5"
          key={task.id}
        >
          <div className="flex min-w-0 flex-wrap items-center gap-1.5">
            <Chip tone="accent">{task.agent}</Chip>
            <StateChip state={task.status.state} />
            <small className="min-w-0 text-[11px] wrap-anywhere text-muted">
              {task.id}
            </small>
            {onOpenThread && (
              <Button
                variant="ghost"
                className="ml-auto"
                onClick={() => onOpenThread(task.id)}
              >
                Open thread →
              </Button>
            )}
          </div>
          {/* The round's feedback is inside the first message's prompt;
              it is shown first as what the person or check sent. */}
          <FeedbackMessage items={feedback} />
          {(task.history ?? []).map((message) => (
            <MessageBubble
              message={message}
              agent={task.agent}
              key={message.messageId}
            />
          ))}
          <FeedbackReplies items={feedback} agent={task.agent} />
          <ArtifactList artifacts={task.artifacts ?? []} />
          {(task.metadata?.review === true || task.metadata?.stage === true) &&
            typeof task.metadata.stepId === "string" && (
              <GateLinks
                runId={run.id}
                gateId={task.metadata.stepId}
                answering={task.metadata.review !== true}
              />
            )}
        </article>
      ))}
    </TabBody>
  );
}
