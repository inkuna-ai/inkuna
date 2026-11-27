import { useEffect, useState } from "react";
import { getRunArtifacts } from "../../bridge";
import { formatBytes } from "../../format";
import type { ArtifactInfo, WorkflowRun } from "../../types";
import { ArtifactLink } from "../artifacts/ArtifactViewer";
import { TableScroll, tableClass, tdClass, thClass } from "../common";
import { InlineAlert, TabBody } from "./shared";

/** Every artifact stored for the run's task; a name opens it in the drawer. */
export function ArtifactsTab({ run }: { run: WorkflowRun }) {
  const [artifacts, setArtifacts] = useState<ArtifactInfo[]>([]);
  // A failed refresh keeps the list on screen and only reports the failure.
  const [listError, setListError] = useState("");
  useEffect(() => {
    let active = true;
    getRunArtifacts(run.id)
      .then((value) => {
        if (active) {
          setArtifacts(value);
          setListError("");
        }
      })
      .catch((reason: unknown) => {
        if (active) setListError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [run.id, run.state, run.events?.length]);
  return (
    <TabBody>
      {listError && <InlineAlert>{listError}</InlineAlert>}
      {artifacts.length === 0 && !listError && (
        <p className="m-0 text-sm text-muted">
          No artifacts have been stored for this task.
        </p>
      )}
      {artifacts.length > 0 && (
        <div className="min-w-0">
          <TableScroll flush>
            <table className={tableClass}>
              <caption className="mb-2 text-left text-muted">
                Shared artifacts (readable by every agent)
              </caption>
              <thead>
                <tr>
                  <th className={thClass}>Kind</th>
                  <th className={thClass}>Name</th>
                  <th className={thClass}>Step</th>
                  <th className={thClass}>Agent</th>
                  <th className={thClass}>Type</th>
                  <th className={thClass}>Size</th>
                </tr>
              </thead>
              <tbody>
                {artifacts.map((artifact) => (
                  <tr
                    key={`${artifact.digest}-${artifact.stepId}-${artifact.kind}`}
                  >
                    <td className={`${tdClass} whitespace-nowrap`}>
                      {artifact.kind}
                    </td>
                    <td className={`${tdClass} min-w-[180px]`}>
                      <ArtifactLink
                        nameOnly
                        target={{
                          digest: artifact.digest,
                          name: artifact.name,
                          kind: artifact.kind,
                          mediaType: artifact.mediaType,
                          size: artifact.size,
                          stepId: artifact.stepId,
                        }}
                      />
                    </td>
                    <td className={`${tdClass} whitespace-nowrap`}>
                      {artifact.stepId || "—"}
                    </td>
                    <td className={`${tdClass} whitespace-nowrap`}>
                      {artifact.agent || "—"}
                    </td>
                    <td className={`${tdClass} whitespace-nowrap`}>
                      {artifact.mediaType}
                    </td>
                    <td className={`${tdClass} whitespace-nowrap`}>
                      {formatBytes(artifact.size)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </TableScroll>
        </div>
      )}
    </TabBody>
  );
}
