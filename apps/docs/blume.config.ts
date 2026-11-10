import { readFile, readdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { defineConfig } from "blume";
import { orama } from "blume/search";
import { custom, filesystem } from "blume/sources";
import { docsPage } from "./docs-index.ts";
import { docsRoute, docsSections } from "./navigation.ts";
import { changelogSource } from "./sources/changelog.ts";
import { troubleshootingSource } from "./sources/troubleshooting.ts";

const title = "Verdict";
const description =
  "Code review any coding agent can run: fetch, review, gate, post. Evidence-graded findings, cost rollups, one static binary — no server, no account, no daemon.";

/** Mount point of the deployed site, shared with every absolute href below. */
const deploymentBase = "/verdict";
const origin = "https://stainless-code.com";

const troubleshooting = docsPage("troubleshooting.md");
const troubleshootingUrl = `${origin}${deploymentBase}${docsRoute(troubleshooting.file)}.md`;
const troubleshootingSummary = troubleshooting.summary.replace(/\.$/u, "");

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
      custom(troubleshootingSource()),
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
        "The long-form reference is left out of `llms-full.txt` for its size:",
        `the ${troubleshooting.title.toLowerCase()} at ${troubleshootingUrl} (${troubleshootingSummary}),`,
        `and the release notes at ${origin}${deploymentBase}/changelog.`,
      ].join("\n"),
    },
  },

  integrations: [webAppHeadIntegration],

  search: {
    provider: orama(),
  },

  navigation: {
    // The header carries the GitHub mark, so the footer stays link-free and
    // Blume renders no footer row on docs pages: only the home page below
    // carries its own sitemap footer.
    repo: false,
    tabs: docsSections.map(({ label, path }) => ({
      label,
      path,
    })),
    sidebar: { display: "flat" },
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
