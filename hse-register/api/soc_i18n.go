package main

import (
	"fmt"
	"html"
	"html/template"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Language of the public SOC pages: Bahasa Indonesia (the default) and English.
//
// The catalog is keyed by the English text, so templates and code read naturally:
//
//	{{tr .Lang "Date of finding"}}     tr(lang, "Choose a category.")
//
// English is the source and needs no entry. A text with no translation shows in English, never as
// a blank or a key. A test (soc_i18n_test.go) fails when a text used in a template or in a tr call
// has no Indonesian entry, so a new sentence cannot ship half translated.
//
// The names that come from the lookup tables (topics, hazards, areas, companies) are data and are
// not translated here; only the hazard classes, control classes and categories have entries.

const (
	langID = "id"
	langEN = "en"

	cookieSOCLang = "soc_lang"
	socLangMaxAge = 365 * 24 * time.Hour
)

// socLang describes one choice of the switcher.
type socLang struct {
	Code  string // value of the cookie and of ?l=
	Label string // written in its own language, so a visitor can always find theirs
	Short string // what the switcher shows in the narrow top bar
	HTML  string // value of <html lang>, also read by the scripts
}

var socLangs = []socLang{
	{langID, "Bahasa Indonesia", "ID", "id"},
	{langEN, "English", "EN", "en"},
}

func validLang(code string) bool {
	for _, l := range socLangs {
		if l.Code == code {
			return true
		}
	}
	return false
}

func htmlLang(code string) string {
	for _, l := range socLangs {
		if l.Code == code {
			return l.HTML
		}
	}
	return "id"
}

// langOf picks the language of a request: the visitor's choice (cookie), else the browser's
// Accept-Language, else Indonesian.
func langOf(r *http.Request) string {
	if c, err := r.Cookie(cookieSOCLang); err == nil && validLang(c.Value) {
		return c.Value
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case tag == "id" || strings.HasPrefix(tag, "id-") || tag == "ms" || strings.HasPrefix(tag, "ms-"):
			return langID
		case tag == "en" || strings.HasPrefix(tag, "en-"):
			return langEN
		}
	}
	return langID
}

// tr translates an English text. English, and any text without an entry, comes back unchanged.
func tr(lang, en string) string {
	t, ok := socText[en]
	if !ok {
		return en
	}
	switch lang {
	case langID:
		if t.id != "" {
			return t.id
		}
	}
	return en
}

// trf is tr for a text with a number in it, e.g. "a post can have at most %d photos".
func trf(lang, en string, args ...any) string { return fmt.Sprintf(tr(lang, en), args...) }

// headingHTML writes a page heading with its last word in the accent colour, as the pages always
// did. The text is escaped.
func headingHTML(lang, en string) template.HTML {
	s := tr(lang, en)
	i := strings.LastIndex(s, " ")
	if i < 0 {
		return template.HTML(html.EscapeString(s))
	}
	return template.HTML(html.EscapeString(s[:i]) + " <span>" + html.EscapeString(s[i+1:]) + "</span>")
}

// safeNext is where /soc/lang may send the visitor back to: only a SOC page of this site.
var safeNext = regexp.MustCompile(`^/soc(/[A-Za-z0-9_./-]*)?$`)

