import { readFile, readdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

import { defineConfig } from "blume";
import { orama } from "blume/search";
import { filesystem, githubReleases } from "blume/sources";

const title = "Verdict";
const description =
  "Verdict splits code review into steps your agent runs on your machine: fetch, review, gate, post. Evidence-graded findings and one static binary.";

/** Mount point of the deployed site, shared with every absolute href below. */
const deploymentBase = "/verdict";

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
      githubReleases({
        owner: "stainless-code",
        repo: "verdict",
        prefix: "changelog",
      }),
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
  },

  integrations: [webAppHeadIntegration],

  search: {
    provider: orama(),
  },

  navigation: {
    // Order is the reader's path, not the repo's: install, see the loop, pick a
    // surface, then argue about positioning and price. Roadmap before Changelog
    // because the future is a shorter read than the log.
    tabs: [
      { label: "Quickstart", path: "/quickstart" },
      { label: "How it reviews", path: "/how-it-reviews" },
      { label: "Integrations", path: "/integrations" },
      { label: "Why Verdict", path: "/why-verdict" },
      { label: "Comparison", path: "/comparison" },
      { label: "Roadmap", path: "/roadmap" },
      { label: "Changelog", path: "/changelog" },
    ],
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
    site: "https://stainless-code.com",
    base: deploymentBase,
  },
});
