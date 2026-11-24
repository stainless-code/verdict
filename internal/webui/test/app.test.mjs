import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import vm from "node:vm";
import test from "node:test";

const appSource = readFileSync(
  process.env.VERDICT_EXPORT
    ? `${process.env.VERDICT_EXPORT}/app.js`
    : new URL("../static/app.js", import.meta.url),
  "utf8",
);
const pageSource = readFileSync(
  process.env.VERDICT_EXPORT
    ? `${process.env.VERDICT_EXPORT}/index.html`
    : new URL("../static/index.html", import.meta.url),
  "utf8",
);

// TextNode stands in for the DOM text node Element.append creates from a
// string, so a cell that appends label text beside a button keeps its text.
class TextNode {
  constructor(text) {
    this.textContent = String(text);
    this.children = [];
  }
}

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
    for (const node of nodes) {
      const child = typeof node === "string" ? new TextNode(node) : node;
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

  // select and remove are the two calls the clipboard fallback makes on its
  // scratch textarea; neither carries behaviour the page can observe.
  select() {
    this.selected = true;
  }

  remove() {
    if (!this.parentNode) return;
    const at = this.parentNode.children.indexOf(this);
    if (at >= 0) this.parentNode.children.splice(at, 1);
    this.parentNode = null;
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
  interactive = false,
  csrf = "",
  hash = "",
  baseURL = "https://example.test/",
  respond,
  clipboard,
  execCommand,
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
  const timers = [];
  const document = {
    baseURI: baseURL,
    body: new Element("body"),
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
  if (execCommand) document.execCommand = execCommand;
  const fetch = async (path, options) => {
    const url = new URL(path, document.baseURI);
    const requestPath = `${url.pathname}${url.search}`;
    fetches.push({ path: requestPath, options });
    if (requestPath === "/v1/health") {
      const health = { watch };
      if (interactive) {
        health.interactive = true;
        health.csrf = csrf;
      }
      return { ok: true, json: async () => ({ data: health }) };
    }
    const response = await respond(
      requestPath,
      fetches.filter((item) => item.path === requestPath).length,
    );
    if (response instanceof Error) throw response;
    return {
      ok: response.status === undefined || response.status < 400,
      status: response.status || 200,
      json: async () => response.body,
    };
  };
  // The clipboard API is absent unless a test installs it, so the selection
  // fallback is the path under test by default.
  const navigator = clipboard ? { clipboard: { writeText: clipboard } } : {};
  vm.runInNewContext(
    appSource,
    {
      document,
      window,
      location,
      fetch,
      EventSource: FakeEventSource,
      Error,
      navigator,
      setTimeout: (fn, ms) => {
        timers.push({ fn, ms });
        return timers.length;
      },
    },
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
    timers,
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

function htmlAttribute(tagName, attributeName) {
  const tag = pageSource.match(new RegExp(`<${tagName}\\b[^>]*>`, "i"))?.[0];
  return tag?.match(new RegExp(`\\b${attributeName}="([^"]+)"`, "i"))?.[1];
}

test("exported page resolves its assets and data beside index.html", async () => {
  const baseURL = "https://example.test/export/index.html";
  assert.equal(
    new URL(htmlAttribute("link", "href"), baseURL).pathname,
    "/export/app.css",
  );
  assert.equal(
    new URL(htmlAttribute("script", "src"), baseURL).pathname,
    "/export/app.js",
  );
  assert.equal(new URL("data.json", baseURL).pathname, "/export/data.json");

  const ui = await createUI({
    mode: "static",
    baseURL,
    respond: async (path) => ({
      body:
        path === "/export/data.json" ? { "/v1/bundles": inbox } : envelope({}),
    }),
  });
  assert.deepEqual(
    ui.fetches.map((item) => item.path),
    ["/export/data.json"],
  );
  assert.equal(ui.elements.get("title").textContent, "Inbox");
});

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
  assert.deepEqual(tableRows(captionedTable(content, "Findings"))[0], [
    "sig-123",
    "open",
    "high",
    "pointed",
    "src/hostile.js",
    "7",
    hostile,
    "Unknown",
  ]);
  assert.deepEqual(tableRows(captionedTable(content, "Evidence ledger"))[0], [
    "review.json",
    "true",
    "abc123",
    "30",
  ]);
  assert.deepEqual(tableRows(captionedTable(content, "Threads"))[0], [
    "thread-1",
    "src/hostile.js",
    "7",
    "open",
    "reviewer",
    hostile,
  ]);
  assert.equal(
    descendants(content).filter((node) => node.tagName === "PRE").length,
    0,
  );
  assert.equal(
    descendants(content).filter((node) => ["IMG", "SVG"].includes(node.tagName))
      .length,
    0,
  );

  await ui.navigate("#/r/root-123/local-good/threads");
  assert.equal(title(), "Threads");
  assert.ok(content.textContent.includes(hostile));
  assert.deepEqual(tableRows(captionedTable(content, "Review threads"))[0], [
    "thread-1",
    "src/hostile.js",
    "7",
    "open",
    "reviewer",
    hostile,
  ]);

  await ui.navigate("#/r/root-123/local-good/gate");
  assert.equal(title(), "Gate");
  assert.ok(content.textContent.includes("passed"));
  assert.deepEqual(tableRows(captionedTable(content, "Gate"))[0], [
    "passed",
    "true",
  ]);

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
  assert.equal(
    descendants(content).filter((node) => node.tagName === "PRE").length,
    0,
  );
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
    "#/r/root-123/local%20space%20name/compare",
  ]);
});

