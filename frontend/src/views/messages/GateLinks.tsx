import { useEffect, useState } from "react";
import { getGateArtifacts } from "../../bridge";
import type { GateStep } from "../../types";
import { ArtifactLink } from "../artifacts/ArtifactViewer";

/**
 * What a review gate or a human-input stage follows, a click from its
 * Approve or its reply: each agent step before it, as Go walks the plan,
 * with the document and files it left.
 */
export function GateLinks({
  runId,
  gateId,
  answering = false,
}: {
  runId: string;
  gateId: string;
  /** A human-input stage, which is answered rather than approved. */
  answering?: boolean;
}) {
  const [loaded, setLoaded] = useState<{
    key: string;
    steps: GateStep[];
  } | null>(null);
  const [error, setError] = useState("");
  const key = `${runId}:${gateId}`;
  useEffect(() => {
    let active = true;
    getGateArtifacts(runId, gateId)
      .then((steps) => {
        if (active) {
          setLoaded({ key: `${runId}:${gateId}`, steps });
          setError("");
        }
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [runId, gateId]);
  const steps = loaded?.key === key ? loaded.steps : null;
  if (error && !steps) {
    return (
      <p className="m-0 text-[11px] wrap-anywhere text-danger">
        What this gate follows could not be listed: {error}
      </p>
    );
  }
  if (!steps) return null;
  const heading = answering ? "Read before answering" : "Read before approving";
  return (
    <section
      aria-label={heading}
      className="grid min-w-0 gap-2.5 border border-line bg-raised p-3"
    >
      <small className="font-mono text-[10px] tracking-wider text-muted uppercase">
        {heading}
      </small>
      {steps.length === 0 && (
        <p className="m-0 text-[11px] text-muted">
          No step before this {answering ? "question" : "gate"} left a document
          or files; what they changed is in the run's Changes tab.
        </p>
      )}
      {steps.map((step) => (
        <div key={step.stepId} className="grid min-w-0 gap-1.5">
          <span className="min-w-0 text-xs wrap-anywhere text-ink-soft">
            {step.title || step.stepId}
            <span className="text-muted">
              {" "}
              · {step.agent} · round {step.round}
            </span>
          </span>
          <ul className="m-0 flex min-w-0 list-none flex-wrap gap-1.5 p-0">
            {step.artifacts.map((artifact) => (
              <li key={artifact.digest} className="min-w-0">
                <ArtifactLink
                  target={{
                    digest: artifact.digest,
                    name: artifact.name || artifact.digest,
                    kind: artifact.kind,
                    mediaType: artifact.mediaType,
                    size: artifact.size,
                    round: step.round,
                    stepId: step.stepId,
                  }}
                />
              </li>
            ))}
          </ul>
        </div>
      ))}
    </section>
  );
}
