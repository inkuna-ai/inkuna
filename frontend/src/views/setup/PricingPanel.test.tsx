import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import type {
  ConfigFieldError,
  ConfigFile,
  ModelPrice,
  PricingTable,
} from "../../types";
import { emptyConfigFile } from "../../types";
import { FieldErrorsContext } from "./overrides";
import { PricingPanel } from "./PricingPanel";

const price = (input: number, output: number): ModelPrice => ({
  provider: "OpenAI",
  input,
  cacheWrite: input * 1.25,
  longCacheWrite: input * 1.25,
  cacheRead: input / 10,
  output,
  source: "https://developers.openai.com/api/docs/pricing",
  asOf: "2026-10-19",
});

const builtIn: PricingTable = {
  version: 1,
  models: { "gpt-5.6-terra": price(2, 12), "gpt-5.6-luna": price(0.2, 1.2) },
};

// The panel inside a draft the way SetupView keeps one, so each edit can be
// read back from what would be saved.
function Harness({
  problems = [],
  onChange,
}: {
  problems?: ConfigFieldError[];
  onChange(file: ConfigFile): void;
}) {
  const [file, setFile] = useState<ConfigFile>(emptyConfigFile);
  return (
    <FieldErrorsContext.Provider value={problems}>
      <PricingPanel
        pricing={file.pricing}
        builtIn={builtIn}
        update={(mutate) =>
          setFile((current) => {
            const draft: ConfigFile = JSON.parse(JSON.stringify(current));
            mutate(draft);
            onChange(draft);
            return draft;
          })
        }
      />
    </FieldErrorsContext.Provider>
  );
}

describe("PricingPanel", () => {
  it("shows each built-in price with its source and overrides one in the draft", () => {
    let saved = emptyConfigFile;
    render(<Harness onChange={(file) => (saved = file)} />);
    const terra = screen.getByRole("row", { name: "gpt-5.6-terra price" });
    expect(terra).toHaveTextContent(
      /gpt-5\.6-terra.*2.*2\.5.*2\.5.*0\.2.*12.*developers\.openai\.com · 2026-10-19/,
    );
    expect(within(terra).queryByText("override")).toBeNull();

    fireEvent.click(
      screen.getByRole("button", {
        name: "Override the price of gpt-5.6-terra",
      }),
    );
    expect(saved.pricing?.models?.["gpt-5.6-terra"]).toEqual({
      input: 2,
      cacheWrite: 2.5,
      longCacheWrite: 2.5,
      cacheRead: 0.2,
      output: 12,
    });
    expect(within(terra).getByText("override")).toBeInTheDocument();
    expect(within(terra).getByText("inkuna.yaml")).toBeInTheDocument();

    // Clearing a cache rate leaves it out, for Go to fill from its fallback.
    fireEvent.change(screen.getByLabelText("gpt-5.6-terra cache read"), {
      target: { value: "" },
    });
    fireEvent.change(screen.getByLabelText("gpt-5.6-terra output"), {
      target: { value: "10.5" },
    });
    expect(saved.pricing?.models?.["gpt-5.6-terra"]).toEqual({
      input: 2,
      cacheWrite: 2.5,
      longCacheWrite: 2.5,
      output: 10.5,
    });

    fireEvent.click(
      screen.getByRole("button", {
        name: "Use the built-in price for gpt-5.6-terra",
      }),
    );
    expect(saved.pricing?.models).toBeUndefined();
  });

  it("adds a model the table lacks and prices Copilot's premium requests", () => {
    let saved = emptyConfigFile;
    render(<Harness onChange={(file) => (saved = file)} />);
    const add = screen.getByRole("button", { name: "Add a price" });
    expect(add).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Model to price"), {
      target: { value: "gpt-5.6-luna" },
    });
    expect(add).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Model to price"), {
      target: { value: "house-model" },
    });
    fireEvent.click(add);
    const row = screen.getByRole("row", { name: "house-model price" });
    expect(within(row).getByText("added")).toBeInTheDocument();
    // Go decides what is missing; the new price starts empty.
    expect(saved.pricing?.models?.["house-model"]).toEqual({});
    fireEvent.change(screen.getByLabelText("house-model input"), {
      target: { value: "0.5" },
    });
    expect(saved.pricing?.models?.["house-model"]).toEqual({ input: 0.5 });

    fireEvent.change(
      screen.getByLabelText("Price of a Copilot premium request"),
      { target: { value: "0.04" } },
    );
    expect(saved.pricing?.premiumRequest).toBe(0.04);
    fireEvent.change(
      screen.getByLabelText("Price of a Copilot premium request"),
      { target: { value: "" } },
    );
    expect(saved.pricing?.premiumRequest).toBeUndefined();

    fireEvent.click(
      screen.getByRole("button", { name: "Remove the price of house-model" }),
    );
    expect(saved.pricing?.models).toBeUndefined();
  });

  it("shows Go's reason beside the price it is about", () => {
    render(
      <Harness
        problems={[
          {
            field: "pricing.premiumRequest",
            message: "the premium request price must be dollars, zero or more",
          },
        ]}
        onChange={() => {}}
      />,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(
      "the premium request price must be dollars, zero or more",
    );
  });
});
