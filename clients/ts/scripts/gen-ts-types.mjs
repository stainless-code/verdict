#!/usr/bin/env node
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(HERE, "..", "..", "..");
const SRC_DIR = join(HERE, "..", "src");
const SHIM_DIR = join(REPO_ROOT, "scripts", "npm-shim");

const NAME_OVERRIDES = { error: "RefusalErrorBody" };

function typeName(name) {
  return NAME_OVERRIDES[name] ?? pascalCase(name);
}

function pascalCase(name) {
  return name.charAt(0).toUpperCase() + name.slice(1);
}

function docComment(description) {
  if (!description) return "";
  const safe = String(description).replace(/\*\//g, "* /");
  return `/** ${safe} */\n`;
}

function hoist(defs) {
  const all = { ...defs };
  const walk = (owner, node) => {
    if (!node || typeof node !== "object" || node.$ref) return;
    if (node.items && typeof node.items === "object") {
      const items = node.items;
      if (items.properties && !items.$ref) {
        const iname = `${owner}Item`;
        all[iname] = items;
        node.items = { $ref: `#/$defs/${iname}` };
        walk(iname, items);
      } else {
        walk(owner, items);
      }
    }
    const props = node.properties;
    if (!props || typeof props !== "object") return;
    for (const [prop, sub] of Object.entries(props)) {
      if (!sub || typeof sub !== "object" || sub.$ref) continue;
      if (sub.properties) {
        const iname = owner + pascalCase(prop);
        all[iname] = sub;
        props[prop] = { $ref: `#/$defs/${iname}` };
        walk(iname, sub);
      } else {
        walk(owner, sub);
      }
    }
  };
  for (const [name, node] of Object.entries(defs)) walk(typeName(name), node);
  return all;
}
function toTS(node, defs) {
  if (node === true || node === undefined) return "unknown";
  if (node === false) return "never";
  if (typeof node !== "object" || node === null) return "unknown";
  if (typeof node.$ref === "string") {
    const name = node.$ref.replace(/^#\/\$defs\//, "");
    if (!Object.hasOwn(defs, name))
      throw new Error(`unknown $ref ${node.$ref}`);
    return typeName(name);
  }
  if (node.const !== undefined) return JSON.stringify(node.const);
  if (Array.isArray(node.enum)) {
    const values = node.enum.map((v) => JSON.stringify(v));
    const inline = values.join(" | ");
    if (inline.length <= 70) return inline;
    return "\n" + values.map((value) => `    | ${value}`).join("\n");
  }
  // anyOf on a node that declares its own shape carries structural
  // constraints -- which of the declared properties must appear -- not
  // alternative value shapes: every branch is a bare {required: [...]}, so
  // unioning them collapsed a fully described object to `unknown`. Render the
  // declared shape; TypeScript cannot express "at least one of" anyway.
  if (Array.isArray(node.anyOf) && !declaresShape(node))
    return anyOfTS(node.anyOf, defs);
  const types = Array.isArray(node.type) ? node.type : [node.type];
  const nullable = types.includes("null");
  const concrete = types.filter((t) => t !== "null" && t !== undefined);
  let base;
  if (concrete.length === 0) {
    base = objectTS(node, defs);
  } else {
    base = concrete.map((t) => singleTS(t, node, defs)).join(" | ");
  }
  return nullable ? `${paren(base)} | null` : base;
}

function paren(type) {
  return type.includes(" | ") ? `(${type})` : type;
}

// declaresShape reports whether a node describes its own value shape. An anyOf
// on such a node constrains which of those properties must appear rather than
// offering alternative shapes.
function declaresShape(node) {
  return (
    node.type !== undefined ||
    node.properties !== undefined ||
    node.items !== undefined ||
    node.const !== undefined
  );
}

// anyOf joins its variants into a union. A null variant renders the way a
// `type: [..., "null"]` array does -- trailing ` | null` -- so both nullable
// spellings produce one shape instead of `null | T` in one place and `T | null`
// in another.
function anyOfTS(variants, defs) {
  const nullable = variants.some(isNullVariant);
  const concrete = variants.filter((variant) => !isNullVariant(variant));
  if (concrete.length === 0) return "null";
  const base = concrete.map((variant) => toTS(variant, defs)).join(" | ");
  return nullable ? `${paren(base)} | null` : base;
}

function isNullVariant(node) {
  if (!node || typeof node !== "object" || node.$ref) return false;
  if (Array.isArray(node.anyOf) || Array.isArray(node.enum)) return false;
  if (node.const !== undefined) return node.const === null;
  return Array.isArray(node.type)
    ? node.type.length === 1 && node.type[0] === "null"
    : node.type === "null";
}

function singleTS(type, node, defs) {
  switch (type) {
    case "string":
      return "string";
    case "integer":
    case "number":
      return "number";
    case "boolean":
      return "boolean";
    case "null":
      return "null";
    case "array":
      return node.items === undefined
        ? "unknown[]"
        : `Array<${toTS(node.items, defs)}>`;
    case "object":
      return objectTS(node, defs);
    default:
      return objectTS(node, defs);
  }
}

function objectTS(node, defs) {
  const props = node.properties;
  if (props === undefined || typeof props !== "object") return "unknown";
  const required = new Set(Array.isArray(node.required) ? node.required : []);
  const lines = [];
  for (const [name, sub] of Object.entries(props)) {
    const optional = required.has(name) ? "" : "?";
    const key = /^[A-Za-z_$][A-Za-z0-9_$]*$/.test(name)
      ? name
      : JSON.stringify(name);
    const rendered = toTS(sub, defs);
    const separator = rendered.startsWith("\n") ? ":" : ": ";
    lines.push(
      `  ${docComment((sub && sub.description) || "").replaceAll("\n", "\n  ")}${key}${optional}${separator}${rendered};`,
    );
  }
  if (node.additionalProperties === true) {
    lines.push("  [key: string]: unknown;");
  }
  return `{\n${lines.join("\n")}\n}`;
}

function genTypes() {
  const schema = JSON.parse(
    readFileSync(
      join(REPO_ROOT, "skills", "verdict", "schemas", "envelope.schema.json"),
      "utf8",
    ),
  );
  const defs = hoist(schema.$defs ?? {});
  const chunks = [];
  for (const [name, node] of Object.entries(defs)) {
    const exported = typeName(name);
    const rendered = toTS(node, defs);
    if (rendered.startsWith("{\n")) {
      chunks.push(
        `${docComment(node.description)}export interface ${exported} ${rendered}`,
      );
    } else {
      chunks.push(
        `${docComment(node.description)}export type ${exported} = ${rendered};`,
      );
    }
  }
  return `${header("skills/verdict/schemas/envelope.schema.json ($defs)")}\n${chunks.join("\n\n")}\n`;
}

function genPlatforms() {
  const contract = JSON.parse(
    readFileSync(join(REPO_ROOT, "release", "contract.json"), "utf8"),
  );
  const archives = contract.archives ?? [];
  if (archives.length === 0) {
    throw new Error("release/contract.json carries no archives");
  }
  const rows = archives.map((entry) => {
    for (const field of ["npmOS", "npmCPU", "npmPackage"]) {
      if (typeof entry[field] !== "string") {
        throw new Error(
          `contract archive row needs a string ${field}: ${JSON.stringify(entry)}`,
        );
      }
    }
    return [`${entry.npmOS}-${entry.npmCPU}`, entry.npmPackage];
  });
  const keys = new Set(rows.map(([key]) => key));
  if (keys.size !== rows.length) {
    throw new Error("release/contract.json repeats an npmOS/npmCPU pair");
  }
  const entries = rows
    .map(([key, pkg]) => `  ${JSON.stringify(key)}: ${JSON.stringify(pkg)},`)
    .join("\n");
  const client =
    `${header("release/contract.json archives[]")}\n` +
    "// platform key (`<process.platform>-<process.arch>`) -> the optional\n" +
    "// dependency package that ships that binary. The npm install shim\n" +
    "// requires scripts/npm-shim/platforms.gen.json, rendered from the same\n" +
    "// rows by this script.\n" +
    `export const PLATFORM_PACKAGES: Record<string, string> = {\n${entries}\n};\n`;
  const shim = `${JSON.stringify(Object.fromEntries(rows), null, 2)}\n`;
  return [
    [join(SRC_DIR, "platforms.gen.ts"), client],
    [join(SHIM_DIR, "platforms.gen.json"), shim],
  ];
}

function header(from) {
  return `// GENERATED from ${from} by clients/ts/scripts/gen-ts-types.mjs. Do not edit.`;
}

function main(argv) {
  const check = argv.includes("--check");
  const outputs = [
    [join(SRC_DIR, "types.gen.ts"), genTypes()],
    ...genPlatforms(),
  ];
  let stale = 0;
  for (const [path, want] of outputs) {
    if (check) {
      const have = readFileSync(path, "utf8");
      if (have !== want) {
        process.stderr.write(`ts-types: stale ${path} (run the generator)\n`);
        stale += 1;
      }
    } else {
      writeFileSync(path, want);
      process.stdout.write(`ts-types: wrote ${path}\n`);
    }
  }
  if (check) {
    if (stale > 0) process.exit(1);
    process.stdout.write("ts-types: generated sources are current\n");
  }
}

main(process.argv.slice(2));
