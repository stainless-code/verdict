#!/usr/bin/env node
// Generate the TS client's generated sources from the repo contracts:
//   schemas/envelope.schema.json ($defs -> types.gen.ts)
//   internal/core/hint.go (isCode literals -> refusal-codes.gen.ts)
// No dependencies beyond Node builtins. Run `bun run gen` (or node this
// script) after changing a schema or a refusal code; `--check` fails when
// the checked-in files are stale (drift gate, like make schemas-check).
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const REPO_ROOT = join(HERE, "..", "..", "..");
const SRC_DIR = join(HERE, "..", "src");

// NAME_OVERRIDES renames $defs that would collide with globals (`error`
// vs the global Error); $refs resolve through the same mapping.
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

// hoist lifts every inline object with properties to a named synthetic
// $def (<Parent><Prop>), so the emitter only ever prints top-level
// interfaces (which bun fmt leaves alone) instead of nested literals
// (which it re-indents). The wire shape is unchanged; names are additive.
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
// toTS maps the schema subset schemagen emits (draft 2020-12: $ref, const,
// enum, type unions with null, objects with required + additionalProperties,
// arrays, formats) to TypeScript. Anything outside that subset is unknown
// by design — the envelope stays readable rather than clever.
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
    return node.enum.map((v) => JSON.stringify(v)).join(" | ");
  }
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
    lines.push(
      `  ${docComment((sub && sub.description) || "").replaceAll("\n", "\n  ")}${key}${optional}: ${toTS(sub, defs)};`,
    );
  }
  if (node.additionalProperties === true) {
    lines.push("  [key: string]: unknown;");
  }
  return `{\n${lines.join("\n")}\n}`;
}

function genTypes() {
  const schema = JSON.parse(
    readFileSync(join(REPO_ROOT, "schemas", "envelope.schema.json"), "utf8"),
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
  return `${header("schemas/envelope.schema.json ($defs)")}\n${chunks.join("\n\n")}\n`;
}

// genCodes extracts every exact refusal code from the isCode(...) call
// sites in internal/core/hint.go. Prefix families (missing-*, bad-*) are
// open-ended by design, so the emitted union stays open via ` | (string &
// {})` at the use site (see errors.ts).
function genCodes() {
  const source = readFileSync(
    join(REPO_ROOT, "internal", "core", "hint.go"),
    "utf8",
  );
  const codes = new Set();
  for (const match of source.matchAll(/\bisCode\(([^)]*)\)/g)) {
    for (const literal of match[1].matchAll(/"([^"\\]*)"/g)) {
      codes.add(literal[1]);
    }
  }
  if (codes.size === 0) throw new Error("no refusal codes found in hint.go");
  const sorted = [...codes].sort();
  const union = sorted.map((c) => `  | ${JSON.stringify(c)}`).join("\n");
  return `${header("internal/core/hint.go isCode(...) literals")}\n// Open families (missing-*, bad-*) accept any suffix; keep this union\n// exact and widen at the use site with \` | (string & {})\`.\nexport type RefusalCode =\n${union};\n`;
}

function header(from) {
  return `// GENERATED from ${from} by clients/ts/scripts/gen-ts-types.mjs. Do not edit.`;
}

function main(argv) {
  const check = argv.includes("--check");
  const outputs = [
    [join(SRC_DIR, "types.gen.ts"), genTypes()],
    [join(SRC_DIR, "refusal-codes.gen.ts"), genCodes()],
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
