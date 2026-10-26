# SOC Keyword List - Plan v8 (keywords become a search index for the mobile form)

Date: 18 Oct 2026. Builds on v7 (10 categories, 40 hazard topics, 7 control tags, 1,276 keywords). The form itself is in `SOC_Form_Spec_v1.1.md`.
New files: `SOC_Topics_v8.csv`, `SOC_Keywords_v8.csv`, `SOC_Form_Picklists_v8.csv`.
Not changed: categories, the 40 hazard topics, the 7 control tags, the hierarchy (`SOC_Hierarchy_v6.csv`), the weights.

## 1. What changes compared with v7

| # | v7 | v8 | Why |
|---|---|---|---|
| 1 | The keyword list assigns the topic by itself. | The keyword list suggests up to 3 topics on the form; the reporter picks one. | A wrong guess costs one tap, not a wrong record. The pick is also the accuracy measure. |
| 2 | Control tags come from keywords. | Control tags come from the picklist answers ("what was unsafe", "what was done well"). Keyword match stays for old reports only. | The reporter states it, so no guessing. |
| 3 | PPE-only reports get a hazard inferred from the PPE item. | The PPE inference stays only as a fallback for old reports and for "no topic picked". | New reports give the work being done as a separate answer. |
| 4 | Matching: whole word for 4 letters or fewer, starts-with for longer. | Same rule, plus two additions (section 2): a root keyword also catches affixed forms of itself, and typo tolerance for unknown words. | Unseen affixed forms and typos. |
| 5 | Topics have a name only. | Topics get a plain Indonesian label, a high-potential flag and a draft Life-Saving Rule. | The reporter never sees category names; the flag drives the stop work prompt. |
| 6 | Keywords have no feedback data. | Two empty columns (`suggested_count`, `picked_count`) are filled from the pilot. | A keyword often suggested but never picked is a removal candidate. |
| 7 | Tested on the file the list was built from. | Tested the same way here, but the real test is the pilot (section 5). | In-sample results cannot show accuracy. |

## 2. Matching rule in v8

1. Lower-case the text; turn punctuation into spaces. (unchanged)
2. A keyword matches as in v7: 4 letters or fewer must be a whole word, longer ones match from the start of a word.
3. **New - root keyword catches affixed text.** If the keyword is itself a root word (for example "bocor", "buang") and a word in the text reduces to it with the Indonesian stemmer (kebocoran, dibuang), it matches. A keyword that is already an affixed form (for example "kedalaman") is not reduced, so it cannot match the common word "dalam".
4. **New - typo tolerance.** A text word that is not a real Indonesian word (not in the stemmer dictionary, and its root is not in it either, and not in a short guard list) may differ from a keyword by one letter if the keyword has 6 or more letters, or by two letters if it has 10 or more. The first letter must be the same.
5. **New - one point per word family.** Keywords in the same topic that reduce to the same root (buang, membuang, dibuang) count once, with the highest weight in the group. This removes double counting.
6. Tie-break and the hazard/control split are unchanged.

## 3. Test on the 242 reports (against the v6/v7 hazard topic)

| Variant | Same hazard topic | Notes |
|---|---|---|
| v7 rule re-implemented | 242 of 242 | Confirms the test setup reproduces v7. |
| Replace prefix matching by stem equality everywhere | 231 of 242 | Rejected: it let "kedalaman" match "dalam" (rows 72, 96, 151), "pentanahan" match "tanah" (row 148), "merusak" match "rusak" (row 233), "sarung tangan" report to Machinery (row 42). |
| Rule 3 only (root keyword catches affixed text) | 240 of 242 | Rows 5 and 37 change (see below). |
| Rule 3 + rule 4 | 239 of 242 | Rows 5, 37, 129 change. |
| **Rules 3 + 4 + 5 (v8 rule)** | **240 of 242** | Rows 37 and 129 change. No hazard topic lost; the old topic is in the top 3 for all 207 reports that had one; control tags unchanged for all 242. |
| Typo tolerance without the dictionary guard | 212 to 199 of 242 | Rejected: "sangat" matched "sengat" (Electrical), "masing" matched "maling" (Security), "memberikan" matched "membersihkan" (Housekeeping), and so on. |

The two changed reports with the v8 rule:
(Row 5, a resident sorting waste correctly, also changed under rules 3 + 4 because "buang" and "membuang" both scored in Housekeeping; rule 5 fixes it.)
- Row 37 (diesel cutting machine in the smoking shelter area): Hot Work becomes Fire & Ignition. Arguable; both fit.
- Row 129 ("Temapat makan ... tidak pada tempatnya"): Housekeeping becomes Welfare - Rest Area, Shelter & Canteen. **Better** - the typo "temapat" (tempat) is now understood.

Typos that the tolerance now catches in your reports: harnes, bariket, putung, perkir, parkira, loundry, dibuag, rumbut, dormitory.

## 4. What this does not give you (correction to my earlier suggestion)

- I said stemming would shrink the list a lot. It does not, here: only **23 of 1,276 keywords** are covered by a root keyword in the same topic (examples: kebocoran by bocor, kelelahan by lelah, ketiduran by tidur). They are marked `merge - covered by root keyword` in `SOC_Keywords_v8.csv`, not deleted. HSE decides. Keywords from the HSE original list or found in your file are protected. The pruned list (1,253 keywords) gave 239 of 242 before rule 5 was added; with rule 5 the merge changes little, because the word-family scoring already stops double counting.
- The saving is mostly future: new affixed forms no longer need to be added one by one.
- The 964 "active - unproven" keywords are still unproven. The pilot pick data will show which to keep.
- The test is in-sample. The value of stemming and typo tolerance is on unseen reports, which this file cannot show.

## 5. Pilot measures (replace in-sample testing)

For 2 to 4 weeks at one site, record:
1. Top-1 rate: how often the first suggestion was the one picked.
2. Top-3 rate: how often the pick was among the three shown.
3. "Lainnya" and "browse list" rate: how often nothing suggested fitted.
4. Time to finish a report, per path.
5. Typo tolerance false suggestions (reporter picks something else and the typed word was a real word) - add that word to the guard list.
Target before full rollout (suggested, HSE to set): top-3 rate of 85% or more.

## 6. Open decisions for HSE

1. Accept the draft Indonesian topic labels (`label_id` in the topics file).
2. Confirm the high-potential topics: Working at Height, Confined Space, Hot Work, Fire & Ignition, Process Safety / Hydrocarbon Release, Lifting Operation, Excavation Work, Electrical Work, Machinery & Energy Isolation, Line of Fire, Transportation, Marine & Working Over Water, Pressurized.
3. Confirm the draft Life-Saving Rule per topic (IOGP rules). Excavation, Process Safety, Fire & Ignition, Pressurized and Marine have no direct rule among the nine.
4. Approve or reject the 23 merge candidates.
5. Open decisions carried over from v7: categories 9 and 10, welfare categories, Natural Events, Dust, control groups C1 to C3, the weight column.
6. Form decisions are in the form spec, section 14.

## 7. Not done

- The Excel workbook (`SOC_Keyword_Source_v2.xlsx`) is not rebuilt; the CSV files and the form replace it.
- The form has not been built. The Indonesian wording of picklists is a draft and needs a test with field workers.
