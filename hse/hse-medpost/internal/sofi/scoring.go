package sofi

import "fmt"

// The Swedish Occupational Fatigue Inventory (SOFI) as the clinic uses it: five dimensions of five
// words each (25 words, given by the clinic with an Indonesian explanation for every word). The
// person rates how much each word describes how they feel, from 0 ("not at all") to 6 ("to a very
// large extent"), the seven-point scale the published SOFI uses. A dimension score is the sum of
// its five words (0-30); the total is the sum of all twenty-five (0-150). Higher means more fatigue.
//
// PROVISIONAL CRITERIA: the SOFI has no published cut-offs. The bands below split each range into
// equal thirds so the form can show a Low / Moderate / High reading. They are a placeholder until
// the doctor approves or replaces them: change only the constants in this block.
const (
	ItemCount         = 25
	itemsPerDimension = 5
	MaxAnswer         = 6

	// Dimension score 0-30.
	dimLowMax      = 10 // 0-10 Low
	dimModerateMax = 20 // 11-20 Moderate, 21-30 High
	// Total score 0-150.
	totalLowMax      = 50  // 0-50 Low
	totalModerateMax = 100 // 51-100 Moderate, 101-150 High
)

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
}

// Dimensions is in form order: items 1-4 belong to the first, 5-8 to the second, and so on.
var Dimensions = []Dimension{
	{"lack_of_energy", "Lack of energy", "Kurang energi"},
	{"physical_exertion", "Physical exertion", "Pengerahan tenaga fisik"},
	{"physical_discomfort", "Physical discomfort", "Ketidaknyamanan fisik"},
	{"lack_of_motivation", "Lack of motivation", "Kurang motivasi"},
	{"sleepiness", "Sleepiness", "Kantuk"},
}

// Item is one word of the inventory.
type Item struct {
	Number    int
	Dimension string
	EN        string
	ID        string
}

// Items are numbered 1-20 in the order they are shown and scored.
var Items = buildItems()

func buildItems() []Item {
	words := [][][2]string{
		{{"Overworked", "kerja berlebihan"}, {"Worn out", "energi terkuras setelah bekerja"}, {"Exhausted", "sangat lelah"}, {"Spent", "tenaga terkuras untuk hal lain"}, {"Drained", "energi banyak berkurang"}},
		{{"Sweaty", "berkeringat"}, {"Breathing heavily", "bernafas berat"}, {"Palpitations", "jantung berdebar"}, {"Warm", "tubuh terasa hangat"}, {"Out of breath", "nafas tersengal-sengal"}},
		{{"Tense muscles", "otot menegang"}, {"Stiff joints", "merasa kaku di persendian"}, {"Numbness", "merasa kram di beberapa titik tubuh"}, {"Hurting", "tubuh terasa sakit"}, {"Aching", "tubuh terasa nyeri"}},
		{{"Uninterested", "tidak tertarik keadaan sekitar"}, {"Passive", "tidak banyak bergerak"}, {"Listless", "lesu tidak bersemangat"}, {"Indifferent", "acuh tak acuh"}, {"Lack of concern", "merasa tidak peduli"}},
		{{"Sleepy", "mengantuk"}, {"Falling asleep", "ingin segera tidur"}, {"Drowsy", "pandangan kabur karena mengantuk"}, {"Yawning", "sering menguap"}, {"Lazy", "merasa malas"}},
	}
	items := make([]Item, 0, ItemCount)
	for d, group := range words {
		for _, w := range group {
			items = append(items, Item{Number: len(items) + 1, Dimension: Dimensions[d].Key, EN: w[0], ID: w[1]})
		}
	}
	return items
}

// DimensionResult is the score of one dimension.
type DimensionResult struct {
	Key   string `json:"key"`
	EN    string `json:"en"`
	ID    string `json:"id"`
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

// Score reads twenty answers (0-6, in item order). It is the only place scores are worked out:
// the server never trusts a score sent by the browser.
func Score(answers []int) (Result, error) {
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
			Key: dim.Key, EN: dim.EN, ID: dim.ID,
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
