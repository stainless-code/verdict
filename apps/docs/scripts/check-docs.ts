import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

import GithubSlugger from "github-slugger";

import { docsPage, docsPages, docsRoot } from "../docs-index.ts";
import { sitePages } from "../navigation.ts";

const appRoot = fileURLToPath(new URL("..", import.meta.url));
const repoRoot = fileURLToPath(new URL("../../..", import.meta.url));
const dist = join(appRoot, process.argv[2] ?? "dist");
const nav = sitePages(docsPage("manual.md"), docsPage("troubleshooting.md"));

const fail = (lines: readonly string[]): never => {
  console.error(lines.join("\n"));
  process.exit(1);
};

const markdown = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      return markdown(path);
    }
    return entry.name.endsWith(".md") ? [path] : [];
  });

const repoPath = (file: string): string =>
  relative(repoRoot, file).split(sep).join("/");

const docsFiles = markdown(docsRoot);
const onDisk = docsFiles
  .map((file) => relative(docsRoot, file).split(sep).join("/"))
  .filter((file) => file !== "README.md");
const indexed = docsPages.map((page) => page.file);

const unlisted = onDisk.filter((file) => !indexed.includes(file));
const stale = indexed.filter((file) => !onDisk.includes(file));
if (unlisted.length > 0 || stale.length > 0) {
  fail([
    "docs index: docs/ and docs/README.md disagree.",
    ...unlisted.map((file) => `  no line for docs/${file}`),
    ...stale.map((file) => `  no file for docs/${file}`),
    "",
    "Add one `- [Title](page.md): what it covers.` line under the section for",
    "the page's surface, or delete the line for a page that is gone.",
  ]);
}

const unmounted = docsPages
  .filter((page) => page.published)
  .filter((page) => !nav.some((site) => site.docsFile === page.file))
  .map(
    (page) =>
      `  docs/${page.file} is published here but mounted nowhere: no entry in apps/docs/navigation.ts`,
  );
// A navigation entry that names a docs file takes that page's own route, so the
// two surfaces cannot drift apart.
const mounted = nav.flatMap((site) => {
  if (site.docsFile === undefined) {
    return [];
  }
  const page = docsPages.find((entry) => entry.file === site.docsFile);
  if (page === undefined || !page.published) {
    return [
      `  ${site.path} mounts docs/${site.docsFile}, which docs/README.md does not publish`,
    ];
  }
  if (page.route !== site.path) {
    return [
      `  ${site.path} mounts docs/${site.docsFile}, whose route is ${page.route}`,
    ];
  }
  return [];
});
const missingRoutes = nav
  .filter(
    (site) =>
      !existsSync(join(dist, site.path, "index.html")) ||
      !existsSync(join(dist, `${site.path}.md`)),
  )
  .map((site) => `  ${site.path} is in the navigation`);

const problems = [...unmounted, ...mounted, ...missingRoutes];
if (problems.length > 0) {
  fail([
    "docs index: docs/README.md and the built site disagree.",
    ...problems,
    "",
    'A page under "Published on the site" is mounted by `apps/docs/sources/` and',
    'listed in `apps/docs/navigation.ts`; a page under "Repository-only" is',
    "neither. Move the line when the page moves.",
  ]);
}

const plain = (heading: string): string =>
  heading
    .replace(/`([^`]*)`/gu, "$1")
    .replace(/\[([^\]]*)\]\([^)]*\)/gu, "$1")
    .replace(/[*_]{1,2}([^*_]+)[*_]{1,2}/gu, "$1")
    .trim();

// github-slugger is what GitHub and Blume both use for heading ids, so an
// anchor that resolves here resolves on either surface.
const anchors = new Map<string, Set<string>>();

const anchorsIn = (file: string): Set<string> => {
  const cached = anchors.get(file);
  if (cached !== undefined) {
    return cached;
  }
  const slugger = new GithubSlugger();
  const found = new Set<string>();
  let fenced = false;
  for (const line of readFileSync(file, "utf8").split("\n")) {
    if (/^ {0,3}(?:```|~~~)/u.test(line)) {
      fenced = !fenced;
      continue;
    }
    const heading = fenced
      ? null
      : /^ {0,3}#{1,6}\s+(.+?)\s*#*\s*$/u.exec(line);
    if (heading === null) {
      continue;
    }
    const pinned = /\[#([\w-]+)\]\s*$/u.exec(heading[1]);
    found.add(pinned === null ? slugger.slug(plain(heading[1])) : pinned[1]);
  }
  anchors.set(file, found);
  return found;
};

const broken: string[] = [];
for (const file of [
  ...readdirSync(repoRoot, { withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith(".md"))
    .map((entry) => join(repoRoot, entry.name)),
  ...docsFiles,
]) {
  for (const link of readFileSync(file, "utf8").matchAll(/\]\(([^)\s]+)\)/gu)) {
    const [href, anchor] = link[1].split("#");
    if (href === "" || href.includes(":") || href.startsWith("/")) {
      continue;
    }
    const target = resolve(dirname(file), decodeURIComponent(href));
    if (!target.endsWith(".md") || !target.startsWith(docsRoot + sep)) {
      continue;
    }
    if (!existsSync(target)) {
      broken.push(`  ${repoPath(file)} links to ${href}, which is gone`);
    } else if (
      anchor !== undefined &&
      anchor !== "" &&
      !anchorsIn(target).has(anchor)
    ) {
      broken.push(
        `  ${repoPath(file)} links to ${href}#${anchor}, and ${repoPath(target)} has no such heading`,
      );
    }
  }
}

if (broken.length > 0) {
  fail([
    "docs index: a link into docs/ points at nothing.",
    ...broken,
    "",
    "A link from `docs/` or the root `README.md` into `docs/` has to resolve",
    "to a page and, with an anchor, to a heading in it: fix the path or the",
    "heading, or drop the link.",
  ]);
}

console.log(
  `docs index: ${docsPages.length} page(s), ${docsPages.filter((page) => page.published).length} published, every link and anchor resolves.`,
);
