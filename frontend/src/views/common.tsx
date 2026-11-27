import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";

export function Header({
  eyebrow,
  title,
  actions,
}: {
  eyebrow: string;
  title: string;
  actions?: React.ReactNode;
}) {
  return (
    <header className="mb-7 flex items-end justify-between gap-4 border-b border-line pb-5 max-[600px]:flex-col max-[600px]:items-start">
      <div className="min-w-0">
        <span className="font-mono text-[10px] tracking-[0.16em] text-accent uppercase">
          {eyebrow}
        </span>
        <h1 className="mt-1 mb-0 text-[clamp(28px,4vw,42px)] leading-tight font-semibold tracking-[-0.04em] break-words">
          {title}
        </h1>
      </div>
      <div className="flex flex-none flex-wrap gap-2">{actions}</div>
    </header>
  );
}

/** Responsive strip of metrics; cells share borders without doubling them. */
export function Metrics({
  children,
  flush = false,
}: {
  children: React.ReactNode;
  /** Drop the outer margin when the parent already spaces its children. */
  flush?: boolean;
}) {
  return (
    <div
      className={`${flush ? "" : "mb-10 "}grid grid-cols-[repeat(auto-fit,minmax(160px,1fr))] gap-px border border-line bg-line`}
    >
      {children}
    </div>
  );
}

export function Metric({ value, label }: { value: string; label: string }) {
  return (
    <div className="flex min-w-0 flex-col bg-canvas p-5">
      <strong className="font-mono text-2xl font-medium break-words text-ink-strong">
        {value}
      </strong>
      <span className="mt-2 text-[11px] text-muted">{label}</span>
    </div>
  );
}

export function Eyebrow({ children }: { children: React.ReactNode }) {
  return (
    <span className="font-mono text-[10px] tracking-[0.16em] text-accent uppercase">
      {children}
    </span>
  );
}

export function SectionTitle({
  title,
  aside,
}: {
  title: string;
  aside?: React.ReactNode;
}) {
  return (
    <div className="mt-8 mb-4 flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
      <h2 className="m-0 text-[15px] font-semibold">{title}</h2>
      {aside && (
        <span className="text-[10px] tracking-widest text-muted uppercase">
          {aside}
        </span>
      )}
    </div>
  );
}

export function Empty({
  icon,
  title,
  children,
}: {
  icon?: string;
  title: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="border border-dashed border-line-strong bg-panel px-5 py-14 text-center">
      {icon && (
        <div aria-hidden="true" className="font-mono text-3xl text-accent">
          {icon}
        </div>
      )}
      <h2 className="my-2 text-xl font-semibold">{title}</h2>
      <div className="mx-auto max-w-[440px] text-sm text-muted [&>button]:mt-5 [&>p]:m-0">
        {children}
      </div>
    </div>
  );
}

export function Notice({
  title,
  children,
  className = "",
  ...props
}: {
  title?: string;
  children: React.ReactNode;
} & React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      {...props}
      className={`mt-5 border-l-2 border-accent bg-raised p-4 text-xs leading-relaxed text-muted ${className}`}
    >
      {title && <b className="mb-1 block text-ink">{title}</b>}
      {children}
    </div>
  );
}

const tabID = (prefix: string, id: string) => `${prefix}-tab-${id}`;
const tabPanelID = (prefix: string, id: string) => `${prefix}-tabpanel-${id}`;

/**
 * ARIA tabs with automatic activation: only the active tab is in the Tab
 * order, arrows/Home/End move between tabs, and each tab controls the
 * TabPanel rendered with the same idPrefix.
 */
