import type { DocsPage } from "./docs-index.ts";

export interface SitePage {
  /**
   * Set when the page's text lives in `docs/`, so `docs/README.md` and this
   * entry are the same page listed for two surfaces.
   */
  readonly docsFile?: string;
  readonly label: string;
  readonly path: string;
}

// The reader's path, not the repo's: install, see the loop, pick a surface,
// then positioning, then the log. The header, the 404 page, `llms.txt` and the
// landing footer all read this list, so a page can't be in one and missing from
// another.
export const sitePages = (manual: DocsPage): readonly SitePage[] => [
  { label: "Quickstart", path: "/quickstart" },
  { label: "How it reviews", path: "/how-it-reviews" },
  { label: "Integrations", path: "/integrations" },
  { label: "Why Verdict", path: "/why-verdict" },
  { label: "Comparison", path: "/comparison" },
  { label: "Roadmap", path: "/roadmap" },
  { docsFile: manual.file, label: manual.title, path: manual.route },
  { label: "Changelog", path: "/changelog" },
];

/** The three the landing footer lists, out of the same order. */
export const footerPaths: readonly string[] = [
  "/quickstart",
  "/how-it-reviews",
  "/comparison",
];
