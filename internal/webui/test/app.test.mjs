import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import vm from "node:vm";
import test from "node:test";

const appSource = readFileSync(
  new URL("../static/app.js", import.meta.url),
  "utf8",
);

class Element {
  constructor(tagName) {
    this.tagName = tagName.toUpperCase();
    this.children = [];
    this.attributes = {};
    this.listeners = new Map();
    this.parentNode = null;
    this.className = "";
    this.hidden = false;
    this.value = "";
    this._text = "";
  }

  set textContent(value) {
    this._text = String(value);
    this.children = [];
  }

  get textContent() {
    return (
      this._text + this.children.map((child) => child.textContent).join("")
    );
  }

  append(...nodes) {
    for (const child of nodes) {
      child.parentNode = this;
      this.children.push(child);
      if (
        this.tagName === "SELECT" &&
        child.tagName === "OPTION" &&
        (child.selected || this.children.length === 1)
      ) {
        this.value = child.value;
      }
    }
  }

  replaceChildren(...nodes) {
    this._text = "";
    this.children = [];
    this.append(...nodes);
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }

  addEventListener(name, listener) {
    this.listeners.set(name, listener);
  }

  dispatch(name, event = {}) {
    return this.listeners.get(name)?.(event);
  }
}

function envelope(data) {
  return {
    data: { partial: false, sources: [], errors: [], ...data },
    error: null,
    meta: { version: "1" },
  };
}

const hostile = '"><img src=x onerror=alert(1)> javascript:alert(1)';
const inbox = envelope({
  rows: [
    {
      root: "root-123",
      bundle: "local-good",
      repo: "acme/widgets",
      model: "reviewer-v1",
      head: "a1b2c3d4",
      open: 1,
      deferred: 0,
      addressed: 0,
    },
    {
      root: "root-123",
      bundle: "local-corrupt",
      repo: "acme/broken",
      model: null,
      head: null,
      open: null,
      deferred: null,
      addressed: null,
    },
  ],
  counts: null,
  partial: true,
  errors: [
    {
      root: "root-123",
      bundle: "local-corrupt",
      code: "corrupt-store",
      message: hostile,
    },
  ],
});

const detail = envelope({
  report: {
    generated_at: "2026-11-12T10:00:00Z",
    dir: "local-good",
    head: { fetched: "a1b2c3d4", base: null, live: null, stale: false },
    verdict: { open: 1, deferred: 0, addressed: 0, terminal: 0 },
    counts: { inline: 1, section: 0, anchored: 1, unanchored: 0 },
    panel: { source: null, members: [] },
    patch: null,
    output: null,
    filters: { status: "all", kind: "all", category: null },
    items: [
      {
        sig: "sig-123",
        status: "open",
        severity: "high",
        rigor: "pointed",
        path: "src/hostile.js",
        line: 7,
        body: hostile,
        suggestion: null,
      },
    ],
  },
  gate: { passed: true, dimensions: [] },
  threads: {
    schema_version: "1",
    provider: "github",
    pr: 9,
    head_sha: "a1b2c3d4",
    fetched_at: "2026-11-12T10:00:00Z",
    truncated: false,
    threads: [
      {
        id: "thread-1",
        path: "src/hostile.js",
        line: 7,
        start_line: null,
        side: "RIGHT",
        resolved: false,
        outdated: false,
        url: null,
        author: "reviewer",
        author_kind: "human",
        finding_sig: null,
        kind: "review-comment",
        comments: [
          {
            id: "comment-1",
            author: "reviewer",
            author_kind: "human",
            body: hostile,
            truncated: false,
            created_at: "2026-11-12T10:00:00Z",
          },
        ],
      },
    ],
  },
  artifacts: [
    { path: "review.json", present: true, sha256: "abc123", bytes: 30 },
  ],
});

const cost = envelope({
  cost: {
    by: "repo",
    rows: [
      {
        dimension: "acme/widgets",
        runs: 2,
        model_runs: 2,
        input_tokens: 125,
        output_tokens: 30,
        total_tokens: 155,
        est_usd: 0.0042,
      },
    ],
    totals: {
      runs: 2,
      model_runs: 2,
      input_tokens: 125,
      output_tokens: 30,
      total_tokens: 155,
      est_usd: 0.0042,
    },
  },
});

const precision = envelope({
  precision: {
    by: "category",
    outcomes: [
      {
        dimension: "security",
        open: 1,
        resolved_unfixed: 0,
        unknown: 0,
        fixed: null,
      },
    ],
    totals: { open: 1, resolved_unfixed: 0, unknown: 0, fixed: null },
    fixed: null,
    as_of: null,
    stale: null,
    next: "Run verdict calibrate to refresh stored outcomes.",
  },
});

