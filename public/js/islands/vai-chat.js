function escapeHTML(value) {
  return String(value ?? "")
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function sanitizeURL(raw) {
  try {
    const url = new URL(String(raw ?? ""), window.location.href);
    if (url.protocol === "http:" || url.protocol === "https:") {
      return url.href;
    }
  } catch {}
  return "#";
}

function renderInlineMarkdown(input) {
  let html = escapeHTML(input);

  html = html.replace(
    /\[([^\]]+)\]\(([^)\s]+)\)/g,
    (_, label, href) =>
      `<a href="${escapeHTML(sanitizeURL(href))}" target="_blank" rel="noreferrer">${escapeHTML(label)}</a>`,
  );
  html = html.replace(/`([^`]+)`/g, "<code>$1</code>");
  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  html = html.replace(/(^|[\s(])\*([^*]+)\*(?=[\s).,!?:;]|$)/g, "$1<em>$2</em>");
  html = html.replace(/(^|[\s(])_([^_]+)_(?=[\s).,!?:;]|$)/g, "$1<em>$2</em>");

  return html;
}

function renderMarkdown(markdown) {
  const source = String(markdown ?? "").replace(/\r\n/g, "\n");
  if (!source.trim()) {
    return "";
  }

  const lines = source.split("\n");
  const out = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];
    const trimmed = line.trim();

    if (!trimmed) {
      i += 1;
      continue;
    }

    if (trimmed.startsWith("```")) {
      const lang = trimmed.slice(3).trim();
      const code = [];
      i += 1;
      while (i < lines.length && !lines[i].trim().startsWith("```")) {
        code.push(lines[i]);
        i += 1;
      }
      if (i < lines.length) {
        i += 1;
      }
      out.push(
        `<pre class="md-pre"><code${lang ? ` data-lang="${escapeHTML(lang)}"` : ""}>${escapeHTML(code.join("\n"))}</code></pre>`,
      );
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      const level = heading[1].length;
      out.push(`<h${level}>${renderInlineMarkdown(heading[2].trim())}</h${level}>`);
      i += 1;
      continue;
    }

    if (/^>\s?/.test(trimmed)) {
      const quote = [];
      while (i < lines.length && /^>\s?/.test(lines[i].trim())) {
        quote.push(lines[i].trim().replace(/^>\s?/, ""));
        i += 1;
      }
      out.push(`<blockquote>${quote.map((part) => renderInlineMarkdown(part)).join("<br>")}</blockquote>`);
      continue;
    }

    if (/^[-*]\s+/.test(trimmed)) {
      const items = [];
      while (i < lines.length && /^[-*]\s+/.test(lines[i].trim())) {
        items.push(lines[i].trim().replace(/^[-*]\s+/, ""));
        i += 1;
      }
      out.push(`<ul>${items.map((item) => `<li>${renderInlineMarkdown(item)}</li>`).join("")}</ul>`);
      continue;
    }

    if (/^\d+\.\s+/.test(trimmed)) {
      const items = [];
      while (i < lines.length && /^\d+\.\s+/.test(lines[i].trim())) {
        items.push(lines[i].trim().replace(/^\d+\.\s+/, ""));
        i += 1;
      }
      out.push(`<ol>${items.map((item) => `<li>${renderInlineMarkdown(item)}</li>`).join("")}</ol>`);
      continue;
    }

    const para = [];
    while (i < lines.length) {
      const next = lines[i];
      const nextTrimmed = next.trim();
      if (
        !nextTrimmed ||
        nextTrimmed.startsWith("```") ||
        /^#{1,6}\s+/.test(next) ||
        /^>\s?/.test(nextTrimmed) ||
        /^[-*]\s+/.test(nextTrimmed) ||
        /^\d+\.\s+/.test(nextTrimmed)
      ) {
        break;
      }
      para.push(nextTrimmed);
      i += 1;
    }
    out.push(`<p>${renderInlineMarkdown(para.join(" "))}</p>`);
  }

  return out.join("");
}

