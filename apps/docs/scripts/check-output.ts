import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

// The two output contracts `blume build` cannot state itself: how large the
// single-file agent corpus is, and that the site sends readers to its own
// manual instead of the GitHub copy.
const docsRoot = fileURLToPath(new URL("..", import.meta.url));
const dist = join(docsRoot, process.argv[2] ?? "dist");

const FULL_BUDGET = 72_000;
const INSTALL_COMMAND = "npm i -g @stainless-code/verdict";
const MANUAL_SOURCE = "blob/main/docs/manual.md";

const fail = (lines: readonly string[]): never => {
  console.error(lines.join("\n"));
  process.exit(1);
};

const read = (file: string): string => {
  try {
    return readFileSync(join(dist, file), "utf8");
  } catch {
    return fail([
      `docs output: no ${relative(docsRoot, join(dist, file))}.`,
      "Run this after `bun run build`; the docs `build` script does.",
    ]);
  }
};

const published = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      return published(path);
    }
    return /\.(?:html|md)$/u.test(entry.name) ? [path] : [];
  });

const home = read("index.md");
const full = read("llms-full.txt");

if (!home.includes(INSTALL_COMMAND)) {
  fail([
    "docs output: index.md is not the landing page.",
    `It does not carry the install command (${INSTALL_COMMAND}), so an agent`,
    "fetching the home page's Markdown gets something other than the hero,",
    "the install and the loop. Keep `content/index.mdx` so `/` has its own",
    "Markdown rather than the llms.txt link index.",
  ]);
}

if (full.length > FULL_BUDGET) {
  fail([
    `docs output: llms-full.txt is ${full.length} chars, over the ${FULL_BUDGET} budget.`,
    "A single-file corpus has to stay inside one agent's context window; split",
    "the page that grew, or keep it out of the corpus with `ai.exclude`.",
  ]);
}

const githubLinks = published(dist).flatMap((file) =>
  readFileSync(file, "utf8").includes(MANUAL_SOURCE)
    ? [`  ${relative(dist, file)}`]
    : [],
);
if (githubLinks.length > 0) {
  fail([
    "docs output: the site still sends readers to the GitHub copy of the manual.",
    ...githubLinks,
    "",
    '`/manual` is the page; link it as `/manual` (or `contentHref("/manual")`',
    "in an Astro component) with the anchor the manual's own headings carry.",
  ]);
}

console.log(
  `docs output: index.md is the landing page, llms-full.txt is ${full.length} chars, no GitHub-copy links.`,
);
