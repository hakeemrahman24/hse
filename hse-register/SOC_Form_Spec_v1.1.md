# SOC Mobile Form - Spec v1.1

Date: 18 Oct 2026. Builds on keyword system v7 (10 categories, 40 hazard topics, 7 control tags, `SOC_Keywords_v7.csv`).
Status: design proposal for HSE review. Nothing here is built yet.
v1.1 changes: section 8 now uses the tested matching rule (plan v8); section 13 and 14 updated; picklists are in `SOC_Form_Picklists_v8.csv`, topic labels in `SOC_Topics_v8.csv`.

## 1. Principles

1. Mobile first: one question per screen, large tap targets, thumb-reachable buttons at the bottom.
2. Target: an unsafe report in under 60 seconds, a safe report in under 30.
3. The reporter confirms the hazard; the system only suggests. The 10 categories are never shown to the reporter - the category is derived from the chosen topic.
4. Dropdowns instead of free text wherever the answer is a known list (area, company, PPE item, what was unsafe).
5. Screen text is in Bahasa Indonesia only.
6. Do not ask for the name of the observed worker - only the company.

## 2. Start screen

One question: "Apa yang Anda lihat?" with four large buttons:

| Button | Path |
|---|---|
| Tindakan tidak aman (unsafe action) | A |
| Kondisi tidak aman (unsafe condition) | B |
| Pengamatan aman (safe observation) | C |
| Saran (suggestion) | D |

Opening the form from an area QR code skips the area question on every path.

## 3. Shared picklists

| List | Content | Owner |
|---|---|---|
| Area | Facility areas, one level (add a second level only if HSE needs it) | HSE |
| Company | Contractor and own-company list, plus "Lainnya" (free text, goes to HSE review) | HSE / contracts |
| What was unsafe (Path A) | PPE tidak ada atau salah; tanpa izin kerja (permit); prosedur atau isolasi dilewati; tanpa barikade atau rambu; bekerja sendiri tanpa pengawas atau helper; posisi atau cara kerja tidak aman; lainnya | HSE |
| PPE item (Path A follow-up) | helm, rompi, sepatu, sarung tangan, kacamata atau face shield, masker, earplug, harness, ID card, lainnya | HSE |
| What was done well (Path C) | PPE benar; izin kerja dan pengecekan lengkap; barikade atau rambu terpasang; kebersihan dan pemilahan sampah; berkendara aman; toolbox meeting atau induksi; stop work digunakan dengan benar; lainnya | HSE |

Mapping to control tags (stored automatically):

| Picklist choice | Control tag |
|---|---|
| PPE (unsafe or good) | PPE |
| Izin kerja (unsafe or good) | Permit To Work |
| Barikade / rambu | Barricade / Sign |
| Tanpa pengawas / helper | Working Without Helper / Supervision |
| Prosedur atau isolasi dilewati | Procedure & Competency |
| Toolbox meeting / induksi | Safety Meeting Induction & Checks |

## 4. Path A - Unsafe action

| # | Screen | Type | Required |
|---|---|---|---|
| 1 | Area | Dropdown (skipped if QR) | Yes |
| 2 | Company | Dropdown | Yes |
| 3 | Pekerjaan apa yang sedang dilakukan? | Free text + topic suggestions (section 8) | Yes |
| 4 | Apa yang tidak aman? | Picklist (section 3) | Yes |
| 4a | If PPE chosen: PPE item | Picklist | Yes |
| 5 | Stop work? | Yes / No | Yes |
| 5a | If Yes: who stopped it (saya sendiri / supervisor / lainnya) | Picklist | Yes |
| 5b | If No and the topic is high-potential: soft prompt "Apakah pekerjaan ini seharusnya dihentikan?" | Yes / No | No (cannot block submit) |
| 6 | Tindakan perbaikan di tempat (what I did on the spot) | Free text | Yes |
| 7 | Rekomendasi | Free text | No |
| 8 | Foto | Camera or gallery | No |

Derived: hazard topic (from screen 3 pick) -> category; control tag (from screen 4).

## 5. Path B - Unsafe condition

| # | Screen | Type | Required |
|---|---|---|---|
| 1 | Area | Dropdown (skipped if QR) | Yes |
| 2 | Pemilik area (area owner) | Dropdown (company list) | Yes |
| 3 | Kondisi apa yang tidak aman? | Free text + topic suggestions | Yes |
| 4 | Ada bahaya langsung? (immediate danger) | Yes / No | Yes |
| 5 | Sudah dibarikade atau dihentikan? | Yes / No | Yes |
| 6 | Tindakan di tempat | Free text | Yes |
| 7 | Rekomendasi | Free text | No |
| 8 | Foto | Camera or gallery | Strongly encouraged |

If immediate danger = Yes and barricade = No: show a message telling the reporter to inform the supervisor now, and flag the report "urgent" for HSE.

## 6. Path C - Safe observation

| # | Screen | Type | Required |
|---|---|---|---|
| 1 | Area | Dropdown (skipped if QR) | Yes |
| 2 | Company | Dropdown | Yes |
| 3 | Apa yang dilakukan dengan baik? | Picklist (section 3) | Yes |
| 4 | Pekerjaan apa? | Free text + topic suggestions | No (see decision 1) |
| 5 | Foto | Camera or gallery | No |

