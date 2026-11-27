import { Alert } from "../common";

/** Vertical stack shared by every detail tab body. */
export function TabBody({ children }: { children: React.ReactNode }) {
  return <div className="mt-5 grid min-w-0 gap-4">{children}</div>;
}

/** Alert placed inside a gap-spaced stack, so it drops its own bottom margin. */
export function InlineAlert({ children }: { children: React.ReactNode }) {
  return <Alert flush>{children}</Alert>;
}

/** Small uppercase heading used above code blocks inside a tab. */
export function SubHeading({ children }: { children: React.ReactNode }) {
  return (
    <h3 className="mt-2 mb-0 min-w-0 text-xs font-semibold tracking-widest break-words text-muted uppercase">
      {children}
    </h3>
  );
}

/** Compact label / value / meta block (agent context, commit header). */
export function Summary({
  label,
  children,
  meta,
}: {
  label: React.ReactNode;
  children: React.ReactNode;
  meta: React.ReactNode;
}) {
  return (
    <div className="grid min-w-0 gap-1.5 bg-field p-3">
      <b className="min-w-0 font-mono text-[9px] font-medium wrap-anywhere text-accent uppercase">
        {label}
      </b>
      <span className="min-w-0 text-xs wrap-anywhere">{children}</span>
      <small className="min-w-0 text-[11px] wrap-anywhere text-muted">
        {meta}
      </small>
    </div>
  );
}
