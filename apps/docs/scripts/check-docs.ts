import { readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const docsRoot = fileURLToPath(new URL("..", import.meta.url));
const dist = join(docsRoot, process.argv[2] ?? "dist");

const MANUAL_ROUTE = "manual/index.html";
const MANUAL_SOURCE = "blob/main/docs/manual.md";
const REPOSITORY_ONLY = "repository-only";

const fail = (lines: readonly string[]): never => {
  console.error(lines.join("\n"));
  process.exit(1);
};

const published = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      return published(path);
    }
    return /\.(?:html|md)$/u.test(entry.name) ? [path] : [];
  });

const main = (): void => {
  const files = published(dist);
  const names = files.map((file) => relative(dist, file));

  if (!names.includes(MANUAL_ROUTE)) {
    fail([
      "docs source: `docs/manual.md` is not a page on the site.",
      "The operator contract is the one document an operator or an agent reaches",
      "for the envelope, the store files and the refusal codes; publish it with",
      "`manualSource()` in `apps/docs/blume.config.ts`.",
    ]);
  }

  const leaked = files.flatMap((file) =>
    readFileSync(file, "utf8").includes(REPOSITORY_ONLY)
      ? [`  ${relative(dist, file)}`]
      : [],
  );
  if (leaked.length > 0) {
    fail([
      "docs source: a `repository-only` fence reached the site.",
      ...leaked,
      "",
      "The manual source strips `<!-- repository-only:start -->` through",
      "`<!-- repository-only:end -->` before publishing; a marker in the output",
      "means a fence is unbalanced or the file was copied rather than loaded.",
    ]);
  }

  const githubLinks = files.flatMap((file) =>
    readFileSync(file, "utf8").includes(MANUAL_SOURCE)
      ? [`  ${relative(dist, file)}`]
      : [],
  );
  if (githubLinks.length > 0) {
    fail([
      "docs source: the site still sends readers to the GitHub copy of the manual.",
      ...githubLinks,
      "",
      '`/manual` is the page; link it as `/manual` (or `contentHref("/manual")`',
      "in an Astro component), with the anchor the manual's own headings carry.",
    ]);
  }

  console.log(
    `docs source: /manual is published, no repository-only fence, ${names.length} file(s) scanned.`,
  );
};

main();