test("clears a passing gate and identifies the target after every request failure", async () => {
  const failingPath = "/v1/roots/root-123/bundles/local-corrupt";
  const failures = [
    {
      name: "typed refusal",
      response: {
        body: {
          data: null,
          error: { code: "corrupt-store", message: "bundle is corrupt" },
        },
      },
      message: "bundle is corrupt",
    },
    {
      name: "HTTP status",
      response: { status: 503, body: {} },
      message: "Request returned 503",
    },
    {
      name: "rejected request",
      response: new Error("network disconnected"),
      message: "network disconnected",
    },
  ];

  for (const failure of failures) {
    const ui = await createUI({
      hash: "#/r/root-123/local-good/gate",
      respond: async (path) => {
        if (path === "/v1/roots/root-123/bundles/local-good")
          return { body: detail };
        if (path === failingPath) return failure.response;
        return { body: envelope({}) };
      },
    });
    assert.ok(ui.elements.get("content").textContent.includes("passed"));
    assert.ok(
      descendants(ui.elements.get("content")).some(
        (element) =>
          element.tagName === "A" && element.href.includes("local-good"),
      ),
    );

    await ui.navigate("#/r/root-123/local-corrupt/gate");
    assert.equal(
      ui.elements.get("title").textContent,
      "Unable to load view",
      failure.name,
    );
    assert.equal(ui.elements.get("content").textContent, "", failure.name);
    assert.equal(ui.elements.get("content").children.length, 0, failure.name);
    assert.equal(ui.elements.get("message").className, "error", failure.name);
    assert.ok(
      ui.elements.get("message").textContent.includes(failingPath),
      failure.name,
    );
    assert.ok(
      ui.elements.get("message").textContent.includes(failure.message),
      failure.name,
    );
  }
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
    baseURL: "https://example.test/export/index.html",
    respond: async (path) => ({
      body: path === "/export/data.json" ? staticFile : envelope({}),
    }),
  });
  assert.deepEqual(
    ui.fetches.map((item) => item.path),
    ["/export/data.json"],
  );
  assert.equal(ui.elements.get("title").textContent, "Inbox");
  assert.ok(ui.elements.get("content").textContent.includes("local-corrupt"));
});

test("static refresh reloads data.json and displays the newer bundle", async () => {
  let reads = 0;
  const bundleList = (bundle) =>
    envelope({
      rows: [
        {
          root: "root-123",
          bundle,
          repo: "acme/widgets",
          model: "reviewer-v1",
          head: "a1b2c3d4",
          open: 0,
          deferred: 0,
          addressed: 0,
        },
      ],
      counts: { bundles: 1, open: 0, deferred: 0, addressed: 0, terminal: 0 },
    });
  const ui = await createUI({
    mode: "static",
    baseURL: "https://example.test/export/index.html",
    respond: async (path) => {
      if (path !== "/export/data.json") return { body: envelope({}) };
      reads++;
      return {
        body: {
          "/v1/bundles": bundleList(reads === 1 ? "local-old" : "local-new"),
        },
      };
    },
  });
  assert.equal(reads, 1);
  assert.ok(ui.elements.get("content").textContent.includes("local-old"));

  ui.elements.get("refresh").dispatch("click");
  await ui.flush();
  assert.equal(reads, 2);
  assert.ok(ui.elements.get("content").textContent.includes("local-new"));
  assert.ok(!ui.elements.get("content").textContent.includes("local-old"));
});

