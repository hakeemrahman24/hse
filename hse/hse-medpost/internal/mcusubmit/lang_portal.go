package mcusubmit

import "regexp"

// The signed-in submitter pages in Indonesian and Chinese (owner, 2026-11-05, item 5 of the
// follow-ups): home, New batch, the send question, the review page, My documents and Settings. Each
// entry holds both languages next to its English, so the two can never drift apart. The reviewer and
// admin pages stay English: they are for the medical post's own staff.
//
// Like lang_id.go and lang_zh.go these are written for plain office readers and should be read
// through by someone who speaks the language before they go to real users.
//
// An entry is {Indonesian, Chinese}.
var portalText = map[string][2]string{
	// Sidebar and shared words
	"Medical checkups": {"Medical check-up", "体检"},
	"Account":          {"Akun", "账户"},
	"ROMIF statement":  {"Pernyataan ROMIF", "ROMIF 声明"},
	"Review":           {"Periksa", "检查"},
	"Draft":            {"Draf", "草稿"},
	"Pending":          {"Menunggu", "待审核"},
	"Reviewed":         {"Ditinjau", "已审阅"},
	"Closed":           {"Selesai", "已完成"},
	"Rejected":         {"Ditolak", "已退回"},
	"Withdrawn":        {"Ditarik", "已撤回"},
	"Save":             {"Simpan", "保存"},
	"Cancel":           {"Batal", "取消"},
	"Worker":           {"Pekerja", "员工"},
	"Status":           {"Status", "状态"},
	"All":              {"Semua", "全部"},

	// Home
	"Welcome,":                  {"Selamat datang,", "欢迎，"},
	"Your documents by status.": {"Dokumen Anda berdasarkan status.", "按状态显示您的文件。"},
	": a rejected document to upload again, or a result with a note to read.": {": dokumen yang ditolak untuk diunggah ulang, atau hasil dengan catatan untuk dibaca.", ":被退回、需要重新上传的文件，或带有备注、需要阅读的结果。"},
	"Expiring": {"Segera", "即将"},
	"soon":     {"kedaluwarsa", "到期"},
	"Workers you submitted whose checkup is about to expire": {"Pekerja yang Anda kirim dan medical check-up-nya akan segera kedaluwarsa", "您提交的员工中，体检即将到期的人员"},
	"Valid until":               {"Berlaku sampai", "有效期至"},
	"Start or continue a batch": {"Mulai atau lanjutkan kumpulan", "开始或继续批次"},

	// The documents table (My documents and the batch page)
	"Sent":                 {"Dikirim", "发送时间"},
	"Uploaded by":          {"Diunggah oleh", "上传人"},
	"Date of birth":        {"Tanggal lahir", "出生日期"},
	"Nationality":          {"Kewarganegaraan", "国籍"},
	"File":                 {"File", "文件"},
	"Action":               {"Tindakan", "操作"},
	"Open PDF":             {"Buka PDF", "打开 PDF"},
	"No file":              {"Tidak ada file", "无文件"},
	"Remove":               {"Hapus", "删除"},
	"Discard":              {"Buang", "放弃"},
	"Withdraw":             {"Tarik", "撤回"},
	"Download certificate": {"Unduh sertifikat", "下载证书"},

	// My documents
	"My":                               {"Dokumen", "我的"},
	"documents":                        {"saya", "文件"},
	"Needs action":                     {"Perlu tindakan", "需要处理"},
	"Account company":                  {"Perusahaan akun", "账户所属公司"},
	"Uploaded from":                    {"Diunggah dari", "上传日期自"},
	"Until":                            {"Sampai", "至"},
	"Filter":                           {"Saring", "筛选"},
	"Clear":                            {"Hapus saringan", "清除"},
	"Previous":                         {"Sebelumnya", "上一页"},
	"Next":                             {"Berikutnya", "下一页"},
	"No document matches this filter.": {"Tidak ada dokumen yang cocok dengan saringan ini.", "没有符合此筛选条件的文件。"},
	"Nothing has been uploaded yet.":   {"Belum ada yang diunggah.", "尚未上传任何文件。"},
	"Download all certificates":        {"Unduh semua sertifikat", "下载全部证书"},
	"Every document of your company, newest first": {"Semua dokumen perusahaan Anda, terbaru lebih dulu", "贵公司的全部文件，最新的在前"},
	"Filter your documents":                        {"Saring dokumen Anda", "筛选您的文件"},

	// New batch
	"New":   {"Buat", "新建"},
	"batch": {"kumpulan", "批次"},
	"Add the workers one by one. Each document is checked by you first. When all are confirmed, send the batch to the medical post. Nothing is visible to the medical post until you press Send.": {"Tambahkan pekerja satu per satu. Setiap dokumen Anda periksa lebih dulu. Setelah semuanya dikonfirmasi, kirim kumpulan ke pos medis. Pos medis tidak dapat melihat apa pun sampai Anda menekan Kirim.", "请逐个添加员工。每份文件都先由您自己检查。全部确认后，把批次发送给医疗站。在您点击发送之前，医疗站看不到任何内容。"},
	"Find the worker (type at least 3 letters of the name)":                 {"Cari pekerja (ketik minimal 3 huruf nama)", "查找员工(至少输入姓名中的 3 个字母)"},
	"Choose another worker":                                                 {"Pilih pekerja lain", "选择其他员工"},
	"Search needs JavaScript. Without it, use the new employee form below.": {"Pencarian memerlukan JavaScript. Tanpa itu, gunakan formulir karyawan baru di bawah.", "搜索需要 JavaScript。如果不可用，请使用下方的新员工表单。"},
	"The worker is not in the list (new employee)":                          {"Pekerja tidak ada di daftar (karyawan baru)", "员工不在列表中(新员工)"},
	"Worker's name":                     {"Nama pekerja", "员工姓名"},
	"Choose a company":                  {"Pilih perusahaan", "请选择公司"},
	"Choose a nationality":              {"Pilih kewarganegaraan", "请选择国籍"},
	"Gender":                            {"Jenis kelamin", "性别"},
	"Choose the gender":                 {"Pilih jenis kelamin", "请选择性别"},
	"Male":                              {"Laki-laki", "男"},
	"Female":                            {"Perempuan", "女"},
	"NIK or passport number (required)": {"NIK atau nomor paspor (wajib)", "NIK 或护照号码(必填)"},
	"Indonesian citizens: the 16-digit NIK. Everyone else: the passport number (6 to 12 letters or digits).": {"Warga negara Indonesia: NIK 16 digit. Lainnya: nomor paspor (6 sampai 12 huruf atau angka).", "印度尼西亚公民：16 位 NIK。其他人：护照号码(6 到 12 位字母或数字)。"},
	"Checkup result (PDF, up to 15 MB)": {"Hasil check-up (PDF, maksimal 15 MB)", "体检结果(PDF,最大 15 MB)"},
	"The file is rebuilt and compressed when it is uploaded. Password-protected PDFs and PDFs with scripts or attachments are refused.": {"File dibangun ulang dan dikompres saat diunggah. PDF yang dilindungi kata sandi serta PDF dengan skrip atau lampiran ditolak.", "文件上传时会被重新生成并压缩。受密码保护的 PDF,以及带脚本或附件的 PDF 将被拒绝。"},
	"Upload and review": {"Unggah dan periksa", "上传并检查"},
	"Documents in this": {"Dokumen dalam", "本批次中的"},
	"No documents yet.": {"Belum ada dokumen.", "暂无文件。"},
	"Matching workers":  {"Pekerja yang cocok", "匹配的员工"},

	// Send the batch
	"Send the":          {"Kirim", "发送"},
	"Back to the batch": {"Kembali ke kumpulan", "返回批次"},

	// Review one document
	"document": {"dokumen", "文件"},
	"Check that this is the right worker and the right file. This is exactly what the medical post will see.": {"Periksa bahwa ini pekerja dan file yang benar. Inilah yang akan dilihat pos medis.", "请检查员工和文件是否正确。医疗站将看到的正是这些内容。"},
	"Not given":                      {"Tidak diisi", "未填写"},
	"Worker record":                  {"Data pekerja", "员工记录"},
	"New employee (not on file yet)": {"Karyawan baru (belum terdaftar)", "新员工(尚无档案)"},
	"Existing worker on file":        {"Pekerja yang sudah terdaftar", "已有档案的员工"},
	"Open the PDF in a new tab":      {"Buka PDF di tab baru", "在新标签页中打开 PDF"},
	"Confirm and add to batch":       {"Konfirmasi dan tambahkan ke kumpulan", "确认并加入批次"},
	"Already confirmed.":             {"Sudah dikonfirmasi.", "已确认。"},

	// Settings
	"Change":           {"Ubah", "修改"},
	"Current password": {"Kata sandi saat ini", "当前密码"},
	"At least 6 characters. Not your username or email, and not a common password. Your other devices are signed out.": {"Minimal 6 karakter. Bukan username atau email Anda, dan bukan kata sandi yang umum. Perangkat Anda yang lain akan dikeluarkan.", "至少 6 个字符。不能是您的用户名或邮箱，也不能是常见密码。您在其他设备上的登录将被退出。"},
	"Repeat the new password": {"Ulangi kata sandi baru", "再次输入新密码"},
	"New email address":       {"Alamat email baru", "新邮箱地址"},
	"We send a link to the new address. The login email changes when you open it. The old address gets a warning.": {"Kami mengirim tautan ke alamat baru. Email login berubah setelah Anda membukanya. Alamat lama mendapat peringatan.", "我们会向新地址发送一个链接。打开链接后，登录邮箱才会更改。旧地址会收到提醒。"},
	"username":     {"username", "用户名"},
	"New username": {"Username baru", "新用户名"},
	"3 to 40 letters, digits, dots or underscores. Use the new name the next time you sign in.": {"3 sampai 40 huruf, angka, titik, atau garis bawah. Gunakan nama baru saat Anda masuk berikutnya.", "3 到 40 个字母、数字、点或下划线。下次登录时请使用新用户名。"},
	"Change username": {"Ubah username", "修改用户名"},
	"Reminder":        {"Pengingat", "提醒"},
	"mails":           {"email", "邮件"},
	"Email me when a medical checkup I submitted is about to expire": {"Kirimi saya email saat medical check-up yang saya kirim akan segera kedaluwarsa", "我提交的体检即将到期时，给我发邮件"},
	"Username changed. Use it the next time you sign in.":            {"Username diubah. Gunakan saat Anda masuk berikutnya.", "用户名已修改。下次登录时请使用新用户名。"},
	"Password changed. Your other devices were signed out.":          {"Kata sandi diubah. Perangkat Anda yang lain telah dikeluarkan.", "密码已修改。您在其他设备上的登录已退出。"},
	"Saved.":                               {"Tersimpan.", "已保存。"},
	"The current password is not correct.": {"Kata sandi saat ini tidak benar.", "当前密码不正确。"},
	"Use":                                  {"Gunakan", "使用"},
	"as the login email of your account?":  {"sebagai email login akun Anda?", "作为您账户的登录邮箱？"},
}

