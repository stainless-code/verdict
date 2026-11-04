import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const docsRoot = fileURLToPath(new URL("..", import.meta.url));
const dist = join(docsRoot, process.argv[2] ?? "dist");

const FULL_BUDGET = 72_000;
const INSTALL_COMMAND = "npm i -g @stainless-code/verdict";
const LONG_FORM_PAGES = ["/verdict/changelog", "/verdict/manual"];
const INDEX_FALLBACK_HEADING = "## Agent resources";
const MERGE_LINE = /^(?:[*-]\s+)?[0-9a-f]{40}\s+\S/u;
const CHANGELOG_SOURCE = /^Source: \S*\/changelog\//mu;

const fail = (lines: readonly string[]): never => {
  console.error(lines.join("\n"));
  process.exit(1);
};

const read = (file: string): string => {
  try {
    return readFileSync(join(dist, file), "utf8");
  } catch {
    return fail([
      `llms files: no ${relative(docsRoot, join(dist, file))}.`,
      "Run this after `bun run build`; the docs `build` script does.",
    ]);
  }
};

const mergeSubjects = (file: string, text: string): string[] =>
  text
    .split("\n")
    .map((line, index) => ({ index, line }))
    .filter(({ line }) => MERGE_LINE.test(line))
    .map(({ index, line }) => `  ${file}:${index + 1}: ${line.slice(0, 80)}`);

const markdownMirrors = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      return markdownMirrors(path);
    }
    return entry.name.endsWith(".md") ? [path] : [];
  });

const main = (): void => {
  const index = read("llms.txt");
  const full = read("llms-full.txt");
  const home = read("index.md");

  if (!home.includes(INSTALL_COMMAND)) {
    fail([
      "llms files: index.md is not the landing page.",
      `It does not carry the install command (${INSTALL_COMMAND}), so an agent`,
      "fetching the home page's Markdown gets something other than the hero,",
      "the install and the loop.",
    ]);
  }

  if (home.includes(INDEX_FALLBACK_HEADING)) {
    fail([
      "llms files: index.md is the link index, not the landing page.",
      "Blume stands the llms.txt index in for `/index.md` when no content page",
      "owns `/`; keep `content/index.mdx` so the home page has its own Markdown.",
    ]);
  }

  if (full.length > FULL_BUDGET) {
    fail([
      `llms files: llms-full.txt is ${full.length} chars, over the ${FULL_BUDGET} budget.`,
      "Release notes dominate it; keep the changelog out of the agent corpus",
      "(`ai.exclude` on its entries) or split the page that grew.",
    ]);
  }

  const changelogSections = full.match(CHANGELOG_SOURCE) ?? [];
  if (changelogSections.length > 0) {
    fail([
      `llms files: llms-full.txt carries ${changelogSections.length} changelog page(s).`,
      "Release notes are the longest pages on the site and the least useful in a",
      "single-file corpus; keep them excluded and point llms.txt at the index",
      "(agents.llmsTxt.details).",
    ]);
  }

  const unlisted = LONG_FORM_PAGES.filter((page) => !index.includes(page));
  if (unlisted.length > 0) {
    fail([
      `llms files: llms.txt does not point at ${unlisted.join(", ")}.`,
      "Both are excluded from the agent files for their size, so the index is the",
      "only way an agent finds them; name them in agents.llmsTxt.details.",
    ]);
  }

  const leaked = mergeSubjects("llms.txt", index)
    .concat(mergeSubjects("llms-full.txt", full))
    .concat(
      markdownMirrors(dist).flatMap((file) =>
        mergeSubjects(relative(dist, file), readFileSync(file, "utf8")),
      ),
    );
  if (leaked.length > 0) {
    fail([
      "llms files: a release's merge subject reached an agent-facing file.",
      ...leaked,
      "",
      "A merge commit names a branch and a pull request, not a change; strip it",
      "where the changelog is generated (apps/docs/sources/changelog.ts).",
    ]);
  }

  console.log(
    `llms files: index.md is the landing page, llms-full.txt is ${full.length} chars, no merge subjects.`,
  );
};

main();
