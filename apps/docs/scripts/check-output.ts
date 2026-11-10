import { readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

// The output contract `blume build` cannot state itself: how large the
// single-file agent corpus is. (Manual links need no output check: the manual
// is repository-only, and `check-docs.ts` proves every section deep-link
// names a heading the manual still carries.)
const docsRoot = fileURLToPath(new URL("..", import.meta.url));
const dist = join(docsRoot, process.argv[2] ?? "dist");

const FULL_BUDGET = 72_000;
const INSTALL_COMMAND = "npm i -g @stainless-code/verdict";

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

console.log(
  `docs output: index.md is the landing page, llms-full.txt is ${full.length} chars.`,
);
