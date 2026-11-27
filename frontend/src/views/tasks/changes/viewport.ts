/** Called when an element comes within reach of the viewport or leaves it. */
type Listener = (near: boolean, height: number) => void;

const listeners = new Map<Element, Listener>();
let observer: IntersectionObserver | null = null;

/**
 * Tells listener whether element is within a viewport's height of the
 * screen, with its height as last laid out, so a long diff renders only
 * what is near. One observer serves every element. Without
 * IntersectionObserver, as under jsdom, everything counts as near.
 */
export function watchNear(element: Element, listener: Listener): () => void {
  if (typeof IntersectionObserver === "undefined") {
    listener(true, 0);
    return () => {};
  }
  observer ??= new IntersectionObserver(
    (entries) => {
      for (const entry of entries)
        listeners.get(entry.target)?.(
          entry.isIntersecting,
          entry.boundingClientRect.height,
        );
    },
    { rootMargin: "100% 0px" },
  );
  listeners.set(element, listener);
  observer.observe(element);
  return () => {
    observer?.unobserve(element);
    listeners.delete(element);
  };
}
