import { join } from "node:path";

import type {
  ContentSource,
  SourceContext,
  SourceEntry,
  SourceLoadResult,
} from "blume/sources";
import {
  githubReleasesSource,
  type GithubReleasesSourceOptions,
} from "blume/sources/github-releases.ts";

const COMMIT_LINE = /^([*-])\s+[0-9a-f]{7,40}\s+(?<subject>.*)$/u;
const CHANGESET_LINE = /^([*-])\s+[0-9a-f]{7,40}:\s+(?<subject>.*)$/u;
const MERGE_SUBJECT = /^Merge\b/u;
const SEO_DESCRIPTION_BLOCK = /^seo:\n(?:[ \t]+[^\n]*\n)+/mu;
const AI_EXCLUDE = "ai:\n  exclude: true\n";
const FRONTMATTER_OPEN = /^---\n/u;

const describe = (title: string): string =>
  `Release notes for Verdict ${title}: what shipped in this release, the fixes it carried, and what changed under the hood.`;

const subjectOf = (line: string): string | undefined => {
  const commit = COMMIT_LINE.exec(line);
  if (commit !== null) {
    return MERGE_SUBJECT.test(commit.groups?.subject ?? "")
      ? ""
      : `${commit[1]} ${commit.groups?.subject ?? ""}`;
  }
  const changeset = CHANGESET_LINE.exec(line);
  return changeset === null
    ? undefined
    : `${changeset[1]} ${changeset.groups?.subject ?? ""}`;
};

const withoutToolArtifacts = (body: string): string =>
  body
    .split("\n")
    .flatMap((line) => {
      const subject = subjectOf(line);
      return subject === undefined || subject === "" ? [] : [subject];
    })
    .join("\n");

const cleanEntry = (entry: SourceEntry): SourceEntry => {
  const body = withoutToolArtifacts(entry.body.text);
  const title =
    typeof entry.data.title === "string" ? entry.data.title : "release";
  const description = describe(title);
  const seo =
    typeof entry.data.seo === "object" && entry.data.seo !== null
      ? entry.data.seo
      : {};
  const raw = entry.raw
    ?.replace(entry.body.text, body)
    .replace(
      SEO_DESCRIPTION_BLOCK,
      `seo:\n  description: ${JSON.stringify(description)}\n`,
    )
    .replace(FRONTMATTER_OPEN, `---\n${AI_EXCLUDE}`);
  return {
    ...entry,
    body: { format: entry.body.format, text: body },
    data: {
      ...entry.data,
      ai: { exclude: true },
      seo: { ...seo, description },
    },
    ...(raw === undefined ? {} : { raw }),
  };
};

const decorate = (inner: ContentSource): ContentSource => {
  const read = inner.read;
  return {
    ...inner,
    load: async (): Promise<SourceLoadResult> => {
      const result = await inner.load();
      return { ...result, entries: result.entries.map(cleanEntry) };
    },
    read:
      read === undefined
        ? undefined
        : async (ref: string) => withoutToolArtifacts(await read(ref)),
  };
};

const detachedContext = (name: string): SourceContext => ({
  cacheDir: join(process.cwd(), ".blume", "cache", name),
  mode: "build",
  projectRoot: process.cwd(),
});

export const changelogSource = (
  options: GithubReleasesSourceOptions,
): ContentSource => {
  const build = (ctx: SourceContext): ContentSource => {
    const source = decorate(githubReleasesSource(options, ctx));
    return { ...source, withContext: (next) => build(next) };
  };
  return build(detachedContext(options.name));
};
