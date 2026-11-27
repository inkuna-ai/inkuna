import { useEffect, useState } from "react";
import { inputClass } from "./common";

function split(text: string) {
  return text
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

/**
 * A comma-separated list. The raw text is kept while typing so a trailing
 * comma or space is not swallowed; the parsed list is reported on every edit.
 */
export function TagInput({
  label,
  values,
  onChange,
  placeholder,
}: {
  label: string;
  values: string[] | undefined;
  onChange(values: string[]): void;
  placeholder?: string;
}) {
  const joined = (values ?? []).join(", ");
  const [text, setText] = useState(joined);
  useEffect(() => {
    setText((current) =>
      split(current).join(", ") === joined ? current : joined,
    );
  }, [joined]);
  return (
    <input
      aria-label={label}
      className={inputClass}
      value={text}
      placeholder={placeholder}
      onChange={(event) => {
        setText(event.target.value);
        onChange(split(event.target.value));
      }}
    />
  );
}
