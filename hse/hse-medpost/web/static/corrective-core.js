// Pure helpers for the Corrective actions tab (corrective.js, corrective-form.js, corrective-detail.js,
// corrective-pic.js, corrective-photo.js). Nothing here touches the page, so web/corrective.test.js loads
// this file on its own. Plain function declarations on purpose: the other pages load their scripts the
// same way (no bundler), and a declaration is visible to the scripts that come after it.

// The owner's limits (plan/corrective-action-plan.md). The server enforces the same numbers.
var CA_MAX_PHOTO_BYTES = 600 * 1024;
var CA_MAX_PHOTO_SIDE = 1600;
var CA_MIN_PHOTO_SIDE = 400;
var CA_MAX_PHOTOS_PER_PHASE = 3;
var CA_PIC_MIN_CHARS = 2;

// "2026-11-01" -> "01/11/2026". Dates are stored as YYYY-MM-DD text; the clinic reads dd/mm/yyyy.
function caFormatDate(iso) {
  var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(iso || ""));
  return m ? m[3] + "/" + m[2] + "/" + m[1] : "";
}

// One word for the status chip. "Overdue" is computed by the server (not closed and past a due date) and
// wins over Open / In progress, because it is the one a reader must not miss.
function caStatusKey(action) {
  if (action.status === "Closed") return "closed";
  if (action.isOverdue) return "overdue";
  if (action.status === "In Progress") return "inprogress";
  return "open";
}

var CA_STATUS_LABELS = { closed: "Closed", overdue: "Overdue", inprogress: "In progress", open: "Open" };

function caStatusLabel(action) {
  return CA_STATUS_LABELS[caStatusKey(action)];
}

// "2 of 3 follow-ups done", or "" when the finding has none (nothing to say).
function caFollowUpSummary(action) {
  var total = action.followUpsTotal || 0;
  if (!total) return "";
  return (action.followUpsDone || 0) + " of " + total + (total === 1 ? " follow-up done" : " follow-ups done");
}

// The compact table shows no pictures. In their place: "2 / 1" = two Before photos and one After photo.
// The photos themselves are in the finding's details.
function caPhotoCounts(action) {
  return (action.beforePhotos || []).length + " / " + (action.afterPhotos || []).length;
}

function caPhotoCountsTitle(action) {
  const before = (action.beforePhotos || []).length;
  const after = (action.afterPhotos || []).length;
  const plural = (n, word) => n + " " + word + " photo" + (n === 1 ? "" : "s");
  return plural(before, "Before") + ", " + plural(after, "After");
}

// "1/2" for the small badge beside the status, or "" when the finding has no follow-ups.
function caFollowUpBadge(action) {
  const total = action.followUpsTotal || 0;
  return total ? (action.followUpsDone || 0) + "/" + total : "";
}

// The hover text for that badge: the full sentence, plus when the next open follow-up is due.
function caFollowUpTitle(action) {
  const summary = caFollowUpSummary(action);
  if (!summary) return "";
  const next = action.status !== "Closed" && action.nextFollowUpDue ? " · next due " + caFormatDate(action.nextFollowUpDue) : "";
  return summary + next;
}

// The query string for GET /api/corrective-actions. Blank filters are left out.
function caListQuery(filters) {
  var params = new URLSearchParams();
  ["procId", "status", "severity", "q", "from", "to", "limit"].forEach(function (key) {
    var value = filters[key];
    if (value !== undefined && value !== null && String(value).trim() !== "" && String(value) !== "0") {
      params.set(key, String(value).trim());
    }
  });
  return params.toString();
}

// Keyboard movement through the PIC dropdown. Down/Up wrap round at the ends, Home/End jump. -1 means
// "nothing highlighted yet". Returns the new index, or the old one for any other key.
function caNextIndex(current, count, key) {
  if (count <= 0) return -1;
  if (key === "ArrowDown") return current < 0 || current >= count - 1 ? 0 : current + 1;
  if (key === "ArrowUp") return current <= 0 ? count - 1 : current - 1;
  if (key === "Home") return 0;
  if (key === "End") return count - 1;
  return current;
}

// The size a photo is drawn at before re-encoding: the longer edge at most `side`, aspect ratio kept,
// never enlarged.
function caPhotoTarget(width, height, side) {
  if (width <= side && height <= side) return { width: width, height: height };
  var scale = side / Math.max(width, height);
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) };
}

// JPEG qualities to try, best first, then the next smaller side. Same ladder as the server's fallback.
function caQualitySteps() {
  return [0.85, 0.75, 0.65, 0.55, 0.45];
}

function caNextSide(side) {
  return Math.floor((side * 3) / 4);
}

// Whether a file can be uploaded as is: a JPEG already under the limit keeps its orientation data, so it
// is sent untouched. Anything else is re-drawn and re-encoded.
function caNeedsReduction(type, size) {
  return !(type === "image/jpeg" && size <= CA_MAX_PHOTO_BYTES);
}

function caIsAcceptedPhotoType(type) {
  return type === "image/jpeg" || type === "image/png";
}

// A PDF picked in a photo box: its first page becomes the picture. Some browsers leave the type blank, so
// the file name decides then.
function caIsPdfFile(file) {
  return file.type === "application/pdf" || (!file.type && /\.pdf$/i.test(file.name || ""));
}

// Documents attached to a finding are PDFs only, at most 10 MB each and 5 per finding; the server checks
// the same numbers.
var CA_MAX_DOCUMENT_BYTES = 10 * 1024 * 1024;
var CA_MAX_DOCUMENTS = 5;

// How many more photos of one phase a finding can take, from the server's photo arrays.
function caPhotoRoom(action, phase) {
  var list = phase === "After" ? action.afterPhotos : action.beforePhotos;
  return Math.max(0, CA_MAX_PHOTOS_PER_PHASE - (list || []).length);
}

// Why a finding cannot be closed yet, in the order the reader should fix them. Empty = it can be.
function caCloseBlockers(action) {
  var reasons = [];
  if (!(action.afterPhotos || []).length) reasons.push("Add at least one After photo.");
  var open = (action.followUps || []).filter(function (f) {
    return f.status !== "Done";
  });
  if (open.length) {
    reasons.push("Finish the open follow-up" + (open.length === 1 ? "" : "s") + ": " + open.map(function (f) { return f.description; }).join("; ") + ".");
  }
  return reasons;
}

// "3 findings · 2 open · 1 overdue" for the line above the table.
function caSummaryLine(actions) {
  var open = 0;
  var overdue = 0;
  actions.forEach(function (a) {
    if (a.status !== "Closed") open++;
    if (a.isOverdue) overdue++;
  });
  return actions.length + (actions.length === 1 ? " finding" : " findings") + " · " + open + " open · " + overdue + " overdue";
}

// The employee line under a name in the PIC dropdown: "E-001 · PT Contoh · Cook", skipping blanks.
function caEmployeeDetail(employee) {
  return [employee.employeeId, employee.company, employee.position]
    .filter(function (part) {
      return part && String(part).trim() !== "";
    })
    .join(" · ");
}

// Why a batch of picked documents cannot be uploaded, or "" when it can. Same rules as the server.
function caDocumentProblem(files, room) {
  if (files.length > room) return "You can attach " + room + " more document" + (room === 1 ? "" : "s") + ".";
  for (var i = 0; i < files.length; i++) {
    if (!caIsPdfFile(files[i])) return "“" + files[i].name + "” is not a PDF.";
    if (files[i].size > CA_MAX_DOCUMENT_BYTES) return "“" + files[i].name + "” is over 10 MB.";
  }
  return "";
}
