import { useEffect, useRef, useState } from "react";
import { getRules } from "../../bridge";
import type { RulesDocument } from "../../types";

/**
 * The rules document for a project, for workflow pickers. Null until loaded
 * or when the project has none reachable; only the newest request may set it.
 */
export function useRules(projectID: string | undefined): RulesDocument | null {
  const [document, setDocument] = useState<RulesDocument | null>(null);
  const latest = useRef(0);
  useEffect(() => {
    const request = ++latest.current;
    if (!projectID) {
      setDocument(null);
      return;
    }
    getRules(projectID)
      .then((info) => request === latest.current && setDocument(info.document))
      .catch(() => request === latest.current && setDocument(null));
  }, [projectID]);
  return document;
}

/** The names a workflow picker offers: every rule, then the default flow. */
export function ruleNames(document: RulesDocument | null): string[] {
  const names = (document?.rules ?? []).map((rule) => rule.name);
  return names.includes("default") ? names : [...names, "default"];
}
