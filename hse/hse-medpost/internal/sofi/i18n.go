package sofi

import (
	"net/http"
	"strings"
	"time"
)

// The /sofi pages show one language at a time, chosen with the ID / EN / 中文 switch and remembered in a
// cookie. Indonesian is the default. Every text a visitor can see is written here as an English
// key and its Indonesian text; a template or handler asks tr(lang, key).
const (
	LangEN = "en"
	LangID = "id"
	LangZH = "zh" // Simplified Chinese (lang_zh.go)

	langCookie = "sofi_lang"
	langTTL    = 365 * 24 * time.Hour
)

// Language is one entry of the switch.
type Language struct {
	Code  string
	Short string
	Label string
}

var languages = []Language{
	{LangID, "ID", "Bahasa Indonesia"},
	{LangEN, "EN", "English"},
	{LangZH, "中文", "简体中文"},
}

// messages are {English, Indonesian}. The English text is also the key.
var messages = [][2]string{
	{"Fatigue screening", "Skrining kelelahan"},
	{"No account needed.", "Tanpa akun."},
	{"1. Tick the box", "1. Centang kotak"},
	{"Your name", "Nama Anda"},
	{"Type at least 3 letters", "Ketik minimal 3 huruf"},
	{"Date of birth", "Tanggal lahir"},
	{"Pick a date", "Pilih tanggal"},
	{"Sign in", "Masuk"},
	{"Sign out", "Keluar"},
	{"No match", "Tidak ditemukan"},
	{"Language", "Bahasa"},
	{"Name search is not available right now.", "Pencarian nama sedang tidak tersedia."},
	{"Choose your name from the list.", "Pilih nama Anda dari daftar."},
	{"Enter your date of birth as dd/mm/yyyy.", "Isi tanggal lahir Anda dengan format dd/mm/yyyy."},
	{"Details not found, please check and try again.", "Data tidak ditemukan, periksa lalu coba lagi."},
	{"Check failed, please try again.", "Pemeriksaan gagal, silakan coba lagi."},
	{"The check could not load. Please check your connection and reload.", "Pemeriksaan tidak dapat dimuat. Periksa koneksi lalu muat ulang halaman."},
	{"Please complete the check first.", "Selesaikan pemeriksaan terlebih dahulu."},
	{"Please wait a moment and try again.", "Mohon tunggu sebentar lalu coba lagi."},
	{"Something went wrong.", "Terjadi kesalahan."},
	{"Too many searches, please wait a minute.", "Terlalu banyak pencarian, tunggu satu menit."},
	{"Sign-in is not available right now. Please try again later.", "Masuk sedang tidak tersedia. Silakan coba lagi nanti."},
	{"Please sign in first.", "Silakan masuk terlebih dahulu."},
	{"Enter the weeks on site and answer every item.", "Isi minggu di lokasi dan jawab semua pernyataan."},
	{"Too many submissions, please try again later.", "Terlalu banyak pengiriman, silakan coba lagi nanti."},

	{"Signed in as", "Masuk sebagai"},
	{"Name", "Nama"},
	{"Work position", "Jabatan"},
	{"Company", "Perusahaan"},
	{"Age", "Usia"},
	{"years", "tahun"},
	{"MCU review status", "Status hasil MCU"},
	{"Weeks on site", "Minggu di lokasi"},
	{"Fit", "Layak"},
	{"Fit with note", "Layak dengan catatan"},
	{"Unfit", "Tidak layak"},
	{"Temporary unfit", "Tidak layak sementara"},
	{"Need follow up", "Perlu tindak lanjut"},
	{noMcuStatus, "Belum ada hasil MCU"},
	{"How much does each statement describe how you feel? Choose 0 to 6.", "Seberapa besar setiap pernyataan menggambarkan perasaan Anda? Pilih 0 sampai 6."},
	{"Not at all", "Tidak sama sekali"},
	{"To a very large extent", "Sangat besar"},
	{"Submit", "Kirim"},
	{"Please answer every item.", "Jawab semua pernyataan."},
	{"Choose the weeks on site.", "Pilih lama di lokasi (minggu)."},
	{"More than 1 year", "Lebih dari 1 tahun"},
	{"Choose", "Pilih"},
	{"Your result", "Hasil Anda"},
	{msgAlreadyDone, "Anda sudah mengisi kuesioner untuk periode ini."},
	{"Your earlier results", "Hasil Anda sebelumnya"},
	{"Date", "Tanggal"},
	{"Total score", "Skor total"},
	{"Low", "Rendah"},
	{"Moderate", "Sedang"},
	{"High", "Tinggi"},
	{"Your fatigue is low. Keep resting well.", "Kelelahan Anda rendah. Tetap istirahat yang cukup."},
	{"Your fatigue is moderate. Rest and drink water, and tell your supervisor if it gets worse.", "Kelelahan Anda sedang. Istirahat dan minum air, lalu beri tahu atasan jika bertambah berat."},
	{"Your fatigue is high. Tell your supervisor and the Medical Team before doing high-risk work.", "Kelelahan Anda tinggi. Beri tahu atasan dan tim medis sebelum melakukan pekerjaan berisiko tinggi."},
	{"This is a screening result, not a diagnosis.", "Ini hasil skrining, bukan diagnosis."},

	{msgClosed, "Kuesioner sedang ditutup."},
	{"The questionnaire is closed", "Kuesioner ditutup"},
	{"It opens on", "Dibuka pada"},
	{"Please come back then.", "Silakan kembali pada waktu tersebut."},
	{"No opening date has been set yet.", "Belum ada jadwal pembukaan."},
	{"Questions? Ask the Medical Team.", "Ada pertanyaan? Tanyakan ke tim medis."},
	{msgPreviewOn, "Pratinjau: jawaban Anda tidak disimpan."},
}

