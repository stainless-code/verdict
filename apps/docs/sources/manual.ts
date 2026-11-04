import { watch } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import type { ContentSource, SourceLoadResult } from "blume/sources";

// `docs/manual.md` is the operator contract and lives in the repository root,
// outside the app's content directory: a second `filesystem()` source rooted
// there is refused with `BLUME_ENTRY_ID_MISMATCH`, since the `docs` collection
// can only be rooted at one directory. So this points at the single file and
// publishes it as staged content, letting Blume do the rest: no frontmatter
// `title`, so the page takes its title from the `#` heading the file opens
// with, and Blume renders that heading once (it drops the body copy).
const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url));
const docsRoot = join(repositoryRoot, "docs");
const manualPath = join(docsRoot, "manual.md");
const EDIT_URL =
  "https://github.com/stainless-code/verdict/edit/main/docs/manual.md";

const description =
  "The operator contract: the --json envelope, exit codes, store files, refusal codes, control files, model wiring, and every provider.";

// The published page needs its own meta description (the site description is the
// landing page's), and a staged entry's head reads it from the file's
// frontmatter. No `title` line: with none, Blume titles the page from the `#`
// heading `docs/manual.md` opens with, and renders that heading once.
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
          sidebar: { label: "Manual" },
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
