import { useEffect, useState } from "react";
import { readArtifact } from "../../bridge";
import type { ArtifactInfo, UsageRecord } from "../../types";
import {
  Chip,
  Code,
  Field,
  inputClass,
  tableClass,
  tdClass,
  thClass,
} from "../common";
import {
  approxTokens,
  callLabel,
  exact,
  sectionLabel,
  usageLine,
} from "../usage";
import { InlineAlert, SubHeading } from "./shared";

const number = `${tdClass} whitespace-nowrap text-right font-mono tabular-nums`;
const numberHead = `${thClass} text-right`;

/**
 * Where one call's prompt went: each section's bytes, roughly four to a
 * token, largest first, with a bar scaled to the largest section, and the
 * prompt itself as the call sent it. A step with several calls (turns and
 * peer answers) picks one; the latest is shown until another is chosen.
 */
export function PromptBreakdown({ calls }: { calls: UsageRecord[] }) {
  // Chosen by call id, which a running call keeps once it is stored.
  const [chosen, setChosen] = useState<string | null>(null);
  const call =
    calls.find((entry) => entry.callId === chosen) ?? calls[calls.length - 1];
  if (!call) {
    return (
      <>
        <SubHeading>Prompt breakdown</SubHeading>
        <p className="m-0 text-xs text-muted">
          Inkuna has recorded no model call for this step.
        </p>
      </>
    );
  }
  const sections = [...(call.sections ?? [])].sort((a, b) => b.bytes - a.bytes);
  const largest = Math.max(1, ...sections.map((section) => section.bytes));
  return (
    <>
      <SubHeading>Prompt breakdown</SubHeading>
      {calls.length > 1 && (
        <div className="max-w-[420px] min-w-0">
          <Field label="Call" flush>
            <select
              aria-label="Prompt call"
              className={inputClass}
              value={call.callId}
              onChange={(event) => setChosen(event.target.value)}
            >
              {calls.map((entry) => (
                <option value={entry.callId} key={entry.callId}>
                  {callLabel(entry)}
                </option>
              ))}
            </select>
          </Field>
        </div>
      )}
      <p className="m-0 text-[11px] wrap-anywhere text-muted">
        {callLabel(call)} sent {exact(call.promptBytes)} bytes (about{" "}
        {exact(approxTokens(call.promptBytes))} tokens) ·{" "}
        {call.outcome === "running"
          ? "running"
          : `the model read ${usageLine(call.usage)} · ${call.outcome}`}
      </p>
      <div className="max-w-full overflow-x-auto">
        <table className={tableClass} aria-label="Prompt sections">
          <thead>
            <tr>
              <th className={thClass}>Section</th>
              <th className={`${thClass} w-[40%]`}>
                <span className="sr-only">Relative size</span>
              </th>
              <th className={numberHead}>Bytes</th>
              <th className={numberHead}>≈ Tokens</th>
              <th className={numberHead}>Share</th>
            </tr>
          </thead>
          <tbody>
            {sections.map((section) => (
              <tr key={`${section.section}-${section.name ?? ""}`}>
                <td className={`${tdClass} min-w-[180px]`}>
                  {sectionLabel(section)}
                  {section.truncated && (
                    <>
                      {" "}
                      <Chip tone="warn">cut to fit</Chip>
                    </>
                  )}
                </td>
                <td className={`${tdClass} align-middle`}>
                  <div
                    aria-hidden="true"
                    className="h-2 rounded-r bg-series-1"
                    style={{ width: `${(100 * section.bytes) / largest}%` }}
                  />
                </td>
                <td className={number}>{exact(section.bytes)}</td>
                <td className={number}>{exact(approxTokens(section.bytes))}</td>
                <td className={number}>
                  {call.promptBytes > 0
                    ? `${Math.round((100 * section.bytes) / call.promptBytes)}%`
                    : "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <SubHeading>Prompt sent</SubHeading>
      {/* Keyed by digest so one call's prompt never shows under another. */}
      <PromptText key={call.promptDigest ?? ""} digest={call.promptDigest} />
    </>
  );
}

/**
 * A call's prompt, read from the artifact store by its digest. A digest
 * names fixed content, so it is read once.
 */
function PromptText({ digest }: { digest?: string }) {
  const [artifact, setArtifact] = useState<ArtifactInfo | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!digest) return;
    let active = true;
    readArtifact(digest)
      .then((value) => {
        if (active) setArtifact(value);
      })
      .catch((reason: unknown) => {
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [digest]);
  if (!digest) {
    return (
      <p className="m-0 text-xs text-muted">
        Inkuna did not keep this call&apos;s prompt: calls made before it kept
        prompts show only their sizes.
      </p>
    );
  }
  if (error) return <InlineAlert>{error}</InlineAlert>;
  if (!artifact)
    return <p className="m-0 text-xs text-muted">Loading the prompt…</p>;
  return (
    <>
      {artifact.truncated && (
        <p className="m-0 text-[11px] wrap-anywhere text-warn">
          Shown up to the preview limit; the stored prompt is longer.
        </p>
      )}
      <Code tall>{artifact.content ?? ""}</Code>
    </>
  );
}
