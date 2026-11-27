import type { RuleStage } from "../../types";
import { describeOrigin } from "./model";

/**
 * Flow stages as a wrapping row. The arrow is a real flex child that travels
 * with its tile, so a wrapped line can never overlap the previous tile.
 */
export function StageChain({ stages }: { stages: RuleStage[] }) {
  return (
    <ol className="m-0 flex list-none flex-wrap gap-2 p-0">
      {stages.map((stage, index) => {
        const detail = [
          describeOrigin(stage.from),
          stage.use && stage.overrides?.length
            ? `overrides ${stage.overrides.join(", ")}`
            : "",
          stage.agent ? `agent ${stage.agent}` : "",
          stage.permission ?? "",
          stage.mcp?.length ? `MCP ${stage.mcp.join(", ")}` : "",
          stage.tracker?.length ? `tracker ${stage.tracker.join(", ")}` : "",
          stage.prompt ? `"${stage.prompt}"` : "",
        ]
          .filter(Boolean)
          .join(" · ");
        return (
          <li key={index} className="flex max-w-full min-w-0 gap-2">
            {index > 0 && (
              <span
                aria-hidden="true"
                className="flex-none self-center text-muted"
              >
                →
              </span>
            )}
            <div className="grid max-w-[280px] min-w-[min(150px,100%)] content-start gap-0.5 border border-line bg-field px-3 py-2.5 wrap-anywhere">
              <span className="font-mono text-[9px] tracking-widest text-accent uppercase">
                {stage.use ? `uses ${stage.use}` : stage.kind}
              </span>
              <b className="text-xs font-semibold">{stage.id || stage.use}</b>
              {detail && (
                <small className="text-[10px] leading-snug text-muted">
                  {detail}
                </small>
              )}
            </div>
          </li>
        );
      })}
    </ol>
  );
}
