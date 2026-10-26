// Plain-text activity summaries. Markers stay in the saved text ("- item",
// "1. item") and the homepage turns those lines into real lists.

function escapeSummary(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}

// Matches absolute http(s) URLs, or app-relative paths like "/mcu?tab=dashboard"
// (slash must start at a word boundary and be followed by a letter, so date
// fragments like "23/09/2026" are left alone).
const SUMMARY_LINK_PATTERN = /(https?:\/\/[^\s<>"]+)|(?:^|(?<=\s))(\/[a-zA-Z][^\s<>"]*)/gi;

// Same text-escaping as escapeSummary, but URL-like substrings become real
// <a> tags instead of plain escaped text — so a pasted link such as
// /mcu?tab=dashboard is clickable once rendered, with no raw-HTML pasting
// allowed (everything else still goes through escapeSummary).
function linkifySummary(value) {
  const text = String(value ?? "");
  let html = "";
  let lastIndex = 0;
  for (const match of text.matchAll(SUMMARY_LINK_PATTERN)) {
    const raw = match[1] || match[2];
    const trimmed = raw.replace(/[.,;:!)]+$/, "");
    const trailing = raw.slice(trimmed.length);
    html += escapeSummary(text.slice(lastIndex, match.index));
    const href = escapeSummary(trimmed);
    html += `<a href="${href}" class="app-summary-link">${href}</a>`;
    html += escapeSummary(trailing);
    lastIndex = match.index + raw.length;
  }
  html += escapeSummary(text.slice(lastIndex));
  return html;
}

function parseBullet(line) {
  const match = line.match(/^(\s*)([-*•])\s+(.*)$/);
  if (!match) return null;
  return { indent: match[1], marker: match[2], content: match[3] };
}

function parseNumber(line) {
  const match = line.match(/^(\s*)(\d+)[.)]\s+(.*)$/);
  if (!match) return null;
  return { indent: match[1], number: Number(match[2]), content: match[3] };
}

function stripMarker(line) {
  const bullet = parseBullet(line);
  if (bullet) return bullet.content;
  const number = parseNumber(line);
  if (number) return number.content;
  return line.replace(/^\s+/, "");
}

function collectList(lines, index, parse) {
  const items = [];
  let cursor = index;
  while (cursor < lines.length) {
    const item = parse(lines[cursor]);
    if (!item) break;
    if (item.content.trim()) items.push(`<li>${linkifySummary(item.content)}</li>`);
    cursor += 1;
  }
  return { items, index: cursor, first: parse(lines[index]) };
}

function renderActivitySummary(text) {
  const source = String(text ?? "").replace(/\r\n/g, "\n");
  if (!source.trim()) return "";
  const lines = source.split("\n");
  const html = [];
  let index = 0;
  while (index < lines.length) {
    if (lines[index].trim() === "") {
      index += 1;
      continue;
    }
    const bullet = parseBullet(lines[index]);
    if (bullet && bullet.content.trim()) {
      const list = collectList(lines, index, parseBullet);
      html.push(`<ul>${list.items.join("")}</ul>`);
      index = list.index;
      continue;
    }
    const number = parseNumber(lines[index]);
    if (number && number.content.trim()) {
      const list = collectList(lines, index, parseNumber);
      html.push(`<ol start="${list.first.number}">${list.items.join("")}</ol>`);
      index = list.index;
      continue;
    }
    const parts = [];
    while (index < lines.length && lines[index].trim() !== "") {
      const asBullet = parseBullet(lines[index]);
      const asNumber = parseNumber(lines[index]);
      if ((asBullet && asBullet.content.trim()) || (asNumber && asNumber.content.trim())) break;
      parts.push(linkifySummary(lines[index]));
      index += 1;
    }
    if (parts.length) html.push(`<p>${parts.join("<br>")}</p>`);
  }
  return html.join("");
}

function selectedLineRange(text, selStart, selEnd) {
  const from = text.lastIndexOf("\n", Math.max(0, selStart - 1)) + 1;
  let endAt = selEnd;
  if (selEnd > selStart && text[selEnd - 1] === "\n") endAt = selEnd - 1;
  const newline = text.indexOf("\n", endAt);
  const to = newline === -1 ? text.length : newline;
  return { from, to: Math.max(from, to) };
}

function listMarker(kind, previousLine) {
  if (kind !== "number") return "- ";
  const parsed = parseNumber(previousLine || "");
  return `${parsed ? parsed.number + 1 : 1}. `;
}

