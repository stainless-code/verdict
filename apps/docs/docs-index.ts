import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

export interface DocsPage {
  /** Path relative to `docs/`, the file name `docs/README.md` links. */
  readonly file: string;
  /** `true` under "Published on the site", `false` under "Repository-only". */
  readonly published: boolean;
  readonly summary: string;
  readonly title: string;
}

export const docsRoot = fileURLToPath(new URL("../../docs", import.meta.url));

const ENTRY =
  /^- \[(?<title>[^\]]+)\]\((?<file>[^)\s#]+?\.md)(?:#[^)\s]*)?\)(?::\s*(?<summary>.+))?$/u;

const parse = (): DocsPage[] => {
  const pages: DocsPage[] = [];
  let published: boolean | null = null;
  for (const line of readFileSync(join(docsRoot, "README.md"), "utf8").split(
    "\n",
  )) {
    const heading = /^##\s+(.+?)\s*$/u.exec(line);
    if (heading !== null) {
      published = /^published\b/iu.test(heading[1])
        ? true
        : /^repository-only\b/iu.test(heading[1])
          ? false
          : null;
      continue;
    }
    const entry = ENTRY.exec(line.trim());
    if (entry?.groups === undefined || published === null) {
      continue;
    }
    const { file, summary = "", title } = entry.groups;
    pages.push({
      file,
      published,
      summary: summary.trim(),
      title: title.trim(),
    });
  }
  return pages;
};

export const docsPages: readonly DocsPage[] = parse();

export const docsPage = (file: string): DocsPage => {
  const page = docsPages.find((candidate) => candidate.file === file);
  if (page === undefined) {
    throw new Error(
      `docs/README.md has no entry for docs/${file}; add \`- [Title](${file}): what it covers.\` to the section for its surface.`,
    );
  }
  return page;
};
