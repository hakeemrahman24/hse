package entrymcu

import (
	"sort"
	"strings"
)

// Dashboard counts for the admin review screen and the summary PDF.
// Rows are folded into maps while scanning — the PDF path does not keep
// the checkup list in memory (Orange Pi 3B, 2 GB RAM).
type dashSrc struct {
	Company  string
	Criteria string
	Result   string
	Status   string
	HC       string
	// Superseded marks a checkup that is not its patient's newest in range.
	Superseded bool

	// Case detail, filled only by the on-screen report. The PDF path leaves
	// Name empty so it never keeps per-person rows in memory.
	CheckUpID      string
	Name           string
	ReviewDate     string
	ResultNotes    string
	Recommendation string
}

// dashCase is one checkup behind a non-fit result row, so a reviewer can expand
// the row and read why. Text is the recommendation for results that need a
// follow-up and the result notes for unfit ones (the same split the review
// form enforces in validateResultDependentFields).
type dashCase struct {
	Company    string `json:"company"`
	CheckUpID  string `json:"checkUpId"`
	Name       string `json:"name"`
	ReviewDate string `json:"reviewDate"`
	Text       string `json:"text"`
}

// dashCaseText returns the note worth showing for a result, or ok=false for
// results that are not expandable (Fit, Fit with note, Waiver, none).
func dashCaseText(result, notes, recommendation string) (string, bool) {
	switch result {
	case "NeedFollowUp", "IncompleteResult":
		return strings.TrimSpace(recommendation), true
	case "TemporaryUnfit", "Unfit":
		return strings.TrimSpace(notes), true
	}
	return "", false
}

// dashSupersededSQL is a SELECT column (plus its bind args): 1 when the same
// patient has a newer record inside the same review-date range (later
// ReviewDate, ties broken by McuRecordID, the order the Employee tab uses for
// "latest"). Each person then counts once, by their newest checkup. The range
// is applied to the newer record too, so a past month's dashboard does not
// change when the patient is seen again later.
func dashSupersededSQL(from, to string) (string, []any) {
	q := `EXISTS (SELECT 1 FROM McuRecord n
		WHERE n.FK_PatientID = m.FK_PatientID
		  AND (n.ReviewDate > m.ReviewDate OR (n.ReviewDate = m.ReviewDate AND n.McuRecordID > m.McuRecordID))`
	var args []any
	if from != "" {
		q += " AND n.ReviewDate >= ?"
		args = append(args, from)
	}
	if to != "" {
		q += " AND n.ReviewDate <= ?"
		args = append(args, to)
	}
	return q + ")", args
}

type dashCount struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
	Color string `json:"color"`
}

type dashCompanyBlock struct {
	Company            string      `json:"company"`
	Total              int         `json:"total"`
	PurposeRows        []dashCount `json:"purposeRows"`
	ResultCriteriaRows []dashCount `json:"resultCriteriaRows"`
	ClosedNoResult     int         `json:"closedNoResultCriteriaCount"`
}

// DashboardAnalytics is the three-chart cohort from the legacy WebApp
// dashboard: check-up status, purpose (criteria), and result criteria for
// closed visits. Close + Need follow up is omitted from every chart.
// A checkup already stamped "Renewed by …" is omitted too, so a renewed
// visit is not counted again next to its replacement.
type DashboardAnalytics struct {
	Total                           int                    `json:"total"`
	ClosedCount                     int                    `json:"closedCount"`
	ClosedNeedFollowUpExcludedCount int                    `json:"closedNeedFollowUpExcludedCount"`
	RenewedExcludedCount            int                    `json:"renewedExcludedCount"`
	SupersededExcludedCount         int                    `json:"supersededExcludedCount"`
	ClosedNoResultCriteriaCount     int                    `json:"closedNoResultCriteriaCount"`
	StatusRows                      []dashCount            `json:"statusRows"`
	PurposeRows                     []dashCount            `json:"purposeRows"`
	ResultCriteriaRows              []dashCount            `json:"resultCriteriaRows"`
	ResultCriteriaTotal             int                    `json:"resultCriteriaTotal"`
	Companies                       []string               `json:"companies"`
	CompanyPivot                    []dashCompanyBlock     `json:"companyPivot"`
	CompanyStatusRows               map[string][]dashCount `json:"companyStatusRows"`
	// ResultCases lists the checkups behind each expandable result key.
	ResultCases map[string][]dashCase `json:"resultCases"`
}

