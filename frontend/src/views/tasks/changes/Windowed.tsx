import { Fragment, useEffect, useLayoutEffect, useRef, useState } from "react";
import { watchNear } from "./viewport";

/** Rows rendered or left out together. */
const chunkRows = 50;
/** A row's height before it has been laid out: one line of code. */
const rowEstimate = 20;

/**
 * Renders rows in chunks, only those within a screen of the viewport; the
 * rest keep their place as empty blocks of the height they last had, so a
 * 20,000-line diff scrolls like a short one.
 */
export function Windowed<T extends { key: string }>({
  rows,
  render,
}: {
  rows: T[];
  render(row: T): React.ReactNode;
}) {
  const chunks = Math.ceil(rows.length / chunkRows);
  return (
    <>
      {Array.from({ length: chunks }, (_, index) => (
        <Chunk
          key={index}
          rows={rows.slice(index * chunkRows, (index + 1) * chunkRows)}
          render={render}
        />
      ))}
    </>
  );
}

function Chunk<T extends { key: string }>({
  rows,
  render,
}: {
  rows: T[];
  render(row: T): React.ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [near, setNear] = useState(
    () => typeof IntersectionObserver === "undefined",
  );
  const shown = useRef(near);
  // The height the chunk had when it was last rendered, which its empty
  // block keeps.
  const height = useRef<number | null>(null);
  useEffect(
    () =>
      watchNear(ref.current!, (isNear, measured) => {
        if (shown.current && !isNear && measured > 0) height.current = measured;
        setNear(isNear);
      }),
    [],
  );
  const placeholder = height.current ?? rows.length * rowEstimate;
  useLayoutEffect(() => {
    if (near === shown.current) return;
    shown.current = near;
    const element = ref.current;
    if (!near || !element) return;
    // Rows can wrap, so a chunk first drawn above the screen may be taller
    // than its block was; scroll by the difference so what is on screen
    // stays put.
    const top = element.getBoundingClientRect().top;
    const delta = element.offsetHeight - placeholder;
    if (top < 0 && delta !== 0) window.scrollBy?.(0, delta);
  }, [near, placeholder]);
  if (!near)
    return <div ref={ref} aria-hidden="true" style={{ height: placeholder }} />;
  return (
    <div ref={ref}>
      {rows.map((row) => (
        <Fragment key={row.key}>{render(row)}</Fragment>
      ))}
    </div>
  );
}
