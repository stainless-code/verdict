#!/usr/bin/env node
// Generate the publishable npm tree (six platform packages + wrapper).
// Naming comes from release/contract.json (frozen); the version defaults to
// the root package.json. Binaries come from staged --bins files or from the
// --dist archives. Writes the tree, never publishes.
import { spawnSync } from "node:child_process";
import {
  chmodSync,
  cpSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const SCRIPTS_DIR = dirname(fileURLToPath(import.meta.url));
export const REPO_ROOT = dirname(SCRIPTS_DIR);

export function loadContract() {
  const path = join(REPO_ROOT, "release", "contract.json");
  return JSON.parse(readFileSync(path, "utf8"));
}

export function rootPackage() {
  return JSON.parse(readFileSync(join(REPO_ROOT, "package.json"), "utf8"));
}

export function shortName(scoped) {
  return scoped.split("/")[1];
}

function fail(message) {
  throw new Error(message);
}

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (!arg.startsWith("--")) {
      fail(`unexpected argument: ${arg}`);
    }
    const key = arg.slice(2);
    if (key === "dry-run") {
      out[key] = true;
      continue;
    }
    const value = argv[i + 1];
    if (value === undefined || value.startsWith("--")) {
      fail(`missing value for ${arg}`);
    }
    out[key] = value;
    i++;
  }
  return out;
}

function findBinary(dir, name) {
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) {
      const found = findBinary(path, name);
      if (found !== undefined) {
        return found;
      }
    } else if (entry === name) {
      return path;
    }
  }
  return undefined;
}

function extractBinary(archive, name, dest) {
  const tmp = `${dest}.extract`;
  rmSync(tmp, { recursive: true, force: true });
  mkdirSync(tmp, { recursive: true });
  if (archive.endsWith(".tar.gz")) {
    const tar = spawnSync("tar", ["-xzf", archive, "-C", tmp], {
      stdio: "inherit",
    });
    if (tar.status !== 0) {
      fail(`tar extract failed for ${archive}`);
    }
  } else if (archive.endsWith(".zip")) {
    const unzip = spawnSync("unzip", ["-q", "-o", archive, "-d", tmp], {
      stdio: "inherit",
    });
    if (unzip.status !== 0) {
      fail(`unzip extract failed for ${archive}`);
    }
  } else {
    fail(`unknown archive format: ${archive}`);
  }
  const found = findBinary(tmp, name);
  if (found === undefined) {
    fail(`${name} not found in ${archive}`);
  }
  mkdirSync(dirname(dest), { recursive: true });
  cpSync(found, dest);
  chmodSync(dest, 0o755);
  rmSync(tmp, { recursive: true, force: true });
}

function findArchive(dist, entry) {
  const suffix = `_${entry.goos}_${entry.goarch}.`;
  const matches = readdirSync(dist).filter(
    (name) =>
      name.startsWith("verdict_") &&
      name.includes(suffix) &&
      (name.endsWith(".tar.gz") || name.endsWith(".zip")),
  );
  if (matches.length !== 1) {
    fail(
      `expected exactly one archive for ${entry.goos}/${entry.goarch} in ${dist}`,
    );
  }
  return join(dist, matches[0]);
}

function archiveVersion(archive, entry) {
  const prefix = "verdict_";
  const suffix = `_${entry.goos}_${entry.goarch}.`;
  const base = archive.split("/").pop();
  const start = prefix.length;
  const end = base.indexOf(suffix);
  if (!base.startsWith(prefix) || end < 0) {
    fail(`cannot read version from ${base}`);
  }
  return base.slice(start, end);
}

