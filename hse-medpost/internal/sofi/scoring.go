package sofi

import "fmt"

// The Swedish Occupational Fatigue Inventory (SOFI) as the clinic uses it: the 20-item Indonesian
// version (Suparman et al., 2022, Indonesian Journal of Community and Occupational Medicine 2(2),
// adapted from Ahsberg et al., 1997): five dimensions of four items each, every item an Indonesian
// sentence supplied by the clinic. The person rates how much each item describes how they feel,
// from 0 ("not at all") to 6 ("to a very large extent"). A dimension score is the sum of its four
// items (0-24); the total is the sum of all twenty (0-120). Higher means more fatigue.
//
// Until 2026-11-09 the form had 25 words (five per dimension). Answers saved then still have 25
// values; Score drops the five words the 20-item version left out (legacyDropped) and scores the
// rest, so old records stay on the same scale as new ones.
//
// PROVISIONAL CRITERIA: the SOFI has no published cut-offs. The bands below split each range into
// equal thirds so the form can show a Low / Moderate / High reading. They are a placeholder until
// the doctor approves or replaces them: change only the constants in this block.
const (
	ItemCount         = 20
	itemsPerDimension = 4
	MaxAnswer         = 6

	// Dimension score 0-24.
	dimLowMax      = 8  // 0-8 Low
	dimModerateMax = 16 // 9-16 Moderate, 17-24 High
	// Total score 0-120.
	totalLowMax      = 40 // 0-40 Low
	totalModerateMax = 80 // 41-80 Moderate, 81-120 High

	// The earlier 25-word form (five items per dimension), still found in saved answers.
	legacyItemCount = 25
)

// legacyDropped are the positions (0-based, in the old 25-word order) of the five words the
// 20-item version does not have: Exhausted, Warm, Hurting, Lack of concern and Lazy.
var legacyDropped = map[int]bool{2: true, 8: true, 13: true, 19: true, 24: true}

const (
	LevelLow      = "Low"
	LevelModerate = "Moderate"
	LevelHigh     = "High"
)

// Dimension is one of the five SOFI scales.
type Dimension struct {
	Key string
	EN  string
	ID  string
	ZH  string
}

// Dimensions is in form order: items 1-4 belong to the first, 5-8 to the second, and so on.
var Dimensions = []Dimension{
	{"lack_of_energy", "Lack of energy", "Kekurangan energi pada pekerjaan", "精力不足"},
	{"physical_exertion", "Physical exertion", "Mengerahkan tenaga fisik", "体力消耗"},
	{"physical_discomfort", "Physical discomfort", "Ketidaknyamanan fisik", "身体不适"},
	{"lack_of_motivation", "Lack of motivation", "Kurangnya motivasi pada pekerjaan", "缺乏动力"},
	{"sleepiness", "Sleepiness", "Perasaan mengantuk", "嗜睡"},
}

// Item is one word of the inventory.
type Item struct {
	Number    int
	Dimension string
	EN        string
	ID        string
	ZH        string
}

// Items are numbered 1-20 in the order they are shown and scored.
var Items = buildItems()