function formatBytes(value) {
  const size = Number(value || 0);
  if (!Number.isFinite(size) || size <= 0) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB"];
  let idx = 0;
  let current = size;
  while (current >= 1024 && idx < units.length - 1) {
    current /= 1024;
    idx += 1;
  }
  return `${current.toFixed(current >= 10 || idx === 0 ? 0 : 1)} ${units[idx]}`;
}

function formatUSD(cents) {
  const amount = Number(cents || 0) / 100;
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
  }).format(amount);
}

function normalizeMessage(msg) {
  return {
    id: String(msg?.id || `local_${Math.random().toString(36).slice(2)}`),
    role: msg?.role === "assistant" ? "assistant" : "user",
    text: String(msg?.text || ""),
    keySource: String(msg?.keySource || ""),
    createdAt: String(msg?.createdAt || ""),
    attachments: Array.isArray(msg?.attachments) ? msg.attachments : [],
    toolTrace: Array.isArray(msg?.toolTrace) ? msg.toolTrace : [],
    pending: !!msg?.pending,
    error: !!msg?.error,
  };
}

function byokStorageKey(provider) {
  return `vai-lite:byok:${provider}`;
}

function preferredKeySourceStorageKey(conversationId) {
  return `vai-lite:key-source:${conversationId}`;
}

function loadBYOK(providerHints) {
  const out = {};
  for (const hint of providerHints || []) {
    const provider = String(hint?.provider || "").trim();
    if (!provider) {
      continue;
    }
    try {
      out[provider] = localStorage.getItem(byokStorageKey(provider)) || "";
    } catch {
      out[provider] = "";
    }
  }
  return out;
}

function saveBYOK(provider, value) {
  try {
    localStorage.setItem(byokStorageKey(provider), value);
  } catch {}
}

function preferredKeySource(props) {
  try {
    const stored = localStorage.getItem(preferredKeySourceStorageKey(props.conversationId));
    if (stored === "browser_byok" || stored === "hosted") {
      return stored;
    }
  } catch {}
  if (props.initialKeySource === "browser_byok") {
    return "browser_byok";
  }
  if (props.hasHostedProviders) {
    return "hosted";
  }
  return "browser_byok";
}

function persistPreferredKeySource(conversationId, value) {
  try {
    localStorage.setItem(preferredKeySourceStorageKey(conversationId), value);
  } catch {}
}

function createElement(html) {
  const template = document.createElement("template");
  template.innerHTML = html.trim();
  return template.content.firstElementChild;
}

function canUseBrowserBYOK(state) {
  return state.props.allowBYOKOverride || !state.props.hasHostedProviders;
}

function effectiveKeySource(state) {
  if (state.keySource === "browser_byok" && canUseBrowserBYOK(state)) {
    return "browser_byok";
  }
  if (state.props.hasHostedProviders) {
    return "hosted";
  }
  return "browser_byok";
}

function byokHeaders(state) {
  const headers = {};
  for (const hint of state.props.providerHints || []) {
    const provider = String(hint?.provider || "").trim();
    const header = String(hint?.header || "").trim();
    if (!provider || !header) {
      continue;
    }
    const value = String(state.byok[provider] || "").trim();
    if (value) {
      headers[header] = value;
    }
  }
  return headers;
}

function hasAnyBYOK(state) {
  return Object.values(byokHeaders(state)).some((value) => String(value || "").trim() !== "");
}

function buildEventSourceParser(onEvent) {
  const decoder = new TextDecoder();
  let buffer = "";

  function flushChunk(chunk) {
    const parts = chunk.split(/\n\n/);
    buffer = parts.pop() || "";
    for (const part of parts) {
      const lines = part.split("\n");
      let eventName = "";
      const dataLines = [];
      for (const rawLine of lines) {
        const line = rawLine.trimEnd();
        if (line.startsWith("event:")) {
          eventName = line.slice(6).trim();
        } else if (line.startsWith("data:")) {
          dataLines.push(line.slice(5).trim());
        }
      }
      if (!eventName) {
        continue;
      }
      let data = null;
      const raw = dataLines.join("\n");
      if (raw) {
        try {
          data = JSON.parse(raw);
        } catch {
          data = raw;
        }
      }
      onEvent({ event: eventName, data });
    }
  }

  return {
    push(chunk) {
      buffer += decoder.decode(chunk, { stream: true }).replace(/\r\n/g, "\n");
      flushChunk(buffer);
    },
    finish() {
      if (buffer.trim()) {
        flushChunk(`${buffer}\n\n`);
      }
      decoder.decode();
    },
  };
}

