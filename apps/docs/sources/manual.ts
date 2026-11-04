import { watch } from "node:fs";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import type { ContentSource, SourceLoadResult } from "blume/sources";

const repositoryRoot = fileURLToPath(new URL("../../../", import.meta.url));
const docsRoot = join(repositoryRoot, "docs");
const manualPath = join(docsRoot, "manual.md");

const REPOSITORY_ONLY =
  /<!-- repository-only:start -->[\s\S]*?<!-- repository-only:end -->\n*/gu;
const TITLE = /^# [^\n]*\n+/u;
const EDIT_URL =
  "https://github.com/stainless-code/verdict/edit/main/docs/manual.md";

const title = "Manual";
const description =
  "The operator contract: the --json envelope, exit codes, store files, refusal codes, control files, model wiring, and every provider.";

const frontmatter = [
  "---",
  `title: ${JSON.stringify(title)}`,
  `description: ${JSON.stringify(description)}`,
  "ai:",
  "  exclude: true",
  "---",
  "",
].join("\n");

const load = async (): Promise<SourceLoadResult> => {
  const manual = await readFile(manualPath, "utf8");
  const body = manual.replace(REPOSITORY_ONLY, "").replace(TITLE, "");
  return {
    diagnostics: [],
    entries: [
      {
        ref: "manual.md",
        data: { ai: { exclude: true }, description, title },
        body: { format: "md", text: body },
        raw: `${frontmatter}${body}`,
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