test("static mode omits the Compare tab the export cannot serve", async () => {
  const staticFile = {
    "/v1/bundles": inbox,
    "/v1/roots/root-123/bundles/local-good": detail,
  };
  const staticUI = await createUI({
    mode: "static",
    baseURL: "https://example.test/export/index.html",
    respond: async (path) => ({
      body: path === "/export/data.json" ? staticFile : envelope({}),
    }),
  });
  await staticUI.navigate("#/r/root-123/local-good");
  const labels = descendants(staticUI.elements.get("content"))
    .filter((element) => element.tagName === "A")
    .map((link) => link.textContent);
  assert.deepEqual(labels, ["Detail", "Threads", "Gate"]);

  const liveUI = await createUI({
    respond: respondFrom(
      new Map([
        ["/v1/bundles", inbox],
        ["/v1/roots/root-123/bundles/local-good", detail],
      ]),
    ),
  });
  await liveUI.navigate("#/r/root-123/local-good");
  const liveLabels = descendants(liveUI.elements.get("content"))
    .filter((element) => element.tagName === "A")
    .map((link) => link.textContent);
  assert.deepEqual(liveLabels, ["Detail", "Threads", "Gate", "Compare"]);
});

if (process.env.VERDICT_EXPORT) {
  test("export renders all six views from its data.json", async () => {
    const data = JSON.parse(
      readFileSync(`${process.env.VERDICT_EXPORT}/data.json`, "utf8"),
    );
    const row = data["/v1/bundles"].data.rows[0];
    const base = `#/r/${row.root}/${row.bundle}`;
    const ui = await createUI({
      mode: "static",
      respond: async () => ({ body: data }),
    });
    for (const [hash, title, caption, needle] of [
      ["#/bundles", "Inbox", "Review bundles", "1234"],
      [
        base,
        "Review detail",
        "Review summary",
        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      ],
      [`${base}/threads`, "Threads", "Review threads", "Exported discussion"],
      [`${base}/gate`, "Gate", "Gate dimensions", "findings"],
      ["#/cost", "Cost", "Cost", "acme/app"],
      ["#/precision", "Precision", "Precision", "untagged"],
    ]) {
      await ui.navigate(hash);
      assert.equal(ui.elements.get("title").textContent, title);
      assert.ok(
        captionedTable(ui.elements.get("content"), caption),
        `missing ${caption}`,
      );
      assert.ok(
        captionedTable(
          ui.elements.get("content"),
          caption,
        ).textContent.includes(needle),
        `missing data in ${title}`,
      );
    }
    assert.deepEqual(
      ui.fetches.map((x) => x.path),
      ["/data.json"],
    );
    assert.equal(ui.sources.length, 0);
  });
}

test("links a retained reproduction to its download", async () => {
  const artifactDetail = envelope({
    report: {
      items: [
        {
          sig: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          reproduction: {
            path: "tests/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.py",
          },
        },
      ],
    },
  });
  const ui = await createUI({
    respond: respondFrom(
      new Map([
        ["/v1/bundles", inbox],
        ["/v1/roots/root-123/bundles/local-good", artifactDetail],
      ]),
    ),
  });
  await ui.navigate("#/r/root-123/local-good");
  const link = ui.elements
    .get("content")
    .children.find((child) => child.tagName === "A");
  assert.equal(
    link?.href,
    "/v1/roots/root-123/bundles/local-good/findings/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/reproduction",
  );
  assert.equal(link?.download, "reproduction.txt");
});