function writeJSON(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`);
}

export function generateNpmTree(options) {
  const contract = loadContract();
  const root = rootPackage();
  const version = options.version ?? root.version;
  if (!version) {
    fail("no version: pass --version or set package.json version");
  }
  const dist = options.dist ?? join(REPO_ROOT, "dist");
  const out = options.out ?? join(REPO_ROOT, "dist", "npm");
  rmSync(out, { recursive: true, force: true });

  const staged = [];
  let archiveVersionSeen;
  for (const entry of contract.archives) {
    const dir = join(out, shortName(entry.npmPackage));
    const binDest = join(dir, entry.npmBin);
    if (options.bins !== undefined) {
      const ext = entry.npmBin.endsWith(".exe") ? ".exe" : "";
      const src = join(
        options.bins,
        `verdict-${entry.goos}-${entry.goarch}${ext}`,
      );
      if (!existsSync(src)) {
        fail(`missing staged binary ${src}`);
      }
      mkdirSync(dirname(binDest), { recursive: true });
      cpSync(src, binDest);
      chmodSync(binDest, 0o755);
    } else {
      const archive = findArchive(dist, entry);
      const seen = archiveVersion(archive, entry);
      if (archiveVersionSeen === undefined) {
        archiveVersionSeen = seen;
      } else if (seen !== archiveVersionSeen) {
        fail(
          `mixed archive versions in ${dist}: ${seen} vs ${archiveVersionSeen}`,
        );
      }
      const exe = entry.npmBin.endsWith(".exe") ? "verdict.exe" : "verdict";
      extractBinary(archive, exe, binDest);
    }
    writeJSON(join(dir, "package.json"), {
      name: entry.npmPackage,
      version,
      description: `${root.description} (${entry.goos}/${entry.goarch} binary)`,
      license: root.license,
      os: [entry.npmOS],
      cpu: [entry.npmCPU],
      files: [entry.npmBin],
    });
    staged.push(entry.npmPackage);
  }

  const deps = {};
  for (const entry of contract.archives) {
    deps[entry.npmPackage] = version;
  }
  const wrapperDir = join(out, shortName(contract.npm.wrapper));
  // TypeScript client (ticket 21): the wrapper ships the built client as a
  // subpath export, so one install carries both the binary and the client.
  // Best-effort by design: the Go contract tests invoke this generator
  // without a built client, and snapshot runs without node_modules skip it.
  // Real releases build it first (scripts/release.sh runs tsc).
  const clientDist = join(REPO_ROOT, "clients", "ts", "dist");
  const clientFiles = [];
  if (
    existsSync(join(clientDist, "index.js")) &&
    existsSync(join(clientDist, "index.d.ts"))
  ) {
    const clientDir = join(wrapperDir, "client");
    mkdirSync(clientDir, { recursive: true });
    for (const entry of readdirSync(clientDist)) {
      if (entry.endsWith(".js") || entry.endsWith(".d.ts")) {
        cpSync(join(clientDist, entry), join(clientDir, entry));
        clientFiles.push(`client/${entry}`);
      }
    }
    // The wrapper root stays CommonJS (the shim requires it); the client
    // subtree opts into ESM with its own package.json.
    writeJSON(join(clientDir, "package.json"), { type: "module" });
  }
  const wrapperManifest = {
    name: contract.npm.wrapper,
    version,
    description: root.description,
    license: root.license,
    repository: root.repository,
    homepage: root.homepage,
    bin: contract.npm.bin,
    files: [
      ...contract.npm.wrapperFiles,
      ...(clientFiles.length > 0 ? ["client/"] : []),
    ],
    optionalDependencies: deps,
  };
  if (clientFiles.length > 0) {
    wrapperManifest.exports = {
      "./client": {
        types: "./client/index.d.ts",
        default: "./client/index.js",
      },
    };
  }
  writeJSON(join(wrapperDir, "package.json"), wrapperManifest);
  const shimSrc = join(REPO_ROOT, "scripts", "npm-shim", "verdict.js");
  const shimDest = join(wrapperDir, contract.npm.bin.verdict);
  mkdirSync(dirname(shimDest), { recursive: true });
  cpSync(shimSrc, shimDest);
  chmodSync(shimDest, 0o755);
  return { version, archiveVersion: archiveVersionSeen, staged, wrapperDir };
}

function main(argv) {
  const args = parseArgs(argv);
  const tree = generateNpmTree({
    dist: args.dist,
    out: args.out,
    bins: args.bins,
    version: args.version,
  });
  process.stdout.write(
    `npm: wrote ${tree.staged.length} platform packages + wrapper v${tree.version}\n`,
  );
}

try {
  if (process.argv[1] === fileURLToPath(import.meta.url)) {
    main(process.argv.slice(2));
  }
} catch (error) {
  process.stderr.write(`generate-npm: ${error.message}\n`);
  process.exit(1);
}