function lineBefore(text, lineStart) {
  if (lineStart === 0) return "";
  const prevEnd = lineStart - 1;
  const prevStart = text.lastIndexOf("\n", Math.max(0, prevEnd - 1)) + 1;
  return text.slice(prevStart, prevEnd);
}

function replaceLine(text, start, end, next) {
  const value = text.slice(0, start) + next + text.slice(end);
  const cursor = start + next.length;
  return { value, selectionStart: cursor, selectionEnd: cursor };
}

function applyListAtCursor(text, cursor, kind) {
  const { start, end, line } = lineAt(text, cursor);
  const bullet = parseBullet(line);
  const number = parseNumber(line);
  if ((kind === "bullet" && bullet) || (kind === "number" && number)) {
    const content = bullet ? bullet.content : number.content;
    const indent = bullet ? bullet.indent : number.indent;
    return replaceLine(text, start, end, indent + content);
  }
  if (bullet || number) {
    return replaceLine(text, start, end, listMarker(kind, lineBefore(text, start)) + stripMarker(line));
  }
  if (line.trim() !== "") {
    const marker = listMarker(kind, line);
    const insert = `\n${marker}`;
    const value = text.slice(0, end) + insert + text.slice(end);
    const pos = end + insert.length;
    return { value, selectionStart: pos, selectionEnd: pos };
  }
  return replaceLine(text, start, end, listMarker(kind, lineBefore(text, start)));
}

function applySummaryList(text, selStart, selEnd, kind) {
  if (kind !== "bullet" && kind !== "number") {
    return { value: text, selectionStart: selStart, selectionEnd: selEnd };
  }
  if (selStart === selEnd) return applyListAtCursor(text, selStart, kind);
  const { from, to } = selectedLineRange(text, selStart, selEnd);
  const block = text.slice(from, to);
  const lines = block.length === 0 ? [""] : block.split("\n");
  const meaningful = lines.filter((line) => line.trim() !== "");
  const allBullets = meaningful.length > 0 && meaningful.every((line) => parseBullet(line));
  const allNumbers = meaningful.length > 0 && meaningful.every((line) => parseNumber(line));
  let nextLines;
  if ((kind === "bullet" && allBullets) || (kind === "number" && allNumbers)) {
    nextLines = lines.map((line) => (line.trim() === "" ? line : stripMarker(line)));
  } else if (kind === "bullet") {
    nextLines = lines.map((line) => (line.trim() === "" ? line : `- ${stripMarker(line)}`));
  } else {
    let count = 1;
    nextLines = lines.map((line) => (line.trim() === "" ? line : `${count++}. ${stripMarker(line)}`));
  }
  if (nextLines.every((line) => line.trim() === "")) {
    nextLines = [listMarker(kind, lineBefore(text, from))];
  }
  const joined = nextLines.join("\n");
  const value = text.slice(0, from) + joined + text.slice(to);
  const cursor = from + joined.length;
  return { value, selectionStart: cursor, selectionEnd: cursor };
}

function lineAt(text, index) {
  const start = text.lastIndexOf("\n", Math.max(0, index - 1)) + 1;
  const newline = text.indexOf("\n", index);
  const end = newline === -1 ? text.length : newline;
  return { start, end, line: text.slice(start, end) };
}

function renumberSummary(text, cursor) {
  const lines = text.split("\n");
  let offset = 0;
  let lineIndex = lines.length - 1;
  let column = 0;
  for (let i = 0; i < lines.length; i++) {
    if (cursor <= offset + lines[i].length) {
      lineIndex = i;
      column = cursor - offset;
      break;
    }
    offset += lines[i].length + 1;
  }
  let start = lineIndex;
  while (start > 0 && parseNumber(lines[start - 1])) start -= 1;
  if (!parseNumber(lines[start])) return { value: text, cursor };
  let count = 1;
  for (let i = start; i < lines.length; i++) {
    const parsed = parseNumber(lines[i]);
    if (!parsed) break;
    const updated = `${parsed.indent}${count}. ${parsed.content}`;
    if (i === lineIndex) {
      const oldMarker = lines[i].length - parsed.content.length;
      const newMarker = updated.length - parsed.content.length;
      column += newMarker - oldMarker;
      if (column < 0) column = 0;
      if (column > updated.length) column = updated.length;
    }
    lines[i] = updated;
    count += 1;
  }
  let nextCursor = column;
  for (let i = 0; i < lineIndex; i++) nextCursor += lines[i].length + 1;
  return { value: lines.join("\n"), cursor: nextCursor };
}