// portalPattern translates a sentence with numbers or names in it. The pattern is matched against
// the whole text, and $1, $2 in the replacements are filled from its groups.
type portalPattern struct {
	re     *regexp.Regexp
	id, zh string
}

var portalPatterns = []portalPattern{
	{regexp.MustCompile(`^(\d+) documents need your action$`), "$1 dokumen perlu tindakan Anda", "$1 份文件需要您处理"},
	{regexp.MustCompile(`^(\d+) documents?, page (\d+) of (\d+)$`), "$1 dokumen, halaman $2 dari $3", "共 $1 份文件，第 $2 页，共 $3 页"},
	{regexp.MustCompile(`^Send batch of (\d+) for review$`), "Kirim kumpulan berisi $1 dokumen untuk ditinjau", "发送包含 $1 份文件的批次以供审核"},
	{regexp.MustCompile(`^Yes, send (\d+)$`), "Ya, kirim $1", "是，发送 $1 份"},
	{regexp.MustCompile(`^(\d+) document\(s\) still need your review\. Confirm or discard them, then you can send the batch\.$`),
		"$1 dokumen masih perlu Anda periksa. Konfirmasi atau buang, lalu Anda dapat mengirim kumpulan.",
		"还有 $1 份文件需要您检查。请先确认或放弃，然后才能发送批次。"},
	{regexp.MustCompile(`^The medical post will see these (\d+) documents\. After sending you cannot add more to this batch or take a document back, except by withdrawing it while it is waiting for review\.$`),
		"Pos medis akan melihat $1 dokumen ini. Setelah dikirim, Anda tidak dapat menambah dokumen ke kumpulan ini atau mengambil kembali dokumen, kecuali dengan menariknya selama menunggu peninjauan.",
		"医疗站将看到这 $1 份文件。发送后，您不能再向此批次添加文件，也不能收回文件，但在文件等待审核期间可以撤回。"},
	{regexp.MustCompile(`^You signed this statement on (.+)\.$`), "Anda menandatangani pernyataan ini pada $1.", "您已于 $1 签署此声明。"},
	{regexp.MustCompile(`^Now: (.+)$`), "Sekarang: $1", "当前：$1"},
}
