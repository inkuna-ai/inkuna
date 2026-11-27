/** Where the doc graph keeps the reader's animation choice in this browser. */
export const animateKey = "inkuna.docs.graph.animate";

/**
 * Whether the doc graph animates focus: the reader's own choice from an
 * earlier session, or, until they make one, off when the system asks for
 * reduced motion. Storage can be unavailable, so a failed read falls back to
 * the system setting.
 */
export function initialAnimate(): boolean {
  try {
    const stored = window.localStorage.getItem(animateKey);
    if (stored === "on") return true;
    if (stored === "off") return false;
  } catch {
    // Fall through to the system setting.
  }
  return !(
    window.matchMedia?.("(prefers-reduced-motion: reduce)").matches ?? false
  );
}

/** Keeps the reader's choice for later sessions, when storage allows. */
export function rememberAnimate(animate: boolean) {
  try {
    window.localStorage.setItem(animateKey, animate ? "on" : "off");
  } catch {
    // The choice still holds for this session.
  }
}

/** Runs callback on the next frame; returns how to cancel it. */
export function nextFrame(callback: () => void): () => void {
  if (typeof window.requestAnimationFrame === "function") {
    const id = window.requestAnimationFrame(() => callback());
    return () => window.cancelAnimationFrame(id);
  }
  const id = window.setTimeout(callback, 16);
  return () => window.clearTimeout(id);
}
