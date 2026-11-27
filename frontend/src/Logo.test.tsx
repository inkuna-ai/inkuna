import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Logo } from "./Logo";

describe("Logo", () => {
  it("is decorative unless a title names it", () => {
    const { container } = render(<Logo />);
    expect(container.querySelector("svg")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
  });

  it("becomes an accessible image when titled", () => {
    render(<Logo title="Inkuna" />);
    expect(screen.getByRole("img", { name: "Inkuna" })).toBeInTheDocument();
  });
});
