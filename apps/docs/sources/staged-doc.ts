import { watch } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

import type { ContentSource, SourceLoadResult } from "blume/sources";

import { docsRoot, type DocsPage } from "../docs-index.ts";

// A second `filesystem()` source rooted at the repository's `docs/` is refused
// with `BLUME_ENTRY_ID_MISMATCH`, since the `docs` collection can only be
// rooted at one directory, so each published `docs/` file is read as a single
// staged file through this one factory.
export const stagedDocSource = (page: DocsPage): ContentSource => {
  const sourcePath = join(docsRoot, page.file);
  const editUrl = `https://github.com/stainless-code/verdict/edit/main/docs/${page.file}`;

  // The page needs its own meta description (the site description is the
  // landing page's), and a staged entry's head reads it from the file's
  // frontmatter. No `title` line: with none, Blume titles the page from the
  // `#` heading `docs/${page.file}` opens with, and renders that heading once.
  const description = page.summary.replace(/^./u, (first) =>
    first.toUpperCase(),
  );
  const frontmatter = `---\nseo:\n  description: ${JSON.stringify(description)}\n---\n\n`;

  const load = async (): Promise<SourceLoadResult> => {
    const body = await readFile(sourcePath, "utf8");
    return {
      diagnostics: [],
      entries: [
        {
          ref: page.file,
          data: {
            ai: { exclude: true },
            sidebar: { label: page.title },
          },
          body: { format: "md", text: body },
          raw: `${frontmatter}${body}`,
          sourcePath,
          editUrl,
        },
      ],
    };
  };

  return {
    contentRoot: docsRoot,
    load,
    name: page.file.replace(/\.md$/u, ""),
    staged: true,
    watch: (onChange) => {
      const watcher = watch(sourcePath, onChange);
      return () => watcher.close();
    },
  };
};
