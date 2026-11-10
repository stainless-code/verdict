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
    // One sitemap footer on the three content landings: Blume renders
    // PageFooter at the foot of every content page with its route, and the
    // component renders only for /agents, /compare and /roadmap. The home
    // page imports the same file directly.
    PageFooter: "./components/SitemapFooter.astro",
  },
});
