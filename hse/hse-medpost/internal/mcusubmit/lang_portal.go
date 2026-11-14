package mcusubmit

import "regexp"

// The signed-in submitter pages in Indonesian and Chinese (owner, 2026-11-05, item 5 of the
// follow-ups): home, New batch, the send question, the review page, My documents and Settings. Each
// entry holds both languages next to its English, so the two can never drift apart. The reviewer and
// admin pages stay English: they are for the medical team's own staff.
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
	"Start or continue a batch": {"Mulai atau lanjutkan batch", "开始或继续批次"},

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

	// Temporary password page
	"Choose your": {"Pilih", "设置您的"},
	"password":    {"kata sandi Anda", "密码"},
	"You signed in with a temporary password. Choose your own before you continue.": {"Anda masuk dengan kata sandi sementara. Pilih kata sandi Anda sendiri sebelum melanjutkan.", "您使用的是临时密码。请先设置自己的密码再继续。"},
	"New password":                        {"Kata sandi baru", "新密码"},
	"Save password":                       {"Simpan kata sandi", "保存密码"},
	"The two passwords are not the same.": {"Kedua kata sandi tidak sama.", "两次输入的密码不一致。"},
	"Choose a password different from the temporary one.": {"Pilih kata sandi yang berbeda dari yang sementara.", "请选择与临时密码不同的密码。"},

	// Employee checkups
	"More":                   {"Selengkapnya", "更多"},
	"Recommendation":         {"Rekomendasi", "建议"},
	"Notes":                  {"Catatan", "备注"},
	"Certificate":            {"Sertifikat", "证书"},
	"Employee checkups":      {"Check-up karyawan", "员工体检"},
	"Employee":               {"Karyawan", "员工"},
	"Include subcontractors": {"Sertakan subkontraktor", "包括分包商"},
	"Export to Excel":        {"Ekspor ke Excel", "导出到 Excel"},
	"checkups":               {"medical check-up", "体检"},
	"The latest medical checkup of each worker of your company and how long it stays valid.": {"Medical check-up terbaru setiap pekerja perusahaan Anda dan berapa lama berlakunya.", "贵公司每位员工最近一次体检及其有效期。"},
	"Latest medical checkup of every worker of your company":                                 {"Medical check-up terbaru setiap pekerja perusahaan Anda", "贵公司每位员工的最近一次体检"},
	"Validity":                         {"Masa berlaku", "有效状态"},
	"Last checkup":                     {"Check-up terakhir", "最近体检"},
	"Valid":                            {"Berlaku", "有效"},
	"Expiring soon":                    {"Segera kedaluwarsa", "即将到期"},
	"Expired":                          {"Kedaluwarsa", "已过期"},
	"expired":                          {"kedaluwarsa", "已过期"},
	"Unfit":                            {"Tidak layak", "不适合"},
	"Temporarily unfit":                {"Tidak layak sementara", "暂时不适合"},
	"Needs follow-up":                  {"Perlu tindak lanjut", "需要跟进"},
	"Checkup in progress":              {"Check-up sedang berjalan", "体检进行中"},
	"No checkup on record":             {"Belum ada check-up", "暂无体检记录"},
	"Filter your employees":            {"Saring karyawan Anda", "筛选您的员工"},
	"Check the status you chose.":      {"Periksa status yang Anda pilih.", "请检查您选择的状态。"},
	"No employee matches this filter.": {"Tidak ada karyawan yang cocok dengan saringan ini.", "没有符合此筛选条件的员工。"},
	"No employee of your company is on record yet.": {"Belum ada karyawan perusahaan Anda yang tercatat.", "贵公司暂无员工记录。"},

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
	"batch": {"batch", "批次"},
	"Add the workers one by one. Each document is checked by you first. When all are confirmed, send the batch to the medical team. Nothing is visible to the medical team until you press Send.": {"Tambahkan pekerja satu per satu. Setiap dokumen Anda periksa lebih dulu. Setelah semuanya dikonfirmasi, kirim batch ke tim medis. Tim medis tidak dapat melihat apa pun sampai Anda menekan Kirim.", "请逐个添加员工。每份文件都先由您自己检查。全部确认后，把批次发送给医疗团队。在您点击发送之前，医疗团队看不到任何内容。"},
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
	"Checkup result (PDF, up to 10 MB)": {"Hasil check-up (PDF, maksimal 10 MB)", "体检结果(PDF,最大 10 MB)"},
	"The file is rebuilt when it is uploaded and is refused if it is over 10 MB. Password-protected PDFs and PDFs with scripts or attachments are refused.": {"File dibangun ulang saat diunggah dan ditolak jika lebih dari 10 MB. PDF yang dilindungi kata sandi serta PDF dengan skrip atau lampiran ditolak.", "文件上传时会被重新生成,超过 10 MB 将被拒绝。受密码保护的 PDF,以及带脚本或附件的 PDF 将被拒绝。"},
	// New batch: the company can only be picked from the list
	"Company not listed yet? Contact the medical team before submitting.": {"Perusahaan belum ada di daftar? Hubungi tim medis sebelum mengirim.", "没有找到您的公司？请在提交前联系医疗团队。"},

	// The NIK / passport field follows the nationality
	"NIK: exactly 16 digits, numbers only.":                    {"NIK: tepat 16 digit, hanya angka.", "NIK:正好 16 位,只能是数字。"},
	"Passport number: 6 to 12 letters or digits, in capitals.": {"Nomor paspor: 6 sampai 12 huruf atau angka, huruf kapital.", "护照号码:6 至 12 位字母或数字,字母需大写。"},
	"Choose the nationality first":                             {"Pilih kewarganegaraan dulu", "请先选择国籍"},
	"16 digits":                                                {"16 digit", "16 位数字"},
	"6 to 12 letters or digits":                                {"6 sampai 12 huruf atau angka", "6 至 12 位字母或数字"},
	// The "make a PDF smaller" tool on the New batch page
	"PDF over 10 MB? Make it smaller here": {"PDF lebih dari 10 MB? Perkecil di sini", "PDF 超过 10 MB?在此压缩"},
	"This works on your own device: the file is not sent anywhere until you press Upload and review. The pages are saved as pictures, so the text in the smaller PDF can no longer be selected or searched. The medical team sees the same pages.": {"Ini berjalan di perangkat Anda sendiri: file tidak dikirim ke mana pun sampai Anda menekan Unggah dan periksa. Halaman disimpan sebagai gambar, sehingga teks di PDF yang lebih kecil tidak bisa lagi dipilih atau dicari. Tim medis melihat halaman yang sama.", "此功能在您自己的设备上运行:在您点击“上传并检查”之前,文件不会发送到任何地方。页面会保存为图片,因此变小后的 PDF 中的文字无法再被选择或搜索。医疗团队看到的页面相同。"},
	"PDF to make smaller":                  {"PDF yang akan diperkecil", "要压缩的 PDF"},
	"Make it smaller than":                 {"Perkecil hingga kurang dari", "压缩到小于"},
	"9 MB (best quality that is accepted)": {"9 MB (kualitas terbaik yang diterima)", "9 MB(可接受的最佳画质)"},
	"5 MB":                                 {"5 MB", "5 MB"},
	"2 MB (smallest)":                      {"2 MB (terkecil)", "2 MB(最小)"},
	"Make smaller":                         {"Perkecil", "压缩"},
	"Use this file for the upload":         {"Pakai file ini untuk unggahan", "使用此文件上传"},
	"Save to my device":                    {"Simpan ke perangkat saya", "保存到我的设备"},
	"Choose the PDF first.":                {"Pilih PDF terlebih dahulu.", "请先选择 PDF。"},
	"Making the file smaller…":             {"Memperkecil file…", "正在压缩文件…"},
	"Made smaller: {from} MB to {to} MB.":  {"Diperkecil: {from} MB menjadi {to} MB.", "已压缩:{from} MB → {to} MB。"},
	"The smaller file is chosen for the upload. Press Upload and review.":                            {"File yang lebih kecil sudah dipilih untuk diunggah. Tekan Unggah dan periksa.", "已选择压缩后的文件用于上传。请点击“上传并检查”。"},
	"This PDF could not be made smaller. Split it into smaller files or scan at a lower resolution.": {"PDF ini tidak bisa diperkecil. Bagi menjadi file yang lebih kecil atau pindai dengan resolusi lebih rendah.", "此 PDF 无法进一步压缩。请拆分成较小的文件,或降低扫描分辨率。"},
	"The result is still {to} MB. Choose a smaller size above, or split the file.":                   {"Hasilnya masih {to} MB. Pilih ukuran yang lebih kecil di atas, atau bagi file.", "结果仍有 {to} MB。请在上方选择更小的尺寸,或拆分文件。"},
	"This PDF could not be read. It may be damaged or protected by a password.":                      {"PDF ini tidak bisa dibaca. Mungkin rusak atau dilindungi kata sandi.", "无法读取此 PDF。文件可能已损坏或受密码保护。"},
	"Uploading the file…": {"Mengunggah file…", "正在上传文件…"},
	"Uploaded. The portal is now checking that the file is a safe, readable PDF. A large file can take a minute. Keep this page open.":        {"Sudah terunggah. Portal sedang memeriksa apakah file ini PDF yang aman dan bisa dibaca. File besar bisa memakan waktu sekitar satu menit. Biarkan halaman ini tetap terbuka.", "已上传。系统正在检查该文件是否为安全、可读取的 PDF。较大的文件可能需要一分钟左右。请保持此页面打开。"},
	"The file is larger than 10 MB. Split it or scan at a lower resolution.":                                                                  {"File lebih besar dari 10 MB. Bagi file atau pindai dengan resolusi lebih rendah.", "文件超过 10 MB。请拆分文件或降低扫描分辨率。"},
	"The connection was cut or timed out. The file may have arrived: look in the list of documents in this batch before you upload it again.": {"Koneksi terputus atau habis waktu. File mungkin sudah sampai: lihat daftar dokumen di batch ini sebelum mengunggah ulang.", "连接中断或超时。文件可能已送达：请先查看本批次的文件列表，再重新上传。"},
	"Already sent: waiting for the medical team": {"Sudah dikirim: menunggu tim medis", "已发送：等待医疗团队处理"},
	"Checkup done {last}, valid until {until}. A new one is accepted only more than 180 days after the last, from {from}.": {"Check-up dilakukan {last}, berlaku sampai {until}. Yang baru diterima hanya lebih dari 180 hari setelah yang terakhir, yaitu mulai {from}.", "体检日期 {last}，有效期至 {until}。仅在上次体检 180 天之后才接受新的体检，从 {from} 起。"},
	"This worker already has a document waiting for the medical team. Wait for its result before sending another.":         {"Pekerja ini sudah memiliki dokumen yang menunggu tim medis. Tunggu hasilnya sebelum mengirim yang lain.", "该员工已有文件在等待医疗团队处理。请等待结果后再提交新的。"},
	"Upload and review": {"Unggah dan periksa", "上传并检查"},
	"Documents in this": {"Dokumen dalam", "本批次中的"},
	"No documents yet.": {"Belum ada dokumen.", "暂无文件。"},
	"Matching workers":  {"Pekerja yang cocok", "匹配的员工"},

	// Send the batch
	"Send the":          {"Kirim", "发送"},
	"Back to the batch": {"Kembali ke batch", "返回批次"},

	// Review one document
	"document": {"dokumen", "文件"},
	"Check that this is the right worker and the right file. This is exactly what the medical team will see.": {"Periksa bahwa ini pekerja dan file yang benar. Inilah yang akan dilihat tim medis.", "请检查员工和文件是否正确。医疗团队将看到的正是这些内容。"},
	"Not given":                      {"Tidak diisi", "未填写"},
	"Worker record":                  {"Data pekerja", "员工记录"},
	"New employee (not on file yet)": {"Karyawan baru (belum terdaftar)", "新员工(尚无档案)"},
	"Existing worker on file":        {"Pekerja yang sudah terdaftar", "已有档案的员工"},
	"Open the PDF in a new tab":      {"Buka PDF di tab baru", "在新标签页中打开 PDF"},
	"Confirm and add to batch":       {"Konfirmasi dan tambahkan ke batch", "确认并加入批次"},
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
	{regexp.MustCompile(`^This worker already has a valid medical checkup \(done on (\S+), valid until (\S+)\)\. A new checkup is accepted only more than 180 days after the last one, from (\S+)\.$`),
		"Pekerja ini sudah memiliki medical check-up yang berlaku (dilakukan $1, berlaku sampai $2). Check-up baru diterima hanya lebih dari 180 hari setelah yang terakhir, yaitu mulai $3.", "该员工已有有效体检（体检日期 $1，有效期至 $2）。仅在上次体检 180 天之后才接受新的体检，从 $3 起。"},
	{regexp.MustCompile(`^(\d+) documents need your action$`), "$1 dokumen perlu tindakan Anda", "$1 份文件需要您处理"},
	{regexp.MustCompile(`^(\d+) employees?, page (\d+) of (\d+)$`), "$1 karyawan, halaman $2 dari $3", "共 $1 名员工，第 $2 页，共 $3 页"},
	{regexp.MustCompile(`^(\d+) days left$`), "$1 hari lagi", "还剩 $1 天"},
	{regexp.MustCompile(`^(\d+) documents?, page (\d+) of (\d+)$`), "$1 dokumen, halaman $2 dari $3", "共 $1 份文件，第 $2 页，共 $3 页"},
	{regexp.MustCompile(`^Send batch of (\d+) for review$`), "Kirim batch berisi $1 dokumen untuk ditinjau", "发送包含 $1 份文件的批次以供审核"},
	{regexp.MustCompile(`^Yes, send (\d+)$`), "Ya, kirim $1", "是，发送 $1 份"},
	{regexp.MustCompile(`^(\d+) document\(s\) still need your review\. Confirm or discard them, then you can send the batch\.$`),
		"$1 dokumen masih perlu Anda periksa. Konfirmasi atau buang, lalu Anda dapat mengirim batch.",
		"还有 $1 份文件需要您检查。请先确认或放弃，然后才能发送批次。"},
	{regexp.MustCompile(`^The medical team will see these (\d+) documents\. After sending you cannot add more to this batch or take a document back, except by withdrawing it while it is waiting for review\.$`),
		"Tim medis akan melihat $1 dokumen ini. Setelah dikirim, Anda tidak dapat menambah dokumen ke batch ini atau mengambil kembali dokumen, kecuali dengan menariknya selama menunggu peninjauan.",
		"医疗团队将看到这 $1 份文件。发送后，您不能再向此批次添加文件，也不能收回文件，但在文件等待审核期间可以撤回。"},
	{regexp.MustCompile(`^You signed this statement on (.+)\.$`), "Anda menandatangani pernyataan ini pada $1.", "您已于 $1 签署此声明。"},
	{regexp.MustCompile(`^Now: (.+)$`), "Sekarang: $1", "当前：$1"},
}