type dashAccum struct {
	statusCounts    map[string]int
	purposeCounts   map[string]int
	resultCounts    map[string]int
	resultCases     map[string][]dashCase
	companyTotals   map[string]int
	companyStatus   map[string]map[string]int
	companyPurpose  map[string]map[string]int
	companyResult   map[string]map[string]int
	companyNoResult map[string]int
	total           int
	closed          int
	nfuExcluded     int
	renewedExcluded int
	supersededSkips int
	closedNoResult  int
}

func newDashAccum() *dashAccum {
	return &dashAccum{
		statusCounts:    map[string]int{},
		purposeCounts:   map[string]int{},
		resultCounts:    map[string]int{},
		resultCases:     map[string][]dashCase{},
		companyTotals:   map[string]int{},
		companyStatus:   map[string]map[string]int{},
		companyPurpose:  map[string]map[string]int{},
		companyResult:   map[string]map[string]int{},
		companyNoResult: map[string]int{},
	}
}

func (d *dashAccum) add(row dashSrc) {
	// A person counts once, by their newest checkup in range.
	if row.Superseded {
		d.supersededSkips++
		return
	}
	if isDashClosed(row.Status) && isDashNeedFollowUp(row.Result) {
		d.nfuExcluded++
		return
	}
	if strings.HasPrefix(strings.TrimSpace(row.HC), "Renewed by ") {
		d.renewedExcluded++
		return
	}

	d.total++
	company := dashCompanyKey(row.Company)
	d.companyTotals[company]++
	ensureDashMap(d.companyStatus, company)
	ensureDashMap(d.companyPurpose, company)
	ensureDashMap(d.companyResult, company)

	status := dashStatusKey(row.Status)
	d.statusCounts[status]++
	d.companyStatus[company][status]++

	purpose := dashPurposeLabel(row.Criteria)
	if purpose != "" && purpose != "_" {
		d.purposeCounts[purpose]++
		d.companyPurpose[company][purpose]++
	}

	// Every checkup with a result counts, open or closed, so Need follow up
	// and Temporary unfit sit beside the closed outcomes in one chart.
	result := strings.TrimSpace(row.Result)
	hasResult := result != "" && result != "_"
	if hasResult {
		d.resultCounts[result]++
		d.companyResult[company][result]++
		if row.Name != "" {
			if text, ok := dashCaseText(result, row.ResultNotes, row.Recommendation); ok {
				d.resultCases[result] = append(d.resultCases[result], dashCase{
					Company: company, CheckUpID: row.CheckUpID, Name: row.Name,
					ReviewDate: row.ReviewDate, Text: text,
				})
			}
		}
	}
	if !isDashClosed(row.Status) {
		return
	}
	d.closed++
	if !hasResult {
		d.closedNoResult++
		d.companyNoResult[company]++
	}
}

func (d *dashAccum) finish() DashboardAnalytics {
	companies := make([]string, 0, len(d.companyTotals))
	for name := range d.companyTotals {
		companies = append(companies, name)
	}
	sort.Slice(companies, func(i, j int) bool {
		if d.companyTotals[companies[i]] != d.companyTotals[companies[j]] {
			return d.companyTotals[companies[i]] > d.companyTotals[companies[j]]
		}
		return companies[i] < companies[j]
	})

	pivot := make([]dashCompanyBlock, 0, len(companies))
	statusByCompany := make(map[string][]dashCount, len(companies))
	for _, company := range companies {
		pivot = append(pivot, dashCompanyBlock{
			Company:            company,
			Total:              d.companyTotals[company],
			PurposeRows:        purposeRowsFrom(d.companyPurpose[company]),
			ResultCriteriaRows: resultRowsFrom(d.companyResult[company]),
			ClosedNoResult:     d.companyNoResult[company],
		})
		statusByCompany[company] = statusRowsFrom(d.companyStatus[company])
	}

	results := resultRowsFrom(d.resultCounts)
	rcTotal := 0
	for _, row := range results {
		rcTotal += row.Count
	}
	return DashboardAnalytics{
		Total:                           d.total,
		ClosedCount:                     d.closed,
		ClosedNeedFollowUpExcludedCount: d.nfuExcluded,
		RenewedExcludedCount:            d.renewedExcluded,
		SupersededExcludedCount:         d.supersededSkips,
		ClosedNoResultCriteriaCount:     d.closedNoResult,
		StatusRows:                      statusRowsFrom(d.statusCounts),
		PurposeRows:                     purposeRowsFrom(d.purposeCounts),
		ResultCriteriaRows:              results,
		ResultCriteriaTotal:             rcTotal,
		Companies:                       nzStrings(companies),
		CompanyPivot:                    pivot,
		CompanyStatusRows:               statusByCompany,
		ResultCases:                     d.resultCases,
	}
}

