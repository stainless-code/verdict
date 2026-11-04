import { watch } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

import type { ContentSource, SourceLoadResult } from "blume/sources";

import { docsPage, docsRoot } from "../docs-index.ts";

// A second `filesystem()` source rooted at the repository's `docs/` is refused
// with `BLUME_ENTRY_ID_MISMATCH`, since the `docs` collection can only be
// rooted at one directory, so the manual is read as a single staged file.
const manualPath = join(docsRoot, "manual.md");
const EDIT_URL =
  "https://github.com/stainless-code/verdict/edit/main/docs/manual.md";

const manualPage = docsPage("manual.md");

// The page needs its own meta description (the site description is the landing
// page's), and a staged entry's head reads it from the file's frontmatter. No
// `title` line: with none, Blume titles the page from the `#` heading
// `docs/manual.md` opens with, and renders that heading once.
const description = manualPage.summary.replace(/^./u, (first) =>
  first.toUpperCase(),
);
const frontmatter = `---\nseo:\n  description: ${JSON.stringify(description)}\n---\n\n`;

const load = async (): Promise<SourceLoadResult> => {
  const manual = await readFile(manualPath, "utf8");
  return {
    diagnostics: [],
    entries: [
      {
        ref: "manual.md",
        data: {
          ai: { exclude: true },
          sidebar: { label: manualPage.title },
        },
        body: { format: "md", text: manual },
        raw: `${frontmatter}${manual}`,
        sourcePath: manualPath,
        editUrl: EDIT_URL,
      },
    ],
  };
};

export const manualSource = (): ContentSource => ({
  contentRoot: docsRoot,
  load,
  name: "manual",
  staged: true,
  watch: (onChange) => {
    const watcher = watch(manualPath, onChange);
    return () => watcher.close();
  },
});
