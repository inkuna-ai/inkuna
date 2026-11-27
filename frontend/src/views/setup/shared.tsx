import { useEffect, useState } from "react";
import { chooseDirectory } from "../../bridge";
import type { ConfigFile } from "../../types";
import { Button, Chip, Panel, inputClass } from "../common";
import { useFieldErrors, useOverride } from "./overrides";

/** Applies a mutation to a draft copy of the configuration file. */
export type UpdateConfig = (mutate: (draft: ConfigFile) => void) => void;

/** One settings card: heading, optional explanation, then the controls. */
export function SetupPanel({
  title,
  description,
  wide = false,
  children,
}: {
  title: string;
  description?: React.ReactNode;
  /** Tables need the full row; half a row squeezes their columns. */
  wide?: boolean;
  children: React.ReactNode;
}) {
  return (
    <Panel className={wide ? "col-span-full" : ""}>
      <h2 className="m-0 text-[15px] font-semibold break-words">{title}</h2>
      {description && (
        <p className="mt-2 mb-0 text-xs leading-relaxed text-muted">
          {description}
        </p>
      )}
      {children}
    </Panel>
  );
}

/** Checkbox with its label text beside it; the text wraps, the box does not. */
export function CheckRow({
  children,
  ...props
}: Omit<React.InputHTMLAttributes<HTMLInputElement>, "type" | "className">) {
  return (
    <label className="my-4 flex items-start gap-2.5 text-xs leading-relaxed text-ink-soft">
      <input
        type="checkbox"
        className="mt-0.5 flex-none accent-accent"
        {...props}
      />
      <span className="min-w-0">{children}</span>
    </label>
  );
}

/** Borderless inline action used inside table rows. */
export function LinkButton(
  props: Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "className">,
) {
  return (
    <button
      type="button"
      className="cursor-pointer border-0 bg-transparent p-0 text-[11px] whitespace-nowrap text-accent hover:underline disabled:cursor-not-allowed disabled:opacity-45"
      {...props}
    />
  );
}

/** Inline add-forms: controls wrap onto new rows instead of squeezing. */
/**
 * A whole-number input that keeps what the user typed but only reports values
 * at or above min. Clearing the field, or typing something out of range,
 * leaves the configuration as it was instead of storing 0.
 */
export function NumberInput({
  value,
  min,
  onCommit,
  ...props
}: {
  value: number;
  min: number;
  onCommit(value: number): void;
} & Omit<
  React.InputHTMLAttributes<HTMLInputElement>,
  "value" | "min" | "onChange" | "type"
>) {
  const [text, setText] = useState(String(value));
  // Follow outside changes (load, discard) unless they match what is typed.
  useEffect(() => {
    setText((current) => (Number(current) === value ? current : String(value)));
  }, [value]);
  const valid = /^\d+$/.test(text.trim()) && Number(text) >= min;
  return (
    <input
      {...props}
      type="number"
      min={min}
      step={1}
      className={inputClass}
      aria-invalid={!valid}
      value={text}
      onChange={(event) => {
        const next = event.target.value;
        setText(next);
        if (/^\d+$/.test(next.trim()) && Number(next) >= min) {
          onCommit(Number(next));
        }
      }}
      onBlur={() => {
        if (!valid) setText(String(value));
      }}
    />
  );
}

/** The problems Go reported for one field; renders nothing when there are none. */
export function FieldErrors({ field }: { field: string }) {
  const problems = useFieldErrors(field);
  if (problems.length === 0) return null;
  return (
    <ul role="alert" className="m-0 mt-2 grid list-none gap-1 p-0">
      {problems.map((problem) => (
        <li key={problem.message} className="text-[11px] text-danger">
          {problem.message}
        </li>
      ))}
    </ul>
  );
}

/**
 * A labelled setting. When an environment variable overrides the field, the
 * badge says which one and what it is set to, since edits to inkuna.yaml
 * would not take effect in this process.
 */
export function Setting({
  label,
  field,
  hint,
  children,
}: {
  label: string;
  field: string;
  hint?: string;
  children: React.ReactNode;
}) {
  const override = useOverride(field);
  return (
    <div className="my-4 grid min-w-0 gap-1.5 text-xs text-ink-soft">
      <label className="grid min-w-0 gap-1.5">
        <span className="flex min-w-0 flex-wrap items-center gap-2">
          {label}
          {override && (
            <Chip tone="warn">
              overridden by ${override.variable}={override.value}
            </Chip>
          )}
        </span>
        {children}
      </label>
      {hint && <small className="text-[11px] text-muted">{hint}</small>}
      <FieldErrors field={field} />
    </div>
  );
}

export function TextSetting({
  label,
  ariaLabel,
  field,
  value,
  placeholder,
  hint,
  onChange,
}: {
  label: string;
  /** The accessible name when it should differ from the visible label. */
  ariaLabel?: string;
  field: string;
  value: string | undefined;
  placeholder?: string;
  hint?: string;
  onChange(value: string): void;
}) {
  const override = useOverride(field);
  return (
    <Setting label={label} field={field} hint={hint}>
      <input
        className={inputClass}
        aria-label={ariaLabel}
        value={value ?? ""}
        placeholder={placeholder}
        disabled={Boolean(override)}
        onChange={(event) => onChange(event.target.value)}
      />
    </Setting>
  );
}

/** Opens the native folder picker; outside the desktop app it explains why not. */
export function BrowseButton({
  title,
  onPick,
  onError,
}: {
  title: string;
  onPick(path: string): void;
  onError(message: string): void;
}) {
  return (
    <Button
      onClick={() => {
        chooseDirectory(title)
          .then((path) => {
            if (path) onPick(path);
          })
          .catch((reason: unknown) => onError(String(reason)));
      }}
    >
      Browse…
    </Button>
  );
}

export const formGridClass =
  "mt-4 grid grid-cols-[repeat(auto-fit,minmax(180px,1fr))] items-center gap-3";
