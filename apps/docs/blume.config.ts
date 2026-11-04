import { readFile, readdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { defineConfig } from "blume";
import { orama } from "blume/search";
import { custom, filesystem } from "blume/sources";
import { docsPage } from "./docs-index.ts";
import { sitePages } from "./navigation.ts";
import { changelogSource } from "./sources/changelog.ts";
import { manualSource } from "./sources/manual.ts";

const title = "Verdict";
const description =
  "Verdict splits code review into steps your agent runs on your machine: fetch, review, gate, post. Evidence-graded findings and one static binary.";

/** Mount point of the deployed site, shared with every absolute href below. */
const deploymentBase = "/verdict";
const origin = "https://stainless-code.com";

const manual = docsPage("manual.md");
const manualUrl = `${origin}${deploymentBase}${manual.route}.md`;
const manualSummary = manual.summary.replace(/\.$/u, "");

// Prerendered pages skip transformIndexHtml, so append tags to dist after build.
const webAppHeadIntegration = {
  name: "verdict-web-app-head",
  hooks: {
    "astro:build:done": async ({ dir }: { dir: URL }) => {
      const root = fileURLToPath(dir);
      const tags = [
        `<link rel="manifest" href="${deploymentBase}/site.webmanifest">`,
        `<meta name="apple-mobile-web-app-title" content="${title}">`,
      ];
      for (const entry of await readdir(root, { recursive: true })) {
        if (!entry.endsWith(".html")) {
          continue;
        }
        const file = join(root, entry);
        const html = await readFile(file, "utf8");
        if (!html.includes("</head>")) {
          continue;
        }
        const missing = tags.filter((tag) => {
          const rel = /rel="([^"]+)"/.exec(tag)?.[1];
          if (rel !== undefined) {
            return !html.includes(`rel="${rel}"`);
          }
          const name = /name="([^"]+)"/.exec(tag)?.[1];
          return name === undefined || !html.includes(name);
        });
        if (missing.length === 0) {
          continue;
        }
        await writeFile(
          file,
          html.replace("</head>", `${missing.join("\n    ")}\n  </head>`),
        );
      }
    },
  },
};

export default defineConfig({
  title,
  description,

  logo: { image: "/logo.svg", text: title },

  github: {
    owner: "stainless-code",
    repo: "verdict",
    branch: "main",
    dir: "apps/docs",
  },

  lastModified: "git",

  content: {
    sources: [
      filesystem({ root: "content" }),
      custom(
        changelogSource({
          name: "changelog",
          owner: "stainless-code",
          repo: "verdict",
          prefix: "changelog",
          site: origin,
        }),
      ),
      custom(manualSource()),
    ],
  },

  changelog: {
    title: "Changelog",
    description:
      "Every Verdict release, newest first, with the notes from each GitHub Release: what changed, what was added, and what was fixed.",
  },

  // A generated site skill is named after the site title, which puts a second
  // `verdict` beside the payload in `skills/`; the docs guide ships as
  // `verdict-docs` instead.
  agents: {
    skillMd: false,
    skills: "./skills",
    llmsTxt: {
      details: [
        "The two long-form references are left out of `llms-full.txt` for their size:",
        `the ${manual.title.toLowerCase()} at ${manualUrl} (${manualSummary}),`,
        `and the release notes at ${origin}${deploymentBase}/changelog.`,
      ].join("\n"),
    },
  },

  integrations: [webAppHeadIntegration],

  search: {
    provider: orama(),
  },

  navigation: {
    tabs: sitePages(manual).map(({ label, path }) => ({ label, path })),
    sidebar: { display: "flat" },
  },

  footer: {
    copyright: `© 2026 stainless-code, MIT licensed.`,
    links: [
      {
        label: "GitHub repository",
        href: "https://github.com/stainless-code/verdict",
      },
      {
        label: "Releases",
        href: "https://github.com/stainless-code/verdict/releases",
      },
      {
        label: "MIT license",
        href: "https://github.com/stainless-code/verdict/blob/main/LICENSE",
      },
    ],
    socials: {
      website: "https://stainless-code.com",
    },
  },

  theme: {
    accent: { light: "#ae2a1e", dark: "#f2a192" },
    background: { light: "#fafafa", dark: "#18181b" },
  },

  seo: {
    og: { enabled: true },
    sitemap: true,
    robots: true,
    structuredData: true,
  },

  deployment: {
    site: origin,
    base: deploymentBase,
  },
});
