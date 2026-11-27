import { useEffect, useState } from "react";
import { getRunUsage } from "../../bridge";
import { useUsageVersion } from "../../events";
import type { RunUsage, WorkflowRun } from "../../types";
import {
  Metric,
  Metrics,
  StateChip,
  TableScroll,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import {
  cacheReadShare,
  compactTokens,
  costLabel,
  costSource,
  exact,
  totalCostLabel,
  totalInput,
} from "../usage";
import { InlineAlert, SubHeading } from "./shared";

const cell = `${tdClass} whitespace-nowrap`;
const number = `${tdClass} whitespace-nowrap text-right font-mono tabular-nums`;
const numberHead = `${thClass} text-right`;

/**
 * What the run's model calls consumed and cost: totals, then one row per
 * call in the order they finished, the planning calls first. Rows, totals,
 * and each cost's source are Go's; the view only formats them.
 */
export function UsagePanel({ run }: { run: WorkflowRun }) {
  const [loaded, setLoaded] = useState<{
    runID: string;
    usage: RunUsage;
  } | null>(null);
  const [error, setError] = useState("");
  // A call is recorded as it ends, and the run's state and events move on
  // then. The last event, not the count: a live run keeps only its latest
  // events, so the count stops changing once a long run reaches the cap.
  const last = run.events?.at(-1);
  const latest = last ? `${last.runId}:${last.sequence}:${last.timestamp}` : "";
  // A call cut short by cancelling can be stored after the run has ended.
  const stored = useUsageVersion(run.id);
  useEffect(() => {
    let active = true;
    getRunUsage(run.id)
      .then((usage) => {
        if (active) {
          setLoaded({ runID: run.id, usage });
          setError("");
        }
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [run.id, run.state, latest, stored]);
  const usage = loaded?.runID === run.id ? loaded.usage : null;
  if (error) {
    return (
      <div className="mt-5">
        <InlineAlert>{error}</InlineAlert>
      </div>
    );
  }
  if (!usage || usage.calls.length === 0) return null;
  const share = cacheReadShare(usage.total);
  const subscription = usage.calls.some(
    (call) =>
      call.cost?.source === "reported" && call.authMode === "subscription",
  );
  const estimated = usage.calls.some(
    (call) => call.cost?.source === "estimated",
  );
  // Each round's turns count from 1, so once a run has been sent back the
  // table says which round each call was in.
  const rounds = usage.calls.some((call) => (call.round ?? 1) > 1);
  return (
    <section className="mt-6 grid min-w-0 gap-3" aria-label="Token usage">
      <SubHeading>Token usage</SubHeading>
      <div className="min-w-0 [&_strong]:text-lg">
        <Metrics flush>
          <Metric
            value={compactTokens(totalInput(usage.total))}
            label="Input tokens"
          />
          <Metric
            value={share === null ? "—" : `${share}%`}
            label="Input read from cache"
          />
          <Metric
            value={compactTokens(usage.total.outputTokens)}
            label="Output tokens"
          />
          <Metric value={String(usage.calls.length)} label="Model calls" />
          <Metric value={totalCostLabel(usage.cost)} label="Cost" />
        </Metrics>
      </div>
      <TableScroll flush>
        <table className={tableClass}>
          <caption className="mb-2 text-left text-muted">
            Usage per call
          </caption>
          <thead>
            <tr>
              <th className={thClass}>Step</th>
              {rounds && <th className={numberHead}>Round</th>}
              <th className={numberHead}>Turn</th>
              <th className={thClass}>Call</th>
              <th className={thClass}>Model</th>
              <th className={thClass}>Outcome</th>
              <th className={numberHead}>Input</th>
              <th className={numberHead}>Cache write</th>
              <th className={numberHead}>Cache read</th>
              <th className={numberHead}>Output</th>
              <th className={numberHead}>Cost</th>
              <th className={numberHead}>Prompt bytes</th>
            </tr>
          </thead>
          <tbody>
            {usage.calls.map((call) => (
              <tr key={call.id}>
                <td className={cell}>{call.stepId}</td>
                {rounds && <td className={number}>{call.round || "—"}</td>}
                <td className={number}>{call.turn}</td>
                <td className={cell}>
                  {call.kind} · {call.agent}
                </td>
                <td className={cell}>
                  {[
                    call.runtime,
                    call.modelUsed || call.model || "default",
                  ].join(" · ")}
                </td>
                <td className={cell}>
                  <StateChip state={call.outcome} />
                </td>
                <td className={number}>{exact(call.usage.inputTokens)}</td>
                <td className={number}>{exact(call.usage.cacheWriteTokens)}</td>
                <td className={number}>{exact(call.usage.cacheReadTokens)}</td>
                <td className={number}>{exact(call.usage.outputTokens)}</td>
                <td className={number} title={costSource(call)}>
                  {costLabel(call.cost)}
                </td>
                <td className={number}>{exact(call.promptBytes)}</td>
              </tr>
            ))}
          </tbody>
          <tfoot>
            <tr className="font-semibold">
              <td className={cell} colSpan={rounds ? 6 : 5}>
                Run total
              </td>
              <td className={number}>{exact(usage.total.inputTokens)}</td>
              <td className={number}>{exact(usage.total.cacheWriteTokens)}</td>
              <td className={number}>{exact(usage.total.cacheReadTokens)}</td>
              <td className={number}>{exact(usage.total.outputTokens)}</td>
              <td className={number}>{totalCostLabel(usage.cost)}</td>
              <td className={number} />
            </tr>
          </tfoot>
        </table>
      </TableScroll>
      {(estimated || subscription) && (
        <p className="m-0 text-[11px] leading-relaxed text-muted">
          {estimated &&
            "≈ marks a cost estimated from the call's tokens, or its Copilot premium requests, with the prices in Settings. "}
          {subscription &&
            "Claude Code was signed in to a subscription: its reported cost is what the call would cost through the API, not what the subscription bills."}
        </p>
      )}
    </section>
  );
}
