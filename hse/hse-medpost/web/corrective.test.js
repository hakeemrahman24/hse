const { readFileSync } = require("fs");
const { test } = require("node:test");
const assert = require("node:assert/strict");
const vm = require("vm");

// corrective-core.js is plain browser script with no page access, so it loads into an empty context.
function load() {
  const context = { URLSearchParams };
  vm.createContext(context);
  vm.runInContext(readFileSync("web/static/corrective-core.js", "utf8"), context);
  return context;
}

const ca = load();

test("dates are shown dd/mm/yyyy and anything else shows nothing", () => {
  assert.equal(ca.caFormatDate("2026-11-01"), "01/11/2026");
  assert.equal(ca.caFormatDate(""), "");
  assert.equal(ca.caFormatDate("01/11/2026"), "");
  assert.equal(ca.caFormatDate(undefined), "");
});

test("overdue wins over open and in progress, closed wins over everything", () => {
  assert.equal(ca.caStatusKey({ status: "Open" }), "open");
  assert.equal(ca.caStatusKey({ status: "In Progress" }), "inprogress");
  assert.equal(ca.caStatusKey({ status: "In Progress", isOverdue: true }), "overdue");
  assert.equal(ca.caStatusKey({ status: "Closed", isOverdue: true }), "closed");
  assert.equal(ca.caStatusLabel({ status: "In Progress" }), "In progress");
});

test("the follow-up summary is blank without follow-ups and singular for one", () => {
  assert.equal(ca.caFollowUpSummary({ followUpsTotal: 0 }), "");
  assert.equal(ca.caFollowUpSummary({}), "");
  assert.equal(ca.caFollowUpSummary({ followUpsDone: 1, followUpsTotal: 1 }), "1 of 1 follow-up done");
  assert.equal(ca.caFollowUpSummary({ followUpsDone: 2, followUpsTotal: 3 }), "2 of 3 follow-ups done");
});

test("the compact list shows photo counts, not pictures", () => {
  assert.equal(ca.caPhotoCounts({ beforePhotos: [1, 2], afterPhotos: [1] }), "2 / 1");
  assert.equal(ca.caPhotoCounts({}), "0 / 0");
  assert.equal(ca.caPhotoCountsTitle({ beforePhotos: [1, 2], afterPhotos: [1] }), "2 Before photos, 1 After photo");
  assert.equal(ca.caPhotoCountsTitle({ beforePhotos: [1], afterPhotos: [] }), "1 Before photo, 0 After photos");
});

test("the follow-up badge is short and its hover text says the rest", () => {
  assert.equal(ca.caFollowUpBadge({ followUpsTotal: 0 }), "");
  assert.equal(ca.caFollowUpBadge({ followUpsDone: 1, followUpsTotal: 2 }), "1/2");
  assert.equal(ca.caFollowUpTitle({ followUpsTotal: 0 }), "");
  assert.equal(
    ca.caFollowUpTitle({ status: "In Progress", followUpsDone: 1, followUpsTotal: 2, nextFollowUpDue: "2026-11-15" }),
    "1 of 2 follow-ups done · next due 15/11/2026",
  );
  assert.equal(ca.caFollowUpTitle({ status: "Closed", followUpsDone: 2, followUpsTotal: 2, nextFollowUpDue: "2026-11-15" }), "2 of 2 follow-ups done");
});

test("the list query leaves blank filters out and trims the rest", () => {
  assert.equal(ca.caListQuery({}), "");
  assert.equal(ca.caListQuery({ procId: 0, status: "", q: "  " }), "");
  assert.equal(ca.caListQuery({ procId: 3, status: "Overdue", q: " floor ", limit: 100 }), "procId=3&status=Overdue&q=floor&limit=100");
});

test("the dropdown moves with the arrow keys, wrapping at the ends", () => {
  assert.equal(ca.caNextIndex(-1, 3, "ArrowDown"), 0);
  assert.equal(ca.caNextIndex(0, 3, "ArrowDown"), 1);
  assert.equal(ca.caNextIndex(2, 3, "ArrowDown"), 0);
  assert.equal(ca.caNextIndex(-1, 3, "ArrowUp"), 2);
  assert.equal(ca.caNextIndex(0, 3, "ArrowUp"), 2);
  assert.equal(ca.caNextIndex(2, 3, "ArrowUp"), 1);
  assert.equal(ca.caNextIndex(1, 3, "Home"), 0);
  assert.equal(ca.caNextIndex(0, 3, "End"), 2);
  assert.equal(ca.caNextIndex(1, 3, "x"), 1);
  assert.equal(ca.caNextIndex(0, 0, "ArrowDown"), -1);
});

