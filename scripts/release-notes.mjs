// The GitHub Release body. Its source is the CHANGELOG.md section for one
// version, with the changeset commit ids (`- 1a2b3c4: note`) removed so a
// reader sees the note and not a hash. When `release/highlights/<version>.md`
// exists, the body leads with it:
//
//   **v<version>** · <N> changes
//   <highlights and `### Breaking changes`, from the highlights file>
//   ### Plus <M> more fixes and improvements.
//   <details><summary>Full changelog</summary> <the section> </details>
//
// N counts the section's top-level entries (one per changeset); M is N less
// the highlight bullets. Without a highlights file the body is the section
// alone, with a warning on stderr. The file lives outside `.changeset/`
// because changesets parses every top-level `.changeset/*.md` as a changeset
// and throws on one with no frontmatter, and outside `docs/` because every page
// there needs an index line, which would cost each release a second edit.
//
// `scripts/release.sh` pipes the body into `goreleaser release
// --release-notes`, and `make release-dry-run` runs it on the current version,
// so a CHANGELOG the extractor cannot read fails CI instead of the release.
// GitHub refuses a release body over 125,000 characters, and release.sh pushes
// the tag before it creates the release, so an oversized body fails here,
// before the tag, instead of stranding a tag with no release behind it.
import { readFile } from "node:fs/promises";

const FAILURE = [
  "release-notes: CHANGELOG.md has no section for the version being released.",
  "The Version PR writes it; tag only a commit whose CHANGELOG.md carries the",
  "bumped version as a `## <version>` heading.",
];

// GitHub's release-body ceiling. `length` counts UTF-16 code units, which is
// never fewer than the characters GitHub counts, so the check errs strict.
const BODY_LIMIT = 125_000;

const version = process.argv[2];
if (!version) {
  console.error("usage: node scripts/release-notes.mjs <version>");
  process.exit(2);
}

const root = new URL("../", import.meta.url);
const source = await readFile(new URL("CHANGELOG.md", root), "utf8");
const lines = source.split("\n");
const start = lines.findIndex((line) => line.trim() === `## ${version}`);
if (start === -1) {
  console.error(FAILURE.join("\n"));
  process.exit(1);
}

const rest = lines.slice(start + 1);
const end = rest.findIndex((line) => /^##\s/u.test(line));
const section = end === -1 ? rest : rest.slice(0, end);
const notes = section
  .join("\n")
  .replaceAll(/^([*-])\s+[0-9a-f]{7,40}:\s+/gmu, "$1 ")
  .trim();

if (notes === "") {
  console.error(FAILURE.join("\n"));
  process.exit(1);
}

const highlightsPath = `release/highlights/${version}.md`;
let highlights = null;
try {
  highlights = (await readFile(new URL(highlightsPath, root), "utf8")).trim();
} catch (err) {
  if (err.code !== "ENOENT") throw err;
}

let body = notes;
if (highlights === null) {
  console.error(
    `release-notes: warning: no ${highlightsPath}; the ${version} body is the CHANGELOG.md section alone, with no highlights.`,
  );
} else {
  // A changeset renders as one column-0 bullet; its continuation lines are
  // indented, so they never count.
  const changes = section.filter((line) => /^[*-]\s/u.test(line)).length;
  const firstHeading = highlights.search(/^#{1,6}\s/mu);
  const lead =
    firstHeading === -1 ? highlights : highlights.slice(0, firstHeading);
  const picked = lead
    .split("\n")
    .filter((line) => /^[*-]\s/u.test(line)).length;
  if (picked === 0) {
    console.error(
      `release-notes: ${highlightsPath} has no highlight bullets before its first heading; lead it with \`- **Title.** what it does for you\` lines.`,
    );
    process.exit(1);
  }
  const { tagPrefix } = JSON.parse(
    await readFile(new URL("release/contract.json", root), "utf8"),
  );
  const more = changes - picked;
  const parts = [
    `**${tagPrefix}${version}** · ${changes} ${changes === 1 ? "change" : "changes"}`,
    highlights,
  ];
  if (more > 0) {
    parts.push(
      `### Plus ${more} more ${more === 1 ? "fix or improvement" : "fixes and improvements"}.`,
    );
  }
  parts.push(
    `<details>\n<summary>Full changelog</summary>\n\n${notes}\n\n</details>`,
  );
  body = parts.join("\n\n");
}

if (body.length > BODY_LIMIT) {
  console.error(
    [
      `release-notes: the ${version} release body renders to ${body.length} characters; GitHub refuses a release body over ${BODY_LIMIT}.`,
      `Trim ${highlights === null ? "" : `${highlightsPath} or `}the .changeset entries the Version PR builds the section from, then let the Version PR regenerate.`,
    ].join("\n"),
  );
  process.exit(1);
}

console.log(body);