async function createUI({
  mode = "live",
  watch = true,
  hash = "",
  respond,
} = {}) {
  const elements = new Map(
    ["title", "message", "content", "live-status", "refresh"].map((id) => [
      id,
      new Element(id),
    ]),
  );
  const fetches = [];
  const sources = [];
  const windowListeners = new Map();
  const location = { hash };
  class FakeEventSource {
    constructor(url, options) {
      this.url = url;
      this.options = options;
      this.listeners = new Map();
      this.closed = false;
      sources.push(this);
    }

    addEventListener(name, listener) {
      this.listeners.set(name, listener);
    }

    emit(name, event = {}) {
      return this.listeners.get(name)?.(event);
    }

    close() {
      this.closed = true;
    }
  }
  const window = {
    EventSource: FakeEventSource,
    addEventListener(name, listener) {
      windowListeners.set(name, listener);
    },
    emit(name, event = {}) {
      return windowListeners.get(name)?.(event);
    },
  };
  const document = {
    getElementById(id) {
      return elements.get(id);
    },
    createElement(tagName) {
      return new Element(tagName);
    },
    querySelector() {
      return { content: mode };
    },
  };
  const fetch = async (path, options) => {
    fetches.push({ path, options });
    if (path === "/v1/health")
      return { ok: true, json: async () => ({ data: { watch } }) };
    const response = await respond(
      path,
      fetches.filter((item) => item.path === path).length,
    );
    if (response instanceof Error) throw response;
    return {
      ok: response.status === undefined || response.status < 400,
      status: response.status || 200,
      json: async () => response.body,
    };
  };
  vm.runInNewContext(
    appSource,
    { document, window, location, fetch, EventSource: FakeEventSource, Error },
    { filename: fileURLToPath(new URL("../static/app.js", import.meta.url)) },
  );
  const flush = async () => new Promise((resolve) => setImmediate(resolve));
  const navigate = async (nextHash) => {
    location.hash = nextHash;
    window.emit("hashchange");
    await flush();
  };
  await flush();
  return {
    elements,
    fetches,
    sources,
    location,
    flush,
    navigate,
  };
}

function respondFrom(fixtures) {
  return async (path) => ({ body: fixtures.get(path) || envelope({}) });
}

function descendants(root) {
  return [root, ...root.children.flatMap(descendants)];
}

function captionedTable(root, caption) {
  const captionNode = descendants(root).find(
    (element) =>
      element.tagName === "CAPTION" && element.textContent === caption,
  );
  return captionNode?.parentNode;
}

function tableRows(table) {
  const body = table.children.find((child) => child.tagName === "TBODY");
  return (
    body?.children.map((row) => row.children.map((cell) => cell.textContent)) ||
    []
  );
}

test("renders all six views from schema-shaped envelopes", async () => {
  const fixtures = new Map([
    ["/v1/bundles", inbox],
    ["/v1/roots/root-123/bundles/local-good", detail],
    ["/v1/cost?by=repo", cost],
    ["/v1/precision?by=category", precision],
  ]);
  const ui = await createUI({ respond: respondFrom(fixtures) });
  const title = () => ui.elements.get("title").textContent;
  const content = ui.elements.get("content");

  assert.equal(title(), "Inbox");
  assert.deepEqual(tableRows(captionedTable(content, "Review bundles"))[1], [
    "local-corrupt",
    "acme/broken",
    "Unknown",
    "Unknown",
    "Unknown",
    "Unknown",
    "Unknown",
  ]);
  assert.deepEqual(tableRows(captionedTable(content, "Bundle errors"))[0], [
    "local-corrupt",
    "corrupt-store",
    hostile,
  ]);
  assert.equal(
    ui.elements.get("message").textContent,
    "Partial view: some values are unknown.",
  );

  await ui.navigate("#/r/root-123/local-good");
  assert.equal(title(), "Review detail");
  assert.ok(content.textContent.includes(hostile));
  assert.equal(
    descendants(content).filter((node) => ["IMG", "SVG"].includes(node.tagName))
      .length,
    0,
  );

  await ui.navigate("#/r/root-123/local-good/threads");
  assert.equal(title(), "Threads");
  assert.ok(content.textContent.includes(hostile));

  await ui.navigate("#/r/root-123/local-good/gate");
  assert.equal(title(), "Gate");
  assert.ok(content.textContent.includes("passed"));

  await ui.navigate("#/cost");
  assert.equal(title(), "Cost");
  assert.deepEqual(tableRows(captionedTable(content, "Cost"))[0], [
    "acme/widgets",
    "2",
    "2",
    "125",
    "30",
    "155",
    "0.0042",
  ]);

  await ui.navigate("#/precision");
  assert.equal(title(), "Precision");
  assert.deepEqual(tableRows(captionedTable(content, "Precision"))[0], [
    "security",
    "1",
    "0",
    "0",
    "Unknown",
  ]);
  assert.ok(content.textContent.includes("Run verdict calibrate"));
  assert.deepEqual(
    ui.fetches
      .filter((item) => item.path !== "/v1/health")
      .map((item) => item.path),
    [
      "/v1/bundles",
      "/v1/roots/root-123/bundles/local-good",
      "/v1/roots/root-123/bundles/local-good",
      "/v1/roots/root-123/bundles/local-good",
      "/v1/cost?by=repo",
      "/v1/precision?by=category",
    ],
  );
});

