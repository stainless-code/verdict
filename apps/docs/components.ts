import { defineComponents } from "blume";

export default defineComponents({
  // Available in every .mdx page without an import; the landing hero imports
  // the same file directly.
  mdx: {
    InstallSwitcher: "./components/InstallSwitcher.astro",
  },
  layout: {
    // One header everywhere: the landing page imports the same file directly,
    // docs pages get it through this slot. String paths, so the `.astro`
    // files stay out of this TypeScript project.
    Header: "./components/blume/Header.astro",
    // Docs pages render the whole tree: sections from the header tabs, pages
    // from the sidebar, so nothing is more than one click away.
    Footer: "./components/blume/Footer.astro",
  },
});
