import "@testing-library/jest-dom/vitest";
import { configure } from "@testing-library/react";

// The app views render a ReactFlow graph, which a loaded CI runner draws
// slowly; the default one-second wait for findBy* and waitFor is too tight
// there. Local runs still finish as soon as the element appears.
configure({ asyncUtilTimeout: 5000 });

class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}

globalThis.ResizeObserver = ResizeObserverMock;
