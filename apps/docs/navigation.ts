export interface DocsSection {
  readonly label: string;
  readonly landing: string;
  readonly path: string;
}

export interface DocsTreePage {
  readonly docsFile?: string;
  readonly route: string;
}

export interface DocsTreeGroup {
  readonly label: string;
  readonly pages: readonly DocsTreePage[];
}

export interface DocsRedirect {
  readonly from: string;
  readonly to: string;
}

/** The header's five sections, each with the route its tab lands on. */
export const docsSections: readonly DocsSection[] = [
  { label: "Docs", landing: "/docs/quickstart", path: "/docs" },
  { label: "Agents", landing: "/agents", path: "/agents" },
  { label: "Compare", landing: "/compare", path: "/compare" },
  { label: "Roadmap", landing: "/roadmap", path: "/roadmap" },
  { label: "Changelog", landing: "/changelog", path: "/changelog" },
];

/** Every docs page, grouped the way the sidebar and footer list it. */
export const docsTree: readonly DocsTreeGroup[] = [
  {
    label: "Start",
    pages: [
      { route: "/docs/quickstart" },
      { route: "/docs/glossary" },
      { route: "/docs/why-verdict" },
    ],
  },
  { label: "Concepts", pages: [{ route: "/docs/how-it-reviews" }] },
  {
    label: "Reference",
    pages: [
      { docsFile: "manual.md", route: "/docs/manual" },
      { docsFile: "troubleshooting.md", route: "/docs/troubleshooting" },
    ],
  },
  { label: "Build on Verdict", pages: [{ route: "/docs/build-a-client" }] },
  { label: "Agents", pages: [{ route: "/agents" }] },
  { label: "Compare", pages: [{ route: "/compare" }] },
  { label: "Roadmap", pages: [{ route: "/roadmap" }] },
];

/** Old routes and where they land now. */
export const docsRedirects: readonly DocsRedirect[] = [
  { from: "/quickstart", to: "/docs/quickstart" },
  { from: "/how-it-reviews", to: "/docs/how-it-reviews" },
  { from: "/integrations", to: "/agents" },
  { from: "/why-verdict", to: "/docs/why-verdict" },
  { from: "/comparison", to: "/compare" },
  { from: "/glossary", to: "/docs/glossary" },
  { from: "/build-a-client", to: "/docs/build-a-client" },
  { from: "/manual", to: "/docs/manual" },
  { from: "/troubleshooting", to: "/docs/troubleshooting" },
];

/** The three the landing footer lists, out of the new routes. */
export const footerPaths: readonly { label: string; path: string }[] = [
  { label: "Quickstart", path: "/docs/quickstart" },
  { label: "How it reviews", path: "/docs/how-it-reviews" },
  { label: "Comparison", path: "/compare" },
];

/** The route a staged `docs/` file publishes at. */
export const docsRoute = (file: string): string => {
  for (const group of docsTree) {
    for (const page of group.pages) {
      if (page.docsFile === file) {
        return page.route;
      }
    }
  }
  throw new Error(
    `apps/docs/navigation.ts lists no route for docs/${file}; add one to docsTree.`,
  );
};
