const conversationList = document.getElementById("conversation-list");
const messagesEl = document.getElementById("messages");
const titleEl = document.getElementById("conversation-title");
const promptEl = document.getElementById("prompt");
const composer = document.getElementById("composer");
const newConversationButton = document.getElementById("new-conversation");

let currentConversationId = null;

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

function renderMessages(items) {
  items = Array.isArray(items) ? items : [];
  messagesEl.innerHTML = items.map((item) => `
    <article class="message message-${item.role}">
      <div class="role">${item.role}</div>
      <pre>${escapeHtml(item.content)}</pre>
    </article>
  `).join("");
  messagesEl.scrollTop = messagesEl.scrollHeight;
}

async function loadConversations() {
  const raw = await api("api/conversations");
  const items = Array.isArray(raw) ? raw : [];
  conversationList.innerHTML = items.map((item) => `
    <button class="conversation-item${item.id === currentConversationId ? " active" : ""}" data-id="${item.id}">
      <strong>${escapeHtml(item.title)}</strong>
      <span>${escapeHtml(item.cwd)}</span>
    </button>
  `).join("");
  conversationList.querySelectorAll("[data-id]").forEach((button) => {
    button.addEventListener("click", () => openConversation(Number(button.dataset.id)));
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
  renderMessages(msgs);
  await loadConversations();
}

async function createConversation() {
  const conversation = await api("api/conversations", {
    method: "POST",
    body: JSON.stringify({ title: "conversation" }),
  });
  await loadConversations();
  await openConversation(conversation.id);
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
  promptEl.disabled = true;
  const button = composer.querySelector("button");
  button.disabled = true;
  try {
    await api(`api/conversations/${currentConversationId}/messages`, {
      method: "POST",
      body: JSON.stringify({ content }),
    });
    promptEl.value = "";
    await openConversation(currentConversationId);
  } catch (error) {
    alert(error.message);
  } finally {
    promptEl.disabled = false;
    button.disabled = false;
    promptEl.focus();
  }
});

newConversationButton.addEventListener("click", () => {
  createConversation().catch((error) => alert(error.message));
});

loadConversations().catch((error) => {
  messagesEl.innerHTML = `<article class="message message-assistant"><div class="role">error</div><pre>${escapeHtml(error.message)}</pre></article>`;
});
