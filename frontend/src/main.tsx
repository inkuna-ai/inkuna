import { renderInkuna } from "./extension";

// Inkuna's own bundle adds no screens; an embedding frontend calls
// renderInkuna with its own.
renderInkuna(document.getElementById("root")!, []);
