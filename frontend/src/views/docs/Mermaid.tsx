import { useEffect, useId, useMemo, useState } from "react";

type MermaidAPI = (typeof import("mermaid"))["default"];

let loading: Promise<MermaidAPI> | null = null;

/**
 * Mermaid is large, so it loads the first time a doc has a diagram. Strict
 * security makes it sanitize labels and refuse click handlers and scripts,
 * since a doc's diagram is repository content, not app code.
 */
function loadMermaid(): Promise<MermaidAPI> {
  loading ??= import("mermaid").then(({ default: mermaid }) => {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: "dark",
      fontFamily: "Inter, ui-sans-serif, system-ui, sans-serif",
    });
    return mermaid;
  });
  return loading;
}

export function MermaidDiagram({ source }: { source: string }) {
  // Mermaid uses the id as an element id and in CSS selectors.
  const id = `doc-diagram-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  const [svg, setSvg] = useState("");
  const [error, setError] = useState("");
  // React re-sets innerHTML whenever this object changes, which would
  // replace the drawn SVG on every render.
  const html = useMemo(() => ({ __html: svg }), [svg]);

  useEffect(() => {
    let active = true;
    setSvg("");
    setError("");
    loadMermaid()
      .then((mermaid) => mermaid.render(id, source))
      .then((result) => {
        if (active) setSvg(result.svg);
      })
      .catch((reason: unknown) => {
        // A failed render can leave Mermaid's scratch element behind.
        document.getElementById(`d${id}`)?.remove();
        if (active) setError(String(reason));
      });
    return () => {
      active = false;
    };
  }, [id, source]);

  if (svg) {
    return (
      <figure
        aria-label="Diagram"
        className="my-4 overflow-x-auto border border-line bg-field p-4 [&_svg]:mx-auto [&_svg]:h-auto [&_svg]:max-w-full"
        // Mermaid's own sanitizer has run over this under strict security.
        dangerouslySetInnerHTML={html}
      />
    );
  }
  return (
    <figure className="my-4 border border-line bg-field p-4">
      <figcaption
        className={`mb-2 text-[11px] ${error ? "text-danger" : "text-muted"}`}
      >
        {error
          ? `The diagram could not be drawn: ${error}`
          : "Drawing diagram…"}
      </figcaption>
      <pre className="m-0 overflow-x-auto font-mono text-xs text-ink-soft">
        {source}
      </pre>
    </figure>
  );
}