function continueSummaryList(text, selStart, selEnd) {
  if (selStart !== selEnd) return null;
  const { start, end, line } = lineAt(text, selStart);
  const bullet = parseBullet(line);
  const number = parseNumber(line);
  if (!bullet && !number) return null;
  const content = bullet ? bullet.content : number.content;
  if (!content.trim()) {
    let cutEnd = end;
    if (start === 0 && text[end] === "\n") cutEnd = end + 1;
    const value = text.slice(0, start) + text.slice(cutEnd);
    return { value, selectionStart: start, selectionEnd: start };
  }
  const indent = bullet ? bullet.indent : number.indent;
  const marker = bullet ? `${indent}${bullet.marker} ` : `${indent}${number.number + 1}. `;
  let value = text.slice(0, selStart) + "\n" + marker + text.slice(selStart);
  let cursor = selStart + 1 + marker.length;
  if (number) {
    const renumbered = renumberSummary(value, cursor);
    value = renumbered.value;
    cursor = renumbered.cursor;
  }
  return { value, selectionStart: cursor, selectionEnd: cursor };
}

function summaryLineText(node) {
  return (node.innerText || node.textContent || "").replace(/\u00a0/g, " ").replace(/\s*\n\s*/g, " ").trim();
}

function serializeInline(node) {
  let text = "";
  for (const child of node.childNodes) {
    if (child.nodeType === Node.TEXT_NODE) {
      text += child.textContent.replace(/\u00a0/g, " ");
      continue;
    }
    if (child.nodeName === "BR") {
      text += "\n";
      continue;
    }
    if (child.nodeName === "UL" || child.nodeName === "OL") {
      const list = serializeList(child);
      if (list) text += (text && !text.endsWith("\n") ? "\n" : "") + list + "\n";
      continue;
    }
    text += serializeInline(child);
  }
  return text;
}

function serializeList(list) {
  let count = Number(list.getAttribute("start")) || 1;
  const ordered = list.nodeName === "OL";
  const lines = [];
  for (const item of list.querySelectorAll(":scope > li")) {
    const itemLines = serializeInline(item)
      .split("\n")
      .map((line) => line.trim())
      .filter((line) => line !== "");
    for (const line of itemLines) {
      lines.push(ordered ? `${count}. ${line}` : `- ${line}`);
      if (ordered) count += 1;
    }
  }
  return lines.join("\n");
}

function serializeSummary(root) {
  const parts = [];
  for (const node of root.childNodes) {
    if (node.nodeType === Node.TEXT_NODE) {
      const text = node.textContent.replace(/\u00a0/g, " ").trim();
      if (text) parts.push(text);
      continue;
    }
    if (node.nodeName === "UL" || node.nodeName === "OL") {
      const list = serializeList(node);
      if (list) parts.push(list);
      continue;
    }
    const text = serializeInline(node)
      .split("\n")
      .map((line) => line.trim())
      .filter((line) => line !== "")
      .join("\n");
    if (text) parts.push(text);
  }
  return parts.join("\n").trim();
}

function summaryCaretBlock(node, root) {
  let current = node?.nodeType === Node.ELEMENT_NODE ? node : node?.parentElement;
  let item = null;
  let list = null;
  while (current && current !== root) {
    if (!item && current.nodeName === "LI") item = current;
    if (!list && (current.nodeName === "UL" || current.nodeName === "OL")) list = current;
    current = current.parentElement;
  }
  return { item, list };
}

function placeSummaryCaret(el) {
  const range = document.createRange();
  range.selectNodeContents(el);
  range.collapse(true);
  const selection = window.getSelection();
  selection.removeAllRanges();
  selection.addRange(range);
}

function insertSummaryParagraph(editor) {
  const selection = window.getSelection();
  const range = selection.getRangeAt(0);
  const paragraph = document.createElement("p");
  paragraph.innerHTML = "<br>";
  const top = summaryTopBlock(range.startContainer, editor);
  if (!top) {
    editor.appendChild(paragraph);
    placeSummaryCaret(paragraph);
    return;
  }
  const after = range.cloneRange();
  after.selectNodeContents(top);
  after.setStart(range.endContainer, range.endOffset);
  const rest = after.extractContents();
  if ((rest.textContent || "").trim()) {
    paragraph.textContent = "";
    paragraph.appendChild(rest);
  }
  if (top.nodeType === Node.ELEMENT_NODE && !summaryLineText(top)) top.innerHTML = "<br>";
  top.after(paragraph);
  placeSummaryCaret(paragraph);
}