// compare is one four-bucket comparison envelope, with a row in every bucket
// the copy column reaches.
const compare = envelope({
  compare: {
    baseline: {
      name: "local-other",
      dir: ".verdict/state/local-other",
      head_sha: null,
      base_sha: null,
      recorded_at: null,
      findings: 1,
    },
    counts: { new: 1, persisting: 1, resolved: 0, not_reviewed: 0 },
    new: [
      {
        sig: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        status: "open",
        severity: "critical",
        path: "src/app.js",
        line: 3,
        claim: "Claim text.",
        reason: "not-in-baseline",
      },
    ],
    persisting: [],
    resolved: [],
    not_reviewed: [],
  },
});

const copyPath =
  "/v1/roots/root-123/bundles/local-good/findings/" +
  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/copy";

const copyPayload = envelope({
  sig: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  prompt: "Fix the finding below in this repository.",
  triage:
    "verdict triage acme/app 9 --sig aaaa --status addressed --rationale 'x'",
});

// firstButton names the first rendered button carrying one label.
function firstButton(root, label) {
  return descendants(root).find(
    (element) => element.tagName === "BUTTON" && element.textContent === label,
  );
}

test("compare lists only same-root baselines and never reuses another root's selection", async () => {
  const multiRootInbox = envelope({
    rows: [
      { root: "root-999", bundle: "local-foreign" },
      { root: "root-123", bundle: "local-good" },
      { root: "root-123", bundle: "local-other" },
      { root: "root-999", bundle: "local-family" },
    ],
    counts: { bundles: 4, open: 0, deferred: 0, addressed: 0, terminal: 0 },
  });
  const ui = await createUI({
    respond: async (path) => {
      if (path === "/v1/bundles") return { body: multiRootInbox };
      if (path.includes("/compare?")) return { body: compare };
      return { body: envelope({}) };
    },
  });

  await ui.navigate("#/r/root-123/local-good/compare");
  assert.deepEqual(
    ui.fetches
      .map((item) => item.path)
      .filter((path) => path.includes("/compare?")),
    ["/v1/roots/root-123/bundles/local-good/compare?baseline=local-other"],
  );
  const picker = descendants(ui.elements.get("content")).find(
    (element) => element.tagName === "SELECT",
  );
  assert.deepEqual(
    picker?.children.map((option) => option.value),
    ["local-other"],
  );

  const before = ui.fetches.length;
  await ui.navigate("#/r/root-999/local-foreign/compare");
  assert.deepEqual(
    ui.fetches.slice(before).map((item) => item.path),
    [
      "/v1/bundles",
      "/v1/roots/root-999/bundles/local-foreign/compare?baseline=local-family",
    ],
  );
});

test("clipboard fallback reports copied only when execCommand copied", async () => {
  for (const [name, execCommand, want] of [
    ["refused", () => false, "Not copied — clipboard unavailable"],
    [
      "throwing",
      () => {
        throw new Error("denied");
      },
      "Not copied — clipboard unavailable",
    ],
    ["accepted", () => true, "Prompt copied"],
  ]) {
    const ui = await createUI({
      hash: "#/r/root-123/local-good/compare/local-other",
      execCommand,
      respond: async (path) => {
        if (path === "/v1/bundles") return { body: inbox };
        if (path === copyPath) return { body: copyPayload };
        return { body: compare };
      },
    });
    const button = firstButton(ui.elements.get("content"), "Copy prompt");
    assert.ok(button, `${name}: no copy button rendered`);
    await button.dispatch("click");
    assert.equal(button.textContent, want, name);
  }
});

test("clipboard API success writes the payload and reports copied", async () => {
  const written = [];
  const ui = await createUI({
    hash: "#/r/root-123/local-good/compare/local-other",
    clipboard: async (text) => {
      written.push(text);
    },
    respond: async (path) => {
      if (path === "/v1/bundles") return { body: inbox };
      if (path === copyPath) return { body: copyPayload };
      return { body: compare };
    },
  });
  const button = firstButton(ui.elements.get("content"), "Copy prompt");
  await button.dispatch("click");
  assert.deepEqual(written, ["Fix the finding below in this repository."]);
  assert.equal(button.textContent, "Prompt copied");
});

