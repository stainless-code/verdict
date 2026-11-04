import { defineComponents } from "blume";

export default defineComponents({
  layout: {
    // One header everywhere: the landing page imports the same file directly,
    // docs pages get it through this slot. String paths, so the `.astro`
    // files stay out of this TypeScript project.
    Header: "./components/blume/Header.astro",
    // Docs pages render no site footer; the landing page carries its own.
    Footer: "./components/blume/EmptyFooter.astro",
  },
});