function renderToolTrace(trace) {
  if (!Array.isArray(trace) || trace.length === 0) {
    return "";
  }
  return `
    <details class="tool-trace">
      <summary>Tool trace (${trace.length})</summary>
      <div class="tool-trace-list">
        ${trace
          .map((step, idx) => {
            const label = step?.name || step?.tool_name || step?.type || `step ${idx + 1}`;
            return `
              <article class="tool-trace-item">
                <header>${escapeHTML(label)}</header>
                <pre>${escapeHTML(JSON.stringify(step, null, 2))}</pre>
              </article>
            `;
          })
          .join("")}
      </div>
    </details>
  `;
}

function attachmentMarkup(attachment) {
  const name = escapeHTML(attachment.filename || "Attachment");
  const url = sanitizeURL(attachment.url || "#");
  const contentType = String(attachment.contentType || "");
  const isImage = contentType.startsWith("image/");
  return `
    <figure class="message-attachment${isImage ? " message-attachment-image" : ""}">
      ${
        isImage && attachment.url
          ? `<img src="${escapeHTML(url)}" alt="${name}" loading="lazy">`
          : `<div class="attachment-fallback">${name.slice(0, 1).toUpperCase()}</div>`
      }
      <figcaption>
        <a href="${escapeHTML(url)}" target="_blank" rel="noreferrer">${name}</a>
        <span>${escapeHTML(formatBytes(attachment.sizeBytes))}</span>
      </figcaption>
    </figure>
  `;
}

function renderMessagesHTML(messages, streaming) {
  if (!messages.length) {
    return `
      <section class="chat-empty">
        <h2>Start the conversation</h2>
        <p>Use hosted workspace keys or switch to browser-local BYOK. Responses stream live and render markdown in this island.</p>
      </section>
    `;
  }

  const lastUserIndex = messages.map((m) => m.role).lastIndexOf("user");
  const lastAssistantIndex = messages.map((m) => m.role).lastIndexOf("assistant");

  return messages
    .map((message, idx) => {
      const isLastAssistant = message.role === "assistant" && idx === lastAssistantIndex && !message.pending;
      const textHTML =
        message.role === "assistant"
          ? renderMarkdown(message.text)
          : `<p>${String(message.text || "")
              .split("\n")
              .map((line) => escapeHTML(line))
              .join("<br>")}</p>`;

      return `
        <article class="message message-${escapeHTML(message.role)}${message.pending ? " message-pending" : ""}${
          message.error ? " message-error" : ""
        }">
          <header class="message-meta">
            <span class="message-role">${message.role === "assistant" ? "Assistant" : "You"}</span>
            <span class="message-source">${escapeHTML(message.keySource || "")}</span>
            ${
              message.createdAt
                ? `<time datetime="${escapeHTML(message.createdAt)}">${escapeHTML(
                    new Date(message.createdAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }),
                  )}</time>`
                : ""
            }
          </header>
          <div class="message-body markdown-body">${textHTML || (message.pending ? "<p>Thinking…</p>" : "<p>(no text content)</p>")}</div>
          ${
            Array.isArray(message.attachments) && message.attachments.length
              ? `<div class="message-attachments">${message.attachments.map(attachmentMarkup).join("")}</div>`
              : ""
          }
          ${renderToolTrace(message.toolTrace)}
          <footer class="message-actions">
            ${
              message.role === "user"
                ? `<button type="button" class="ghost-action" data-action="edit" data-message-id="${escapeHTML(message.id)}">Edit</button>`
                : ""
            }
            ${
              isLastAssistant && !streaming && lastUserIndex >= 0
                ? `<button type="button" class="ghost-action" data-action="regenerate">Regenerate</button>`
                : ""
            }
          </footer>
        </article>
      `;
    })
    .join("");
}