test("a finding with no copy payload reports that instead of copying", async () => {
  const ui = await createUI({
    hash: "#/r/root-123/local-good/compare/local-other",
    clipboard: async () => {
      throw new Error("clipboard must not be written");
    },
    respond: async (path) => {
      if (path === "/v1/bundles") return { body: inbox };
      if (path === copyPath)
        return {
          status: 404,
          body: {
            status: 404,
            reason: "not-found",
            message: "no such root or bundle",
          },
        };
      return { body: compare };
    },
  });
  const button = firstButton(ui.elements.get("content"), "Copy prompt");
  await button.dispatch("click");
  assert.equal(button.textContent, "No copy for this finding");
});

const detailPath = "/v1/roots/root-123/bundles/local-good";

function detailFixtures(extra = []) {
  return new Map([["/v1/bundles", inbox], [detailPath, detail], ...extra]);
}

function findControl(root, label) {
  return descendants(root).find(
    (element) => element.attributes["aria-label"] === label,
  );
}

function writeButtons(root) {
  return descendants(root).filter(
    (element) =>
      element.tagName === "BUTTON" &&
      ["Triage", "Promote to KB"].includes(element.textContent),
  );
}

test("a read-only server renders no write controls", async () => {
  const ui = await createUI({
    hash: "#/r/root-123/local-good",
    respond: respondFrom(detailFixtures()),
  });
  assert.deepEqual(writeButtons(ui.elements.get("content")), []);
  assert.equal(
    findControl(ui.elements.get("content"), "Triage status"),
    undefined,
  );
});

test("static mode never renders write controls, even when the server advertises them", async () => {
  const ui = await createUI({
    mode: "static",
    interactive: true,
    csrf: "csrf-test-token",
    hash: "#/r/root-123/local-good",
    respond: respondFrom(new Map([["/data.json", { [detailPath]: detail }]])),
  });
  assert.deepEqual(writeButtons(ui.elements.get("content")), []);
  assert.equal(
    findControl(ui.elements.get("content"), "Triage status"),
    undefined,
  );
});

test("interactive mode posts triage with the CSRF header and renders the result", async () => {
  const rationale = "Verified against the diff: this is a false alarm here.";
  const ui = await createUI({
    interactive: true,
    csrf: "csrf-test-token",
    respond: respondFrom(
      detailFixtures([
        ["/v1/triage", envelope({ sig: "sig-123", status: "false-positive" })],
      ]),
    ),
  });
  await ui.navigate("#/r/root-123/local-good");
  const content = ui.elements.get("content");
  const status = findControl(content, "Triage status");
  assert.deepEqual(
    status.children.map((option) => option.textContent),
    ["open", "addressed", "wont-fix", "false-positive", "deferred"],
  );
  const rationaleInput = findControl(content, "Rationale");
  const force = findControl(content, "Force");
  const button = firstButton(content, "Triage");
  status.value = "false-positive";
  rationaleInput.value = rationale;
  await button.dispatch("click");
  const sent = ui.fetches.find((item) => item.path === "/v1/triage");
  assert.ok(sent, "triage was not posted");
  assert.equal(sent.options.method, "POST");
  assert.equal(sent.options.headers["Content-Type"], "application/json");
  assert.equal(sent.options.headers["X-Verdict-CSRF"], "csrf-test-token");
  assert.deepEqual(JSON.parse(sent.options.body), {
    root: "root-123",
    bundle: "local-good",
    sig: "sig-123",
    status: "false-positive",
    rationale,
    force: false,
  });
  assert.equal(
    ui.elements.get("message").textContent,
    "Triaged sig-123 -> false-positive.",
  );
  assert.equal(
    ui.fetches.filter((item) => item.options?.method === "POST").length,
    1,
  );
});

test("interactive triage renders a refusal envelope honestly", async () => {
  const ui = await createUI({
    interactive: true,
    csrf: "csrf-test-token",
    respond: respondFrom(
      detailFixtures([
        [
          "/v1/triage",
          {
            data: null,
            error: {
              code: "rationale-required",
              message: "refusing triage: rationale required",
            },
          },
        ],
      ]),
    ),
  });
  await ui.navigate("#/r/root-123/local-good");
  await firstButton(ui.elements.get("content"), "Triage").dispatch("click");
  assert.equal(
    ui.elements.get("message").textContent,
    "refusing triage: rationale required",
  );
  assert.equal(ui.elements.get("message").className, "error");
});

