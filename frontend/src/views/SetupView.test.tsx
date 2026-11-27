import { render, screen } from "@testing-library/react";
import { emptyConfigFile, type ConfigurationInfo } from "../types";
import { SetupView } from "./SetupView";

// The panels keep the bridge's own outside the app, which answer with preview
// data; only the configuration is this test's.
const getConfiguration = vi.hoisted(() => vi.fn());
const getCapabilities = vi.hoisted(() => vi.fn());
vi.mock("../bridge", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../bridge")>()),
  getConfiguration,
  getCapabilities,
}));

const configuration: ConfigurationInfo = {
  path: "/home/me/.config/inkuna/inkuna.yaml",
  exists: true,
  dataDir: "/home/me/.local/share/inkuna",
  file: emptyConfigFile,
  secrets: [],
  build: {
    version: "v0.4.0-2-g297037d-dirty",
    commit: "297037d0c5e1b2a3f4d5e6f708192a3b4c5d6e7f",
  },
};

describe("SetupView", () => {
  it("says which build is running", async () => {
    getConfiguration.mockResolvedValue(configuration);
    getCapabilities.mockResolvedValue({
      product: { name: "Inkuna", version: "v0.4.0", inkuna: true },
      capabilities: [],
    });
    render(<SetupView onSaved={() => {}} />);
    expect(
      await screen.findByText(
        "Inkuna v0.4.0-2-g297037d-dirty · commit 297037d0c5e1b2a3f4d5e6f708192a3b4c5d6e7f",
      ),
    ).toBeInTheDocument();
  });

  it("names a product that embeds Inkuna and what its extensions add", async () => {
    getConfiguration.mockResolvedValue(configuration);
    getCapabilities.mockResolvedValue({
      product: { name: "Acme Desk", version: "2.1.0", inkuna: false },
      capabilities: [
        { id: "audit.records", label: "Audit records", extension: "audit" },
        {
          id: "acme-sso.sign-in",
          label: "Single sign-on",
          extension: "acme-sso",
        },
      ],
    });
    render(<SetupView onSaved={() => {}} />);
    expect(await screen.findByText("Acme Desk 2.1.0")).toBeInTheDocument();
    expect(
      screen.getByText("extensions add Audit records, Single sign-on"),
    ).toBeInTheDocument();
    expect(
      screen.getByText(/^Inkuna v0\.4\.0-2-g297037d-dirty · commit/),
    ).toBeInTheDocument();
  });

  it("names no other product for Inkuna's own build", async () => {
    getConfiguration.mockResolvedValue(configuration);
    getCapabilities.mockResolvedValue({
      product: { name: "Inkuna", version: "v0.4.0", inkuna: true },
      capabilities: null,
    });
    render(<SetupView onSaved={() => {}} />);
    await screen.findByText(/^Inkuna v0\.4\.0-2-g297037d-dirty/);
    expect(screen.queryByText(/extensions add/)).not.toBeInTheDocument();
    expect(screen.queryByText("Inkuna v0.4.0")).not.toBeInTheDocument();
  });
});
