const conversationList = document.getElementById("conversation-list");
const messagesEl = document.getElementById("messages");
const titleEl = document.getElementById("conversation-title");
const modelEl = document.getElementById("conversation-model");
const promptEl = document.getElementById("prompt");
const composer = document.getElementById("composer");
const newConversationButton = document.getElementById("new-conversation");
const modelSelect = document.getElementById("model-select");
const renameConversationButton = document.getElementById("rename-conversation");
const deleteConversationButton = document.getElementById("delete-conversation");
const trayToggleButton = document.getElementById("tray-toggle");
const trayCloseButton = document.getElementById("tray-close");
const sidebarEl = document.querySelector(".sidebar");

let currentConversationId = null;
const collapsedMessages = new Set();

function updateDocumentTitle(title) {
  const base = "smolagent";
  const next = (title || "").trim();
  document.title = next ? `${next} · ${base}` : base;
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || response.statusText);
  }
  return response.json();
}

function escapeHtml(text) {
  return text
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function messageKey(item) {
  return `${item.role}:${item.id}`;
}

function defaultCollapsed(item) {
  return item.role === "system";
}

function renderMessage(item) {
  const key = messageKey(item);
  const collapsed = collapsedMessages.has(key) || (defaultCollapsed(item) && !collapsedMessages.has(`open:${key}`));
  const preview = escapeHtml((item.content || "").split("\n")[0].slice(0, 120));
  return `
    <article class="message message-${item.role}${collapsed ? " collapsed" : ""}" data-key="${key}">
      <div class="message-head" data-key="${key}">
        <div class="role">${item.role}</div>
      </div>
      ${collapsed ? `<div class="message-preview">${preview}</div>` : ""}
      <pre class="message-body">${escapeHtml(item.content)}</pre>
    </article>
  `;
}

function renderMessages(items) {
  items = Array.isArray(items) ? items : [];
  messagesEl.innerHTML = items.map(renderMessage).join("");
  messagesEl.querySelectorAll(".message-head").forEach((header) => {
    header.addEventListener("click", () => {
      const key = header.dataset.key;
      if (!key) return;
      if (collapsedMessages.has(key)) {
        collapsedMessages.delete(key);
        collapsedMessages.add(`open:${key}`);
      } else {
        collapsedMessages.add(key);
        collapsedMessages.delete(`open:${key}`);
      }
      renderMessages(items);
    });
  });
  messagesEl.scrollTop = messagesEl.scrollHeight;
}

async function loadConversations() {
  const raw = await api("api/conversations");
  const items = Array.isArray(raw) ? raw : [];
  conversationList.innerHTML = items.map((item) => `
    <button class="conversation-item${item.id === currentConversationId ? " active" : ""}" data-id="${item.id}">
      <strong>${escapeHtml(item.title)}</strong>
      <span>${escapeHtml(item.model_id || "")}</span>
    </button>
  `).join("");
  conversationList.querySelectorAll("[data-id]").forEach((button) => {
    button.addEventListener("click", async () => {
      await openConversation(Number(button.dataset.id));
      closeTray();
    });
  });
  if (!currentConversationId && items.length > 0) {
    await openConversation(items[0].id);
  }
}

async function openConversation(id) {
  currentConversationId = id;
  const [conversation, msgs] = await Promise.all([
    api(`api/conversations/${id}`),
    api(`api/conversations/${id}/messages`),
  ]);
  titleEl.textContent = conversation.title;
  updateDocumentTitle(conversation.title);
  if (modelEl) {
    modelEl.textContent = conversation.model_id || "";
  }
  if (modelSelect && conversation.model_id) {
    modelSelect.value = conversation.model_id;
  }
  renderMessages(msgs);
  await loadConversations();
}

async function createConversation() {
  const conversation = await api("api/conversations", {
    method: "POST",
    body: JSON.stringify({
      title: "conversation",
      model_id: modelSelect ? modelSelect.value : undefined,
    }),
  });
  await loadConversations();
  await openConversation(conversation.id);
  closeTray();
}

async function renameConversation() {
  if (!currentConversationId) return;
  const nextTitle = window.prompt("New conversation title:", titleEl.textContent || "conversation");
  if (!nextTitle) return;
  const conversation = await api(`api/conversations/${currentConversationId}`, {
    method: "PATCH",
    body: JSON.stringify({ title: nextTitle }),
  });
  titleEl.textContent = conversation.title;
  await loadConversations();
}

async function deleteConversation() {
  if (!currentConversationId) return;
  if (!window.confirm("Delete this conversation and all of its messages?")) return;
  await fetch(`api/conversations/${currentConversationId}`, { method: "DELETE" });
  currentConversationId = null;
  titleEl.textContent = "";
  updateDocumentTitle("");
  if (modelEl) modelEl.textContent = modelSelect ? modelSelect.value : "";
  renderMessages([]);
  await loadConversations();
  closeTray();
}

function isSmallScreen() {
  return window.matchMedia("(max-width: 1100px)").matches;
}

function openTray() {
  if (sidebarEl && isSmallScreen()) {
    sidebarEl.classList.add("open");
  }
}

function closeTray() {
  if (sidebarEl && isSmallScreen()) {
    sidebarEl.classList.remove("open");
  }
}

composer.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!currentConversationId) {
    await createConversation();
  }
  const content = promptEl.value.trim();
  if (!content) {
    return;
  }
  const button = composer.querySelector("button");
  promptEl.value = "";
  promptEl.disabled = true;
  button.disabled = true;
  if (newConversationButton) {
    newConversationButton.disabled = true;
  }
  if (modelSelect) {
    modelSelect.disabled = true;
  }
  try {
    await api(`api/conversations/${currentConversationId}/messages`, {
      method: "POST",
      body: JSON.stringify({ content }),
    });
    await openConversation(currentConversationId);
  } catch (error) {
    promptEl.value = content;
    alert(error.message);
  } finally {
    promptEl.disabled = false;
    button.disabled = false;
    if (newConversationButton) {
      newConversationButton.disabled = false;
    }
    if (modelSelect) {
      modelSelect.disabled = false;
    }
    promptEl.focus();
  }
});

newConversationButton.addEventListener("click", () => {
  createConversation().catch((error) => alert(error.message));
});

if (renameConversationButton) {
  renameConversationButton.addEventListener("click", () => {
    renameConversation().catch((error) => alert(error.message));
  });
}

if (deleteConversationButton) {
  deleteConversationButton.addEventListener("click", () => {
    deleteConversation().catch((error) => alert(error.message));
  });
}

if (trayToggleButton) {
  trayToggleButton.addEventListener("click", () => {
    if (!sidebarEl) return;
    if (sidebarEl.classList.contains("open")) {
      closeTray();
    } else {
      openTray();
    }
  });
}

if (trayCloseButton) {
  trayCloseButton.addEventListener("click", () => {
    closeTray();
  });
}

window.addEventListener("resize", () => {
  if (!isSmallScreen() && sidebarEl) {
    sidebarEl.classList.remove("open");
  }
});

loadConversations().catch((error) => {
  updateDocumentTitle("Error");
  messagesEl.innerHTML = `<article class="message message-assistant"><div class="role">error</div><pre>${escapeHtml(error.message)}</pre></article>`;
});