test("interactive triage renders a transport refusal honestly", async () => {
  const ui = await createUI({
    interactive: true,
    csrf: "csrf-test-token",
    respond: async (path) => {
      if (path === "/v1/triage")
        return {
          status: 403,
          body: {
            status: 403,
            reason: "bad-csrf",
            message: "writes require the per-session X-Verdict-CSRF token",
          },
        };
      return { body: detailFixtures().get(path) || envelope({}) };
    },
  });
  await ui.navigate("#/r/root-123/local-good");
  await firstButton(ui.elements.get("content"), "Triage").dispatch("click");
  assert.equal(
    ui.elements.get("message").textContent,
    "writes require the per-session X-Verdict-CSRF token",
  );
});

test("interactive mode promotes a KB rule with the CSRF header", async () => {
  const ui = await createUI({
    interactive: true,
    csrf: "csrf-test-token",
    respond: respondFrom(
      detailFixtures([
        ["/v1/kb/promote", envelope({ rule: { id: "never-log-secrets" } })],
      ]),
    ),
  });
  await ui.navigate("#/r/root-123/local-good");
  const content = ui.elements.get("content");
  const rule = findControl(content, "KB rule text");
  const scope = findControl(content, "KB scope");
  const rigor = findControl(content, "KB rigor");
  assert.deepEqual(
    rigor.children.map((option) => option.textContent),
    ["said", "pointed", "walked", "ran", "reproduced"],
  );
  assert.equal(rigor.value, "pointed");
  rule.value = "Never log secrets in error paths";
  scope.value = "src/*.go";
  await firstButton(content, "Promote to KB").dispatch("click");
  const sent = ui.fetches.find((item) => item.path === "/v1/kb/promote");
  assert.ok(sent, "promotion was not posted");
  assert.equal(sent.options.method, "POST");
  assert.equal(sent.options.headers["X-Verdict-CSRF"], "csrf-test-token");
  assert.deepEqual(JSON.parse(sent.options.body), {
    root: "root-123",
    rule: "Never log secrets in error paths",
    scope: "src/*.go",
    rigor: "pointed",
  });
  assert.equal(
    ui.elements.get("message").textContent,
    "Promoted rule never-log-secrets to KB.",
  );
});

// jobButtons names every rendered Tier B control by its button label.
// Verify is bundle-scoped, so the only verify control is the bundle-level
// button: there is no per-finding `Reproduce` that claims a single finding
// was isolated.
function jobButtons(root) {
  return descendants(root).filter(
    (element) =>
      element.tagName === "BUTTON" &&
      ["Reproduce in sandbox", "Preview PR post", "Cancel"].includes(
        element.textContent,
      ),
  );
}

test("a read-only server renders no job controls", async () => {
  const ui = await createUI({
    hash: "#/r/root-123/local-good",
    respond: respondFrom(detailFixtures()),
  });
  assert.deepEqual(jobButtons(ui.elements.get("content")), []);
  assert.equal(
    findControl(ui.elements.get("content"), "Reproduce sig-123 in sandbox"),
    undefined,
  );
});

test("static mode never renders job controls, even when the server advertises them", async () => {
  const ui = await createUI({
    mode: "static",
    interactive: true,
    csrf: "csrf-test-token",
    hash: "#/r/root-123/local-good",
    respond: respondFrom(new Map([["/data.json", { [detailPath]: detail }]])),
  });
  assert.deepEqual(jobButtons(ui.elements.get("content")), []);
  assert.equal(
    findControl(ui.elements.get("content"), "Reproduce sig-123 in sandbox"),
    undefined,
  );
});

