import { fireEvent, render, screen, within } from "@testing-library/react";
import { useState } from "react";
import type { ConfigFieldError, ConfigFile } from "../../types";
import { emptyConfigFile } from "../../types";
import { MCPServersPanel } from "./MCPServersPanel";
import { FieldErrorsContext } from "./overrides";

// The panel inside a draft the way SetupView keeps one, so each edit can be
// read back from what would be saved.
function Harness({
  initial = emptyConfigFile,
  problems = [],
  onChange,
}: {
  initial?: ConfigFile;
  problems?: ConfigFieldError[];
  onChange(file: ConfigFile): void;
}) {
  const [file, setFile] = useState<ConfigFile>(initial);
  return (
    <FieldErrorsContext.Provider value={problems}>
      <MCPServersPanel
        servers={file.mcpServers ?? []}
        secrets={[{ name: "DOCS_TOKEN" }, { name: "LINEAR_API_KEY" }]}
        update={(mutate) =>
          setFile((current) => {
            const draft: ConfigFile = JSON.parse(JSON.stringify(current));
            mutate(draft);
            onChange(draft);
            return draft;
          })
        }
      />
    </FieldErrorsContext.Provider>
  );
}

function type(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

describe("MCPServersPanel", () => {
  it("declares a command server with its argv and the secrets it is given", () => {
    let saved = emptyConfigFile;
    render(<Harness onChange={(file) => (saved = file)} />);
    expect(screen.getByText("No MCP servers declared.")).toBeInTheDocument();
    type("MCP server name", "docs-search");
    type("MCP server description", "Searches the design archive");
    type("MCP server command", 'npx -y "@example/docs mcp@2.1.0"');
    type("MCP server environment", "DOCS_TOKEN=DOCS_TOKEN");
    type("MCP server tools", "search, read_page");
    fireEvent.click(screen.getByRole("button", { name: "Add server" }));
    expect(saved.mcpServers).toEqual([
      {
        name: "docs-search",
        description: "Searches the design archive",
        command: ["npx", "-y", "@example/docs mcp@2.1.0"],
        env: { DOCS_TOKEN: "DOCS_TOKEN" },
        tools: ["search", "read_page"],
      },
    ]);
    const row = within(
      screen.getByRole("table", { name: "Declared MCP servers" }),
    ).getAllByRole("row")[1];
    expect(row).toHaveTextContent("DOCS_TOKEN ← DOCS_TOKEN");
    expect(row).toHaveTextContent("search, read_page");
  });

  it("edits a URL server in place, and removes one", () => {
    let saved = emptyConfigFile;
    const initial: ConfigFile = {
      ...emptyConfigFile,
      mcpServers: [
        { name: "linear", url: "https://mcp.linear.app/mcp" },
        { name: "other", command: ["other-mcp"] },
      ],
    };
    render(<Harness initial={initial} onChange={(file) => (saved = file)} />);
    fireEvent.click(
      screen.getByRole("button", { name: "Edit MCP server linear" }),
    );
    expect(screen.getByLabelText("MCP server URL")).toHaveValue(
      "https://mcp.linear.app/mcp",
    );
    fireEvent.change(screen.getByLabelText("MCP server bearer secret"), {
      target: { value: "LINEAR_API_KEY" },
    });
    fireEvent.change(screen.getByLabelText("MCP server tracker"), {
      target: { value: "linear" },
    });
    type("MCP server tools", "get_issue");
    fireEvent.click(screen.getByRole("button", { name: "Update server" }));
    expect(saved.mcpServers).toEqual([
      { name: "other", command: ["other-mcp"] },
      {
        name: "linear",
        url: "https://mcp.linear.app/mcp",
        bearer: "LINEAR_API_KEY",
        tools: ["get_issue"],
        tracker: "linear",
      },
    ]);
    fireEvent.click(
      screen.getByRole("button", { name: "Remove MCP server other" }),
    );
    expect(saved.mcpServers?.map((server) => server.name)).toEqual(["linear"]);
  });

  it("keeps both servers when a rename takes another's name, for Go to refuse", () => {
    let saved = emptyConfigFile;
    const initial: ConfigFile = {
      ...emptyConfigFile,
      mcpServers: [
        { name: "docs", command: ["docs-mcp"] },
        { name: "archive", command: ["archive-mcp"] },
      ],
    };
    render(<Harness initial={initial} onChange={(file) => (saved = file)} />);
    fireEvent.click(
      screen.getByRole("button", { name: "Edit MCP server docs" }),
    );
    type("MCP server name", "archive");
    fireEvent.click(screen.getByRole("button", { name: "Update server" }));
    expect(saved.mcpServers).toEqual([
      { name: "archive", command: ["archive-mcp"] },
      { name: "archive", command: ["docs-mcp"] },
    ]);
  });

  it("keeps both servers when a new one takes another's name, for Go to refuse", () => {
    let saved = emptyConfigFile;
    const initial: ConfigFile = {
      ...emptyConfigFile,
      mcpServers: [{ name: "docs", command: ["docs-mcp"] }],
    };
    render(<Harness initial={initial} onChange={(file) => (saved = file)} />);
    type("MCP server name", "docs");
    type("MCP server command", "other-docs-mcp");
    fireEvent.click(screen.getByRole("button", { name: "Add server" }));
    expect(saved.mcpServers).toEqual([
      { name: "docs", command: ["docs-mcp"] },
      { name: "docs", command: ["other-docs-mcp"] },
    ]);
  });

  it("edits or removes one of two rows sharing a name and keeps the other", () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    let saved = emptyConfigFile;
    // What a rename onto another server's name leaves until Go refuses it.
    const initial: ConfigFile = {
      ...emptyConfigFile,
      mcpServers: [
        { name: "archive", command: ["archive-mcp"] },
        { name: "archive", command: ["docs-mcp"] },
      ],
    };
    render(<Harness initial={initial} onChange={(file) => (saved = file)} />);
    fireEvent.click(
      screen.getAllByRole("button", { name: "Edit MCP server archive" })[1],
    );
    type("MCP server name", "docs");
    fireEvent.click(screen.getByRole("button", { name: "Update server" }));
    expect(saved.mcpServers).toEqual([
      { name: "archive", command: ["archive-mcp"] },
      { name: "docs", command: ["docs-mcp"] },
    ]);
    fireEvent.click(
      screen.getByRole("button", { name: "Remove MCP server archive" }),
    );
    expect(saved.mcpServers).toEqual([{ name: "docs", command: ["docs-mcp"] }]);
    expect(error.mock.calls.flat().join(" ")).not.toMatch(/same key/);
    error.mockRestore();
  });

  it("shows Go's reason beside the panel", () => {
    render(
      <Harness
        problems={[
          {
            field: "mcpServers",
            message:
              'MCP server "linear": a tracker\'s server must list its tools, and only ones that read',
          },
        ]}
        onChange={() => {}}
      />,
    );
    expect(
      screen.getByText(/a tracker's server must list its tools/),
    ).toBeInTheDocument();
  });
});
