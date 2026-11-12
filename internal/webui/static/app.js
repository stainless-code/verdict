"use strict";

(() => {
  const title = document.getElementById("title");
  const message = document.getElementById("message");
  const content = document.getElementById("content");
  const live = document.getElementById("live-status");
  const refresh = document.getElementById("refresh");
  const mode = document.querySelector('meta[name="verdict-mode"]')?.content;
  let staticData;
  let eventSource;
  let reconnecting = false;
  let costBy = "repo";
  let precisionBy = "category";

  function node(tag, text, className) {
    const element = document.createElement(tag);
    if (text !== undefined && text !== null) element.textContent = String(text);
    if (className) element.className = className;
    return element;
  }

  function showUnknown(value) {
    return value === null || value === undefined || value === "";
  }

  function cell(value) {
    if (showUnknown(value)) return node("span", "Unknown", "unknown");
    if (typeof value === "object") {
      return node(
        "pre",
        JSON.stringify(
          value,
          (_key, item) => (item === null ? "Unknown" : item),
          2,
        ),
      );
    }
    return node("span", value);
  }

  function table(caption, columns, rows) {
    const wrapper = node("div", undefined, "panel");
    const tableEl = node("table");
    tableEl.append(node("caption", caption));
    const head = node("thead");
    const headRow = node("tr");
    for (const column of columns) {
      const th = node("th", column.label);
      th.scope = "col";
      headRow.append(th);
    }
    head.append(headRow);
    tableEl.append(head);
    const body = node("tbody");
    for (const row of rows) {
      const tr = node("tr");
      for (const column of columns) {
        const td = node("td");
        const value = row?.[column.key];
        if (column.link && typeof value === "string" && row.root) {
          const link = node("a", value);
          link.href = `#/r/${encodeURIComponent(row.root)}/${encodeURIComponent(value)}`;
          td.append(link);
        } else {
          td.append(cell(value));
        }
        tr.append(td);
      }
      body.append(tr);
    }
    tableEl.append(body);
    wrapper.append(tableEl);
    return wrapper;
  }

  function objectPanel(caption, object) {
    const entries = Object.entries(object || {});
    return table(
      caption,
      [
        { key: "key", label: "Field" },
        { key: "value", label: "Value" },
      ],
      entries.map(([key, value]) => ({ key, value })),
    );
  }

  function encodeHashSegment(segment) {
    let decoded = segment || "";
    try {
      decoded = decodeURIComponent(decoded);
    } catch (_) {}
    return encodeURIComponent(decoded);
  }

  function detailNavigation(current) {
    const parts = location.hash.slice(1).split("/").filter(Boolean);
    const base = `#/r/${encodeHashSegment(parts[1])}/${encodeHashSegment(parts[2])}`;
    const nav = node("nav", undefined, "section-nav");
    nav.setAttribute("aria-label", "Review sections");
    for (const [suffix, label] of [
      ["", "Detail"],
      ["/threads", "Threads"],
      ["/gate", "Gate"],
    ]) {
      const link = node("a", label);
      link.href = `${base}${suffix}`;
      const page = suffix.slice(1) || "detail";
      if (current === page) link.setAttribute("aria-current", "page");
      nav.append(link);
    }
    return nav;
  }

  function dimensionPicker(label, id, values, selected, update) {
    const wrapper = node("div", undefined, "filters");
    const caption = node("label", label);
    caption.htmlFor = id;
    const select = node("select");
    select.id = id;
    for (const value of values) {
      const option = node("option", value);
      option.value = value;
      option.selected = value === selected;
      select.append(option);
    }
    select.addEventListener("change", () => update(select.value));
    wrapper.append(caption, select);
    return wrapper;
  }

  function envelopeData(body) {
    if (!body || body.error) {
      throw new Error(body?.error?.message || "The server returned no view.");
    }
    return body.data || {};
  }

  function renderInbox(data) {
    const rows = Array.isArray(data.rows) ? data.rows : [];
    if (Array.isArray(data.errors) && data.errors.length) {
      content.append(
        table(
          "Bundle errors",
          [
            { key: "bundle", label: "Bundle" },
            { key: "code", label: "Code" },
            { key: "message", label: "Details" },
          ],
          data.errors,
        ),
      );
    }
    content.append(
      table(
        "Review bundles",
        [
          { key: "bundle", label: "Bundle", link: true },
          { key: "repo", label: "Repository" },
          { key: "model", label: "Model" },
          { key: "head", label: "Head" },
          { key: "open", label: "Open" },
          { key: "deferred", label: "Deferred" },
          { key: "addressed", label: "Addressed" },
        ],
        rows,
      ),
    );
    content.append(
      objectPanel(
        "Portfolio counts",
        data.counts || { partial: data.partial, counts: "Unknown" },
      ),
    );
  }

  function renderDetail(data, gateOnly) {
    const report = data.report || {};
    const gate = data.gate;
    const items = Array.isArray(report.items) ? report.items : [];
    if (!gateOnly) {
      content.append(objectPanel("Review summary", report));
      content.append(
        table(
          "Findings",
          [
            { key: "sig", label: "Signature" },
            { key: "status", label: "Status" },
            { key: "severity", label: "Severity" },
            { key: "rigor", label: "Evidence" },
            { key: "path", label: "Path" },
            { key: "line", label: "Line" },
            { key: "body", label: "Finding" },
            { key: "suggestion", label: "Suggestion" },
          ],
          items,
        ),
      );
      content.append(objectPanel("Evidence ledger", data.artifacts));
      content.append(objectPanel("Threads", data.threads));
    }
    content.append(objectPanel("Gate", gate));
    if (gate === null || gate === undefined)
      message.textContent = "Gate status is unknown.";
  }

  function render(view, data) {
    content.replaceChildren();
    message.textContent = "";
    message.className = "";
    if (view === "inbox") {
      title.textContent = "Inbox";
      renderInbox(data);
    } else if (view === "detail" || view === "gate" || view === "threads") {
      title.textContent =
        view === "gate"
          ? "Gate"
          : view === "threads"
            ? "Threads"
            : "Review detail";
      content.append(detailNavigation(view));
      if (view === "threads") {
        content.append(objectPanel("Review threads", data.threads));
      } else {
        renderDetail(data, view === "gate");
      }
    } else if (view === "cost" || view === "precision") {
      title.textContent = view === "cost" ? "Cost" : "Precision";
      if (view === "cost") {
        content.append(
          dimensionPicker(
            "Group cost by",
            "cost-by",
            ["repo", "week", "model"],
            costBy,
            (value) => {
              costBy = value;
              fetchView();
            },
          ),
        );
      } else {
        content.append(
          dimensionPicker(
            "Group precision by",
            "precision-by",
            ["category", "tier"],
            precisionBy,
            (value) => {
              precisionBy = value;
              fetchView();
            },
          ),
        );
      }
      const aggregate = view === "cost" ? data.cost : data.precision;
      const rows =
        view === "cost"
          ? Array.isArray(aggregate?.rows)
            ? aggregate.rows
            : []
          : Array.isArray(aggregate?.outcomes)
            ? aggregate.outcomes
            : [];
      content.append(
        table(
          title.textContent,
          rows.length
            ? Object.keys(rows[0]).map((key) => ({ key, label: key }))
            : [{ key: "status", label: "Status" }],
          rows,
        ),
      );
      content.append(objectPanel("Totals", aggregate?.totals));
      if (view === "precision") {
        content.append(
          objectPanel("Precision status", {
            by: aggregate?.by,
            fixed: aggregate?.fixed,
            as_of: aggregate?.as_of,
            stale: aggregate?.stale,
            next: aggregate?.next,
          }),
        );
      }
      if (data.partial)
        message.textContent =
          "Partial view: one or more source values are unknown.";
    }
    if (data.partial && !message.textContent)
      message.textContent = "Partial view: some values are unknown.";
  }

  function route() {
    const parts = location.hash.slice(1).split("/").filter(Boolean);
    if (!parts.length || parts[0] === "bundles")
      return { view: "inbox", path: "/v1/bundles" };
    if (parts[0] === "cost")
      return {
        view: "cost",
        path: `/v1/cost?by=${encodeURIComponent(costBy)}`,
      };
    if (parts[0] === "precision")
      return {
        view: "precision",
        path: `/v1/precision?by=${encodeURIComponent(precisionBy)}`,
      };
    if (parts[0] === "r" && parts.length >= 3) {
      const view =
        parts[3] === "threads"
          ? "threads"
          : parts[3] === "gate"
            ? "gate"
            : "detail";
      return {
        view,
        path: `/v1/roots/${encodeHashSegment(parts[1])}/bundles/${encodeHashSegment(parts[2])}`,
      };
    }
    return { view: "inbox", path: "/v1/bundles" };
  }

  async function fetchView() {
    const current = route();
    try {
      let body;
      if (mode === "static") {
        if (!staticData) {
          const response = await fetch("data.json", { cache: "no-store" });
          if (!response.ok)
            throw new Error(`data.json returned ${response.status}`);
          staticData = await response.json();
        }
        body =
          staticData[current.path] ||
          staticData[current.path.replace(/\?.*$/, "")] ||
          staticData;
      } else {
        const response = await fetch(current.path, {
          credentials: "same-origin",
          cache: "no-store",
        });
        if (!response.ok)
          throw new Error(`Request returned ${response.status}`);
        body = await response.json();
      }
      const data = envelopeData(body);
      if (data) render(current.view, data);
    } catch (error) {
      content.replaceChildren();
      title.textContent = "Unable to load view";
      const detail = error instanceof Error ? error.message : String(error);
      message.textContent = `${current.path}: ${detail}`;
      message.className = "error";
    }
  }

  async function startLive() {
    if (mode === "static") {
      live.hidden = true;
      refresh.hidden = false;
      return;
    }
    try {
      const response = await fetch("/v1/health", {
        credentials: "same-origin",
        cache: "no-store",
      });
      const health = await response.json();
      if (health.data?.watch === false) {
        live.hidden = true;
        refresh.hidden = false;
        return;
      }
    } catch (_) {
      live.textContent = "Live status unavailable";
      return;
    }
    if (!window.EventSource) {
      live.hidden = true;
      refresh.hidden = false;
      return;
    }
    eventSource = new EventSource("/v1/events", { withCredentials: true });
    eventSource.addEventListener("open", () => {
      live.textContent = "Live";
      if (reconnecting) fetchView();
      reconnecting = false;
    });
    eventSource.addEventListener("error", () => {
      live.textContent = "Reconnecting";
      reconnecting = true;
    });
    eventSource.addEventListener("bundles", () => fetchView());
  }

  refresh.addEventListener("click", fetchView);
  window.addEventListener("hashchange", fetchView);
  window.addEventListener("beforeunload", () => eventSource?.close());
  fetchView();
  startLive();
})();
