import { useEffect, useState } from "react";
import { inputClass } from "../common";
import { formatCommands, parseCommands } from "./model";

/**
 * Commands as editable text. The raw text is kept while typing so a
 * half-written quote is not rewritten under the cursor, but whenever the
 * stage's commands stop matching what the text parses to (the stage was
 * moved, replaced, or reloaded) the text follows the stage.
 */
export function CommandsInput({
  label,
  commands,
  onChange,
}: {
  label: string;
  commands: string[][] | undefined;
  onChange(commands: string[][]): void;
}) {
  const formatted = formatCommands(commands);
  const [text, setText] = useState(formatted);
  useEffect(() => {
    setText((current) =>
      formatCommands(parseCommands(current)) === formatted
        ? current
        : formatted,
    );
  }, [formatted]);
  return (
    <textarea
      aria-label={label}
      className={`${inputClass} min-h-20 resize-y font-mono`}
      value={text}
      placeholder="task test"
      onChange={(event) => {
        setText(event.target.value);
        onChange(parseCommands(event.target.value));
      }}
    />
  );
}
