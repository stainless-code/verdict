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
  // The compare baseline belongs to one root and one bundle: a selection made
  // on one review must never be spent on another root's comparison.
  let compareBaseline = "";
  let compareScope = "";
  let inboxRows = [];
  // The server advertises interactive mode and its per-session CSRF token on
  // /v1/health; both stay unset in static mode and on a read-only server,
  // so no write control can render there.
  let interactive = false;
  let csrfToken = "";
  // The Tier B job drawer shows the most recent job snapshot the SSE
  // stream published; null until one job has been started.
  let activeJob = null;
  let jobDrawer = null;
  let diffMode = "unified";
  const triageStatuses = [
    "open",
    "addressed",
    "wont-fix",
    "false-positive",
    "deferred",
  ];
  const kbRigors = ["said", "pointed", "walked", "ran", "reproduced"];

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
        if (column.action) {
          column.action(row, td);
        } else if (
          column.link &&
          typeof row?.[column.key] === "string" &&
          row.root
        ) {
          const link = node("a", row[column.key]);
          link.href = `#/r/${encodeURIComponent(row.root)}/${encodeURIComponent(row[column.key])}`;
          td.append(link);
        } else {
          td.append(cell(row?.[column.key]));
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

  function reviewSummary(report) {
    return {
      generated_at: report.generated_at,
      bundle: report.dir,
      fetched_head: report.head?.fetched,
      stale: report.head?.stale,
      open: report.verdict?.open,
      deferred: report.verdict?.deferred,
      addressed: report.verdict?.addressed,
      terminal: report.verdict?.terminal,
      inline_findings: report.counts?.inline,
      section_findings: report.counts?.section,
      anchored: report.counts?.anchored,
      unanchored: report.counts?.unanchored,
    };
  }

  function threadTable(caption, file) {
    const rows = [];
    for (const thread of file?.threads || []) {
      const comments =
        Array.isArray(thread.comments) && thread.comments.length
          ? thread.comments
          : [null];
      for (const comment of comments) {
        rows.push({
          id: thread.id,
          path: thread.path,
          line: thread.line,
          status: thread.resolved ? "resolved" : "open",
          author: comment?.author || thread.author,
          body: comment?.body,
        });
      }
    }
    return table(
      caption,
      [
        { key: "id", label: "Thread" },
        { key: "path", label: "Path" },
        { key: "line", label: "Line" },
        { key: "status", label: "Status" },
        { key: "author", label: "Author" },
        { key: "body", label: "Comment" },
      ],
      rows,
    );
  }

  function renderGate(gate) {
    content.append(objectPanel("Gate", { passed: gate?.passed }));
    content.append(
      table(
        "Gate dimensions",
        [
          { key: "id", label: "Dimension" },
          { key: "status", label: "Status" },
          { key: "detail", label: "Details" },
          { key: "count", label: "Count" },
        ],
        Array.isArray(gate?.dimensions) ? gate.dimensions : [],
      ),
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
    const links = [
      ["", "Detail"],
      ["/diff", "Diff"],
      ["/threads", "Threads"],
      ["/gate", "Gate"],
    ];
    if (mode !== "static") links.push(["/compare", "Compare"]);
    for (const [suffix, label] of links) {
      const link = node("a", label);
      link.href = `${base}${suffix}`;
      const page = suffix.slice(1) || "detail";
      if (current === page) link.setAttribute("aria-current", "page");
      nav.append(link);
    }
    return nav;
  }

  // writeClipboard reports whether the text really reached the clipboard. The
  // selection fallback is the only path left when the async clipboard API is
  // absent, and document.execCommand answers false (or throws) when the copy
  // did not happen — a caller that ignored the answer would claim "Copied" for
  // a no-op.
  async function writeClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
    if (typeof document.execCommand !== "function") return false;
    const area = node("textarea");
    area.value = text;
    document.body.append(area);
    area.select();
    let copied = false;
    try {
      copied = document.execCommand("copy") === true;
    } catch (_) {
      copied = false;
    }
    area.remove();
    return copied;
  }

  async function copyPayload(button, root, bundle, sig, kind) {
    const path =
      `/v1/roots/${encodeURIComponent(root)}/bundles/` +
      `${encodeURIComponent(bundle)}/findings/${encodeURIComponent(sig)}/copy`;
    try {
      const response = await fetch(path, {
        credentials: "same-origin",
        cache: "no-store",
      });
      if (response.status === 404) {
        // No payload exists for this finding: the store does not hold it, or
        // policy withheld it from every published body.
        button.textContent = "No copy for this finding";
        return;
      }
      if (!response.ok) throw new Error(`Request returned ${response.status}`);
      const body = await response.json();
      const payload = envelopeData(body);
      const text = payload?.[kind];
      if (!text) {
        button.textContent = "No prompt for this finding";
        return;
      }
      if (!(await writeClipboard(text))) {
        button.textContent = "Not copied — clipboard unavailable";
        return;
      }
      const idle = kind === "prompt" ? "Copy prompt" : "Copy triage";
      button.textContent =
        kind === "prompt" ? "Prompt copied" : "Command copied";
      setTimeout(() => {
        button.textContent = idle;
      }, 2000);
    } catch (error) {
      message.textContent =
        error instanceof Error ? error.message : String(error);
      message.className = "error";
    }
  }

  function copyButton(label, root, bundle, sig, kind) {
    const button = node("button", label);
    button.type = "button";
    button.addEventListener("click", () =>
      copyPayload(button, root, bundle, sig, kind),
    );
    return button;
  }

  // writeJSON posts one JSON body with the per-session CSRF header. The
  // response body is returned for the caller to render honestly: a refusal
  // envelope is not thrown.
  async function writeJSON(path, payload) {
    const response = await fetch(path, {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers: {
        "Content-Type": "application/json",
        "X-Verdict-CSRF": csrfToken,
      },
      body: JSON.stringify(payload),
    });
    return { response, body: await response.json() };
  }

  // submitTriage writes one finding's status and renders the server's
  // answer in the status line. It never refetches: the operator reads the
  // result, then refreshes to see the updated store.
  async function submitTriage(payload) {
    try {
      const { response, body } = await writeJSON("/v1/triage", payload);
      if (!response.ok) {
        message.textContent =
          body?.message || `Request returned ${response.status}`;
        message.className = "error";
        return;
      }
      if (body?.error) {
        message.textContent = body.error.message || "Triage refused.";
        message.className = "error";
        return;
      }
      const data = body?.data || {};
      message.textContent = `Triaged ${data.sig || payload.sig} -> ${
        data.status || payload.status
      }.`;
      message.className = "";
    } catch (error) {
      message.textContent =
        error instanceof Error ? error.message : String(error);
      message.className = "error";
    }
  }

  // submitKBPromote appends one KB rule and renders the server's answer.
  async function submitKBPromote(payload) {
    try {
      const { response, body } = await writeJSON("/v1/kb/promote", payload);
      if (!response.ok) {
        message.textContent =
          body?.message || `Request returned ${response.status}`;
        message.className = "error";
        return;
      }
      if (body?.error) {
        message.textContent = body.error.message || "Promotion refused.";
        message.className = "error";
        return;
      }
      const rule = body?.data?.rule || {};
      message.textContent = `Promoted rule ${rule.id || payload.rule} to KB.`;
      message.className = "";
    } catch (error) {
      message.textContent =
        error instanceof Error ? error.message : String(error);
      message.className = "error";
    }
  }

  // triageControl is one finding's inline write control: the triage
  // service's status vocabulary, a rationale field, and the CLI's force
  // escape. It renders only in interactive live mode.
  function triageControl(root, bundle, sig) {
    const wrapper = node("div", undefined, "triage");
    const status = node("select");
    status.setAttribute("aria-label", "Triage status");
    for (const value of triageStatuses) {
      const option = node("option", value);
      option.value = value;
      status.append(option);
    }
    const rationale = node("input");
    rationale.placeholder = "Rationale (30+ characters, or check Force)";
    rationale.setAttribute("aria-label", "Rationale");
    const force = node("input");
    force.type = "checkbox";
    force.setAttribute("aria-label", "Force");
    const button = node("button", "Triage");
    button.type = "button";
    button.addEventListener("click", () =>
      submitTriage({
        root,
        bundle,
        sig,
        status: status.value,
        rationale: rationale.value,
        force: force.checked === true,
      }),
    );
    wrapper.append(status, rationale, force, button);
    return wrapper;
  }

  // kbPromoteForm is the detail view's KB promotion control: one appended
  // learn rule with the CLI's rigor vocabulary (pointed is its default).
  function kbPromoteForm(root) {
    const wrapper = node("div", undefined, "kb-promote");
    const rule = node("input");
    rule.placeholder = "New KB rule text";
    rule.setAttribute("aria-label", "KB rule text");
    const scope = node("input");
    scope.placeholder = "Scope, e.g. src/*.go";
    scope.setAttribute("aria-label", "KB scope");
    const rigor = node("select");
    rigor.setAttribute("aria-label", "KB rigor");
    for (const value of kbRigors) {
      const option = node("option", value);
      option.value = value;
      option.selected = value === "pointed";
      rigor.append(option);
    }
    const button = node("button", "Promote to KB");
    button.type = "button";
    button.addEventListener("click", () =>
      submitKBPromote({
        root,
        rule: rule.value,
        scope: scope.value,
        rigor: rigor.value,
      }),
    );
    wrapper.append(rule, scope, rigor, button);
    return wrapper;
  }

  // jobEnabled gates every Tier B control on the same advertisement the
  // triage and KB controls read; static exports never start a job.
  function jobEnabled() {
    return interactive && mode !== "static";
  }

  // startJob posts one Tier B action and keeps its snapshot for the
  // drawer. The response is rendered honestly: a refusal envelope and a
  // transport failure both land in the status line.
  async function startJob(path, payload) {
    try {
      const { response, body } = await writeJSON(path, payload);
      if (!response.ok) {
        message.textContent =
          body?.message || `Request returned ${response.status}`;
        message.className = "error";
        return;
      }
      if (body?.error) {
        message.textContent = body.error.message || "Job refused.";
        message.className = "error";
        return;
      }
      activeJob = body?.data || null;
      renderActiveJob();
      if (!eventSource && activeJob?.id) pollJob(activeJob.id);
    } catch (error) {
      message.textContent =
        error instanceof Error ? error.message : String(error);
      message.className = "error";
    }
  }

  // pollJob is the no-stream fallback: without /v1/events the drawer
  // reads the status until it settles.
  async function pollJob(id) {
    const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
    while (
      activeJob?.id === id &&
      !["succeeded", "failed", "cancelled"].includes(activeJob.status)
    ) {
      await delay(500);
      try {
        const response = await fetch(`/v1/jobs/${encodeURIComponent(id)}`, {
          credentials: "same-origin",
          cache: "no-store",
        });
        if (!response.ok) return;
        const body = await response.json();
        if (body?.data?.id !== id) return;
        activeJob = body.data;
        renderActiveJob();
      } catch (_) {
        return;
      }
    }
  }

  // cancelActiveJob asks the server to stop the active job; the SSE
  // stream carries the terminal cancelled state back.
  async function cancelActiveJob() {
    if (!activeJob?.id) return;
    try {
      const { response, body } = await writeJSON(
        `/v1/jobs/${encodeURIComponent(activeJob.id)}/cancel`,
        {},
      );
      if (!response.ok) {
        message.textContent =
          body?.message || `Request returned ${response.status}`;
        message.className = "error";
        return;
      }
      if (body?.data) {
        activeJob = body.data;
        renderActiveJob();
      }
    } catch (error) {
      message.textContent =
        error instanceof Error ? error.message : String(error);
      message.className = "error";
    }
  }

  // renderActiveJob paints the drawer from the active snapshot: status,
  // a Cancel while queued or running, the result panel when succeeded,
  // and the refusal honestly when failed or cancelled.
  function renderActiveJob() {
    if (!jobDrawer) return;
    jobDrawer.replaceChildren();
    if (!activeJob?.id) {
      jobDrawer.append(node("p", "No job running."));
      return;
    }
    const snap = activeJob;
    jobDrawer.append(
      node("p", `Job ${snap.id} (${snap.kind}) — ${snap.status}`, "job-status"),
    );
    if (snap.status === "queued" || snap.status === "running") {
      const cancel = node("button", "Cancel");
      cancel.type = "button";
      cancel.addEventListener("click", () => cancelActiveJob());
      jobDrawer.append(cancel);
    }
    if (snap.error) {
      jobDrawer.append(
        node("p", `${snap.error.code}: ${snap.error.message}`, "error"),
      );
    }
    if (snap.result) {
      jobDrawer.append(objectPanel("Result", snap.result));
    }
  }

  // onJobEvent applies one jobs SSE frame to the drawer. A malformed
  // frame is ignored instead of breaking the status line.
  function onJobEvent(event) {
    try {
      const body = JSON.parse(event.data);
      const data = envelopeData(body);
      if (data?.id && (!activeJob || data.id === activeJob.id)) {
        activeJob = data;
        renderActiveJob();
      }
    } catch (_) {}
  }

  // jobActionsPanel is the detail view's Tier B panel: the bundle-level
  // sandbox reproduction and the dry-run post preview, plus the drawer.
  function jobActionsPanel(root, bundle) {
    const wrapper = node("div", undefined, "job-actions");
    const verify = node("button", "Reproduce in sandbox");
    verify.type = "button";
    verify.addEventListener("click", () =>
      startJob("/v1/jobs/verify", { root, bundle }),
    );
    const preview = node("button", "Preview PR post");
    preview.type = "button";
    preview.addEventListener("click", () =>
      startJob("/v1/jobs/post-preview", { root, bundle }),
    );
    wrapper.append(verify, preview);
    jobDrawer = node("div", undefined, "job-drawer");
    jobDrawer.setAttribute("aria-live", "polite");
    renderActiveJob();
    wrapper.append(jobDrawer);
    return wrapper;
  }

  function compareBuckets(root, bundle) {
    const columns = [
      { key: "sig", label: "Signature" },
      { key: "status", label: "Status" },
      { key: "severity", label: "Severity" },
      { key: "path", label: "Path" },
      { key: "line", label: "Line" },
      { key: "claim", label: "Claim" },
      { key: "reason", label: "Reason" },
    ];
    const actionable = [
      ...columns,
      {
        label: "Copy",
        action: (row, td) => {
          td.append(
            copyButton("Copy prompt", root, bundle, row.sig, "prompt"),
            " ",
            copyButton("Copy triage", root, bundle, row.sig, "triage"),
          );
        },
      },
    ];
    if (interactive && mode !== "static") {
      actionable.push({
        label: "Triage",
        action: (row, td) => {
          td.append(triageControl(root, bundle, row.sig));
        },
      });
    }
    return { columns, actionable };
  }

  // compareChoices names the baselines one comparison may name: the inbox rows
  // of the same served root, minus the bundle being compared. A cross-root row
  // is another repository's review, and the compare route refuses it.
  function compareChoices(root, bundle) {
    return inboxRows
      .filter((row) => row.root === root && row.bundle !== bundle)
      .map((row) => row.bundle);
  }

  async function renderComparePicker(container, root, bundle) {
    if (!inboxRows.length) {
      try {
        const response = await fetch("/v1/bundles", {
          credentials: "same-origin",
          cache: "no-store",
        });
        if (!response.ok)
          throw new Error(`Request returned ${response.status}`);
        const data = envelopeData(await response.json());
        inboxRows = Array.isArray(data.rows) ? data.rows : [];
      } catch (error) {
        message.textContent =
          error instanceof Error ? error.message : String(error);
        message.className = "error";
        return;
      }
    }
    const choices = compareChoices(root, bundle);
    if (!choices.length) return;
    if (!compareBaseline || !choices.includes(compareBaseline))
      compareBaseline = choices[0];
    container.append(
      dimensionPicker(
        "Compare against",
        "compare-by",
        choices,
        compareBaseline,
        (value) => {
          compareBaseline = value;
          fetchView();
        },
      ),
    );
  }

  function renderCompare(data, root, bundle) {
    const compare = data.compare || {};
    const counts = compare.counts || {};
    const buckets = compareBuckets(root, bundle);
    content.append(
      objectPanel("Baseline", {
        bundle: compare.baseline?.name,
        dir: compare.baseline?.dir,
        head_sha: compare.baseline?.head_sha,
        recorded_at: compare.baseline?.recorded_at,
        findings: compare.baseline?.findings,
      }),
    );
    content.append(
      objectPanel("Counts", {
        new: counts.new,
        persisting: counts.persisting,
        resolved: counts.resolved,
        not_reviewed: counts.not_reviewed,
      }),
    );
    const pickerHost = node("div");
    content.append(pickerHost);
    renderComparePicker(pickerHost, root, bundle);
    const sections = [
      ["New", compare.new, buckets.actionable],
      ["Persisting", compare.persisting, buckets.actionable],
      ["Resolved", compare.resolved, buckets.columns],
      ["Not reviewed", compare.not_reviewed, buckets.columns],
    ];
    for (const [caption, rows, columns] of sections) {
      content.append(table(caption, columns, Array.isArray(rows) ? rows : []));
    }
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

  function renderDetail(data, gateOnly, routeInfo) {
    const report = data.report || {};
    const gate = data.gate;
    const items = Array.isArray(report.items) ? report.items : [];
    const root = routeInfo?.root || "";
    const bundle = routeInfo?.bundle || "";
    if (!gateOnly) {
      content.append(objectPanel("Review summary", reviewSummary(report)));
      for (const item of items) {
        if (
          mode === "static" ||
          !item.reproduction ||
          !/^[a-f0-9]{40}$/.test(item.sig)
        )
          continue;
        const link = node("a", `Reproduction ${item.sig.slice(0, 8)}`);
        link.href = `${route().path}/findings/${item.sig}/reproduction`;
        link.download = "reproduction.txt";
        content.append(link);
      }
      const findingColumns = [
        { key: "sig", label: "Signature" },
        { key: "status", label: "Status" },
        { key: "severity", label: "Severity" },
        { key: "rigor", label: "Evidence" },
        { key: "path", label: "Path" },
        { key: "line", label: "Line" },
        { key: "body", label: "Finding" },
        { key: "suggestion", label: "Suggestion" },
      ];
      // Verify is bundle-scoped: the Detail view carries one bundle-level
      // reproduction button in the job-actions panel, never a per-finding
      // filter that claims a single finding was isolated.
      if (jobEnabled()) {
        findingColumns.push({
          label: "Triage",
          action: (row, td) => {
            td.append(triageControl(root, bundle, row.sig));
          },
        });
      }
      content.append(table("Findings", findingColumns, items));
      if (jobEnabled()) {
        content.append(jobActionsPanel(root, bundle));
      }
      if (interactive && mode !== "static") {
        content.append(node("h2", "Promote to KB rule"));
        content.append(kbPromoteForm(root));
      }
      content.append(
        table(
          "Evidence ledger",
          [
            { key: "path", label: "Path" },
            { key: "present", label: "Present" },
            { key: "sha256", label: "SHA-256" },
            { key: "bytes", label: "Bytes" },
          ],
          Array.isArray(data.artifacts) ? data.artifacts : [],
        ),
      );
      content.append(threadTable("Threads", data.threads));
    }
    renderGate(gate);
    if (gate === null || gate === undefined)
      message.textContent = "Gate status is unknown.";
  }

  function diffCodeCell(line) {
    const td = node("td", undefined, "diff-code");
    const spans = Array.isArray(line.spans) ? line.spans : [];
    if (!spans.length) {
      td.textContent = line.text;
      return td;
    }
    const cls = line.kind === "add" ? "diff-word-add" : "diff-word-del";
    for (const span of spans) {
      if (span.changed) {
        const mark = node("span", span.text, cls);
        td.append(mark);
      } else {
        td.append(span.text);
      }
    }
    return td;
  }

  function diffFindingRow(finding, span) {
    const tr = node("tr", undefined, "diff-finding-row");
    const td = node("td", undefined, "diff-finding");
    td.colSpan = span;
    const head = node(
      "strong",
      `${finding.severity} ${finding.status} ${finding.category} ${finding.sig || ""}`.trim(),
    );
    td.append(head);
    td.append(` ${finding.body || ""}`);
    tr.append(td);
    return tr;
  }

  function diffGapRow(count, span) {
    const div = node("div", `… ${count} unchanged lines`, "diff-gap");
    div.setAttribute("data-gap", String(count));
    return div;
  }

  function renderDiffFile(file) {
    const section = node("section", undefined, "diff-file");
    section.setAttribute("data-path", file.path || "");
    const head = node(
      "h3",
      `${file.path} ${file.lane} +${file.additions} -${file.deletions}`,
    );
    section.append(head);
    const hunks = Array.isArray(file.hunks) ? file.hunks : [];
    for (const hunk of hunks) {
      if (hunk.gap_before) section.append(diffGapRow(hunk.gap_before, 3));
      const tableEl = node("table");
      tableEl.className =
        diffMode === "split"
          ? "diff-table diff-split"
          : "diff-table diff-unified";
      const tbody = node("tbody");
      const lines = Array.isArray(hunk.lines) ? hunk.lines : [];
      if (diffMode === "split") {
        let i = 0;
        while (i < lines.length) {
          const line = lines[i];
          if (line.kind === "context") {
            const tr = node("tr", undefined, "diff-line-context");
            tr.append(node("td", String(line.old_no ?? ""), "diff-no"));
            tr.append(node("td", String(line.new_no ?? ""), "diff-no"));
            tr.append(diffCodeCell(line));
            tr.append(node("td", line.text, "diff-code"));
            tbody.append(tr);
            i++;
          } else if (
            line.kind === "del" &&
            i + 1 < lines.length &&
            lines[i + 1].kind === "add"
          ) {
            const next = lines[i + 1];
            const tr = node("tr", undefined, "diff-line-change");
            tr.append(node("td", String(line.old_no ?? ""), "diff-no"));
            tr.append(node("td", String(next.new_no ?? ""), "diff-no"));
            tr.append(diffCodeCell(line));
            tr.append(diffCodeCell(next));
            tbody.append(tr);
            if (Array.isArray(line.findings))
              for (const f of line.findings) tbody.append(diffFindingRow(f, 4));
            if (Array.isArray(next.findings))
              for (const f of next.findings) tbody.append(diffFindingRow(f, 4));
            i += 2;
          } else {
            const tr = node("tr", undefined, "diff-line-single");
            tr.append(
              node("td", String(line.old_no ?? line.new_no ?? ""), "diff-no"),
            );
            tr.append(
              node("td", String(line.new_no ?? line.old_no ?? ""), "diff-no"),
            );
            tr.append(diffCodeCell(line));
            tr.append(
              node("td", line.kind === "add" ? line.text : "", "diff-code"),
            );
            tbody.append(tr);
            if (Array.isArray(line.findings))
              for (const f of line.findings) tbody.append(diffFindingRow(f, 4));
            i++;
          }
        }
      } else {
        for (const line of lines) {
          const tr = node("tr", undefined, `diff-line-${line.kind}`);
          tr.append(node("td", String(line.old_no ?? ""), "diff-no"));
          tr.append(node("td", String(line.new_no ?? ""), "diff-no"));
          const sign = node(
            "td",
            line.kind === "add" ? "+" : line.kind === "del" ? "−" : " ",
            "diff-sign",
          );
          tr.append(sign);
          tr.append(diffCodeCell(line));
          tbody.append(tr);
          if (Array.isArray(line.findings))
            for (const f of line.findings) tbody.append(diffFindingRow(f, 4));
        }
      }
      tableEl.append(tbody);
      section.append(tableEl);
    }
    return section;
  }

  function renderDiff(data) {
    const wrap = node("div", undefined, "diff-view");
    const controls = node("div", undefined, "diff-controls");
    const unifiedBtn = node("button", "Unified");
    unifiedBtn.type = "button";
    unifiedBtn.setAttribute("aria-pressed", String(diffMode === "unified"));
    unifiedBtn.addEventListener("click", () => {
      diffMode = "unified";
      toggleDiff(data);
    });
    const splitBtn = node("button", "Side by side");
    splitBtn.type = "button";
    splitBtn.setAttribute("aria-pressed", String(diffMode === "split"));
    splitBtn.addEventListener("click", () => {
      diffMode = "split";
      toggleDiff(data);
    });
    controls.append(unifiedBtn);
    controls.append(splitBtn);
    wrap.append(controls);
    const lanes = Array.isArray(data.lanes) ? data.lanes : [];
    const laneBox = node("div", undefined, "diff-lanes");
    laneBox.append(node("h2", "Change lanes"));
    const laneTable = node("table");
    laneTable.append(node("caption", "Lanes"));
    const head = node("thead");
    const headerRow = node("tr");
    for (const label of [
      "Lane",
      "Files",
      "Additions",
      "Deletions",
      "Findings",
    ]) {
      headerRow.append(node("th", label));
    }
    head.append(headerRow);
    laneTable.append(head);
    const laneBody = node("tbody");
    for (const lane of lanes) {
      const row = node("tr", undefined, "diff-lane");
      row.setAttribute("data-lane", lane.lane || "");
      row.append(node("td", lane.lane || ""));
      row.append(node("td", String(lane.file_count ?? 0)));
      row.append(node("td", `+${lane.additions ?? 0}`));
      row.append(node("td", `-${lane.deletions ?? 0}`));
      row.append(node("td", String(lane.findings ?? 0)));
      laneBody.append(row);
    }
    laneTable.append(laneBody);
    laneBox.append(laneTable);
    wrap.append(laneBox);
    const files = Array.isArray(data.files) ? data.files : [];
    for (const file of files) wrap.append(renderDiffFile(file));
    content.append(wrap);
  }

  function toggleDiff(data) {
    for (const child of [...content.children]) {
      if ((child.className || "").includes("diff-view")) child.remove();
    }
    renderDiff(data);
  }

  function decodeHashSegment(segment) {
    try {
      return decodeURIComponent(segment || "");
    } catch (_) {
      return segment || "";
    }
  }

  function render(view, data, routeInfo) {
    content.replaceChildren();
    message.textContent = "";
    message.className = "";
    if (view === "inbox") {
      title.textContent = "Inbox";
      renderInbox(data);
    } else if (
      view === "detail" ||
      view === "gate" ||
      view === "threads" ||
      view === "compare" ||
      view === "compare-pick" ||
      view === "diff"
    ) {
      title.textContent =
        view === "gate"
          ? "Gate"
          : view === "threads"
            ? "Threads"
            : view === "diff"
              ? "Diff"
              : view === "compare" || view === "compare-pick"
                ? "Compare"
                : "Review detail";
      content.append(detailNavigation(view));
      if (view === "diff") {
        renderDiff(data);
      } else if (view === "threads") {
        content.append(threadTable("Review threads", data.threads));
      } else if (view === "compare-pick") {
        inboxRows = Array.isArray(data.rows) ? data.rows : [];
        const choices = compareChoices(
          routeInfo?.root || "",
          routeInfo?.bundle || "",
        );
        if (!choices.length) {
          message.textContent =
            "No other bundle in this root to compare against.";
        } else {
          if (!compareBaseline || !choices.includes(compareBaseline))
            compareBaseline = choices[0];
          fetchView();
        }
      } else if (view === "compare") {
        renderCompare(data, routeInfo?.root || "", routeInfo?.bundle || "");
      } else {
        renderDetail(data, view === "gate", routeInfo);
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

  // selectBaseline keeps the compare selection inside the review it was made
  // for: a hash that names a bundle and no baseline starts from nothing when the
  // active comparison moved to another root or bundle, so one root's baseline is
  // never requested from another root. An explicit hash segment always wins.
  function selectBaseline(root, bundle, explicit) {
    const scope = `${root}\u0000${bundle}`;
    if (scope !== compareScope) {
      compareBaseline = "";
      compareScope = scope;
    }
    if (explicit) compareBaseline = explicit;
    return compareBaseline;
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
      const root = encodeHashSegment(parts[1]);
      const bundle = encodeHashSegment(parts[2]);
      const decoded = {
        root: decodeHashSegment(parts[1]),
        bundle: decodeHashSegment(parts[2]),
      };
      if (parts[3] === "compare") {
        const baseline = selectBaseline(
          decoded.root,
          decoded.bundle,
          parts[4] ? decodeHashSegment(parts[4]) : "",
        );
        if (!baseline)
          return { view: "compare-pick", path: "/v1/bundles", ...decoded };
        return {
          view: "compare",
          path: `/v1/roots/${root}/bundles/${bundle}/compare?baseline=${encodeURIComponent(baseline)}`,
          ...decoded,
        };
      }
      if (parts[3] === "diff") {
        return {
          view: "diff",
          path: `/v1/roots/${root}/bundles/${bundle}/diff`,
          ...decoded,
        };
      }
      const view =
        parts[3] === "threads"
          ? "threads"
          : parts[3] === "gate"
            ? "gate"
            : "detail";
      return {
        view,
        path: `/v1/roots/${root}/bundles/${bundle}`,
        ...decoded,
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
      if (data) render(current.view, data, current);
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
      if (health.data?.interactive === true) {
        interactive = true;
        csrfToken =
          typeof health.data?.csrf === "string" ? health.data.csrf : "";
      }
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
    eventSource.addEventListener("jobs", onJobEvent);
  }

  refresh.addEventListener("click", () => {
    if (mode === "static") staticData = undefined;
    fetchView();
  });
  window.addEventListener("hashchange", fetchView);
  window.addEventListener("beforeunload", () => eventSource?.close());
  // Health arrives first so the interactive advertisement lands before the
  // first render: a write control can never flash on a read-only server.
  (async () => {
    await startLive();
    fetchView();
  })();
})();