function topPendingAttachmentHTML(attachment, idx) {
  return `
    <article class="pending-attachment">
      <div>
        <strong>${escapeHTML(attachment.filename)}</strong>
        <span>${escapeHTML(formatBytes(attachment.sizeBytes))}</span>
      </div>
      <button type="button" class="ghost-action" data-action="remove-attachment" data-index="${idx}">Remove</button>
    </article>
  `;
}

export function mount(el, props, api) {
  const state = {
    props: props || {},
    messages: Array.isArray(props?.messages) ? props.messages.map(normalizeMessage) : [],
    pendingAttachments: [],
    byok: loadBYOK(props?.providerHints),
    keySource: preferredKeySource(props || {}),
    currentModel:
      props?.model ||
      (Array.isArray(props?.modelOptions) ? props.modelOptions.find((item) => item?.selected)?.id : "") ||
      "oai-resp/gpt-5-mini",
    draft: "",
    editMessageId: "",
    busy: false,
    status: "",
    statusTone: "neutral",
    abortController: null,
  };

  const root = createElement(`
    <div class="chat-island-shell">
      <section class="chat-control-bar">
        <div class="control-group">
          <label class="control-label" for="chat-model">Model</label>
          <select id="chat-model" class="control-input"></select>
        </div>
        <div class="control-group control-group-wide">
          <div class="control-label">Key source</div>
          <div class="segmented" data-role="key-source"></div>
        </div>
        <div class="control-group control-group-meta">
          <div class="balance-chip">Wallet <strong data-role="balance"></strong></div>
          <a class="ghost-link" data-role="settings-keys" href="#">Workspace keys</a>
          <a class="ghost-link" data-role="settings-billing" href="#">Billing</a>
        </div>
      </section>
      <section class="chat-banner" data-role="banner" hidden></section>
      <section class="byok-panel" data-role="byok-panel" hidden></section>
      <section class="message-list" data-role="messages"></section>
      <section class="pending-attachments-wrap">
        <div class="pending-attachments" data-role="pending-attachments"></div>
      </section>
      <form class="composer" data-role="composer">
        <div class="composer-meta" data-role="composer-meta"></div>
        <textarea class="composer-input" rows="4" placeholder="Send a message. Shift+Enter for a newline." data-role="composer-input"></textarea>
        <div class="composer-actions">
          <label class="btn btn-secondary btn-file">
            <input type="file" accept="image/*" multiple data-role="file-input" hidden>
            <span>Add images</span>
          </label>
          <div class="composer-spacer"></div>
          <button type="button" class="btn btn-secondary" data-role="stop-button" hidden>Stop</button>
          <button type="submit" class="btn btn-primary" data-role="send-button">Send</button>
        </div>
      </form>
    </div>
  `);

  el.innerHTML = "";
  el.appendChild(root);

  const refs = {
    banner: root.querySelector('[data-role="banner"]'),
    modelSelect: root.querySelector("#chat-model"),
    keySource: root.querySelector('[data-role="key-source"]'),
    balance: root.querySelector('[data-role="balance"]'),
    byokPanel: root.querySelector('[data-role="byok-panel"]'),
    messages: root.querySelector('[data-role="messages"]'),
    pendingAttachments: root.querySelector('[data-role="pending-attachments"]'),
    composer: root.querySelector('[data-role="composer"]'),
    composerMeta: root.querySelector('[data-role="composer-meta"]'),
    textarea: root.querySelector('[data-role="composer-input"]'),
    fileInput: root.querySelector('[data-role="file-input"]'),
    sendButton: root.querySelector('[data-role="send-button"]'),
    stopButton: root.querySelector('[data-role="stop-button"]'),
    settingsKeys: root.querySelector('[data-role="settings-keys"]'),
    settingsBilling: root.querySelector('[data-role="settings-billing"]'),
  };

  refs.settingsKeys.href = String(state.props.settingsKeysURL || "/settings/keys");
  refs.settingsBilling.href = String(state.props.settingsBillingURL || "/settings/billing");

  function setStatus(message, tone = "neutral") {
    state.status = String(message || "");
    state.statusTone = tone;
    renderBanner();
  }

  function renderBanner() {
    if (!state.status) {
      refs.banner.hidden = true;
      refs.banner.className = "chat-banner";
      refs.banner.textContent = "";
      return;
    }
    refs.banner.hidden = false;
    refs.banner.className = `chat-banner chat-banner-${state.statusTone}`;
    refs.banner.textContent = state.status;
  }

  function renderModelOptions() {
    const options = Array.isArray(state.props.modelOptions) ? state.props.modelOptions : [];
    refs.modelSelect.innerHTML = options
      .map((item) => {
        const id = String(item?.id || "");
        const label = String(item?.label || id);
        const selected = id === state.currentModel ? ' selected="selected"' : "";
        return `<option value="${escapeHTML(id)}"${selected}>${escapeHTML(label)}</option>`;
      })
      .join("");
  }

  function renderKeySourceControls() {
    const current = effectiveKeySource(state);
    const canBrowser = canUseBrowserBYOK(state);
    refs.keySource.innerHTML = `
      <button type="button" class="segment${current === "hosted" ? " segment-active" : ""}" data-key-source="hosted"${
        state.props.hasHostedProviders ? "" : ' disabled="disabled"'
      }>Hosted</button>
      <button type="button" class="segment${current === "browser_byok" ? " segment-active" : ""}" data-key-source="browser_byok"${
        canBrowser ? "" : ' disabled="disabled"'
      }>Browser BYOK</button>
    `;
  }

  function renderBYOKPanel() {
    const show = effectiveKeySource(state) === "browser_byok";
    refs.byokPanel.hidden = !show;
    if (!show) {
      refs.byokPanel.innerHTML = "";
      return;
    }
    refs.byokPanel.innerHTML = `
      <header class="panel-header">
        <div>
          <h3>Browser-local provider keys</h3>
          <p>Keys stay in this browser only. They are sent on requests from the chat island and never stored server-side.</p>
        </div>
      </header>
      <div class="byok-grid">
        ${(state.props.providerHints || [])
          .map((hint) => {
            const provider = String(hint?.provider || "");
            return `
              <label class="byok-field">
                <span>${escapeHTML(provider)}</span>
                <input type="password" data-provider-input="${escapeHTML(provider)}" value="${escapeHTML(state.byok[provider] || "")}" placeholder="Paste ${escapeHTML(provider)} key">
              </label>
            `;
          })
          .join("")}
      </div>
    `;
  }

  function renderPendingAttachments() {
    refs.pendingAttachments.innerHTML = state.pendingAttachments.map(topPendingAttachmentHTML).join("");
  }

  function renderComposerMeta() {
    const source = effectiveKeySource(state);
    const chips = [];
    chips.push(`<span class="meta-chip">${source === "hosted" ? "Hosted workspace keys" : "Browser-local BYOK"}</span>`);
    chips.push(`<span class="meta-chip">Model ${escapeHTML(state.currentModel)}</span>`);
    if (state.editMessageId) {
      chips.push(
        `<button type="button" class="ghost-action" data-action="cancel-edit">Cancel edit</button><span class="meta-chip meta-chip-warning">Editing earlier message</span>`,
      );
    }
    if (source === "hosted" && Number(state.props.currentBalanceCents || 0) <= 0) {
      chips.push(`<span class="meta-chip meta-chip-danger">Hosted credits depleted</span>`);
    }
    refs.composerMeta.innerHTML = chips.join("");
  }

  function scrollMessagesToEnd() {
    requestAnimationFrame(() => {
      refs.messages.scrollTop = refs.messages.scrollHeight;
    });
  }

  function renderMessages() {
    refs.messages.innerHTML = renderMessagesHTML(state.messages, state.busy);
    scrollMessagesToEnd();
  }

  function updateBusyControls() {
    refs.sendButton.disabled = state.busy;
    refs.modelSelect.disabled = state.busy;
    refs.fileInput.disabled = state.busy;
    refs.stopButton.hidden = !state.busy;
    refs.stopButton.disabled = !state.busy;
    refs.sendButton.textContent = state.editMessageId ? "Save and rerun" : "Send";
  }

  function renderStaticBits() {
    refs.balance.textContent = formatUSD(state.props.currentBalanceCents || 0);
    renderModelOptions();
    renderKeySourceControls();
    renderBYOKPanel();
    renderPendingAttachments();
    renderComposerMeta();
    renderMessages();
    renderBanner();
    updateBusyControls();
  }

  async function uploadFile(file) {
    const intentResp = await fetch(String(state.props.uploadIntentURL), {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": String(state.props.csrfToken || ""),
      },
      body: JSON.stringify({
        conversation_id: state.props.conversationId,
        filename: file.name,
        content_type: file.type,
        size: file.size,
      }),
    });
    if (!intentResp.ok) {
      const err = await intentResp.json().catch(() => ({ error: "Upload intent failed" }));
      throw new Error(err.error || "Upload intent failed");
    }
    const intent = await intentResp.json();

    const putHeaders = new Headers(intent.headers || {});
    putHeaders.set("Content-Type", intent.content_type || file.type || "application/octet-stream");
    const putResp = await fetch(intent.upload_url, {
      method: "PUT",
      headers: putHeaders,
      body: file,
    });
    if (!putResp.ok) {
      throw new Error(`Upload failed (${putResp.status})`);
    }

    const claimResp = await fetch(String(state.props.uploadClaimURL), {
      method: "POST",
      credentials: "same-origin",
      headers: {
        "Content-Type": "application/json",
        "X-CSRF-Token": String(state.props.csrfToken || ""),
      },
      body: JSON.stringify({
        conversation_id: state.props.conversationId,
        filename: file.name,
        content_type: file.type,
        size: file.size,
        intent_token: intent.intent_token,
      }),
    });
    if (!claimResp.ok) {
      const err = await claimResp.json().catch(() => ({ error: "Upload claim failed" }));
      throw new Error(err.error || "Upload claim failed");
    }
    return claimResp.json();
  }

  async function handleFiles(files) {
    const selected = Array.from(files || []);
    if (!selected.length) {
      return;
    }
    setStatus("Uploading attachments…", "neutral");
    try {
      for (const file of selected) {
        const attachment = await uploadFile(file);
        state.pendingAttachments.push(attachment);
        renderPendingAttachments();
      }
      setStatus(`Uploaded ${selected.length} attachment${selected.length === 1 ? "" : "s"}.`, "success");
    } catch (err) {
      setStatus(err?.message || "Attachment upload failed.", "error");
    } finally {
      refs.fileInput.value = "";
    }
  }

  function applyStreamingEvent(event, assistantMessage) {
    if (!assistantMessage || !event) {
      return;
    }

    if (event.event === "tool_call_start" || event.event === "tool_result" || event.event === "step_complete") {
      assistantMessage.toolTrace.push(event.data);
      renderMessages();
      return;
    }

    if (event.event === "run_complete" && event.data?.result?.response?.content) {
      const text = event.data.result.response.content
        .filter((block) => block?.type === "text")
        .map((block) => block.text || "")
        .join("");
      if (text) {
        assistantMessage.text = text;
      }
      assistantMessage.pending = false;
      assistantMessage.toolTrace = Array.isArray(event.data.result.steps) ? event.data.result.steps : assistantMessage.toolTrace;
      renderMessages();
      return;
    }

    if (event.event !== "stream_event") {
      return;
    }
    const inner = event.data?.event;
    if (!inner) {
      return;
    }
    if (inner.type === "content_block_delta" && inner.delta?.type === "text_delta") {
      assistantMessage.text += inner.delta.text || "";
      renderMessages();
      return;
    }
    if (inner.type === "message_delta" && inner.delta?.stop_reason) {
      assistantMessage.stopReason = inner.delta.stop_reason;
      return;
    }
  }

  function outgoingHeaders() {
    const headers = {
      "Content-Type": "application/json",
      "X-CSRF-Token": String(state.props.csrfToken || ""),
    };
    if (effectiveKeySource(state) === "browser_byok") {
      Object.assign(headers, byokHeaders(state));
    }
    return headers;
  }

  async function streamRequest(payload, assistantMessage) {
    state.abortController = new AbortController();
    const response = await fetch(String(state.props.streamURL), {
      method: "POST",
      credentials: "same-origin",
      headers: outgoingHeaders(),
      body: JSON.stringify(payload),
      signal: state.abortController.signal,
    });

    if (!response.ok || !response.body) {
      let message = `Request failed (${response.status})`;
      try {
        const body = await response.json();
        message = body.error || message;
      } catch {}
      throw new Error(message);
    }

    const parser = buildEventSourceParser((event) => applyStreamingEvent(event, assistantMessage));
    const reader = response.body.getReader();
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        break;
      }
      parser.push(value);
    }
    parser.finish();
  }

  function trimMessagesAfter(messageId) {
    const index = state.messages.findIndex((message) => message.id === messageId);
    if (index >= 0) {
      state.messages = state.messages.slice(0, index + 1);
    }
  }

  function lastUserMessage() {
    for (let i = state.messages.length - 1; i >= 0; i -= 1) {
      if (state.messages[i].role === "user") {
        return state.messages[i];
      }
    }
    return null;
  }

  async function submitComposer({ regenerate = false } = {}) {
    if (state.busy) {
      return;
    }

    const source = effectiveKeySource(state);
    if (source === "browser_byok" && !hasAnyBYOK(state)) {
      setStatus("Add at least one browser-local provider key before sending with BYOK.", "error");
      return;
    }
    if (source === "hosted" && Number(state.props.currentBalanceCents || 0) <= 0) {
      setStatus("Hosted wallet balance is depleted. Add credits or switch to browser-local BYOK.", "error");
      return;
    }

    const text = state.draft.trim();
    if (!regenerate && !state.editMessageId && !text && state.pendingAttachments.length === 0) {
      setStatus("Type a message or attach an image before sending.", "error");
      return;
    }

    state.busy = true;
    updateBusyControls();
    setStatus(source === "hosted" ? "Streaming response with hosted workspace keys…" : "Streaming response with browser-local BYOK…", "neutral");

    let assistantMessage = normalizeMessage({
      id: `pending_${Date.now()}`,
      role: "assistant",
      keySource: source,
      text: "",
      toolTrace: [],
      pending: true,
      createdAt: new Date().toISOString(),
    });

    if (state.editMessageId) {
      const existing = state.messages.find((message) => message.id === state.editMessageId);
      if (existing) {
        existing.text = text;
        trimMessagesAfter(state.editMessageId);
      }
    } else if (regenerate) {
      const lastUser = lastUserMessage();
      if (!lastUser) {
        setStatus("There is no user message to regenerate from yet.", "error");
        state.busy = false;
        updateBusyControls();
        return;
      }
      trimMessagesAfter(lastUser.id);
    } else {
      state.messages.push(
        normalizeMessage({
          id: `local_user_${Date.now()}`,
          role: "user",
          text,
          keySource: source,
          attachments: [...state.pendingAttachments],
          createdAt: new Date().toISOString(),
        }),
      );
    }

    state.messages.push(assistantMessage);
    renderMessages();

    const payload = {
      conversation_id: String(state.props.conversationId),
      model: state.currentModel,
      message: text,
      attachment_ids: state.pendingAttachments.map((attachment) => attachment.id),
      regenerate,
      edit_message_id: state.editMessageId || "",
    };

    try {
      await streamRequest(payload, assistantMessage);
      assistantMessage.pending = false;
      state.pendingAttachments = [];
      state.draft = "";
      state.editMessageId = "";
      refs.textarea.value = "";
      setStatus("Response complete.", "success");
      renderPendingAttachments();
      renderComposerMeta();
      renderMessages();
    } catch (err) {
      assistantMessage.pending = false;
      assistantMessage.error = true;
      assistantMessage.text = assistantMessage.text || `Stream failed: ${err?.message || "unknown error"}`;
      setStatus(err?.message || "Streaming request failed.", "error");
      renderMessages();
    } finally {
      state.abortController = null;
      state.busy = false;
      updateBusyControls();
    }
  }

  refs.modelSelect.addEventListener("change", (event) => {
    state.currentModel = event.target.value;
    renderComposerMeta();
  });

  refs.keySource.addEventListener("click", (event) => {
    const button = event.target.closest("[data-key-source]");
    if (!button || button.disabled) {
      return;
    }
    state.keySource = button.dataset.keySource || "hosted";
    persistPreferredKeySource(state.props.conversationId, state.keySource);
    renderKeySourceControls();
    renderBYOKPanel();
    renderComposerMeta();
  });

  refs.byokPanel.addEventListener("input", (event) => {
    const input = event.target.closest("[data-provider-input]");
    if (!input) {
      return;
    }
    const provider = input.getAttribute("data-provider-input");
    state.byok[provider] = input.value;
    saveBYOK(provider, input.value);
  });

  refs.fileInput.addEventListener("change", (event) => {
    handleFiles(event.target.files).catch((err) => {
      setStatus(err?.message || "Attachment upload failed.", "error");
    });
  });

  refs.pendingAttachments.addEventListener("click", (event) => {
    const button = event.target.closest('[data-action="remove-attachment"]');
    if (!button) {
      return;
    }
    const idx = Number(button.getAttribute("data-index"));
    if (!Number.isInteger(idx) || idx < 0 || idx >= state.pendingAttachments.length) {
      return;
    }
    state.pendingAttachments.splice(idx, 1);
    renderPendingAttachments();
  });

  refs.messages.addEventListener("click", (event) => {
    const button = event.target.closest("[data-action]");
    if (!button) {
      return;
    }
    const action = button.getAttribute("data-action");
    if (action === "edit") {
      const messageID = button.getAttribute("data-message-id");
      const message = state.messages.find((item) => item.id === messageID);
      if (!message || message.role !== "user") {
        return;
      }
      state.editMessageId = messageID;
      state.draft = message.text;
      refs.textarea.value = message.text;
      refs.textarea.focus();
      renderComposerMeta();
      setStatus("Editing earlier user message. Sending will rewrite the conversation tail.", "neutral");
      return;
    }
    if (action === "regenerate") {
      submitComposer({ regenerate: true }).catch((err) => {
        setStatus(err?.message || "Regeneration failed.", "error");
      });
      return;
    }
  });

  refs.composerMeta.addEventListener("click", (event) => {
    const button = event.target.closest('[data-action="cancel-edit"]');
    if (!button) {
      return;
    }
    state.editMessageId = "";
    state.draft = "";
    refs.textarea.value = "";
    renderComposerMeta();
    setStatus("Edit canceled.", "neutral");
  });

  refs.textarea.addEventListener("input", (event) => {
    state.draft = event.target.value;
  });

  refs.textarea.addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey) {
      event.preventDefault();
      submitComposer().catch((err) => {
        setStatus(err?.message || "Message send failed.", "error");
      });
    }
  });

  refs.composer.addEventListener("submit", (event) => {
    event.preventDefault();
    submitComposer().catch((err) => {
      setStatus(err?.message || "Message send failed.", "error");
    });
  });

  refs.stopButton.addEventListener("click", () => {
    state.abortController?.abort();
    setStatus("Stopped streaming.", "warning");
  });

  renderStaticBits();

  return {
    update(nextProps) {
      state.props = nextProps || {};
      state.currentModel = String(nextProps?.model || state.currentModel);
      state.messages = Array.isArray(nextProps?.messages) ? nextProps.messages.map(normalizeMessage) : state.messages;
      renderStaticBits();
    },
    destroy() {
      state.abortController?.abort();
      el.innerHTML = "";
    },
    onReconnect() {
      setStatus("Connection restored. Refresh the page if the conversation looks stale.", "neutral");
    },
  };
}
