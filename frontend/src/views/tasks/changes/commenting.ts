import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { getRunComments } from "../../../bridge";
import type {
  CommentThread,
  DiffSide,
  ReviewTarget,
  RunComments,
} from "../../../types";

/**
 * Lines a person is writing a new comment on, numbered as the branch was at
 * commit when they picked them, and what they wrote so far.
 */
export interface Composer {
  path: string;
  side: DiffSide;
  line: number;
  endLine: number;
  commit: string;
  body: string;
  target: string;
}

/**
 * What the diff needs to show a run's comment threads and take new ones:
 * the threads placed on the commit the view shows, the stages a comment can
 * be for, and Go's reason none can be written now.
 */
export interface Commenting {
  runID: string;
  /** The branch tip the threads are placed on, which a new thread is written against. */
  commit: string;
  /** Whether Go has said what the threads are and whether comments can be written. */
  loaded: boolean;
  targets: ReviewTarget[];
  refusal: string;
  threads: CommentThread[];
  composer: Composer | null;
  setComposer(
    next: Composer | null | ((current: Composer | null) => Composer | null),
  ): void;
  /** Reads the threads again, after a change here or a round. */
  reload(): void;
}

export const CommentsContext = createContext<Commenting | null>(null);

export function useCommenting(): Commenting | null {
  return useContext(CommentsContext);
}

/** Whether new comments can be written: Go gave no reason they cannot. */
export const canComment = (commenting: Commenting | null) =>
  Boolean(
    commenting?.loaded && !commenting.refusal && commenting.targets.length > 0,
  );

// A comment being written is kept for its run while the Changes tab is
// closed, as drafts are kept in SQLite once saved.
const unsaved = new Map<string, Composer>();

/**
 * Reads a run's threads placed on commit to, again whenever version
 * changes, and keeps the comment being written.
 */
export function useComments(
  runID: string,
  to: string | undefined,
  version: string,
  onChanged?: () => void,
): Commenting & { error: string } {
  const [loaded, setLoaded] = useState<{
    key: string;
    comments: RunComments;
  } | null>(null);
  const [error, setError] = useState("");
  const [reloads, setReloads] = useState(0);
  const key = `${runID}:${to ?? ""}`;
  useEffect(() => {
    if (!to) return;
    let active = true;
    getRunComments(runID, to)
      .then((comments) => {
        if (!active) return;
        setLoaded({ key: `${runID}:${to}`, comments });
        setError("");
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [runID, to, version, reloads]);
  const [composer, setOwnComposer] = useState<Composer | null>(
    () => unsaved.get(runID) ?? null,
  );
  const setComposer = useCallback(
    (next: Composer | null | ((current: Composer | null) => Composer | null)) =>
      setOwnComposer((current) => {
        const value = typeof next === "function" ? next(current) : next;
        if (value) unsaved.set(runID, value);
        else unsaved.delete(runID);
        return value;
      }),
    [runID],
  );
  const reload = useCallback(() => {
    setReloads((count) => count + 1);
    onChanged?.();
  }, [onChanged]);
  const comments = loaded?.key === key ? loaded.comments : null;
  // Every line of the diff reads this, so it changes only when it must.
  return useMemo(
    () => ({
      runID,
      commit: comments?.to ?? to ?? "",
      loaded: comments !== null,
      targets: comments?.targets ?? [],
      refusal: comments?.refusal ?? "",
      threads: comments?.threads ?? [],
      composer,
      setComposer,
      reload,
      error,
    }),
    [runID, to, comments, composer, setComposer, reload, error],
  );
}

/**
 * The comment being written, when its lines were picked on the version the
 * diff shows; one picked on another version is not placed on these lines.
 */
export function currentComposer(commenting: Commenting | null) {
  const composer = commenting?.composer;
  return composer && composer.commit === commenting.commit ? composer : null;
}

/** "line 3" or "lines 3–5", with "removed" for the old side. */
export function linesLabel(side: DiffSide, line: number, endLine: number) {
  const lines = endLine > line ? `lines ${line}–${endLine}` : `line ${line}`;
  return side === "old" ? `removed ${lines}` : lines;
}
