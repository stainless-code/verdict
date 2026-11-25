// The GitHub Release body: the CHANGELOG.md section for one version, with the
// changeset commit ids (`- 1a2b3c4: note`) removed so a reader sees the note
// and not a hash. `scripts/release.sh` pipes it into `goreleaser release
// --release-notes`, and `make release-dry-run` runs it on the current version,
// so a CHANGELOG the extractor cannot read fails CI instead of the release.
// GitHub refuses a release body over 125,000 characters, and release.sh pushes
// the tag before it creates the release, so an oversized section fails here,
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

const source = await readFile(
  new URL("../CHANGELOG.md", import.meta.url),
  "utf8",
);
const lines = source.split("\n");
const start = lines.findIndex((line) => line.trim() === `## ${version}`);
if (start === -1) {
  console.error(FAILURE.join("\n"));
  process.exit(1);
}

const rest = lines.slice(start + 1);
const end = rest.findIndex((line) => /^##\s/u.test(line));
const notes = (end === -1 ? rest : rest.slice(0, end))
  .join("\n")
  .replaceAll(/^([*-])\s+[0-9a-f]{7,40}:\s+/gmu, "$1 ")
  .trim();

if (notes === "") {
  console.error(FAILURE.join("\n"));
  process.exit(1);
}

if (notes.length > BODY_LIMIT) {
  console.error(
    [
      `release-notes: the ${version} section renders to ${notes.length} characters; GitHub refuses a release body over ${BODY_LIMIT}.`,
      "Trim the .changeset entries the Version PR builds it from, then let the Version PR regenerate.",
    ].join("\n"),
  );
  process.exit(1);
}

console.log(notes);