function insertSummaryItem(item) {
  const next = document.createElement("li");
  const selection = window.getSelection();
  if (selection?.rangeCount && item.contains(selection.anchorNode)) {
    const range = selection.getRangeAt(0);
    const after = range.cloneRange();
    after.selectNodeContents(item);
    after.setStart(range.endContainer, range.endOffset);
    const rest = after.extractContents();
    if ((rest.textContent || "").replace(/\u00a0/g, " ").trim()) next.appendChild(rest);
    else next.innerHTML = "<br>";
  } else {
    next.innerHTML = "<br>";
  }
  if (!summaryLineText(item)) item.innerHTML = "<br>";
  item.after(next);
  placeSummaryCaret(next);
}

function leaveSummaryList(item, list) {
  const paragraph = document.createElement("p");
  paragraph.innerHTML = "<br>";
  const following = [];
  let sibling = item.nextElementSibling;
  while (sibling) {
    following.push(sibling);
    sibling = sibling.nextElementSibling;
  }
  item.remove();
  if (!list.querySelector("li")) list.replaceWith(paragraph);
  else list.after(paragraph);
  if (following.length) {
    const rest = document.createElement(list.nodeName.toLowerCase());
    for (const nextItem of following) rest.appendChild(nextItem);
    paragraph.after(rest);
  }
  placeSummaryCaret(paragraph);
}

function summaryTopBlock(node, editor) {
  if (!node || node === editor || !editor.contains(node)) return null;
  let current = node.nodeType === Node.ELEMENT_NODE ? node : node.parentNode;
  if (current === editor) return node.nodeType === Node.TEXT_NODE ? node : null;
  while (current && current.parentNode !== editor) current = current.parentNode;
  return current;
}

function summaryLinesOf(node) {
  if (node.nodeType === Node.TEXT_NODE) {
    const text = node.textContent.replace(/\u00a0/g, " ").trim();
    return text ? [text] : [];
  }
  const lines = [];
  let current = "";
  const flush = () => {
    const text = current.replace(/\u00a0/g, " ").trim();
    if (text) lines.push(text);
    current = "";
  };
  const walk = (el) => {
    for (const child of el.childNodes) {
      if (child.nodeType === Node.TEXT_NODE) current += child.textContent;
      else if (child.nodeName === "BR") flush();
      else if (child.nodeName === "UL" || child.nodeName === "OL" || child.nodeName === "LI") {
        flush();
        walk(child);
      } else walk(child);
    }
  };
  walk(node);
  flush();
  return lines;
}

function selectedSummaryBlocks(editor) {
  const blocks = [...editor.childNodes].filter((node) => {
    if (node.nodeType === Node.TEXT_NODE) return node.textContent.trim() !== "";
    return node.nodeType === Node.ELEMENT_NODE;
  });
  const selection = window.getSelection();
  if (!blocks.length || !selection?.rangeCount || !editor.contains(selection.anchorNode)) return blocks;
  const range = selection.getRangeAt(0);
  const picked = blocks.filter((block) => range.intersectsNode(block));
  return picked.length ? picked : blocks;
}

function summaryParagraph(line) {
  const paragraph = document.createElement("p");
  if (line) paragraph.textContent = line;
  else paragraph.innerHTML = "<br>";
  return paragraph;
}

function startSummaryList(editor, kind) {
  const listName = kind === "number" ? "OL" : "UL";
  let blocks = selectedSummaryBlocks(editor);
  if (!blocks.length) {
    const paragraph = summaryParagraph("");
    editor.appendChild(paragraph);
    blocks = [paragraph];
  }
  const lines = blocks.flatMap((block) => {
    const found = summaryLinesOf(block);
    return found.length ? found : [""];
  });
  const already = blocks.every((block) => block.nodeName === listName);
  const first = blocks[0];
  if (already) {
    const paragraphs = lines.map(summaryParagraph);
    const fragment = document.createDocumentFragment();
    for (const paragraph of paragraphs) fragment.appendChild(paragraph);
    first.replaceWith(fragment);
    for (const block of blocks.slice(1)) block.remove();
    placeSummaryCaret(paragraphs[0] || editor);
    return;
  }
  const list = document.createElement(listName.toLowerCase());
  for (const line of lines) {
    const item = document.createElement("li");
    if (line) item.textContent = line;
    else item.innerHTML = "<br>";
    list.appendChild(item);
  }
  first.replaceWith(list);
  for (const block of blocks.slice(1)) block.remove();
  placeSummaryCaret(list.querySelector("li") || list);
}