test("interactive mode posts a sandbox verify with CSRF and renders SSE status and result", async () => {
  const queued = {
    data: {
      id: "job-1",
      kind: "verify",
      root: "root-123",
      bundle: "local-good",
      status: "queued",
    },
    error: null,
  };
  const ui = await createUI({
    interactive: true,
    csrf: "csrf-test-token",
    respond: respondFrom(detailFixtures([["/v1/jobs/verify", queued]])),
  });
  await ui.navigate("#/r/root-123/local-good");
  const content = ui.elements.get("content");
  assert.ok(firstButton(content, "Reproduce in sandbox"));
  assert.ok(firstButton(content, "Preview PR post"));
  assert.equal(firstButton(content, "Reproduce"), undefined);
  assert.equal(findControl(content, "Reproduce sig-123 in sandbox"), undefined);
  await firstButton(content, "Reproduce in sandbox").dispatch("click");
  const sent = ui.fetches.find((item) => item.path === "/v1/jobs/verify");
  assert.ok(sent, "verify job was not posted");
  assert.equal(sent.options.method, "POST");
  assert.equal(sent.options.headers["Content-Type"], "application/json");
  assert.equal(sent.options.headers["X-Verdict-CSRF"], "csrf-test-token");
  assert.deepEqual(JSON.parse(sent.options.body), {
    root: "root-123",
    bundle: "local-good",
  });
  assert.ok(content.textContent.includes("Job job-1 (verify) — queued"));
  const source = ui.sources[0];
  assert.equal(source.url, "/v1/events");
  source.emit("jobs", {
    data: JSON.stringify({
      data: {
        id: "job-1",
        kind: "verify",
        root: "root-123",
        bundle: "local-good",
        status: "running",
      },
      error: null,
    }),
  });
  await ui.flush();
  assert.ok(content.textContent.includes("Job job-1 (verify) — running"));
  assert.ok(firstButton(content, "Cancel"));
  source.emit("jobs", {
    data: JSON.stringify({
      data: {
        id: "job-1",
        kind: "verify",
        root: "root-123",
        bundle: "local-good",
        status: "succeeded",
        result: { exit: 0, sandboxed: true, outputTail: "proof holds" },
      },
      error: null,
    }),
  });
  await ui.flush();
  assert.ok(content.textContent.includes("Job job-1 (verify) — succeeded"));
  assert.ok(content.textContent.includes("proof holds"));
  assert.equal(firstButton(content, "Cancel"), undefined);
});

test("interactive preview posts dry-run and renders the refusal honestly", async () => {
  const ui = await createUI({
    interactive: true,
    csrf: "csrf-test-token",
    respond: respondFrom(
      detailFixtures([
        [
          "/v1/jobs/post-preview",
          {
            data: null,
            error: {
              code: "missing-review-file",
              message: "missing review.json",
            },
          },
        ],
      ]),
    ),
  });
  await ui.navigate("#/r/root-123/local-good");
  await firstButton(ui.elements.get("content"), "Preview PR post").dispatch(
    "click",
  );
  const sent = ui.fetches.find((item) => item.path === "/v1/jobs/post-preview");
  assert.ok(sent, "preview was not posted");
  assert.equal(sent.options.headers["X-Verdict-CSRF"], "csrf-test-token");
  assert.deepEqual(JSON.parse(sent.options.body), {
    root: "root-123",
    bundle: "local-good",
  });
  assert.equal(ui.elements.get("message").textContent, "missing review.json");
  assert.equal(ui.elements.get("message").className, "error");
});

test("interactive job cancellation posts cancel with CSRF and renders cancelled", async () => {
  const queued = {
    data: {
      id: "job-1",
      kind: "post-preview",
      root: "root-123",
      bundle: "local-good",
      status: "queued",
    },
    error: null,
  };
  const cancelled = {
    data: {
      id: "job-1",
      kind: "post-preview",
      root: "root-123",
      bundle: "local-good",
      status: "cancelled",
    },
    error: null,
  };
  const ui = await createUI({
    interactive: true,
    csrf: "csrf-test-token",
    respond: async (path) => {
      if (path === "/v1/jobs/post-preview") return { body: queued };
      if (path === "/v1/jobs/job-1/cancel") return { body: cancelled };
      return { body: detailFixtures().get(path) || envelope({}) };
    },
  });
  await ui.navigate("#/r/root-123/local-good");
  const content = ui.elements.get("content");
  await firstButton(content, "Preview PR post").dispatch("click");
  assert.ok(content.textContent.includes("queued"));
  await firstButton(content, "Cancel").dispatch("click");
  const sent = ui.fetches.find((item) => item.path === "/v1/jobs/job-1/cancel");
  assert.ok(sent, "cancel was not posted");
  assert.equal(sent.options.method, "POST");
  assert.equal(sent.options.headers["X-Verdict-CSRF"], "csrf-test-token");
  assert.deepEqual(JSON.parse(sent.options.body), {});
  assert.ok(content.textContent.includes("cancelled"));
});
