/// <reference types="vite/client" />
import ts from "typescript";

// Testing Library unmounts in its own afterEach, which Vitest runs after a
// describe block's. A describe-level afterEach that resets or restores mocks,
// or deletes a binding such as window.go, so does it while the component is
// still mounted, and an effect still pending from a late re-render then
// calls a mock that returns undefined or a binding that is gone. It needs a
// slow runner to leave an effect pending, so it shows only as an
// intermittent CI failure (#147, #157, #191). No hook in setup.ts can
// unmount sooner, because file-level hooks run after describe-level ones,
// so each such afterEach must call cleanup() first, and this test checks
// every test file does.

/** Mock methods and vi calls that change what a mock or global returns. */
const resets = new Set([
  "mockReset",
  "mockRestore",
  "resetAllMocks",
  "restoreAllMocks",
  "unstubAllGlobals",
]);

/** The identifier an expression such as `(window as X).go.main` starts at. */
function root(node: ts.Expression): string | undefined {
  for (;;) {
    if (
      ts.isParenthesizedExpression(node) ||
      ts.isAsExpression(node) ||
      ts.isSatisfiesExpression(node) ||
      ts.isNonNullExpression(node) ||
      ts.isPropertyAccessExpression(node) ||
      ts.isElementAccessExpression(node)
    ) {
      node = node.expression;
    } else {
      return ts.isIdentifier(node) ? node.text : undefined;
    }
  }
}

/** Whether node resets a mock or deletes a global binding anywhere inside. */
function tearsDown(node: ts.Node): boolean {
  if (
    ts.isCallExpression(node) &&
    ts.isPropertyAccessExpression(node.expression) &&
    resets.has(node.expression.name.text)
  ) {
    return true;
  }
  if (ts.isDeleteExpression(node)) {
    const name = root(node.expression);
    if (name === "window" || name === "globalThis") return true;
  }
  return ts.forEachChild(node, tearsDown) ?? false;
}

function isCleanup(node: ts.Node): boolean {
  return (
    ts.isCallExpression(node) &&
    ts.isIdentifier(node.expression) &&
    node.expression.text === "cleanup"
  );
}

function unmountsFirst(body: ts.ConciseBody): boolean {
  if (!ts.isBlock(body)) return isCleanup(body);
  const first = body.statements[0];
  return (
    first !== undefined &&
    ts.isExpressionStatement(first) &&
    isCleanup(first.expression)
  );
}

/** The lines of afterEach hooks in source that tear down before cleanup(). */
function offenders(name: string, source: string): number[] {
  const file = ts.createSourceFile(
    name,
    source,
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const lines: number[] = [];
  const visit = (node: ts.Node) => {
    if (
      ts.isCallExpression(node) &&
      ts.isIdentifier(node.expression) &&
      node.expression.text === "afterEach"
    ) {
      const hook = node.arguments[0];
      if (
        hook &&
        (ts.isArrowFunction(hook) || ts.isFunctionExpression(hook)) &&
        tearsDown(hook.body) &&
        !unmountsFirst(hook.body)
      ) {
        lines.push(
          file.getLineAndCharacterOfPosition(node.getStart()).line + 1,
        );
      }
    }
    ts.forEachChild(node, visit);
  };
  visit(file);
  return lines;
}

const sources = import.meta.glob<string>("../**/*.test.{ts,tsx}", {
  query: "?raw",
  import: "default",
  eager: true,
});

describe("afterEach hooks", () => {
  it("unmount with cleanup() before they reset mocks or delete bindings", () => {
    // A glob that matched nothing would pass without checking anything.
    expect(Object.keys(sources).length).toBeGreaterThan(20);
    const found = Object.entries(sources).flatMap(([path, source]) =>
      offenders(path, source).map((line) => `${path.slice(3)}:${line}`),
    );
    expect(found, "call cleanup() first in these afterEach hooks").toEqual([]);
  });

  it("are told apart by the check", () => {
    const flagged = [
      "afterEach(() => bridge.getRunFailure.mockReset());",
      "afterEach(() => { delete window.go; vi.restoreAllMocks(); });",
      "afterEach(() => { delete (window as { matchMedia?: unknown }).matchMedia; });",
      "afterEach(() => { for (const mock of Object.values(bridge)) mock.mockReset(); });",
      "afterEach(function () { vi.useRealTimers(); cleanup(); getCost.mockReset(); });",
    ];
    for (const source of flagged) {
      expect(offenders("flagged.test.tsx", source), source).toEqual([1]);
    }
    const fine = [
      "afterEach(() => { cleanup(); bridge.getRunFailure.mockReset(); });",
      "afterEach(() => { cleanup(); delete window.go; });",
      "afterEach(() => { window.innerHeight = height; });",
      "afterEach(() => vi.useRealTimers());",
      "beforeEach(() => bridge.getRunFailure.mockReset());",
    ];
    for (const source of fine) {
      expect(offenders("fine.test.tsx", source), source).toEqual([]);
    }
  });
});
