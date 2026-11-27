import type {
  LibraryDefinition,
  OverridableField,
  Rule,
  RuleCondition,
  RuleGroup,
  RuleStage,
  RulesDocument,
  StageLibrary,
  TaskKind,
} from "../../types";

export const taskKinds: TaskKind[] = [
  "research",
  "bugfix",
  "implementation",
  "review",
  "documentation",
  "skill-authoring",
];

export const stageKinds = [
  "agent",
  "script",
  "evaluation",
  "human-input",
  "approval",
] as const;

export type Flow = { stages: RuleStage[] };

/** A name not yet taken, derived from base: base, base-2, base-3… */
export function uniqueName(base: string, taken: string[]): string {
  if (!taken.includes(base)) return base;
  let suffix = 2;
  while (taken.includes(`${base}-${suffix}`)) suffix += 1;
  return `${base}-${suffix}`;
}

/** Every flow must end with an approval, so a new flow starts valid. */
export function newFlow(): Flow {
  return {
    stages: [
      { id: "implement", kind: "agent", agent: "auto" },
      { id: "approve", kind: "approval", dependsOn: ["implement"] },
    ],
  };
}

export function newRule(taken: string[]): Rule {
  return { name: uniqueName("new-rule", taken), when: {}, then: newFlow() };
}

export function newStage(taken: string[]): RuleStage {
  return { id: uniqueName("stage", taken), kind: "agent", agent: "auto" };
}

