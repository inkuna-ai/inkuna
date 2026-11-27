import type { CheckTotals } from "../../types";

/** How a run's checks read at a glance: any failed, all passed, or neither yet. */
export function checksTone(totals: CheckTotals): "error" | "green" | "neutral" {
  if (totals.failed > 0) return "error";
  if (totals.passed === totals.total) return "green";
  return "neutral";
}