test("a photo is scaled down to the longer edge and never enlarged", () => {
  assert.deepEqual({ ...ca.caPhotoTarget(4000, 3000, 1600) }, { width: 1600, height: 1200 });
  assert.deepEqual({ ...ca.caPhotoTarget(3000, 4000, 1600) }, { width: 1200, height: 1600 });
  assert.deepEqual({ ...ca.caPhotoTarget(800, 600, 1600) }, { width: 800, height: 600 });
  assert.deepEqual({ ...ca.caPhotoTarget(5000, 2, 100) }, { width: 100, height: 1 });
});

test("the shrink ladder matches the server: quality falls, then the side drops by a quarter", () => {
  assert.deepEqual([...ca.caQualitySteps()], [0.85, 0.75, 0.65, 0.55, 0.45]);
  assert.equal(ca.caNextSide(1600), 1200);
  assert.equal(ca.caNextSide(1200), 900);
});

test("only a JPEG already under 600 KB is uploaded untouched", () => {
  assert.equal(ca.caNeedsReduction("image/jpeg", 500 * 1024), false);
  assert.equal(ca.caNeedsReduction("image/jpeg", 600 * 1024), false);
  assert.equal(ca.caNeedsReduction("image/jpeg", 600 * 1024 + 1), true);
  assert.equal(ca.caNeedsReduction("image/png", 10), true);
  assert.equal(ca.caIsAcceptedPhotoType("image/jpeg"), true);
  assert.equal(ca.caIsAcceptedPhotoType("image/png"), true);
  assert.equal(ca.caIsAcceptedPhotoType("image/gif"), false);
  assert.equal(ca.caIsAcceptedPhotoType("application/pdf"), false);
  assert.equal(ca.caIsPdfFile({ type: "application/pdf", name: "a" }), true);
  assert.equal(ca.caIsPdfFile({ type: "", name: "Report.PDF" }), true);
  assert.equal(ca.caIsPdfFile({ type: "image/png", name: "a.pdf" }), false);
});

test("photo room is three minus what the finding already has", () => {
  assert.equal(ca.caPhotoRoom({ beforePhotos: [], afterPhotos: [] }, "Before"), 3);
  assert.equal(ca.caPhotoRoom({ beforePhotos: [1, 2], afterPhotos: [] }, "Before"), 1);
  assert.equal(ca.caPhotoRoom({ beforePhotos: [1, 2, 3], afterPhotos: [1] }, "Before"), 0);
  assert.equal(ca.caPhotoRoom({ beforePhotos: [1, 2, 3], afterPhotos: [1] }, "After"), 2);
  assert.equal(ca.caPhotoRoom({}, "After"), 3);
});

test("close blockers name the missing After photo and every open follow-up", () => {
  assert.deepEqual([...ca.caCloseBlockers({ afterPhotos: [{}], followUps: [] })], []);
  assert.deepEqual([...ca.caCloseBlockers({ afterPhotos: [], followUps: [] })], ["Add at least one After photo."]);
  const blockers = [
    ...ca.caCloseBlockers({
      afterPhotos: [],
      followUps: [
        { description: "Buy shelves", status: "Open" },
        { description: "Install shelves", status: "Open" },
        { description: "Paint", status: "Done" },
      ],
    }),
  ];
  assert.equal(blockers.length, 2);
  assert.match(blockers[1], /follow-ups: Buy shelves; Install shelves\./);
  assert.match([...ca.caCloseBlockers({ afterPhotos: [{}], followUps: [{ description: "Paint", status: "Open" }] })][0], /follow-up: Paint\./);
});

test("the summary line counts open and overdue findings", () => {
  assert.equal(ca.caSummaryLine([]), "0 findings · 0 open · 0 overdue");
  assert.equal(
    ca.caSummaryLine([
      { status: "Open", isOverdue: true },
      { status: "Closed" },
      { status: "In Progress" },
    ]),
    "3 findings · 2 open · 1 overdue",
  );
  assert.equal(ca.caSummaryLine([{ status: "Open" }]), "1 finding · 1 open · 0 overdue");
});

test("the employee detail line skips blank parts", () => {
  assert.equal(ca.caEmployeeDetail({ employeeId: "E-001", company: "PT Contoh", position: "Cook" }), "E-001 · PT Contoh · Cook");
  assert.equal(ca.caEmployeeDetail({ employeeId: "", company: "PT Contoh", position: " " }), "PT Contoh");
  assert.equal(ca.caEmployeeDetail({}), "");
});

test("documents must be PDFs under 10 MB and fit the room left", () => {
  const pdf = (size) => ({ type: "application/pdf", name: "a.pdf", size });
  assert.equal(ca.caDocumentProblem([pdf(100)], 5), "");
  assert.match(ca.caDocumentProblem([pdf(1), pdf(1)], 1), /1 more document\./);
  assert.match(ca.caDocumentProblem([{ type: "image/png", name: "x.png", size: 1 }], 5), /not a PDF/);
  assert.match(ca.caDocumentProblem([pdf(10 * 1024 * 1024 + 1)], 5), /over 10 MB/);
});