var idText = func() map[string]string {
	m := make(map[string]string, len(messages))
	for _, p := range messages {
		m[p[0]] = p[1]
	}
	return m
}()

// publicMessages lists every translated key; the tests check each has an Indonesian text.
var publicMessages = func() []string {
	keys := make([]string, 0, len(messages))
	for _, p := range messages {
		keys = append(keys, p[0])
	}
	return keys
}()

// tr returns the text in lang. A key with no text in that language stays in English.
func tr(lang, key string) string {
	switch lang {
	case LangID:
		if t, ok := idText[key]; ok {
			return t
		}
	case LangZH:
		if t, ok := zhMessages[key]; ok {
			return t
		}
	}
	return key
}

// knownLang reports whether code is one of the languages in the switch.
func knownLang(code string) bool {
	for _, l := range languages {
		if l.Code == code {
			return true
		}
	}
	return false
}

// langOf reads the visitor's language: the cookie if it holds a known code, otherwise Indonesian.
func langOf(r *http.Request) string {
	if c, err := r.Cookie(langCookie); err == nil && knownLang(c.Value) {
		return c.Value
	}
	return LangID
}

// Text returns the word in lang. Indonesian shows the clinic's explanation of the word, with a
// capital letter; Chinese is a plain translation (lang_zh.go).
func (i Item) Text(lang string) string {
	switch lang {
	case LangID:
		return strings.ToUpper(i.ID[:1]) + i.ID[1:]
	case LangZH:
		return i.ZH
	}
	return i.EN
}

// Name returns the dimension name in lang.
func (d Dimension) Name(lang string) string {
	switch lang {
	case LangID:
		return d.ID
	case LangZH:
		return d.ZH
	}
	return d.EN
}

// switchTargets are the only pages the language switch may send the visitor back to.
var switchTargets = map[string]bool{"/sofi": true, "/sofi/start": true}

// SetLang stores the chosen language and returns to the page the switch was on.
func (a *API) SetLang(w http.ResponseWriter, r *http.Request) {
	if l := r.URL.Query().Get("l"); knownLang(l) {
		http.SetCookie(w, &http.Cookie{
			Name:     langCookie,
			Value:    l,
			Path:     "/sofi",
			HttpOnly: true,
			Secure:   a.secureCookie,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(langTTL.Seconds()),
		})
	}
	next := r.URL.Query().Get("next")
	if !switchTargets[next] {
		next = "/sofi"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}