export function move<T>(items: T[], from: number, to: number): T[] {
  if (to < 0 || to >= items.length) return items;
  const next = [...items];
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

/** The id an entry gives its stage: its own, or for a use that sets none,
 * the name of the shared stage or group it uses. */
export function stageName(stage: RuleStage): string {
  return stage.id || stage.use || "";
}

/** Renaming a stage keeps later dependsOn and feedback references pointing
 * at it. A use whose id is cleared goes back to the name it uses. */
export function renameStage(flow: Flow, index: number, id: string): Flow {
  const previous = stageName(flow.stages[index]);
  const name = stageName({ ...flow.stages[index], id });
  return {
    stages: flow.stages.map((stage, position) => {
      if (position === index) return { ...stage, id };
      let next = stage;
      if (stage.dependsOn?.includes(previous))
        next = {
          ...next,
          dependsOn: stage.dependsOn.map((dependency) =>
            dependency === previous ? name : dependency,
          ),
        };
      if (stage.feedback?.target === previous)
        next = { ...next, feedback: { ...stage.feedback, target: name } };
      return next;
    }),
  };
}

/** Removing a stage drops references to it; feedback that named it goes
 * back to the default target. While another stage still has its name, as a
 * second use of the same shared stage does, the references are that
 * stage's and are kept. */
export function removeStage(flow: Flow, index: number): Flow {
  const removed = stageName(flow.stages[index]);
  const remaining = flow.stages.filter((_, position) => position !== index);
  if (remaining.some((stage) => stageName(stage) === removed))
    return { stages: remaining };
  return {
    stages: remaining.map((stage) => {
      let next = stage;
      if (stage.dependsOn?.includes(removed))
        next = {
          ...next,
          dependsOn: stage.dependsOn.filter(
            (dependency) => dependency !== removed,
          ),
        };
      if (stage.feedback?.target === removed)
        next = {
          ...next,
          feedback: { ...stage.feedback, target: undefined },
        };
      return next;
    }),
  };
}

/**
 * Commands are edited one argv per line. An argument is written bare unless it
 * is empty or contains whitespace, a double quote, or a backslash; then it is
 * wrapped in double quotes with \" and \\ escaped, so every argv value
 * survives format -> parse. There is no shell: nothing here is expanded.
 */
function parseLine(line: string): string[] {
  const argv: string[] = [];
  let current = "";
  let started = false;
  let quoted = false;
  for (let index = 0; index < line.length; index += 1) {
    const character = line[index];
    if (quoted) {
      if (character === "\\" && index + 1 < line.length) {
        index += 1;
        current += line[index];
      } else if (character === '"') {
        quoted = false;
      } else {
        current += character;
      }
    } else if (character === '"') {
      // Opening a quote starts an argument even when it stays empty ("").
      quoted = true;
      started = true;
    } else if (/\s/.test(character)) {
      if (started) argv.push(current);
      current = "";
      started = false;
    } else {
      current += character;
      started = true;
    }
  }
  if (started) argv.push(current);
  return argv;
}

export function parseCommands(text: string): string[][] {
  return text
    .split("\n")
    .map(parseLine)
    .filter((argv) => argv.length > 0);
}

function formatArgument(argument: string): string {
  if (argument !== "" && !/[\s"\\]/.test(argument)) return argument;
  return `"${argument.replace(/[\\"]/g, "\\$&")}"`;
}

export function formatCommands(commands: string[][] | undefined): string {
  return (commands ?? [])
    .map((argv) => argv.map(formatArgument).join(" "))
    .join("\n");
}

/** Exit codes typed as a list, split on commas and spaces. The text keeps
 * only digits and separators, so each entry is a number for Go to check. */
export function parseExitCodes(text: string): number[] {
  return text
    .split(/[\s,]+/)
    .filter(Boolean)
    .map(Number);
}

/** Exit codes as the field shows them: "3, 75". */
export function formatExitCodes(codes: number[] | undefined): string {
  return (codes ?? []).join(", ");
}

/** The exit codes that pause a check loop for the person, in prose:
 * "exit 3 pauses for you", "exit 3 or 75 pause for you". */
export function systemCodesSummary(codes: number[]): string {
  const listed =
    codes.length === 1
      ? String(codes[0])
      : `${codes.slice(0, -1).join(", ")}${codes.length > 2 ? "," : ""} or ${codes[codes.length - 1]}`;
  return `exit ${listed} ${codes.length === 1 ? "pauses" : "pause"} for you`;
}

/** Drops a stage's empty optional fields so saved YAML stays as small as
 * hand-written. A use goes back to Go exactly as it came, with the fields it
 * overrides even when false or empty; Go writes it as it was written. */
export function tidyStage(value: RuleStage): RuleStage {
  if (value.use) {
    // Provenance is Go's to set on an expanded stage, never written.
    const use = { ...value };
    delete use.from;
    return use;
  }
  const next: RuleStage = { id: value.id.trim(), kind: value.kind };
  if (value.title?.trim()) next.title = value.title.trim();
  if (value.kind === "agent") {
    if (value.agent) next.agent = value.agent;
    if (value.runtime) next.runtime = value.runtime;
    if (value.permission) next.permission = value.permission;
    if (value.skills?.length) next.skills = value.skills;
    if (value.mcp?.length) next.mcp = value.mcp;
    if (value.tracker?.length) next.tracker = value.tracker;
    if (value.allowedPaths?.length) next.allowedPaths = value.allowedPaths;
  }
  if (value.kind === "human-input" && value.agent) next.agent = value.agent;
  if (value.dependsOn?.length) next.dependsOn = value.dependsOn;
  if (value.prompt?.trim()) next.prompt = value.prompt;
  if (value.commands?.length) next.commands = value.commands;
  if (value.optional) next.optional = true;
  if (value.kind === "evaluation" && value.continueOnFailure)
    next.continueOnFailure = true;
  if (value.kind === "approval") {
    if (value.review) next.review = true;
    if (value.feedback?.target)
      next.feedback = { target: value.feedback.target };
  }
  if (value.kind === "evaluation" && value.onFailure === "feedback") {
    next.onFailure = "feedback";
    const feedback: RuleStage["feedback"] = {};
    if (value.feedback?.target) feedback.target = value.feedback.target;
    // A cap typed as 0 is kept, so Go can say it is out of range.
    if (value.feedback?.maxRounds !== undefined)
      feedback.maxRounds = value.feedback.maxRounds;
    if (value.feedback?.systemExitCodes?.length)
      feedback.systemExitCodes = value.feedback.systemExitCodes;
    if (
      feedback.target ||
      feedback.maxRounds !== undefined ||
      feedback.systemExitCodes
    )
      next.feedback = feedback;
  }
  return next;
}

/** Drops empty optional fields from every stage, so saved YAML stays as
 * small as hand-written. Uses go back as they came (tidyStage). */
export function tidy(document: RulesDocument): RulesDocument {
  const stage = tidyStage;
  return {
    version: document.version,
    ...(document.stages
      ? {
          stages: Object.fromEntries(
            Object.entries(document.stages).map(([name, value]) => [
              name,
              stage(value),
            ]),
          ),
        }
      : {}),
    ...(document.groups
      ? {
          groups: Object.fromEntries(
            Object.entries(document.groups).map(([name, group]) => [
              name,
              { stages: group.stages.map(stage) },
            ]),
          ),
        }
      : {}),
    rules: document.rules.map((rule) => ({
      name: rule.name.trim(),
      ...(rule.description?.trim()
        ? { description: rule.description.trim() }
        : {}),
      when: Object.fromEntries(
        Object.entries(rule.when).filter(([, value]) =>
          Array.isArray(value) ? value.length > 0 : Boolean(value),
        ),
      ),
      then: { stages: rule.then.stages.map(stage) },
    })),
    ...(document.default
      ? { default: { stages: document.default.stages.map(stage) } }
      : {}),
    source: "",
  };
}

export function describeCondition(when: RuleCondition): string[] {
  const parts: string[] = [];
  if (when.kind?.length) parts.push(`kind ∈ {${when.kind.join(", ")}}`);
  if (when.labels?.length) parts.push(`labels ∩ {${when.labels.join(", ")}}`);
  if (when.source?.length) parts.push(`source ∈ {${when.source.join(", ")}}`);
  if (when.keywords?.length)
    parts.push(`objective mentions ${when.keywords.join(" | ")}`);
  if (when.repositories?.length)
    parts.push(`repository ∈ {${when.repositories.join(", ")}}`);
  if (when.complexity)
    parts.push(`complexity ${when.complexity.min}–${when.complexity.max}`);
  if (when.minPriority) parts.push(`priority ≥ ${when.minPriority}`);
  return parts.length ? parts : ["always"];
}

/** The definition a use of name gets, as Go listed the library: the one no
 * higher scope overrides, unless two global files define it. */
export function resolveDefinition(
  library: StageLibrary | null,
  name: string,
): LibraryDefinition | undefined {
  return (library?.definitions ?? []).find(
    (definition) =>
      definition.name === name &&
      !definition.overridden &&
      !definition.ambiguous,
  );
}

/** Whether Go lets a use override field on a stage of kind. */
export function overrides(field: OverridableField, kind: string): boolean {
  return !field.kinds?.length || field.kinds.includes(kind);
}

/** Whether a use sets field itself rather than taking its definition's. */
export function isOverridden(use: RuleStage, field: string): boolean {
  return use.overrides?.includes(field) ?? false;
}

/** The stage a use of a shared stage stands for: the definition's fields,
 * with those the use overrides on top, under the use's id and dependsOn. */
export function usedStage(use: RuleStage, definition: RuleStage): RuleStage {
  const stage: RuleStage = structuredClone(definition);
  const fields = stage as unknown as Record<string, unknown>;
  const own = use as unknown as Record<string, unknown>;
  for (const field of use.overrides ?? []) {
    if (own[field] === undefined) delete fields[field];
    else fields[field] = structuredClone(own[field]);
  }
  stage.id = stageName(use);
  stage.dependsOn = use.dependsOn;
  delete stage.use;
  delete stage.from;
  return stage;
}

/** Sets fields on a use as overrides, so they replace the definition's for
 * this use only. A field Go does not let a use set is left alone. */
export function overrideFields(
  use: RuleStage,
  patch: Partial<RuleStage>,
  overridable: string[],
): RuleStage {
  const next = { ...use } as unknown as Record<string, unknown>;
  const listed = [...(use.overrides ?? [])];
  for (const [field, value] of Object.entries(patch)) {
    if (!overridable.includes(field)) continue;
    next[field] = value;
    if (!listed.includes(field)) listed.push(field);
  }
  return { ...(next as unknown as RuleStage), overrides: listed };
}

/** Drops a use's override of field, so it takes the definition's again. */
export function resetField(use: RuleStage, field: string): RuleStage {
  const next = { ...use } as unknown as Record<string, unknown>;
  delete next[field];
  const listed = (use.overrides ?? []).filter((name) => name !== field);
  if (listed.length) next.overrides = listed;
  else delete next.overrides;
  return next as unknown as RuleStage;
}

/** Inserts a use of a shared stage or group before the closing approval,
 * as a new stage is, with an id of its own when its name is taken. */
export function insertUse(flow: Flow, name: string): Flow {
  const ids = flow.stages.map(stageName);
  const use: RuleStage = {
    use: name,
    id: ids.includes(name) ? uniqueName(name, ids) : "",
    kind: "",
  };
  const last = flow.stages[flow.stages.length - 1];
  const at =
    last?.kind === "approval" ? flow.stages.length - 1 : flow.stages.length;
  const stages = [...flow.stages];
  stages.splice(at, 0, use);
  return { stages };
}

/** The names shared stages and groups have, in the library and the
 * document's own, so a new one can be given a name no use already means. */
export function definitionNames(
  library: StageLibrary | null,
  document: RulesDocument,
): string[] {
  return [
    ...(library?.definitions ?? []).map((definition) => definition.name),
    ...Object.keys(document.stages ?? {}),
    ...Object.keys(document.groups ?? {}),
  ];
}

/** Turns an inline stage into a repository shared stage called name and
 * puts a use of it in its place, with the same id and dependsOn, so the
 * flow plans as before. */
export function extractStage(
  document: RulesDocument,
  flow: Flow,
  index: number,
  name: string,
): { document: RulesDocument; flow: Flow } {
  const stage = flow.stages[index];
  const definition = tidyStage({ ...stage, dependsOn: undefined });
  definition.id = "";
  const use: RuleStage = {
    use: name,
    id: stage.id === name ? "" : stage.id,
    kind: "",
    ...(stage.dependsOn?.length ? { dependsOn: stage.dependsOn } : {}),
  };
  return {
    document: {
      ...document,
      stages: { ...(document.stages ?? {}), [name]: definition },
    },
    flow: {
      stages: flow.stages.map((entry, position) =>
        position === index ? use : entry,
      ),
    },
  };
}

/** A copy of a built-in or global definition in the repository's own
 * rules, which overrides it for this repository's flows. */
export function copyDefinition(
  document: RulesDocument,
  definition: LibraryDefinition,
): RulesDocument {
  if (definition.group)
    return {
      ...document,
      groups: {
        ...(document.groups ?? {}),
        [definition.name]: structuredClone(definition.group),
      },
    };
  const stage = structuredClone(definition.stage ?? { id: "", kind: "" });
  delete stage.from;
  return {
    ...document,
    stages: { ...(document.stages ?? {}), [definition.name]: stage },
  };
}

/** Sets or adds the repository's shared stage or group called name. */
export function setDefinition(
  document: RulesDocument,
  name: string,
  value: { stage: RuleStage } | { group: RuleGroup },
): RulesDocument {
  if ("group" in value)
    return {
      ...document,
      groups: { ...(document.groups ?? {}), [name]: value.group },
    };
  return {
    ...document,
    stages: { ...(document.stages ?? {}), [name]: value.stage },
  };
}

/** Removes the repository's shared stage or group called name. Uses of it
 * then resolve to a global or built-in one, or Go refuses them. */
export function removeDefinition(
  document: RulesDocument,
  name: string,
): RulesDocument {
  const stages = { ...(document.stages ?? {}) };
  const groups = { ...(document.groups ?? {}) };
  delete stages[name];
  delete groups[name];
  const next: RulesDocument = { ...document, stages, groups };
  if (!Object.keys(stages).length) delete next.stages;
  if (!Object.keys(groups).length) delete next.groups;
  return next;
}

/** A new shared stage: an agent stage the orchestrator staffs. */
export function newSharedStage(): RuleStage {
  return { id: "", kind: "agent", agent: "auto" };
}

/** A new group: one agent stage, for the person to build on. */
export function newGroup(): RuleGroup {
  return { stages: [{ id: "work", kind: "agent", agent: "auto" }] };
}

/** How each scope a definition can live in is named in the editor. */
export const scopeLabels: Record<string, string> = {
  builtin: "built-in",
  global: "global",
  repository: "repository",
};

/** Where an expanded stage came from, as Go recorded it: "shared stage
 * verify in group check, built-in". Empty for a stage written inline. */
export function describeOrigin(from: RuleStage["from"]): string {
  if (!from || (!from.stage && !from.groups?.length)) return "";
  const origin = [
    ...(from.stage ? [`shared stage ${from.stage}`] : []),
    ...[...(from.groups ?? [])].reverse().map((group) => `group ${group}`),
  ].join(" in ");
  const overridden = from.overrides?.length
    ? `, overriding ${from.overrides.join(", ")}`
    : "";
  return `from ${origin}, ${scopeLabels[from.scope] ?? from.scope}${overridden}`;
}