No stop work, no corrective action. Derived: control tag from screen 3; hazard topic from screen 4 if given.

## 7. Path D - Suggestion

| # | Screen | Type | Required |
|---|---|---|---|
| 1 | Area | Dropdown (skipped if QR) | Yes |
| 2 | Apa yang perlu diperbaiki? | Free text + topic suggestions | Yes |
| 3 | Usulan perbaikan | Free text | Yes |
| 4 | Foto | Camera or gallery | No |

## 8. Topic suggestion (all free-text "work" or "condition" screens)

1. Starts after 3 typed letters.
2. Text is lower-cased and punctuation turned into spaces.
3. Matching follows plan v8, section 2: keywords of 4 letters or fewer match whole words, longer ones from the start of a word; a root keyword also catches affixed forms of itself (Indonesian stemmer); a word that is not a real Indonesian word may differ by one letter (keyword of 6+ letters) or two (10+ letters), first letter the same; keywords in the same topic that share a root count once.
4. Score with the keyword weights; show the top 3 hazard topics by their Indonesian label (`label_id`), best first. Never show category numbers.
5. If nothing matches, show the full browse list. Always show two extra buttons: "Pilih dari daftar" (the 40 topics, grouped in plain words) and "Lainnya / tidak ada yang cocok".
6. Nothing is selected for the reporter. If they skip the suggestions on a required screen, the topic is stored as "not picked" and the report goes to HSE review.
7. Matching runs on the phone from a cached list file, so it works offline. Refresh the cached list whenever the keyword list changes.
8. Words wrongly "corrected" by typo tolerance (real words that look like a keyword) are added to the guard list by HSE each month.

## 9. High-potential topics (for the stop work prompt)

Soft prompt only. Starter list, to be confirmed by HSE from `SOC_Topics_v6.csv`: Working at Height, Confined Space, Machinery & Energy Isolation, Hot Work, Process Safety / Hydrocarbon Release, lifting operations, excavation, electrical work. Each of these could also carry the IOGP Life-Saving Rule it belongs to (HSE to assign).

## 10. Data stored per report

| Field | Notes |
|---|---|
| report_id, submitted_at, device_time_zone | Automatic |
| path | A, B, C or D |
| area, company (or area owner) | From dropdowns; "Lainnya" text kept |
| work_or_condition_text | Exactly what the reporter typed |
| suggestions_shown | The top 3 topics and their scores |
| topic_picked | Reporter's choice, or "browse list", or "none" |
| topic_final | Picked topic (hazard) |
| category_final | Derived from topic_final |
| control_tag | Derived from the picklist (section 3) |
| unsafe_reason, ppe_item | Path A |
| immediate_danger, barricaded | Path B |
| stop_work, stopped_by | Path A |
| corrective_action, recommendation | Paths A, B, D |
| photo | Optional |
| review_flags | e.g. "topic not picked", "Lainnya company", "urgent", "stop work not used on high-potential topic" |
| hse_review_status, hse_decision | Filled by HSE later |

The pair (suggestions_shown, topic_picked) is the free accuracy measure: how often the top suggestion was the one picked. Report it monthly.

## 11. Mobile and offline rules

- Save a draft after every screen; resume where the reporter left off.
- If there is no signal, queue the report on the phone and send it when back online; show "Tersimpan, akan dikirim".
- Compress photos on the phone before upload.
- Remember the last company and area for the next report (reporter can change it).
- Minimum text size 16 px; buttons at least 48 px high.
- Free-text fields get a short example line under the question, e.g. "Contoh: gerinda di workshop".

## 12. Privacy

- No field for the name of the observed worker. Show a hint: "Jangan tulis nama orang."
- Optional later: mask names found in free text before reports appear on dashboards.
- Photos: remind the reporter not to show faces if avoidable.

## 13. Relation to the keyword system

- The v8 plan documents the tested matching rule. The v7 PPE inference rules are kept only as a fallback for old reports and for reports where topic_picked is "none". New reports do not need them because screen 4 gives the control tag and screen 3 gives the work.
- Keywords marked "active - unproven" get real feedback from the pick data. A keyword that is often suggested but never picked is a candidate for removal.
- "Lainnya" texts are the source of new keywords each month.

## 14. Open decisions for HSE

1. Path C screen 4 (work being done): optional (shortest form, recommended) or required (every safe card gets a hazard category)?
2. Area list: one level or two (area and sub-area)?
3. Who maintains the company list, and how are new contractors added?
4. Confirm the high-potential topic list and assign Life-Saving Rules.
5. Is photo mandatory for unsafe conditions?
6. Sign-in: anonymous, shared site login, or personal login? This affects follow-up and privacy, and is not designed here.
7. The matching rule (section 8) passed the in-sample test only (240 of 242 reports keep their topic). It needs the pilot measures in plan v8, section 5, before go-live. Suggested target: top-3 rate of 85% or more.
8. Indonesian picklist and topic wording is a draft; test it with 5 to 10 field workers.