test("refetches after invalidation and after an EventSource reconnect", async () => {
  const ui = await createUI({
    respond: async (path) => ({
      body: path === "/v1/bundles" ? inbox : envelope({}),
    }),
  });
  const source = ui.sources[0];
  assert.equal(source.url, "/v1/events");
  const countBundles = () =>
    ui.fetches.filter((item) => item.path === "/v1/bundles").length;
  assert.equal(countBundles(), 1);

  source.emit("bundles");
  await ui.flush();
  assert.equal(countBundles(), 2);

  source.emit("error");
  source.emit("open");
  await ui.flush();
  assert.equal(countBundles(), 3);
});

test("decodes a bundle hash segment once before requesting and linking it", async () => {
  const encodedPath = "/v1/roots/root-123/bundles/local%20space%20name";
  const spaceInbox = envelope({
    rows: [
      {
        root: "root-123",
        bundle: "local space name",
        repo: "acme/widgets",
        model: "reviewer-v1",
        head: "a1b2c3d4",
        open: 1,
        deferred: 0,
        addressed: 0,
      },
    ],
    counts: { bundles: 1, open: 1, deferred: 0, addressed: 0, terminal: 0 },
  });
  const ui = await createUI({
    respond: async (path) => {
      if (path === "/v1/bundles") return { body: spaceInbox };
      if (path === encodedPath) return { body: detail };
      return { status: 404, body: { error: { message: "no such bundle" } } };
    },
  });
  const bundleLink = descendants(ui.elements.get("content")).find(
    (element) =>
      element.tagName === "A" && element.textContent === "local space name",
  );
  assert.equal(bundleLink.href, "#/r/root-123/local%20space%20name");

  await ui.navigate(`${bundleLink.href}/threads`);
  assert.equal(ui.fetches.at(-1).path, encodedPath);
  const sectionLinks = descendants(ui.elements.get("content"))
    .filter((element) => element.tagName === "A")
    .map((element) => element.href);
  assert.deepEqual(sectionLinks, [
    "#/r/root-123/local%20space%20name",
    "#/r/root-123/local%20space%20name/threads",
    "#/r/root-123/local%20space%20name/gate",
  ]);
});

test("no-watch mode exposes a manual refresh that refetches the active view", async () => {
  const ui = await createUI({
    watch: false,
    respond: async () => ({ body: inbox }),
  });
  assert.equal(ui.sources.length, 0);
  assert.equal(ui.elements.get("live-status").hidden, true);
  assert.equal(ui.elements.get("refresh").hidden, false);
  assert.equal(
    ui.fetches.filter((item) => item.path === "/v1/bundles").length,
    1,
  );

  ui.elements.get("refresh").dispatch("click");
  await ui.flush();
  assert.equal(
    ui.fetches.filter((item) => item.path === "/v1/bundles").length,
    2,
  );
});

test("static mode reads data.json and renders the selected envelope", async () => {
  const staticFile = {
    "/v1/bundles": inbox,
    "/v1/cost?by=repo": cost,
  };
  const ui = await createUI({
    mode: "static",
    respond: async (path) => ({
      body: path === "data.json" ? staticFile : envelope({}),
    }),
  });
  assert.deepEqual(
    ui.fetches.map((item) => item.path),
    ["data.json"],
  );
  assert.equal(ui.elements.get("title").textContent, "Inbox");
  assert.ok(ui.elements.get("content").textContent.includes("local-corrupt"));
});
