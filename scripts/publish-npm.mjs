#!/usr/bin/env node
// Publish the npm tree: regenerate it, then `npm publish` each platform
// package in contract order and the wrapper last. --dry-run (or
// PUBLISH_DRY_RUN=1) appends --dry-run so nothing reaches the registry.
import { spawnSync } from "node:child_process";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  REPO_ROOT,
  generateNpmTree,
  loadContract,
  shortName,
} from "./generate-npm.mjs";

function parseArgs(argv) {
  const out = {};
  for (const arg of argv) {
    if (arg === "--dry-run") {
      out.dryRun = true;
    } else if (arg.startsWith("--dist=")) {
      out.dist = arg.slice("--dist=".length);
    } else if (arg.startsWith("--out=")) {
      out.out = arg.slice("--out=".length);
    } else if (arg.startsWith("--version=")) {
      out.version = arg.slice("--version=".length);
    } else if (arg.startsWith("--bins=")) {
      out.bins = arg.slice("--bins=".length);
    } else {
      throw new Error(`unexpected argument: ${arg}`);
    }
  }
  return out;
}

function npmPublish(pkgDir, extraArgs) {
  const npm = process.env.NPM_BIN ?? "npm";
  const child = spawnSync(npm, ["publish", pkgDir, ...extraArgs], {
    stdio: "inherit",
  });
  if (child.status !== 0) {
    throw new Error(`npm publish failed for ${pkgDir}`);
  }
}

function main(argv) {
  const args = parseArgs(argv);
  const dryRun = args.dryRun === true || process.env.PUBLISH_DRY_RUN === "1";
  const dist = args.dist ?? join(REPO_ROOT, "dist");
  const out = args.out ?? join(dist, "npm");
  const contract = loadContract();
  const tree = generateNpmTree({
    dist,
    out,
    bins: args.bins,
    version: args.version,
  });
  if (!dryRun && tree.archiveVersion !== tree.version) {
    throw new Error(
      `dist archives are v${tree.archiveVersion}, refusing to publish v${tree.version}`,
    );
  }
  const extra = [...contract.npm.publishArgs];
  if (dryRun) {
    extra.push("--dry-run");
  }
  for (const entry of contract.archives) {
    npmPublish(join(out, shortName(entry.npmPackage)), extra);
  }
  npmPublish(tree.wrapperDir, extra);
  process.stdout.write(
    `npm: published ${contract.archives.length} platform packages + wrapper v${tree.version}${dryRun ? " (dry run)" : ""}\n`,
  );
}

try {
  if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main(process.argv.slice(2));
  }
} catch (error) {
  process.stderr.write(`publish-npm: ${error.message}\n`);
  process.exit(1);
}