func buildDashboard(rows []dashSrc) DashboardAnalytics {
	acc := newDashAccum()
	for _, row := range rows {
		acc.add(row)
	}
	return acc.finish()
}

func ensureDashMap(m map[string]map[string]int, key string) {
	if m[key] == nil {
		m[key] = map[string]int{}
	}
}

func isDashNeedFollowUp(rc string) bool {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(rc), " ", "")) == "needfollowup"
}

func isDashClosed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "close", "closed":
		return true
	default:
		return false
	}
}

func dashStatusKey(status string) string {
	t := strings.TrimSpace(status)
	if t == "" || t == "_" {
		return "Other"
	}
	switch strings.ToLower(t) {
	case "open":
		return "Open"
	case "close", "closed":
		return "Close"
	case "archived / exit":
		return "Archived / Exit"
	default:
		return "Other"
	}
}

// dashPurposeLabel shows the retired Fit for Task and Follow Up purposes, and
// the current ConsultationResult one, as a single "Consultation result" so
// old and new records chart together. Stored names are left as they are.
func dashPurposeLabel(criteria string) string {
	t := strings.TrimSpace(criteria)
	switch normalizeKey(t) {
	case "fitfortask", "fitfortasktype", "fitforwork", "followup", "consultationresult":
		return "Consultation result"
	default:
		return t
	}
}

func dashCompanyKey(name string) string {
	t := strings.TrimSpace(name)
	if t == "" || t == "_" {
		return "(No company)"
	}
	return t
}

func dashResultLabel(key string) string {
	switch key {
	case "Fit":
		return "Fit"
	case "FitWithNote":
		return "Fit with note"
	case "NeedFollowUp":
		return "Need follow up"
	case "TemporaryUnfit":
		return "Temporary unfit"
	case "Unfit":
		return "Unfit"
	case "Waiver":
		return "Waiver"
	default:
		if key == "" {
			return "(none)"
		}
		return key
	}
}

func dashStatusColor(key string) string {
	switch key {
	case "Open":
		return "#1a73e8"
	case "Close":
		return "#2ea043"
	case "Archived / Exit":
		return "#8b949e"
	default:
		return "#6b7280"
	}
}

func dashResultColor(key string) string {
	switch key {
	case "Fit":
		return "#2ea043"
	case "FitWithNote":
		return "#00897b"
	case "NeedFollowUp":
		return "#d29922"
	case "TemporaryUnfit":
		return "#e67c27"
	case "Unfit":
		return "#da3633"
	case "Waiver":
		return "#8e24aa"
	default:
		return "#545d6a"
	}
}

var dashPurposePalette = []string{
	"#1a73e8", "#00897b", "#d29922", "#8e24aa", "#e67c27",
	"#6366f1", "#0d9488", "#ca8a04", "#64748b", "#be185d",
}

func dashPurposeColor(i int) string {
	if len(dashPurposePalette) == 0 {
		return "#545d6a"
	}
	return dashPurposePalette[i%len(dashPurposePalette)]
}

