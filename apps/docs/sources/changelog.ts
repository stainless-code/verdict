import { join } from "node:path";

import type {
  ContentSource,
  SourceContext,
  SourceLoadResult,
} from "blume/sources";
import {
  githubReleasesSource,
  type GithubReleasesSourceOptions,
} from "blume/sources/github-releases.ts";

// `githubReleases()` has no per-entry `ai.exclude`, and a release page is the
// longest page on the site: eight of them add more bytes than every guide
// together. Marking each entry excluded keeps them out of `llms.txt` and
// `llms-full.txt`, which point agents at `/changelog` instead (see
// `agents.llmsTxt.details`); everything else about the entry stays Blume's.
const excluded = (source: ContentSource): ContentSource => ({
  ...source,
  load: async (): Promise<SourceLoadResult> => {
    const result = await source.load();
    return {
      ...result,
      entries: result.entries.map((entry) => ({
        ...entry,
        data: { ...entry.data, ai: { exclude: true } },
      })),
    };
  },
});

// `custom()` carries a live instance, not a factory, so the engine is built
// once against the app directory and re-bound to the real context on `scan`.
const detachedContext = (name: string): SourceContext => ({
  cacheDir: join(process.cwd(), ".blume", "cache", name),
  mode: "build",
  projectRoot: process.cwd(),
});

export const changelogSource = (
  options: GithubReleasesSourceOptions,
): ContentSource => {
  const build = (ctx: SourceContext): ContentSource => {
    const source = excluded(githubReleasesSource(options, ctx));
    return { ...source, withContext: (next) => build(next) };
  };
  return build(detachedContext(options.name));
};