func buildItems() []Item {
	// English, then the clinic's Indonesian item exactly as in its table (the source text; the
	// English and Chinese translate these sentences). The SOFI word each one stands for is in the
	// comment: the order is the old 25-word order with five words left out (see legacyDropped), so
	// old answers still line up.
	words := [][][2]string{
		{ // Lack of energy
			{"Having too many activities to do", "Terlalu banyak aktifitas yang dikerjakan"},                                                                           // Overworked
			{"Feeling out of energy while doing activities", "Merasa kehabisan tenaga saat melakukan aktifitas"},                                                       // Worn out
			{"Feeling so exhausted that I have no energy left for other activities", "Merasa sangat kelelahan hingga kehabisan tenaga untuk melakukan aktifitas lain"}, // Spent
			{"Energy being drained while doing activities", "Terkurasnya energi saat melakukan aktifitas"},                                                             // Drained
		},
		{ // Physical exertion
			{"Sweating", "Berkeringat"}, // Sweaty
			{"Breathing heavily to the point of gasping", "Bernafas dengan berat hingga terengah-engah"}, // Breathing heavily
			{"Feeling heart palpitations", "Merasa berdebar-debar"},                                      // Palpitations
			{"Experiencing shortness of breath", "Mengalami sesak nafas"},                                // Out of breath
		},
		{ // Physical discomfort
			{"Feeling tense muscles", "Otot merasa tegang"},                             // Tense muscles
			{"Joints feeling stiff", "Sendi mengalami rasa kaku"},                       // Stiff joints
			{"Numbness in parts of the body", "Rasa Mati Rasa/ Baal pada bagian tubuh"}, // Numbness
			{"Aching pain in parts of the body", "Rasa Nyeri pada bagian tubuh"},        // Aching
		},
		{ // Lack of motivation
			{"Not interested in work activities", "Tidak tertarik pada aktifitas pekerjaan"},                                                                                                        // Uninterested
			{"Being passive or not active in work activities", "Pasif/ tidak giat dalam aktifitas pekerjaan"},                                                                                       // Passive
			{"Lacking concentration while doing activities", "Kurang Berkonsentrasi saat melakukan aktifitas"},                                                                                      // Listless
			{"Not interested in, not caring about, and indifferent to the results and quality of the work", "Tidak tertarik/ tidak peduli/ sikap masa bodoh dengan hasil/ kualitas mutu pekerjaan"}, // Indifferent
		},
		{ // Sleepiness
			{"Feeling sleepy or tired while doing activities", "Mengantuk/ Letih saat melakukan aktifitas"},         // Sleepy
			{"Feeling like falling asleep while doing activities", "Merasa akan tertidur saat melakukan aktifitas"}, // Falling asleep
			{"Feeling that work activities make me sleepy", "Merasa aktifitas pekerjaan membuat mengantuk"},         // Drowsy
			{"Yawning while doing activities", "Menguap saat melakukan aktifitas"},                                  // Yawning
		},
	}
	items := make([]Item, 0, ItemCount)
	for d, group := range words {
		for _, w := range group {
			items = append(items, Item{Number: len(items) + 1, Dimension: Dimensions[d].Key, EN: w[0], ID: w[1], ZH: zhItems[len(items)]})
		}
	}
	return items
}

// DimensionResult is the score of one dimension.
type DimensionResult struct {
	Key   string `json:"key"`
	EN    string `json:"en"`
	ID    string `json:"id"`
	ZH    string `json:"zh"`
	Score int    `json:"score"`
	Max   int    `json:"max"`
	Level string `json:"level"`
}

// Result is the full reading of one completed inventory.
type Result struct {
	Dimensions []DimensionResult `json:"dimensions"`
	Total      int               `json:"total"`
	TotalMax   int               `json:"totalMax"`
	Level      string            `json:"level"`
}

// Score reads twenty answers (0-6, in item order), or the twenty-five of the earlier form. It is
// the only place scores are worked out: the server never trusts a score sent by the browser.
func Score(answers []int) (Result, error) {
	if len(answers) == legacyItemCount {
		answers = withoutLegacyDropped(answers)
	}
	if len(answers) != ItemCount {
		return Result{}, fmt.Errorf("expected %d answers, got %d", ItemCount, len(answers))
	}
	res := Result{TotalMax: ItemCount * MaxAnswer}
	for d, dim := range Dimensions {
		sum := 0
		for _, a := range answers[d*itemsPerDimension : (d+1)*itemsPerDimension] {
			if a < 0 || a > MaxAnswer {
				return Result{}, fmt.Errorf("answer %d is outside 0-%d", a, MaxAnswer)
			}
			sum += a
		}
		res.Dimensions = append(res.Dimensions, DimensionResult{
			Key: dim.Key, EN: dim.EN, ID: dim.ID, ZH: dim.ZH,
			Score: sum, Max: itemsPerDimension * MaxAnswer, Level: band(sum, dimLowMax, dimModerateMax),
		})
		res.Total += sum
	}
	res.Level = band(res.Total, totalLowMax, totalModerateMax)
	return res, nil
}

func band(score, lowMax, moderateMax int) string {
	switch {
	case score <= lowMax:
		return LevelLow
	case score <= moderateMax:
		return LevelModerate
	default:
		return LevelHigh
	}
}

// withoutLegacyDropped keeps the twenty answers of the earlier 25-word form that the 20-item
// version still asks, in the same order.
func withoutLegacyDropped(answers []int) []int {
	kept := make([]int, 0, ItemCount)
	for i, a := range answers {
		if !legacyDropped[i] {
			kept = append(kept, a)
		}
	}
	return kept
}
