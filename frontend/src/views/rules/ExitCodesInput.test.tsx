import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { describe, expect, it } from "vitest";
import { ExitCodesInput } from "./ExitCodesInput";

/** Holds the codes as a stage would, recording each list sent. */
function Harness({ seen }: { seen: number[][] }) {
  const [codes, setCodes] = useState<number[] | undefined>([3]);
  return (
    <ExitCodesInput
      label="Exit codes"
      codes={codes}
      onChange={(next) => {
        seen.push(next);
        setCodes(next);
      }}
    />
  );
}

describe("ExitCodesInput", () => {
  it("keeps the text while typing and sends the codes it lists", () => {
    const seen: number[][] = [];
    render(<Harness seen={seen} />);
    const input = screen.getByLabelText("Exit codes");
    expect(input).toHaveValue("3");
    fireEvent.change(input, { target: { value: "3," } });
    expect(input).toHaveValue("3,");
    fireEvent.change(input, { target: { value: "3, x75" } });
    expect(input).toHaveValue("3, 75");
    expect(seen).toEqual([[3], [3, 75]]);
  });
});
