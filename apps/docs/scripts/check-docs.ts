import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

import GithubSlugger from "github-slugger";

import { docsPages, docsRoot } from "../docs-index.ts";
import { docsSections, docsTree } from "../navigation.ts";

const appRoot = fileURLToPath(new URL("..", import.meta.url));
const repoRoot = fileURLToPath(new URL("../../..", import.meta.url));
const dist = join(appRoot, process.argv[2] ?? "dist");

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

const treeRoutes = docsTree.flatMap((group) =>
  group.pages.map((page) => page.route),
);
const treeDocsFiles = docsTree.flatMap((group) =>
  group.pages.flatMap((page) =>
    page.docsFile === undefined ? [] : [page.docsFile],
  ),
);

const problems: string[] = [];

const seen = new Set<string>();
for (const route of treeRoutes) {
  if (seen.has(route)) {
    problems.push(`  ${route} is in the tree twice`);
  }
  seen.add(route);
}

// Every page docs/README.md publishes has one home in the tree.
const homeless = docsPages
  .filter((page) => page.published)
  .filter((page) => !treeDocsFiles.includes(page.file))
  .map((page) => `  docs/${page.file} is published but has no tree entry`);
problems.push(...homeless);

// Every tree route builds as a page with its Markdown twin.
problems.push(
  ...treeRoutes
    .filter(
      (route) =>
        !existsSync(join(dist, route, "index.html")) ||
        !existsSync(join(dist, `${route}.md`)),
    )
    .map((route) => `  ${route} is in the tree but builds no page`),
);

// Every built page is either in the tree or on the known non-docs list: the
// home page, the section landings, the generated changelog timeline, and the
// 404 page. A stray top-level page (exactly the old `/manual` and `/glossary`
// routes this restructure removed) fails the build.
const builtRoutes: string[] = [];
const collect = (dir: string, prefix: string): void => {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      collect(join(dir, entry.name), `${prefix}/${entry.name}`);
    } else if (entry.isFile() && entry.name === "index.html") {
      builtRoutes.push(prefix === "" ? "/" : prefix);
    }
  }
};
collect(dist, "");
const knownRoutes = new Set([
  "/",
  "/404",
  ...treeRoutes,
  ...docsSections.map((section) => section.landing),
]);
const generatedPrefixes = ["/changelog/"];
const orphans = builtRoutes.filter(
  (route) =>
    !knownRoutes.has(route) &&
    !generatedPrefixes.some((prefix) => route.startsWith(prefix)),
);
problems.push(
  ...orphans.map((route) => `  ${route} builds but is in no tree group`),
);

// Every tree page's built sidebar label is the tree label. The sidebar, the
// sitemap footer, and the pager all read `docsTree`; the page itself carries
// the short name as frontmatter `sidebar.label`, or as `title` when the title
// already is the short name (staged `docs/` pages carry it as their index
// title instead). A drift between the two surfaces the wrong short name.
const contentRoot = join(appRoot, "content");

const frontmatterOf = (file: string): string => {
  const text = readFileSync(file, "utf8");
  return /^---\r?\n([\s\S]*?)\r?\n---(?:\r?\n|$)/u.exec(text)?.[1] ?? "";
};

const scalarField = (frontmatter: string, field: string): string | null => {
  const match = new RegExp(`^${field}:\\s*(?:"([^"]*)"|(.+?))\\s*$`, "mu").exec(
    frontmatter,
  );
  return match?.[1] ?? match?.[2] ?? null;
};

const sidebarLabelIn = (file: string): string | null => {
  const frontmatter = frontmatterOf(file);
  const nested = /^sidebar:\s*\r?\n  label:\s*(?:"([^"]*)"|(.+?))\s*$/mu.exec(
    frontmatter,
  );
  return nested?.[1] ?? nested?.[2] ?? scalarField(frontmatter, "title");
};

