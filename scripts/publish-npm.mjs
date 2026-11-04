#!/usr/bin/env node
// Publish the npm tree: regenerate it, then `npm publish` each platform
// package in contract order and the wrapper last. --dry-run (or
// PUBLISH_DRY_RUN=1) appends --dry-run so nothing reaches the registry.
// --require-client (or PUBLISH_REQUIRE_CLIENT=1) refuses to generate a wrapper
// without the built TS client, which is what scripts/release.sh passes: a
// published version cannot be fixed by a later build.
// --skip-existing makes a re-run after a partial publish resume: versions the
// registry already serves are skipped, the rest keep the same order.
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
    } else if (arg === "--require-client") {
      out.requireClient = true;
    } else if (arg === "--skip-existing") {
      out.skipExisting = true;
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

function npmBin() {
  return process.env.NPM_BIN ?? "npm";
}

function npmPublish(pkgDir, extraArgs) {
  const child = spawnSync(npmBin(), ["publish", pkgDir, ...extraArgs], {
    stdio: "inherit",
  });
  if (child.status !== 0) {
    throw new Error(`npm publish failed for ${pkgDir}`);
  }
}

// npmPublished reports whether name@version is already on the registry.
function npmPublished(name, version) {
  const child = spawnSync(npmBin(), ["view", `${name}@${version}`, "version"], {
    encoding: "utf8",
  });
  return child.status === 0 && child.stdout.trim() === version;
}

function main(argv) {
  const args = parseArgs(argv);
  const dryRun = args.dryRun === true || process.env.PUBLISH_DRY_RUN === "1";
  const requireClient =
    args.requireClient === true || process.env.PUBLISH_REQUIRE_CLIENT === "1";
  const dist = args.dist ?? join(REPO_ROOT, "dist");
  const out = args.out ?? join(dist, "npm");
  const contract = loadContract();
  const tree = generateNpmTree({
    dist,
    out,
    bins: args.bins,
    version: args.version,
    requireClient,
  });
  // --bins stages explicit binaries; there is no dist archive version to
  // compare against (dry runs and the contract test use this path).
  if (
    !dryRun &&
    args.bins === undefined &&
    tree.archiveVersion !== tree.version
  ) {
    throw new Error(
      `dist archives are v${tree.archiveVersion}, refusing to publish v${tree.version}`,
    );
  }
  const extra = [...contract.npm.publishArgs];
  if (dryRun) {
    extra.push("--dry-run");
  }
  const skipExisting = args.skipExisting === true && !dryRun;
  const targets = [
    ...contract.archives.map((entry) => ({
      name: entry.npmPackage,
      dir: join(out, shortName(entry.npmPackage)),
    })),
    { name: contract.npm.wrapper, dir: tree.wrapperDir },
  ];
  let published = 0;
  let skipped = 0;
  for (const target of targets) {
    if (skipExisting && npmPublished(target.name, tree.version)) {
      process.stdout.write(
        `npm: ${target.name}@${tree.version} already published; skipping\n`,
      );
      skipped += 1;
      continue;
    }
    npmPublish(target.dir, extra);
    published += 1;
  }
  process.stdout.write(
    `npm: published ${published} package(s), skipped ${skipped} v${tree.version}${dryRun ? " (dry run)" : ""}\n`,
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
