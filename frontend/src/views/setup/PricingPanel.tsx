import { useEffect, useState } from "react";
import type {
  ConfigFile,
  ModelPrice,
  PricingRates,
  PricingTable,
} from "../../types";
import {
  Button,
  Chip,
  TableScroll,
  inputClass,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import {
  FieldErrors,
  LinkButton,
  Setting,
  SetupPanel,
  type UpdateConfig,
} from "./shared";

const rates: Array<{ key: keyof PricingRates; label: string }> = [
  { key: "input", label: "Input" },
  { key: "cacheWrite", label: "Cache write, 5 min" },
  { key: "longCacheWrite", label: "Cache write, 1 hour" },
  { key: "cacheRead", label: "Cache read" },
  { key: "output", label: "Output" },
];

// What Go charges for a cache rate left out; the field shows it as a hint.
const fallbacks: Partial<Record<keyof PricingRates, string>> = {
  cacheWrite: "= input",
  longCacheWrite: "= 5 min",
  cacheRead: "= input",
};

const number = `${tdClass} whitespace-nowrap text-right font-mono tabular-nums`;
const numberHead = `${thClass} text-right`;

/**
 * The prices calls are estimated with, in US dollars per million tokens.
 * Each built-in price names the page it was read from and when; a model
 * priced in inkuna.yaml replaces its built-in price or adds one. Go checks
 * every rate when the configuration is saved.
 */
export function PricingPanel({
  pricing,
  builtIn,
  update,
}: {
  pricing: ConfigFile["pricing"];
  builtIn: PricingTable | undefined;
  update: UpdateConfig;
}) {
  const [adding, setAdding] = useState("");
  const custom = pricing?.models ?? {};
  const models = [
    ...new Set([...Object.keys(builtIn?.models ?? {}), ...Object.keys(custom)]),
  ].sort();
  function setRates(model: string, next: PricingRates | undefined) {
    update((draft) => {
      const current = { ...(draft.pricing?.models ?? {}) };
      if (next) current[model] = next;
      else delete current[model];
      draft.pricing = {
        ...draft.pricing,
        models: Object.keys(current).length > 0 ? current : undefined,
      };
    });
  }
  // One rate is read and written within the draft, so edits that land
  // before the next render all keep each other.
  function setRate(
    model: string,
    key: keyof PricingRates,
    value: number | undefined,
  ) {
    update((draft) => {
      const current = draft.pricing?.models?.[model];
      if (!current) return;
      draft.pricing = {
        ...draft.pricing,
        models: {
          ...draft.pricing?.models,
          [model]: { ...current, [key]: value },
        },
      };
    });
  }
  const name = adding.trim();
  return (
    <SetupPanel
      wide
      title="Model prices"
      description="When a runtime reports no cost, Inkuna estimates one from the call's tokens with these prices. Each built-in price names the page it was read from and when. A model priced here replaces its built-in price or adds one; Go checks every rate when you save."
    >
      <Setting
        label="Copilot premium request, US dollars"
        field="pricing.premiumRequest"
        hint="Copilot bills premium requests, not tokens. Without a price, Copilot calls stay unknown."
      >
        <div className="max-w-60">
          <RateInput
            aria-label="Price of a Copilot premium request"
            placeholder="not priced"
            value={pricing?.premiumRequest}
            onCommit={(premiumRequest) =>
              update((draft) => {
                draft.pricing = { ...draft.pricing, premiumRequest };
              })
            }
          />
        </div>
      </Setting>
      <TableScroll flush>
        <table className={tableClass}>
          <caption className="mb-2 text-left text-muted">
            US dollars per million tokens
            {builtIn ? ` · built-in table v${builtIn.version}` : ""}
          </caption>
          <thead>
            <tr>
              <th className={thClass}>Model</th>
              {rates.map((rate) => (
                <th key={rate.key} className={numberHead}>
                  {rate.label}
                </th>
              ))}
              <th className={thClass}>Source</th>
              <th className={thClass}>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {models.map((model) => {
              const price = builtIn?.models[model];
              const own = custom[model];
              return (
                <tr key={model} aria-label={`${model} price`}>
                  <td className={`${tdClass} whitespace-nowrap`}>
                    <span className="flex items-center gap-2 font-mono text-ink">
                      {model}
                      {own && (
                        <Chip tone="accent">
                          {price ? "override" : "added"}
                        </Chip>
                      )}
                    </span>
                  </td>
                  {rates.map((rate) =>
                    own ? (
                      <td key={rate.key} className={`${tdClass} min-w-24`}>
                        <RateInput
                          aria-label={`${model} ${rate.label.toLowerCase()}`}
                          placeholder={fallbacks[rate.key] ?? "required"}
                          value={own[rate.key]}
                          onCommit={(value) => setRate(model, rate.key, value)}
                        />
                      </td>
                    ) : (
                      <td key={rate.key} className={number}>
                        {price ? price[rate.key] : ""}
                      </td>
                    ),
                  )}
                  <td className={`${tdClass} whitespace-nowrap text-muted`}>
                    {own
                      ? "inkuna.yaml"
                      : price && <PriceSource price={price} />}
                  </td>
                  <td className={`${tdClass} whitespace-nowrap`}>
                    {own ? (
                      <LinkButton
                        aria-label={`${price ? "Use the built-in price for" : "Remove the price of"} ${model}`}
                        onClick={() => setRates(model, undefined)}
                      >
                        {price ? "Use built-in" : "Remove"}
                      </LinkButton>
                    ) : (
                      <LinkButton
                        aria-label={`Override the price of ${model}`}
                        onClick={() =>
                          price && setRates(model, copyRates(price))
                        }
                      >
                        Override
                      </LinkButton>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </TableScroll>
      <FieldErrors field="pricing.models" />
      {Object.keys(custom)
        .sort()
        .map((model) => (
          <FieldErrors key={model} field={`pricing.models.${model}`} />
        ))}
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <input
          className={`${inputClass} max-w-64`}
          aria-label="Model to price"
          placeholder="model id, as the runtime reports it"
          value={adding}
          onChange={(event) => setAdding(event.target.value)}
        />
        <Button
          disabled={name === "" || models.includes(name)}
          onClick={() => {
            setRates(name, {});
            setAdding("");
          }}
        >
          Add a price
        </Button>
      </div>
    </SetupPanel>
  );
}

function copyRates(price: ModelPrice): PricingRates {
  return {
    input: price.input,
    cacheWrite: price.cacheWrite,
    longCacheWrite: price.longCacheWrite,
    cacheRead: price.cacheRead,
    output: price.output,
  };
}

/** The page a built-in price was read from, by host, and the day it was read. */
function PriceSource({ price }: { price: ModelPrice }) {
  let host = price.source;
  try {
    host = new URL(price.source).host;
  } catch {
    // Not a URL: show it as it is.
  }
  return (
    <span title={[price.source, price.note].filter(Boolean).join("\n")}>
      {host}
      {price.asOf && ` · ${price.asOf}`}
    </span>
  );
}

/**
 * A rate in dollars. Clearing it leaves the rate out, so Go uses its
 * fallback; anything else that reads as a number is sent for Go to check.
 */
function RateInput({
  value,
  onCommit,
  ...props
}: {
  value: number | undefined;
  onCommit(value: number | undefined): void;
} & Omit<
  React.InputHTMLAttributes<HTMLInputElement>,
  "value" | "onChange" | "type"
>) {
  const shown = value === undefined ? "" : String(value);
  const [text, setText] = useState(shown);
  // Follow outside changes (load, discard) unless they match what is typed.
  useEffect(() => {
    setText((current) =>
      current.trim() !== "" && Number(current) === value ? current : shown,
    );
  }, [shown, value]);
  return (
    <input
      {...props}
      inputMode="decimal"
      className={inputClass}
      value={text}
      onChange={(event) => {
        const next = event.target.value;
        setText(next);
        if (next.trim() === "") onCommit(undefined);
        else if (Number.isFinite(Number(next))) onCommit(Number(next));
      }}
      onBlur={() => setText(shown)}
    />
  );
}