// setLanguage stores the visitor's choice and returns them to the page they came from.
func (h *socHandlers) setLanguage(w http.ResponseWriter, r *http.Request) {
	if code := r.URL.Query().Get("l"); validLang(code) {
		http.SetCookie(w, &http.Cookie{
			Name: cookieSOCLang, Value: code, Path: "/soc", MaxAge: int(socLangMaxAge.Seconds()),
			HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteLaxMode,
		})
	}
	next := r.URL.Query().Get("next")
	if !safeNext.MatchString(next) || strings.Contains(next, "..") {
		next = "/soc"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

type socTranslation struct{ id string }

// socText is the catalog: English text -> Indonesian.
var socText = func() map[string]socTranslation {
	rows := [][2]string{
		// ---- shell ----
		{"Sign out", "Keluar"},
		{"Developed by Minhajul Mubarok, dr. Arif Rahman Hakim.", "Dikembangkan oleh Minhajul Mubarok, dr. Arif Rahman Hakim."},
		{"Safety Observation Communication. Internal HSE record-keeping only.", "Komunikasi Observasi Keselamatan. Hanya untuk pencatatan internal HSE."},
		{"Sign in", "Masuk"},
		{"Observation", "Observasi"},
		{"Thank you", "Terima kasih"},
		{"My submissions", "Laporan saya"},
		{"Close the finding", "Tutup temuan"},
		{"Cannot edit", "Tidak dapat diubah"},
		{"Closed", "Ditutup"},

		// ---- sign-in page ----
		{"Safety Observation", "Observasi Keselamatan"},
		{"Report your safety observation. No account needed.", "Laporkan temuan keselamatan Anda. Tidak perlu akun."},
		{"1. Tick the box", "1. Centang kotak ini"},
		{"2. Reporter", "2. Pelapor"},
		{"Your name", "Nama Anda"},
		{"Type at least 3 letters", "Ketik minimal 3 huruf"},
		{"Date of birth", "Tanggal lahir"},
		{"Pick a date", "Pilih tanggal"},
		{"Enter your date of birth as dd/mm/yyyy.", "Masukkan tanggal lahir dengan format dd/mm/yyyy."},
		{"Position and company are filled in from Medpost.", "Posisi dan perusahaan diisi otomatis dari Medpost."},

		// ---- report form ----
		{"Observation Finding", "Temuan Observasi"},
		{"Edit Report", "Ubah Laporan"},
		{"Observation form. Reporter details come from Medpost.", "Formulir observasi. Data pelapor diambil dari Medpost."},
		{"Edit your report", "Ubah laporan Anda"},
		{"You can change it until HSE verifies it.", "Anda dapat mengubahnya sampai diverifikasi oleh HSE."},
		{"Could not save, please try again.", "Gagal menyimpan, silakan coba lagi."},
		{"Reporter", "Pelapor"},
		{"Name", "Nama"},
		{"Position", "Posisi"},
		{"Company", "Perusahaan"},
		{"From Medpost, cannot be changed.", "Dari Medpost, tidak dapat diubah."},
		{"Finding", "Temuan"},
		{"Date of finding", "Tanggal temuan"},
		{"Category", "Kategori"},
		{"Select…", "Pilih…"},
		{"Describe your findings", "Jelaskan temuan Anda"},
		{"Hazard topic", "Topik bahaya"},
		{"Suggested topics", "Topik yang disarankan"},
		{"Hazard class", "Kelas bahaya"},
		{"Follows the topic", "Mengikuti topik"},
		{"None", "Tidak ada"},
		{"Choose from the list", "Pilih dari daftar"},
		{"Other / none of these fit", "Lainnya / tidak ada yang cocok"},
		{"Observation area", "Area observasi"},
		{"Type to search", "Ketik untuk mencari"},
		{"Responsible company (if known)", "Perusahaan yang bertanggung jawab (jika tahu)"},
		{"Evidence", "Dokumentasi"},
		{"Current photos", "Foto saat ini"},
		{"Remove", "Hapus"},
		{"Photo", "Foto"},
		{"Documentation, max 4 photos (if any)", "Dokumentasi, maks. 4 foto (jika ada)"},
		{"Photos are compressed automatically to 500 KB max.", "Foto dikompres otomatis hingga maks. 500 KB."},
		{"After action (optional)", "Setelah perbaikan (opsional)"},
		{"After photos, max 4 (if already fixed)", "Foto sesudah perbaikan, maks. 4 (jika sudah diperbaiki)"},
		{"Remarks, notes or recommendation (if any)", "Catatan / rekomendasi (jika ada)"},
		{"What was done, or what do you recommend?", "Apa yang sudah dilakukan atau disarankan?"},
		{"Close this finding now", "Tutup temuan ini sekarang"},
		{"Needs at least one After photo, except a Positive Finding. It cannot be changed once closed.", "Perlu minimal satu foto sesudah, kecuali Temuan Positif. Setelah ditutup tidak dapat diubah."},
		{"Submit", "Kirim"},
		{"Save changes", "Simpan perubahan"},

		// ---- topic / hazard / control groups ----
		{"Hazard", "Bahaya"},
		{"Control", "Kontrol"},
		{"Safety", "Keselamatan"},
		{"Physical", "Fisik"},
		{"Chemical", "Kimia"},
		{"Biological", "Biologi"},
		{"Ergonomic", "Ergonomi"},
		{"Psychosocial", "Psikososial"},
		{"Environmental", "Lingkungan"},
		{"Elimination", "Eliminasi"},
		{"Substitution", "Substitusi"},
		{"Engineering", "Rekayasa"},
		{"Administrative", "Administratif"},
		{"PPE", "APD"},
		{"Other", "Lainnya"},
		{"Unsafe Action", "Tindakan Tidak Aman"},
		{"Unsafe Condition", "Kondisi Tidak Aman"},
		{"Positive Finding", "Temuan Positif"},

		// ---- After page ----
		{"After photos", "Foto sesudah"},
		{"After photos for report", "Foto sesudah untuk laporan"},
		{"Upload photos taken after the fix, then close the finding when it is done. HSE verifies it afterwards.", "Unggah foto setelah perbaikan, lalu tutup temuan jika sudah selesai. Verifikasi dilakukan oleh tim HSE setelah itu."},
		{"After photos, max 4", "Foto sesudah, maks. 4"},
		{"After photo", "Foto sesudah"},
		{"The maximum of 4 After photos is reached.", "Sudah 4 foto sesudah (batas maksimum)."},
		{"To close, add at least one After photo.", "Untuk menutup, tambahkan minimal satu foto sesudah."},
		{"Save photos", "Simpan foto"},
		{"Close finding", "Tutup temuan"},
		{"Close this finding? It cannot be changed afterwards.", "Tutup temuan ini? Setelah ditutup tidak dapat diubah."},

		// ---- My submissions / thanks / locked ----
		{"My reports", "Laporan saya"},
		{"Your own submissions: reference, date, category, status and photos.", "Laporan Anda sendiri: nomor referensi, tanggal, kategori, status, dan foto."},
		{"Ref", "Ref"},
		{"Date", "Tanggal"},
		{"Status", "Status"},
		{"Edit", "Ubah"},
		{"After photos & close", "Foto sesudah & tutup"},
		{"No submissions yet.", "Belum ada laporan."},
		{"New report", "Kirim laporan"},
		{"Your report has been received.", "Laporan Anda sudah diterima."},
		{"Reference number:", "Nomor referensi:"},
		{"The HSE team will check this report.", "Tim HSE akan memeriksa laporan ini."},
		{"Submit another", "Kirim lagi"},
		{"Report photo", "Foto laporan"},
		{"Cannot be edited", "Tidak dapat diubah"},
		{"Report", "Laporan"},
		{"is closed. Verification is up to the HSE team.", "sudah ditutup. Verifikasi menunggu tim HSE."},
		{"has been verified by the HSE team or closed, so it can no longer be edited.", "sudah diverifikasi oleh tim HSE atau sudah ditutup, sehingga tidak bisa diubah lagi."},
		{"Changes saved", "Perubahan disimpan"},
		{"After photos saved", "Foto sesudah disimpan"},
		{"Finding closed; waiting for HSE verification", "Temuan ditutup; menunggu verifikasi HSE"},
		{"Closed and verified", "Ditutup dan terverifikasi"},
		{"Closed, waiting for HSE verification", "Ditutup, menunggu verifikasi HSE"},
		{"In progress", "Sedang diproses"},
		{"Verified", "Terverifikasi"},
		{"Received", "Diterima"},

		// ---- form errors ----
		{"Enter a valid date.", "Masukkan tanggal yang valid."},
		{"The date cannot be in the future.", "Tanggal tidak boleh di masa depan."},
		{"Choose a category.", "Pilih kategori."},
		{"Describe your finding.", "Jelaskan temuan Anda."},
		{"Choose the area.", "Pilih area."},
		{"Choose an area from the list.", "Pilih area dari daftar."},
		{"Choose a topic from the list.", "Pilih topik dari daftar."},
		{"Choose a hazard from the list.", "Pilih bahaya dari daftar."},
		{"Maximum %d characters.", "Maksimum %d karakter."},
		{"Add at least one After photo before closing.", "Tambahkan minimal satu foto sesudah perbaikan sebelum menutup."},
		{"Photo not accepted", "Foto tidak dapat diterima"},
		{"Max 4 photos: JPEG, PNG, WebP or GIF.", "Maks. 4 foto: JPEG, PNG, WebP atau GIF."},
		{"a post can have at most %d photos", "satu laporan maksimal %d foto"},
		{"the photo is too big", "foto terlalu besar"},
		{"the photo is empty", "foto kosong"},
		{"use a JPEG, PNG, WebP or GIF photo", "gunakan foto JPEG, PNG, WebP atau GIF"},
		{"the file is not a valid picture", "file bukan gambar yang valid"},
		{"the photo is too large in pixels", "foto terlalu besar ukuran pikselnya"},
		{"the photo could not be compressed enough", "foto tidak dapat dikompres cukup kecil"},
		{"could not read the photo", "foto tidak dapat dibaca"},

		// ---- server answers to the sign-in scripts, and plain errors ----
		{"Details not found, please check and try again.", "Data tidak ditemukan, periksa lagi dan coba kembali."},
		{"Please sign in first.", "Silakan masuk terlebih dahulu."},
		{"Please wait a moment and try again.", "Mohon tunggu sebentar lalu coba lagi."},
		{"Check failed, please try again.", "Pemeriksaan gagal, silakan coba lagi."},
		{"Something went wrong.", "Terjadi kesalahan."},
		{"Please complete the check first.", "Silakan selesaikan pemeriksaan terlebih dahulu."},
		{"Name search is not available right now.", "Pencarian nama tidak tersedia saat ini."},
		{"Sign-in is not available right now. Please try again later.", "Masuk tidak tersedia saat ini. Silakan coba lagi nanti."},
		{"Could not load the report.", "Laporan tidak dapat dimuat."},
		{"Could not save. Please try again.", "Gagal menyimpan. Silakan coba lagi."},
		{"Could not save your changes. Please try again.", "Perubahan gagal disimpan. Silakan coba lagi."},
		{"Could not load the photo.", "Foto tidak dapat dimuat."},
		{"Could not load your submissions.", "Laporan Anda tidak dapat dimuat."},
		{"The form is not available right now. Please try again later.", "Formulir tidak tersedia saat ini. Silakan coba lagi nanti."},
		{"The form or the photos are too large.", "Formulir atau foto terlalu besar."},

		// ---- scripts (copied into data attributes by the templates) ----
		{"The check could not load. Please check your connection and reload.", "Pemeriksaan tidak dapat dimuat. Periksa koneksi Anda lalu muat ulang halaman."},
		{"Choose your name from the list.", "Pilih nama dari daftar."},
		{"No match", "Tidak ada hasil"},
		// ---- wizard ----
		{"What did you see?", "Apa yang Anda lihat?"},
		{"Sub-area", "Sub-area"},
		{"Choose the sub-area.", "Pilih sub-area."},
		{"Choose a sub-area from the list.", "Pilih sub-area dari daftar."},
		{"Unsafe action", "Tindakan tidak aman"},
		{"Unsafe condition", "Kondisi tidak aman"},
		{"Safe observation", "Pengamatan aman"},
		{"Suggestion", "Saran"},
		{"Step {n} of {total}", "Langkah {n} dari {total}"},
		{"Start over", "Mulai dari awal"},
		{"Next", "Lanjut"},
		{"Back", "Kembali"},
		{"Yes", "Ya"},
		{"No", "Tidak"},
		{"Please answer this question first.", "Jawab pertanyaan ini dulu."},
		{"Company of the people you saw", "Perusahaan orang yang Anda amati"},
		{"Do not write people's names.", "Jangan tulis nama orang."},
		{"Area owner (company)", "Pemilik area (perusahaan)"},
		{"What work was being done?", "Pekerjaan apa yang sedang dilakukan?"},
		{"Example: grinding in the workshop", "Contoh: gerinda di workshop"},
		{"What unsafe condition did you see?", "Kondisi apa yang tidak aman?"},
		{"Example: cables across the walkway", "Contoh: kabel melintang di jalan"},
		{"What needs to be improved?", "Apa yang perlu diperbaiki?"},
		{"What is unsafe?", "Apa yang tidak aman?"},
		{"Which PPE item?", "APD yang mana?"},
		{"Was the work stopped?", "Apakah pekerjaan dihentikan?"},
		{"Who stopped it?", "Siapa yang menghentikan?"},
		{"Should this work have been stopped?", "Apakah pekerjaan ini seharusnya dihentikan?"},
		{"Is there immediate danger?", "Ada bahaya langsung?"},
		{"Is it barricaded or stopped?", "Sudah dibarikade atau dihentikan?"},
		{"Tell your supervisor now. HSE is told this is urgent.", "Segera beri tahu supervisor. HSE akan diberi tahu bahwa ini urgent."},
		{"What was done well?", "Apa yang dilakukan dengan baik?"},
		{"What work was it? (optional)", "Pekerjaan apa? (opsional)"},
		{"What did you do on the spot?", "Tindakan perbaikan di tempat"},
		{"Recommendation (optional)", "Rekomendasi (opsional)"},
		{"Your proposal", "Usulan perbaikan"},
		{"Photo (optional)", "Foto (opsional)"},
		{"Do not show faces in photos if you can avoid it.", "Hindari menampilkan wajah di foto."},
		{"Enter the company.", "Isi nama perusahaan."},
		{"Describe the work.", "Jelaskan pekerjaannya."},
		{"Describe the condition.", "Jelaskan kondisinya."},
		{"Describe what needs to be improved.", "Jelaskan apa yang perlu diperbaiki."},
		{"Choose what was unsafe.", "Pilih apa yang tidak aman."},
		{"Choose the PPE item.", "Pilih APD-nya."},
		{"Answer yes or no.", "Jawab ya atau tidak."},
		{"Choose who stopped the work.", "Pilih siapa yang menghentikan."},
		{"Describe what you did on the spot.", "Jelaskan tindakan Anda di tempat."},
		{"Choose what was done well.", "Pilih apa yang dilakukan dengan baik."},
		{"Describe your proposal.", "Jelaskan usulan Anda."},
		{"Choose what you saw.", "Pilih apa yang Anda lihat."},
		{"Tap the topic that fits, or choose from the list.", "Ketuk topik yang cocok, atau pilih dari daftar."},
		{"No suggestion. Choose from the list.", "Tidak ada saran. Pilih dari daftar."},
	}
	out := make(map[string]socTranslation, len(rows))
	for _, r := range rows {
		out[r[0]] = socTranslation{id: r[1]}
	}
	return out
}()
