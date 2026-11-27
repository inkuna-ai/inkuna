import { describe, expect, it } from "vitest";
import {
  copyDefinition,
  extractStage,
  formatCommands,
  formatExitCodes,
  insertUse,
  isOverridden,
  move,
  newFlow,
  overrideFields,
  overrides,
  parseCommands,
  parseExitCodes,
  systemCodesSummary,
  removeDefinition,
  removeStage,
  renameStage,
  resetField,
  resolveDefinition,
  stageName,
  tidy,
  uniqueName,
  usedStage,
} from "./model";
import type {
  LibraryDefinition,
  RuleStage,
  RulesDocument,
  StageLibrary,
} from "../../types";

describe("rules editor model", () => {
  it("keeps dependsOn pointing at a renamed stage and drops a removed one", () => {
    const flow = newFlow();
    const renamed = renameStage(flow, 0, "build");
    expect(renamed.stages[1].dependsOn).toEqual(["build"]);
    expect(removeStage(renamed, 0).stages).toEqual([
      { id: "approve", kind: "approval", dependsOn: [] },
    ]);
  });

  it("keeps a feedback target on a renamed stage and drops it with a removed one", () => {
    const flow = {
      stages: [
        { id: "write", kind: "agent", agent: "auto" },
        {
          id: "verify",
          kind: "evaluation",
          dependsOn: ["write"],
          onFailure: "feedback" as const,
          feedback: { target: "write", maxRounds: 3 },
        },
        {
          id: "approve",
          kind: "approval",
          dependsOn: ["verify"],
          review: true,
          feedback: { target: "write" },
        },
      ],
    };
    const renamed = renameStage(flow, 0, "build");
    expect(renamed.stages[1].feedback).toEqual({
      target: "build",
      maxRounds: 3,
    });
    expect(renamed.stages[2].feedback).toEqual({ target: "build" });
    const removed = removeStage(renamed, 0);
    expect(removed.stages[0].feedback).toEqual({ maxRounds: 3 });
    expect(removed.stages[1].feedback?.target).toBeUndefined();
  });

  it("keeps feedback fields only where they apply", () => {
    const stages = (list: RuleStage[]) =>
      tidy({ version: 1, source: "", rules: [], default: { stages: list } })
        .default?.stages;
    expect(
      stages([
        {
          id: "verify",
          kind: "evaluation",
          onFailure: "feedback",
          feedback: { target: "write", maxRounds: 3 },
        },
        { id: "lint", kind: "evaluation", onFailure: "feedback", feedback: {} },
        {
          id: "zero",
          kind: "evaluation",
          onFailure: "feedback",
          feedback: { maxRounds: 0 },
        },
        {
          id: "check",
          kind: "evaluation",
          onFailure: "fail",
          feedback: { target: "write" },
        },
        {
          id: "work",
          kind: "agent",
          agent: "auto",
          review: true,
          onFailure: "feedback",
          feedback: { target: "x" },
        },
        {
          id: "approve",
          kind: "approval",
          review: true,
          onFailure: "feedback",
          feedback: { target: "write", maxRounds: 2 },
        },
      ]),
    ).toEqual([
      {
        id: "verify",
        kind: "evaluation",
        onFailure: "feedback",
        feedback: { target: "write", maxRounds: 3 },
      },
      { id: "lint", kind: "evaluation", onFailure: "feedback" },
      {
        id: "zero",
        kind: "evaluation",
        onFailure: "feedback",
        feedback: { maxRounds: 0 },
      },
      { id: "check", kind: "evaluation" },
      { id: "work", kind: "agent", agent: "auto" },
      {
        id: "approve",
        kind: "approval",
        review: true,
        feedback: { target: "write" },
      },
    ]);
  });

  it("parses commands as argv without a shell", () => {
    const commands = parseCommands('task test\n\ngo test -run "Test A" ./...');
    expect(commands).toEqual([
      ["task", "test"],
      ["go", "test", "-run", "Test A", "./..."],
    ]);
    expect(parseCommands(formatCommands(commands))).toEqual(commands);
  });

  it("round-trips arguments with quotes, backslashes, spaces, and empty values", () => {
    const commands = [
      ["echo", 'say "hi" now'],
      ["printf", "", "%s"],
      ["grep", "-E", "a\\.b", "C:\\dir with space\\file"],
      ["sh-like", "$HOME", "*.go", "a|b"],
    ];
    const text = formatCommands(commands);
    expect(text.split("\n")[0]).toBe('echo "say \\"hi\\" now"');
    expect(text.split("\n")[1]).toBe('printf "" %s');
    expect(parseCommands(text)).toEqual(commands);
    // Formatting what was parsed is stable, so form edits do not drift.
    expect(formatCommands(parseCommands(text))).toBe(text);
    // Hand-typed input: an unterminated quote keeps the rest of the line.
    expect(parseCommands('say "unfinished thought')).toEqual([
      ["say", "unfinished thought"],
    ]);
  });

  it("moves within bounds only and finds free names", () => {
    expect(move(["a", "b", "c"], 2, 0)).toEqual(["c", "a", "b"]);
    expect(move(["a", "b"], 0, -1)).toEqual(["a", "b"]);
    expect(uniqueName("rule", ["rule", "rule-2"])).toBe("rule-3");
  });

  it("keeps an agent stage's tracker kinds and drops them from other kinds", () => {
    const stages = (list: RuleStage[]) =>
      tidy({ version: 1, source: "", rules: [], default: { stages: list } })
        .default?.stages;
    expect(
      stages([
        { id: "triage", kind: "agent", agent: "auto", tracker: ["comment"] },
        { id: "check", kind: "evaluation", tracker: ["comment"] },
        { id: "plain", kind: "agent", agent: "auto", tracker: [] },
      ]),
    ).toEqual([
      { id: "triage", kind: "agent", agent: "auto", tracker: ["comment"] },
      { id: "check", kind: "evaluation" },
      { id: "plain", kind: "agent", agent: "auto" },
    ]);
  });

  it("keeps an agent stage's MCP servers and drops them from other kinds", () => {
    const stages = (list: RuleStage[]) =>
      tidy({ version: 1, source: "", rules: [], default: { stages: list } })
        .default?.stages;
    expect(
      stages([
        { id: "triage", kind: "agent", agent: "auto", mcp: ["linear"] },
        { id: "check", kind: "evaluation", mcp: ["linear"] },
        { id: "plain", kind: "agent", agent: "auto", mcp: [] },
      ]),
    ).toEqual([
      { id: "triage", kind: "agent", agent: "auto", mcp: ["linear"] },
      { id: "check", kind: "evaluation" },
      { id: "plain", kind: "agent", agent: "auto" },
    ]);
  });

  it("drops empty optional fields and fields that do not apply to the kind", () => {
    const tidied = tidy({
      version: 1,
      source: "/somewhere",
      rules: [
        {
          name: " docs ",
          description: " ",
          when: { kind: ["documentation"], labels: [], minPriority: 0 },
          then: {
            stages: [
              {
                id: "verify",
                kind: "evaluation",
                commands: [["task", "test"]],
                continueOnFailure: true,
              },
              {
                id: "approve",
                kind: "approval",
                agent: "implementer",
                permission: "worktree-write",
                skills: [],
                prompt: "",
                continueOnFailure: true,
              },
            ],
          },
        },
      ],
    });
    expect(tidied).toEqual({
      version: 1,
      source: "",
      rules: [
        {
          name: "docs",
          when: { kind: ["documentation"] },
          then: {
            stages: [
              {
                id: "verify",
                kind: "evaluation",
                commands: [["task", "test"]],
                continueOnFailure: true,
              },
              { id: "approve", kind: "approval" },
            ],
          },
        },
      ],
    });
  });

  it("sends uses and the shared stages and groups back to Go as they came", () => {
    const shared: RuleStage = {
      id: "",
      kind: "approval",
      review: true,
    };
    const use: RuleStage = {
      use: "approval",
      id: "",
      kind: "",
      dependsOn: ["check"],
      review: false,
      overrides: ["review"],
    };
    const tidied = tidy({
      version: 2,
      source: "",
      stages: { approval: shared },
      groups: { check: { stages: [{ use: "verify", id: "", kind: "" }] } },
      rules: [],
      default: {
        stages: [
          { use: "check", id: "", kind: "" },
          { ...use, from: { stage: "approval", scope: "repository" } },
        ],
      },
    });
    expect(tidied.stages).toEqual({ approval: shared });
    expect(tidied.groups).toEqual({
      check: { stages: [{ use: "verify", id: "", kind: "" }] },
    });
    // review: false is kept with its override; provenance is Go's alone.
    expect(tidied.default?.stages).toEqual([
      { use: "check", id: "", kind: "" },
      use,
    ]);
  });

  it("names a use by its id, or by what it uses when it sets none", () => {
    expect(stageName({ use: "verify", id: "", kind: "" })).toBe("verify");
    expect(stageName({ use: "verify", id: "docs", kind: "" })).toBe("docs");
    const flow = {
      stages: [
        { use: "verify", id: "", kind: "" },
        { id: "approve", kind: "approval", dependsOn: ["verify"] },
      ],
    };
    expect(removeStage(flow, 0).stages).toEqual([
      { id: "approve", kind: "approval", dependsOn: [] },
    ]);
  });

  it("keeps references to a removed stage's name while another stage has it", () => {
    const flow = {
      stages: [
        { use: "verify", id: "", kind: "" },
        { use: "verify", id: "", kind: "" },
        {
          id: "approve",
          kind: "approval",
          dependsOn: ["verify"],
          feedback: { target: "verify" },
        },
      ],
    };
    expect(removeStage(flow, 0).stages).toEqual(flow.stages.slice(1));
  });

  it("round-trips uses and their overrides, and tidies the definitions", () => {
    const document: RulesDocument = {
      version: 2,
      source: "",
      stages: {
        review: {
          id: "",
          kind: "agent",
          agent: "reviewer",
          title: "  Review  ",
          skills: [],
        },
      },
      groups: {
        gate: {
          stages: [
            {
              use: "review",
              id: "",
              kind: "",
              skills: [],
              overrides: ["skills"],
            },
            {
              id: "approval",
              kind: "approval",
              dependsOn: ["review"],
              prompt: "",
            },
          ],
        },
      },
      rules: [],
      default: {
        stages: [
          { use: "gate", id: "api", kind: "", dependsOn: [] },
          { use: "approval", id: "", kind: "", dependsOn: ["api"] },
        ],
      },
    };
    const tidied = tidy(document);
    expect(tidied.stages).toEqual({
      review: { id: "", kind: "agent", agent: "reviewer", title: "Review" },
    });
    expect(tidied.groups?.gate.stages).toEqual([
      { use: "review", id: "", kind: "", skills: [], overrides: ["skills"] },
      { id: "approval", kind: "approval", dependsOn: ["review"] },
    ]);
    expect(tidied.default?.stages).toEqual(document.default?.stages);
    // Tidying twice changes nothing, so the form and YAML agree.
    expect(tidy(tidied)).toEqual(tidied);
  });

  it("shows a use's inherited fields under its overrides, and resets one to inherited", () => {
    const definition: RuleStage = {
      id: "",
      kind: "evaluation",
      commands: [["task", "ci"]],
      continueOnFailure: true,
    };
    const use: RuleStage = {
      use: "verify",
      id: "docs",
      kind: "",
      dependsOn: ["write"],
    };
    expect(usedStage(use, definition)).toEqual({
      id: "docs",
      kind: "evaluation",
      commands: [["task", "ci"]],
      continueOnFailure: true,
      dependsOn: ["write"],
    });

    const overridden = overrideFields(
      use,
      { commands: [["go", "test"]], continueOnFailure: false, id: "x" },
      ["commands", "continueOnFailure"],
    );
    // id wires the use in; it is never an override.
    expect(overridden.id).toBe("docs");
    expect(overridden.overrides).toEqual(["commands", "continueOnFailure"]);
    expect(isOverridden(overridden, "commands")).toBe(true);
    expect(usedStage(overridden, definition)).toMatchObject({
      commands: [["go", "test"]],
      continueOnFailure: false,
    });
    // The definition is never changed by a use.
    expect(definition.commands).toEqual([["task", "ci"]]);

    const reset = resetField(overridden, "commands");
    expect(reset.commands).toBeUndefined();
    expect(reset.overrides).toEqual(["continueOnFailure"]);
    expect(usedStage(reset, definition).commands).toEqual([["task", "ci"]]);
    const inherited = resetField(reset, "continueOnFailure");
    expect(inherited).toEqual(use);
  });

  it("offers an override only where Go applies it", () => {
    expect(overrides({ name: "title" }, "approval")).toBe(true);
    expect(
      overrides({ name: "commands", kinds: ["agent", "evaluation"] }, "script"),
    ).toBe(false);
  });

  it("extracts an inline stage to a shared stage and uses it in its place", () => {
    const flow = {
      stages: [
        { id: "write", kind: "agent", agent: "auto" },
        {
          id: "check",
          kind: "evaluation",
          dependsOn: ["write"],
          commands: [["task", "ci"]],
          title: "",
        },
        { id: "approve", kind: "approval", dependsOn: ["check"] },
      ],
    };
    const document: RulesDocument = { version: 1, source: "", rules: [] };
    const same = extractStage(document, flow, 1, "check");
    expect(same.document.stages).toEqual({
      check: { id: "", kind: "evaluation", commands: [["task", "ci"]] },
    });
    expect(same.flow.stages[1]).toEqual({
      use: "check",
      id: "",
      kind: "",
      dependsOn: ["write"],
    });
    // The stage keeps its id, so what depends on it still does.
    expect(same.flow.stages.map(stageName)).toEqual([
      "write",
      "check",
      "approve",
    ]);
    const renamed = extractStage(document, flow, 1, "ci");
    expect(renamed.flow.stages[1]).toMatchObject({ use: "ci", id: "check" });
    expect(Object.keys(renamed.document.stages ?? {})).toEqual(["ci"]);
  });

  it("inserts a use before the closing approval, with an id of its own when the name is taken", () => {
    const flow = insertUse(newFlow(), "verify-and-approve");
    expect(flow.stages.map(stageName)).toEqual([
      "implement",
      "verify-and-approve",
      "approve",
    ]);
    expect(flow.stages[1]).toEqual({
      use: "verify-and-approve",
      id: "",
      kind: "",
    });
    const twice = insertUse(flow, "verify-and-approve");
    expect(twice.stages[2]).toMatchObject({ id: "verify-and-approve-2" });
  });

  it("renames a use, and a cleared id goes back to the name it uses", () => {
    const flow = {
      stages: [
        { use: "verify", id: "", kind: "" },
        { id: "approve", kind: "approval", dependsOn: ["verify"] },
      ],
    };
    const renamed = renameStage(flow, 0, "docs");
    expect(renamed.stages[1].dependsOn).toEqual(["docs"]);
    expect(renameStage(renamed, 0, "").stages[1].dependsOn).toEqual(["verify"]);
  });

  it("resolves a name to the definition Go did not mark overridden, and copies or removes one", () => {
    const builtin: LibraryDefinition = {
      name: "verify",
      scope: "builtin",
      kind: "stage",
      stage: { id: "", kind: "evaluation" },
      overridden: true,
    };
    const global: LibraryDefinition = {
      name: "verify",
      scope: "global",
      file: "/stages/checks.yaml",
      kind: "stage",
      stage: { id: "", kind: "evaluation", commands: [["task", "ci"]] },
    };
    const library: StageLibrary = {
      definitions: [builtin, global],
      problems: [],
      overridable: [],
    };
    expect(resolveDefinition(library, "verify")).toBe(global);
    expect(resolveDefinition(library, "missing")).toBeUndefined();

    const document: RulesDocument = { version: 2, source: "", rules: [] };
    const copied = copyDefinition(document, global);
    expect(copied.stages?.verify).toEqual(global.stage);
    expect(copied.stages?.verify).not.toBe(global.stage);
    expect(removeDefinition(copied, "verify")).toEqual(document);
  });

  it("reads exit codes as a list and says which pause the run", () => {
    expect(parseExitCodes("3, 75  124,")).toEqual([3, 75, 124]);
    expect(parseExitCodes("")).toEqual([]);
    expect(formatExitCodes([3, 75])).toBe("3, 75");
    expect(formatExitCodes(undefined)).toBe("");
    expect(systemCodesSummary([3])).toBe("exit 3 pauses for you");
    expect(systemCodesSummary([3, 75])).toBe("exit 3 or 75 pause for you");
    expect(systemCodesSummary([3, 75, 124])).toBe(
      "exit 3, 75, or 124 pause for you",
    );
  });

  it("keeps system exit codes on a check loop and drops an empty list", () => {
    const stages = (list: RuleStage[]) =>
      tidy({ version: 1, source: "", rules: [], default: { stages: list } })
        .default?.stages;
    expect(
      stages([
        {
          id: "verify",
          kind: "evaluation",
          onFailure: "feedback",
          feedback: { systemExitCodes: [3] },
        },
        {
          id: "lint",
          kind: "evaluation",
          onFailure: "feedback",
          feedback: { systemExitCodes: [] },
        },
      ]),
    ).toEqual([
      {
        id: "verify",
        kind: "evaluation",
        onFailure: "feedback",
        feedback: { systemExitCodes: [3] },
      },
      { id: "lint", kind: "evaluation", onFailure: "feedback" },
    ]);
  });
});
