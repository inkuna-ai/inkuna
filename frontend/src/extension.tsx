import { StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import "./index.css";
import App from "./App";
import AppErrorBoundary from "./AppErrorBoundary";
import type { ExtensionView } from "./types";
import { ArtifactViewerProvider } from "./views/artifacts/ArtifactViewer";

export type { ExtensionView, ExtensionViewID } from "./types";

/**
 * Renders Inkuna into root, with the screens an embedding frontend adds.
 * Inkuna's own bundle passes none. Each view is shown only while Go reports
 * its capability, so a build without the extension never shows its screen.
 * It returns the React root, which a test unmounts.
 */
export function renderInkuna(root: HTMLElement, views: ExtensionView[]): Root {
  const ids = new Set<string>();
  for (const view of views) {
    if (ids.has(view.id))
      throw new Error(`two extension views share the id "${view.id}"`);
    ids.add(view.id);
  }
  const app = createRoot(root);
  app.render(
    <StrictMode>
      <AppErrorBoundary>
        {/* One artifact drawer for every view, opened from any link. */}
        <ArtifactViewerProvider>
          <App views={views} />
        </ArtifactViewerProvider>
      </AppErrorBoundary>
    </StrictMode>,
  );
  return app;
}
