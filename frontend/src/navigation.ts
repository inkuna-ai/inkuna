import type { Capability, ExtensionView, View } from "./types";

export type NavItem = { id: View; label: string; glyph: string };
/** A group without a label renders as a standalone item between the sections. */
export type NavGroup = { label?: string; items: NavItem[] };

const work: NavItem[] = [
  { id: "projects", label: "Projects", glyph: "◇" },
  { id: "request", label: "New request", glyph: "+" },
  { id: "workflow", label: "Workflow", glyph: "⌘" },
  { id: "tasks", label: "Tasks", glyph: "◉" },
  { id: "costs", label: "Costs", glyph: "$" },
  { id: "docs", label: "Docs", glyph: "¶" },
  { id: "branches", label: "Branches", glyph: "⎇" },
];

const configure: NavItem[] = [
  { id: "agents", label: "Agents", glyph: "⚇" },
  { id: "rules", label: "Rules", glyph: "⇶" },
  { id: "runtimes", label: "Runtimes", glyph: "▣" },
  { id: "setup", label: "Settings", glyph: "⚙" },
  { id: "connections", label: "Connections", glyph: "↗" },
];

/**
 * The sidebar: Inkuna's own screens, which are never gated, with each shown
 * extension screen at the end of its group.
 */
export function navigation(shown: ExtensionView[]): NavGroup[] {
  const added = (group: ExtensionView["group"]): NavItem[] =>
    shown
      .filter((view) => view.group === group)
      .map((view) => ({
        id: view.id,
        label: view.label,
        glyph: view.glyph ?? "•",
      }));
  return [
    { label: "Work", items: [...work, ...added("work")] },
    { items: [{ id: "messages", label: "Messages", glyph: "✉" }] },
    { label: "Configure", items: [...configure, ...added("configure")] },
  ];
}

/**
 * The extension screens to show, in the order they were passed: those whose
 * capability Go reports. Go decides what a build has; the view only follows.
 */
export function shownViews(
  views: ExtensionView[],
  capabilities: Capability[],
): ExtensionView[] {
  const reported = new Set(capabilities.map((capability) => capability.id));
  return views.filter((view) => reported.has(view.id));
}
