/** How a patch is laid out: one column, or the two versions side by side. */
export type DiffMode = "unified" | "split";

/** Where the Changes view keeps the reader's layout choice in this browser. */
export const modeKey = "inkuna.diff.mode";

/** The reader's layout from an earlier session, else unified. */
export function storedMode(): DiffMode {
  try {
    return window.localStorage.getItem(modeKey) === "split"
      ? "split"
      : "unified";
  } catch {
    return "unified";
  }
}

/** Keeps the reader's layout for later sessions, when storage allows. */
export function rememberMode(mode: DiffMode) {
  try {
    window.localStorage.setItem(modeKey, mode);
  } catch {
    // The choice still holds for this session.
  }
}
