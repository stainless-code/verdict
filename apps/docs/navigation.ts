export interface DocsSection {
  readonly label: string;
  readonly landing: string;
  readonly path: string;
}

export interface DocsTreePage {
  readonly label: string;
  readonly docsFile?: string;
  readonly route: string;
}

export interface DocsTreeGroup {
  readonly label: string;
  readonly pages: readonly DocsTreePage[];
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
      { label: "Quickstart", route: "/docs/quickstart" },
      { label: "Glossary", route: "/docs/glossary" },
    ],
  },
  {
    label: "Guides",
    pages: [
      {
        label: "Review a branch without a PR",
        route: "/docs/local-review",
      },
      { label: "Gate a PR in CI", route: "/docs/ci-gate" },
      { label: "Review an untrusted PR", route: "/docs/untrusted-pr" },
    ],
  },
  {
    label: "Concepts",
    pages: [{ label: "How it reviews", route: "/docs/how-it-reviews" }],
  },
  {
    label: "Reference",
    pages: [
      {
        label: "Refusal codes",
        docsFile: "troubleshooting.md",
        route: "/docs/troubleshooting",
      },
      { label: "JSON envelope", route: "/docs/json-envelope" },
    ],
  },
  {
    label: "Build on Verdict",
    pages: [
      { label: "Build a client", route: "/docs/build-a-client" },
      { label: "MCP server", route: "/docs/mcp-server" },
      { label: "Go SDK", route: "/docs/go-sdk" },
      { label: "TypeScript client", route: "/docs/typescript-client" },
    ],
  },
  { label: "Agents", pages: [{ label: "Agents", route: "/agents" }] },
  { label: "Compare", pages: [{ label: "Compare", route: "/compare" }] },
  { label: "Roadmap", pages: [{ label: "Roadmap", route: "/roadmap" }] },
];

export interface FooterLink {
  readonly label: string;
  readonly path: string;
}

export interface FooterColumn {
  readonly label: string;
  readonly links: readonly FooterLink[];
}

/** Columns the landing footer renders: one per docs group, then Project. */
export const footerColumns: readonly FooterColumn[] = [
  ...docsTree
    .filter((group) =>
      group.pages.every((page) => page.route.startsWith("/docs/")),
    )
    .map((group) => ({
      label: group.label,
      links: group.pages.map((page) => ({
        label: page.label,
        path: page.route,
      })),
    })),
  {
    label: "Project",
    links: docsSections
      .filter((section) => section.path !== "/docs")
      .map((section) => ({ label: section.label, path: section.landing })),
  },
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