function sentenceCaseSummaryCaret(editor) {
  const selection = window.getSelection();
  if (!selection?.rangeCount || !selection.isCollapsed) return;
  const node = selection.anchorNode;
  if (!node || node.nodeType !== Node.TEXT_NODE || !editor.contains(node)) return;
  const next = toSentenceCase(node.textContent);
  if (next === node.textContent) return;
  const offset = Math.min(selection.anchorOffset, next.length);
  node.textContent = next;
  const range = document.createRange();
  range.setStart(node, offset);
  range.collapse(true);
  selection.removeAllRanges();
  selection.addRange(range);
}

function mountSummaryEditor(textarea) {
  if (!textarea || textarea.dataset.summaryEditor) return;
  textarea.dataset.summaryEditor = "1";
  textarea.hidden = true;
  textarea.tabIndex = -1;

  const shell = document.createElement("div");
  shell.className = "summary-doc";
  const toolbar = document.createElement("div");
  toolbar.className = "summary-toolbar";
  toolbar.innerHTML =
    '<button type="button" data-summary-list="bullet" title="Bullets" aria-label="Bullets">•</button>' +
    '<button type="button" data-summary-list="number" title="Numbering" aria-label="Numbering">1.</button>';
  const editor = document.createElement("div");
  editor.className = "summary-doc-body";
  editor.contentEditable = "true";
  editor.setAttribute("role", "textbox");
  editor.setAttribute("aria-multiline", "true");
  editor.setAttribute("aria-label", "Summary");
  editor.innerHTML = renderActivitySummary(textarea.value) || "<p><br></p>";
  shell.append(toolbar, editor);
  textarea.insertAdjacentElement("afterend", shell);

  const sync = () => {
    textarea.value = serializeSummary(editor);
  };
  const paintToolbar = () => {
    const bullet = toolbar.querySelector('[data-summary-list="bullet"]');
    const number = toolbar.querySelector('[data-summary-list="number"]');
    const selection = window.getSelection();
    const active = document.activeElement === editor && selection?.rangeCount && editor.contains(selection.anchorNode);
    const { list } = active ? summaryCaretBlock(selection.anchorNode, editor) : { list: null };
    bullet.setAttribute("aria-pressed", String(Boolean(active && list?.nodeName === "UL")));
    number.setAttribute("aria-pressed", String(Boolean(active && list?.nodeName === "OL")));
  };

  textarea.addEventListener("focus", () => editor.focus());
  editor.addEventListener("input", () => {
    sentenceCaseSummaryCaret(editor);
    sync();
  });
  editor.addEventListener("paste", (event) => {
    event.preventDefault();
    const text = event.clipboardData?.getData("text/plain") || "";
    document.execCommand("insertText", false, text);
  });
  editor.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" || event.shiftKey || event.isComposing) return;
    if (event.ctrlKey || event.metaKey || event.altKey) return;
    event.preventDefault();
    const selection = window.getSelection();
    if (!selection?.isCollapsed) return;
    const { item, list } = summaryCaretBlock(selection.anchorNode, editor);
    if (item && list && !summaryLineText(item)) leaveSummaryList(item, list);
    else if (item && list) insertSummaryItem(item);
    else insertSummaryParagraph(editor);
    sync();
  });
  toolbar.addEventListener("mousedown", (event) => {
    if (event.target.closest("button")) event.preventDefault();
  });
  toolbar.addEventListener("click", (event) => {
    const button = event.target.closest("[data-summary-list]");
    if (!button) return;
    event.preventDefault();
    editor.focus();
    startSummaryList(editor, button.dataset.summaryList);
    sync();
    paintToolbar();
  });
  document.addEventListener("selectionchange", paintToolbar);
  textarea.closest("form")?.addEventListener("submit", sync, true);
  textarea.closest("form")?.addEventListener("reset", () => {
    setTimeout(() => {
      editor.innerHTML = renderActivitySummary(textarea.value) || "<p><br></p>";
    }, 0);
  });
}