func statusRowsFrom(counts map[string]int) []dashCount {
	order := []string{"Open", "Close", "Archived / Exit", "Other"}
	out := make([]dashCount, 0, len(order))
	seen := map[string]bool{}
	for _, key := range order {
		if counts[key] == 0 {
			continue
		}
		seen[key] = true
		out = append(out, dashCount{Key: key, Label: key, Count: counts[key], Color: dashStatusColor(key)})
	}
	extras := make([]string, 0)
	for key, n := range counts {
		if n == 0 || seen[key] {
			continue
		}
		extras = append(extras, key)
	}
	sort.Strings(extras)
	for _, key := range extras {
		out = append(out, dashCount{Key: key, Label: key, Count: counts[key], Color: dashStatusColor(key)})
	}
	return out
}

func purposeRowsFrom(counts map[string]int) []dashCount {
	keys := make([]string, 0, len(counts))
	for key, n := range counts {
		if n > 0 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]dashCount, 0, len(keys))
	for i, key := range keys {
		out = append(out, dashCount{Key: key, Label: key, Count: counts[key], Color: dashPurposeColor(i)})
	}
	return out
}

func resultRowsFrom(counts map[string]int) []dashCount {
	order := []string{"Fit", "FitWithNote", "NeedFollowUp", "TemporaryUnfit", "Unfit", "Waiver"}
	out := make([]dashCount, 0, len(order))
	seen := map[string]bool{}
	for _, key := range order {
		if counts[key] == 0 {
			continue
		}
		seen[key] = true
		out = append(out, dashCount{Key: key, Label: dashResultLabel(key), Count: counts[key], Color: dashResultColor(key)})
	}
	extras := make([]string, 0)
	for key, n := range counts {
		if n == 0 || seen[key] {
			continue
		}
		extras = append(extras, key)
	}
	sort.Strings(extras)
	for _, key := range extras {
		out = append(out, dashCount{Key: key, Label: dashResultLabel(key), Count: counts[key], Color: dashResultColor(key)})
	}
	return out
}

func nzStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// filterDashboard narrows charts and the company pivot to one company.
// Exclusion footnotes stay on the full date range.
func filterDashboard(full DashboardAnalytics, company string) DashboardAnalytics {
	company = strings.TrimSpace(company)
	if company == "" {
		return full
	}
	var block *dashCompanyBlock
	for i := range full.CompanyPivot {
		if full.CompanyPivot[i].Company == company {
			block = &full.CompanyPivot[i]
			break
		}
	}
	if block == nil {
		return DashboardAnalytics{
			ClosedNeedFollowUpExcludedCount: full.ClosedNeedFollowUpExcludedCount,
			RenewedExcludedCount:            full.RenewedExcludedCount,
			SupersededExcludedCount:         full.SupersededExcludedCount,
			StatusRows:                      []dashCount{},
			PurposeRows:                     []dashCount{},
			ResultCriteriaRows:              []dashCount{},
			Companies:                       []string{},
			CompanyPivot:                    []dashCompanyBlock{},
			CompanyStatusRows:               map[string][]dashCount{},
		}
	}
	status := full.CompanyStatusRows[company]
	if status == nil {
		status = []dashCount{}
	}
	closed := 0
	for _, row := range status {
		if isDashClosed(row.Key) {
			closed += row.Count
		}
	}
	rcTotal := 0
	for _, row := range block.ResultCriteriaRows {
		rcTotal += row.Count
	}
	noResult := block.ClosedNoResult
	return DashboardAnalytics{
		Total:                           block.Total,
		ClosedCount:                     closed,
		ClosedNeedFollowUpExcludedCount: full.ClosedNeedFollowUpExcludedCount,
		RenewedExcludedCount:            full.RenewedExcludedCount,
		SupersededExcludedCount:         full.SupersededExcludedCount,
		ClosedNoResultCriteriaCount:     noResult,
		StatusRows:                      status,
		PurposeRows:                     block.PurposeRows,
		ResultCriteriaRows:              block.ResultCriteriaRows,
		ResultCriteriaTotal:             rcTotal,
		Companies:                       []string{company},
		CompanyPivot:                    []dashCompanyBlock{*block},
		CompanyStatusRows:               map[string][]dashCount{company: status},
	}
}
