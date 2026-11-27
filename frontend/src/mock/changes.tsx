import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "../index.css";
import { mockService } from "./fixtures";
import { Mock } from "./Mock";

// The bridge reaches Go through window.go, so the fixtures stand in there.
window.go = { app: { Service: mockService as never } };

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Mock />
  </StrictMode>,
);
