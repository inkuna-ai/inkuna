import { navigation, shownViews } from "./navigation";
import type { ExtensionView } from "./types";

const audit: ExtensionView = {
  id: "audit.records",
  label: "Audit records",
  group: "work",
  glyph: "§",
  render: () => null,
};
const access: ExtensionView = {
  id: "acme-sso.access",
  label: "Access",
  group: "configure",
  render: () => null,
};

describe("navigation", () => {
  it("lists only Inkuna's screens with no extension screen shown", () => {
    const groups = navigation([]);
    expect(groups.map((group) => group.label)).toEqual([
      "Work",
      undefined,
      "Configure",
    ]);
    expect(groups[0].items.at(-1)?.id).toBe("branches");
    expect(groups[2].items.at(-1)?.id).toBe("connections");
  });

  it("adds each extension screen at the end of its group", () => {
    const groups = navigation([audit, access]);
    expect(groups[0].items.at(-1)).toEqual({
      id: "audit.records",
      label: "Audit records",
      glyph: "§",
    });
    expect(groups[1].items.map((item) => item.id)).toEqual(["messages"]);
    expect(groups[2].items.at(-1)).toEqual({
      id: "acme-sso.access",
      label: "Access",
      glyph: "•",
    });
  });
});

describe("shownViews", () => {
  it("keeps the screens whose capability Go reports, in their order", () => {
    expect(
      shownViews(
        [audit, access],
        [
          { id: "acme-sso.access", label: "Access", extension: "acme-sso" },
          { id: "audit.records", label: "Audit records", extension: "audit" },
        ],
      ),
    ).toEqual([audit, access]);
  });

  it("hides a screen whose capability Go does not report", () => {
    expect(
      shownViews(
        [audit, access],
        [{ id: "audit.records", label: "Audit records", extension: "audit" }],
      ),
    ).toEqual([audit]);
    expect(shownViews([audit], [])).toEqual([]);
  });
});
