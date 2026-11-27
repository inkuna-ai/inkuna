import type {
  FeedbackItem,
  RunEvent,
  RunRound,
  StepRun,
  WorkflowRun,
} from "../../types";

/** The round the run detail shows, or every round. */
export type RoundPick = number | "all";

/** One round as the run detail shows it. */
export interface ShownRound {
  round: number;
  since?: string;
  steps: StepRun[];
  events: RunEvent[];
  hiddenEvents: number;
  feedback: FeedbackItem[];
  /** A person started the round by resuming the run after it stopped. */
  resumed?: boolean;
  /** The steps whose plan they edited for the round, which it ran again. */
  edited?: string[];
}

/**
 * The run's rounds, oldest first. Earlier rounds are as SQLite keeps them;
 * the current one is the run view's, which is fresher while the run is live
 * and which Go fills from SQLite once it is not.
 */
export function shownRounds(
  run: WorkflowRun,
  stored: RunRound[],
): ShownRound[] {
  const current = run.round ?? 1;
  const rounds: ShownRound[] = stored
    .filter((round) => round.round < current)
    .map((round) => ({
      round: round.round,
      since: round.since,
      steps: round.steps ?? [],
      events: round.events ?? [],
      hiddenEvents: round.hiddenEvents ?? 0,
      feedback: round.feedback ?? [],
      resumed: round.resumed,
      edited: round.edited,
    }));
  const latest = stored.find((round) => round.round === current);
  rounds.push({
    round: current,
    since: latest?.since,
    steps: run.steps ?? [],
    events: run.events ?? [],
    hiddenEvents: 0,
    feedback: latest?.feedback ?? [],
    resumed: latest?.resumed,
    edited: latest?.edited,
  });
  return rounds;
}

/** The steps that ran in their round, leaving out results kept from before. */
export function ranIn(round: ShownRound): StepRun[] {
  return round.steps.filter(
    (step) => (step.round ?? round.round) === round.round,
  );
}

/**
 * Whether a failed check, not a person, started a round. A resume sends
 * again the items its stopped steps never finished, a check's among them,
 * but a person started it.
 */
export function automatic(round: ShownRound): boolean {
  return (
    !round.resumed &&
    round.feedback.length > 0 &&
    round.feedback.every((item) => item.from === "check")
  );
}
