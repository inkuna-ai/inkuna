import { useEffect, useState } from "react";
import { inputClass } from "../common";
import { formatExitCodes, parseExitCodes } from "./model";

/**
 * Exit codes as editable text, kept while typing so a trailing comma is not
 * removed under the cursor, and following the stage whenever its codes stop
 * matching what the text parses to. Anything but digits and separators is
 * dropped as it is typed; whether a code is allowed is Go's to say.
 */
export function ExitCodesInput({
  label,
  codes,
  onChange,
}: {
  label: string;
  codes: number[] | undefined;
  onChange(codes: number[]): void;
}) {
  const formatted = formatExitCodes(codes);
  const [text, setText] = useState(formatted);
  useEffect(() => {
    setText((current) =>
      formatExitCodes(parseExitCodes(current)) === formatted
        ? current
        : formatted,
    );
  }, [formatted]);
  return (
    <input
      aria-label={label}
      className={`${inputClass} font-mono`}
      inputMode="numeric"
      value={text}
      placeholder="3"
      onChange={(event) => {
        const typed = event.target.value.replace(/[^0-9,\s]/g, "");
        setText(typed);
        onChange(parseExitCodes(typed));
      }}
    />
  );
}
