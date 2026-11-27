import { useRef, useState } from "react";
import { markStoryDone, openExternal } from "../../bridge";
import type { RunStory as Story, StoryDone, WorkflowRun } from "../../types";
import { Button } from "../common";
import { InlineAlert, Summary } from "./shared";

const trackerNames: Record<string, string> = {
  linear: "Linear",
  github: "GitHub issue",
  jira: "Jira",
};

/** A story as its tracker names it: ENG-12, or #12 for a GitHub issue. */
function storyLabel(story: Story): string {
  return story.tracker === "github" ? `#${story.id}` : story.id;
}

/**
 * The story the run is for, and Mark done when Go offers it: Go checks
 * whether the run's pull request merged and, when it did, fills in what the
 * tracker has not done already. What happened is Go's to say; it is on the
 * run's timeline too.
 */
export function RunStory({ run }: { run: WorkflowRun }) {
  const [pending, setPending] = useState(false);
  const [done, setDone] = useState<StoryDone | null>(null);
  const [error, setError] = useState("");
  // The ref refuses a second request before the pending state renders.
  const asking = useRef(false);
  const story = run.story;
  if (!story) return null;

  async function markDone() {
    if (asking.current) return;
    asking.current = true;
    setPending(true);
    setError("");
    try {
      setDone(await markStoryDone(run.id));
    } catch (reason) {
      setDone(null);
      setError(String(reason));
    } finally {
      asking.current = false;
      setPending(false);
    }
  }

  const pull = done?.pullRequest;
  return (
    <div className="mt-4 grid min-w-0 gap-2" aria-label="Run story">
      <Summary
        label={`Story · ${trackerNames[story.tracker] ?? story.tracker}`}
        meta={
          story.markDone
            ? "Once its pull request merges, Mark done moves it to Done."
            : "Inkuna updates it as the repository's tracker.yaml allows."
        }
      >
        {storyLabel(story)}
      </Summary>
      {story.markDone && (
        <div>
          <Button disabled={pending} onClick={() => void markDone()}>
            {pending ? "Checking the pull request…" : "Mark done"}
          </Button>
        </div>
      )}
      {done && (
        <div className="grid min-w-0 gap-1 text-xs" role="status">
          <span className="wrap-anywhere">
            {done.summary}
            {pull && (
              <>
                {" "}
                <button
                  type="button"
                  className="cursor-pointer border-0 bg-transparent p-0 text-accent underline"
                  onClick={() => openExternal(pull.url)}
                >
                  Open #{pull.number}
                </button>
              </>
            )}
          </span>
          {done.notes.map((note) => (
            <small className="text-[11px] wrap-anywhere text-muted" key={note}>
              {note}
            </small>
          ))}
        </div>
      )}
      {error && <InlineAlert>{error}</InlineAlert>}
    </div>
  );
}