export function Tabs<T extends string>({
  tabs,
  active,
  onChange,
  label,
  idPrefix,
}: {
  tabs: Array<{ id: T; label: string }>;
  active: T;
  onChange: (id: T) => void;
  label: string;
  idPrefix: string;
}) {
  function onKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    const current = tabs.findIndex((tab) => tab.id === active);
    const last = tabs.length - 1;
    const next =
      event.key === "ArrowRight"
        ? current === last
          ? 0
          : current + 1
        : event.key === "ArrowLeft"
          ? current <= 0
            ? last
            : current - 1
          : event.key === "Home"
            ? 0
            : event.key === "End"
              ? last
              : -1;
    if (next < 0 || next === current) return;
    event.preventDefault();
    onChange(tabs[next].id);
    document.getElementById(tabID(idPrefix, tabs[next].id))?.focus();
  }
  return (
    <div
      role="tablist"
      aria-label={label}
      className="flex flex-wrap border-b border-line"
      onKeyDown={onKeyDown}
    >
      {tabs.map((tab) => (
        <button
          key={tab.id}
          id={tabID(idPrefix, tab.id)}
          type="button"
          role="tab"
          aria-selected={active === tab.id}
          aria-controls={tabPanelID(idPrefix, tab.id)}
          tabIndex={active === tab.id ? 0 : -1}
          className={`flex-none cursor-pointer border-0 border-b-2 bg-transparent px-3.5 py-2 text-sm ${
            active === tab.id
              ? "border-accent text-accent"
              : "border-transparent text-ink-soft hover:text-ink-strong"
          }`}
          onClick={() => onChange(tab.id)}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}

/** The panel for the active tab of the Tabs sharing idPrefix. */
export function TabPanel({
  idPrefix,
  tab,
  children,
}: {
  idPrefix: string;
  tab: string;
  children: React.ReactNode;
}) {
  return (
    <div
      role="tabpanel"
      id={tabPanelID(idPrefix, tab)}
      aria-labelledby={tabID(idPrefix, tab)}
      className="min-w-0"
    >
      {children}
    </div>
  );
}

/** Preformatted output that scrolls inside its own box and never widens the page. */
export function Code({
  children,
  tall = false,
}: {
  children: React.ReactNode;
  tall?: boolean;
}) {
  return (
    <pre
      className={`m-0 max-w-full overflow-auto border border-line bg-field p-3.5 font-mono text-[11px] leading-relaxed wrap-anywhere whitespace-pre-wrap ${
        tall ? "max-h-[640px]" : "max-h-[480px]"
      }`}
    >
      {children}
    </pre>
  );
}

// Table cells: apply to <table>, <th>, <td>; wrap the table in TableScroll.
export const tableClass = "w-full border-collapse text-left text-[11px]";
export const thClass =
  "border-b border-line p-2.5 font-mono text-[9px] font-normal whitespace-nowrap text-muted uppercase";
export const tdClass = "border-b border-line p-2.5 align-top break-words";

const pill =
  "inline-block rounded-full border px-2 py-1 font-mono text-[9px] leading-none tracking-widest whitespace-nowrap uppercase";

const tones = {
  neutral: "border-line-strong text-ink-soft",
  muted: "border-line-strong text-muted",
  accent: "border-accent-line text-accent",
  green: "border-ok-line text-ok",
  warn: "border-warn-line text-warn",
  error: "border-danger-line text-danger",
};

type Tone = keyof typeof tones;

const stateTones: Record<string, Tone> = {
  completed: "green",
  ready: "green",
  applied: "green",
  running: "accent",
  applying: "accent",
  waiting: "warn",
  reviewing: "warn",
  cancelling: "warn",
  queued: "muted",
  pending: "muted",
  missing: "muted",
  discarded: "muted",
  failed: "error",
  refused: "error",
  "timed-out": "error",
  cancelled: "error",
  interrupted: "error",
  error: "error",
};

export function StateChip({ state }: { state: string }) {
  return (
    <span className={`${pill} ${tones[stateTones[state] ?? "muted"]}`}>
      {state}
    </span>
  );
}

export function Chip({
  children,
  tone = "neutral",
}: {
  children: React.ReactNode;
  tone?: Tone;
}) {
  return <span className={`${pill} ${tones[tone]}`}>{children}</span>;
}

export function Disclosure({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="border-t border-line">
      <button
        type="button"
        aria-expanded={open}
        className="w-full border-0 bg-transparent px-0 py-2.5 text-left text-xs text-ink-soft hover:text-ink-strong"
        onClick={() => setOpen((value) => !value)}
      >
        {open ? "▾" : "▸"} {label}
      </button>
      {open && <div className="pb-3">{children}</div>}
    </div>
  );
}

export function Alert({
  children,
  flush = false,
}: {
  children: React.ReactNode;
  flush?: boolean;
}) {
  return (
    <div
      role="alert"
      className={`${flush ? "" : "mb-4 "}border border-danger-line p-3 text-sm break-words text-danger`}
    >
      {children}
    </div>
  );
}

export function Panel({
  className = "",
  children,
}: {
  className?: string;
  children: React.ReactNode;
}) {
  return (
    <section className={`min-w-0 border border-line bg-panel p-5 ${className}`}>
      {children}
    </section>
  );
}

const buttonVariants = {
  default: "border-line-strong bg-raised text-ink hover:text-ink-strong",
  primary: "border-accent bg-accent font-bold text-accent-ink",
  danger: "border-danger-line bg-raised text-danger",
  ghost: "border-transparent bg-transparent text-accent",
};

export function Button({
  variant = "default",
  className = "",
  type = "button",
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: keyof typeof buttonVariants;
}) {
  return (
    <button
      type={type}
      className={`cursor-pointer rounded-md border px-3.5 py-2 text-sm disabled:cursor-not-allowed disabled:opacity-45 ${buttonVariants[variant]} ${className}`}
      {...props}
    />
  );
}

export const inputClass =
  "w-full rounded border border-line-strong bg-field p-2.5 text-sm text-ink-strong disabled:cursor-not-allowed disabled:opacity-55";

export function Field({
  label,
  children,
  flush = false,
}: {
  label: string;
  children: React.ReactNode;
  flush?: boolean;
}) {
  return (
    <label
      className={`${flush ? "" : "my-4 "}grid min-w-0 gap-1.5 text-xs text-ink-soft`}
    >
      {label}
      {children}
    </label>
  );
}

/** Wide tables scroll inside their own box instead of widening the page. */
export function TableScroll({
  children,
  flush = false,
}: {
  children: React.ReactNode;
  flush?: boolean;
}) {
  return (
    <div className={`${flush ? "" : "mt-5 "}max-w-full overflow-x-auto`}>
      {children}
    </div>
  );
}

const focusableSelector =
  'a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])';

export function Modal({
  title,
  onClose,
  wide = false,
  children,
}: {
  title: string;
  onClose: () => void;
  /** Room for a summary with lists, such as what a delete removes. */
  wide?: boolean;
  children: React.ReactNode;
}) {
  const titleID = useId();
  const dialog = useRef<HTMLDivElement>(null);
  const close = useRef(onClose);
  useEffect(() => {
    close.current = onClose;
  }, [onClose]);

  // Focus moves into the dialog on open, Tab cycles inside it, and the element
  // that had focus before gets it back on close.
  useEffect(() => {
    const node = dialog.current;
    if (!node) return;
    const previous = document.activeElement;
    const focusable = () =>
      Array.from(node.querySelectorAll<HTMLElement>(focusableSelector)).filter(
        (element) => !element.hasAttribute("disabled"),
      );
    (focusable()[0] ?? node).focus();

    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        close.current();
        return;
      }
      if (event.key !== "Tab") return;
      const items = focusable();
      if (items.length === 0) {
        event.preventDefault();
        node.focus();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const active = document.activeElement;
      if (event.shiftKey && (active === first || !node.contains(active))) {
        event.preventDefault();
        last.focus();
      } else if (
        !event.shiftKey &&
        (active === last || !node.contains(active))
      ) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      if (previous instanceof HTMLElement) previous.focus();
    };
  }, []);

  return createPortal(
    <div className="fixed inset-0 z-modal grid place-items-center overflow-y-auto bg-[#05090bc9] p-4">
      <div
        ref={dialog}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        tabIndex={-1}
        className={`w-full ${wide ? "max-w-[640px]" : "max-w-[480px]"} border border-line-strong bg-panel p-6 shadow-[0_24px_80px_#000b]`}
      >
        <h2 id={titleID} className="mt-0 mb-2 text-xl">
          {title}
        </h2>
        {children}
      </div>
    </div>,
    document.body,
  );
}
