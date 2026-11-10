import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

import { docsTree } from "../navigation.ts";

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

// `llms.txt` lists the docs tree in sidebar order with one line per page, so
// the route sequence it carries must equal the `docsTree` order minus the
// pages kept out of the corpus. Corpus membership is frontmatter `ai.exclude`
// on the content file, plus two source-level exclusions: the staged
// troubleshooting page (`sources/troubleshooting.ts` sets `ai.exclude`) and
// the changelog timeline (every entry excluded in `sources/changelog.ts`).
const contentDir = join(docsRoot, "content");
const excluded = new Set(["/docs/troubleshooting"]);
const scan = (dir: string): void => {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      scan(path);
    } else if (
      entry.isFile() &&
      entry.name.endsWith(".mdx") &&
      entry.name !== "index.mdx"
    ) {
      // Group folders `(name)` contribute no route segment; every other page
      // file publishes at `/docs/<basename>`.
      const text = readFileSync(path, "utf8");
      if (/^ai:\s*\n  exclude:\s*true\s*$/mu.test(text)) {
        excluded.add(`/docs/${entry.name.replace(/\.mdx$/u, "")}`);
      }
    }
  }
};
scan(contentDir);
// The three content landings publish at `/<tab>` from `0N-<tab>/index.mdx`.
for (const entry of readdirSync(contentDir, { withFileTypes: true })) {
  if (!entry.isDirectory()) {
    continue;
  }
  const tab = entry.name.replace(/^\d+-/u, "");
  const twin = join(contentDir, entry.name, "index.mdx");
  if (
    existsSync(twin) &&
    /^ai:\s*\n  exclude:\s*true\s*$/mu.test(readFileSync(twin, "utf8"))
  ) {
    excluded.add(`/${tab}`);
  }
}

const expected = [
  "/",
  ...docsTree.flatMap((group) => group.pages.map((page) => page.route)),
].filter((route) => !excluded.has(route));

const listed: string[] = [];
for (const line of read("llms.txt").split("\n")) {
  const item = /^-\s*\[[^\]]*\]\((https?:\/\/[^)]+)\)/u.exec(line);
  if (item === undefined || item === null) {
    continue;
  }
  const path = new URL(item[1]).pathname.replace(/^\/[^/]*/u, "") || "/";
  if (
    path === "/" ||
    docsTree.some((group) => group.pages.some((page) => page.route === path))
  ) {
    listed.push(path);
  }
}

const divergence = expected.findIndex((route, i) => listed[i] !== route);
if (listed.length !== expected.length || divergence !== -1) {
  const at =
    divergence === -1 ? Math.min(listed.length, expected.length) : divergence;
  fail([
    "docs output: llms.txt order drifted from the docs tree in navigation.ts.",
    `  first divergence at position ${at}: llms.txt has ${listed[at] ?? "(end)"}, tree has ${expected[at] ?? "(end)"}.`,
    "Keep the sidebar order (`meta.ts` page lists) and `docsTree` on the same",
    "sequence: `llms.txt` follows the sidebar, and this check pins it to the tree.",
  ]);
}

console.log(
  `docs output: llms.txt lists ${listed.length} pages in tree order.`,
);