const contentFileFor = (route: string): string | null => {
  if (route.startsWith("/docs/")) {
    const wanted = `${route.slice("/docs/".length)}.mdx`;
    const found: string[] = [];
    const scan = (dir: string): void => {
      for (const entry of readdirSync(dir, { withFileTypes: true })) {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) {
          scan(path);
        } else if (entry.isFile() && entry.name === wanted) {
          found.push(path);
        }
      }
    };
    scan(contentRoot);
    return found[0] ?? null;
  }
  const tab = route.slice(1);
  for (const entry of readdirSync(contentRoot, { withFileTypes: true })) {
    if (
      entry.isDirectory() &&
      entry.name.replace(/^\d+-/u, "") === tab &&
      existsSync(join(contentRoot, entry.name, "index.mdx"))
    ) {
      return join(contentRoot, entry.name, "index.mdx");
    }
  }
  return null;
};

for (const group of docsTree) {
  for (const page of group.pages) {
    if (page.docsFile !== undefined) {
      const entry = docsPages.find(
        (candidate) => candidate.file === page.docsFile,
      );
      const label = entry?.title ?? null;
      if (label !== page.label) {
        problems.push(
          `  ${page.route} sidebar label is ${label ?? "(docs/README.md has no such file)"}, tree says ${page.label}`,
        );
      }
      continue;
    }
    const file = contentFileFor(page.route);
    const label = file === null ? null : sidebarLabelIn(file);
    if (label !== page.label) {
      problems.push(
        `  ${page.route} sidebar label is ${label ?? "(no page file or title)"}, tree says ${page.label}`,
      );
    }
  }
}

// Every section landing builds.
problems.push(
  ...docsSections
    .filter((section) => !existsSync(join(dist, section.landing, "index.html")))
    .map(
      (section) =>
        `  ${section.label} lands on ${section.landing}, which builds no page`,
    ),
);

if (problems.length > 0) {
  fail([
    "docs index: docs/README.md and the built site disagree.",
    ...problems,
    "",
    'A page under "Published on the site" has one entry in',
    "apps/docs/navigation.ts; every entry builds a page with its Markdown",
    "twin; every section landing builds too.",
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

// The tree model stays the single source: every published page the index
// knows and every route the site serves.
const publishedCount = docsPages.filter((page) => page.published).length;

// The manual is repository-only, so site pages deep-link its GitHub copy.
// Every such link must name a heading the manual still carries, slugged the
// way GitHub slugs it (see `anchorsIn` above).
const githubManual =
  /^https:\/\/github\.com\/stainless-code\/verdict\/blob\/.+\/docs\/manual\.md$/u;
const manualFile = join(docsRoot, "manual.md");
const siteFiles: string[] = [];
const collectSite = (dir: string): void => {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      collectSite(path);
    } else if (entry.isFile() && /\.(?:md|mdx)$/u.test(entry.name)) {
      siteFiles.push(path);
    }
  }
};
collectSite(join(appRoot, "content"));

const dangling: string[] = [];
for (const file of siteFiles) {
  for (const link of readFileSync(file, "utf8").matchAll(/\]\(([^)\s]+)\)/gu)) {
    const [href, anchor] = link[1].split("#");
    if (anchor === undefined || anchor === "") {
      continue;
    }
    const targetsManual =
      githubManual.test(href) ||
      (!href.includes(":") &&
        !href.startsWith("/") &&
        resolve(dirname(file), decodeURIComponent(href)) === manualFile);
    if (
      targetsManual &&
      !anchorsIn(manualFile).has(decodeURIComponent(anchor))
    ) {
      dangling.push(
        `  ${repoPath(file)} links to ${link[1]}, and docs/manual.md has no such heading`,
      );
    }
  }
}

if (dangling.length > 0) {
  fail([
    "docs index: a site page links a manual section that does not exist.",
    ...dangling,
    "",
    "Deep-link the exact GitHub section (`.../docs/manual.md#<anchor>`) and",
    "spell the anchor the way GitHub slugs the heading, or drop the link.",
  ]);
}

console.log(
  `docs index: ${docsPages.length} page(s), ${publishedCount} published, ${treeRoutes.length} tree route(s), every link and anchor resolves.`,
);
