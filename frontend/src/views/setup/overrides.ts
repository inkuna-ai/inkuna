import { createContext, useContext } from "react";
import type { ConfigFieldError, ConfigOverride } from "../../types";

/** Fields an environment variable overrides, keyed by dotted field path. */
export const OverridesContext = createContext<Map<string, ConfigOverride>>(
  new Map(),
);

export function useOverride(field: string) {
  return useContext(OverridesContext).get(field);
}

/** Problems Go found in the draft, shown beside the control they are about. */
export const FieldErrorsContext = createContext<ConfigFieldError[]>([]);

export function useFieldErrors(field: string) {
  return useContext(FieldErrorsContext).filter(
    (problem) => problem.field === field,
  );
}
