package mcusubmit

// indonesianText maps an English phrase, exactly as the template or handler writes it, to
// Indonesian. Headings are split in the templates ("Sign <span>in</span>"), so their halves are
// separate entries. The owner should read these through before the portal goes public: they are
// written for plain office readers, not as legal text.
var indonesianText = map[string]string{
	// Header and shared
	"Skip to content":            "Lewati ke konten",
	"Main navigation":            "Navigasi utama",
	"Home":                       "Beranda",
	"New batch":                  "Kumpulan baru",
	"My documents":               "Dokumen saya",
	"Settings":                   "Pengaturan",
	"Admin":                      "Admin",
	"Sign out":                   "Keluar",
	"Sign in":                    "Masuk",
	"Request an account":         "Ajukan akun",
	"Medical checkup submission": "Pengiriman medical check-up",
	"Error:":                     "Kesalahan:",
	"Back to Home":               "Kembali ke Beranda",
	"Language":                   "Bahasa",

	// Landing
	"Medical checkup ": "Pengiriman hasil ",
	"submission":       "medical check-up",
	"Send a worker's medical checkup result to the medical post as a PDF, and follow what happens to it.": "Kirim hasil medical check-up pekerja ke pos medis dalam bentuk PDF, dan pantau perkembangannya.",
	"How it works":        "Cara kerjanya",
	"Request an account.": "Ajukan akun.",
	"Confirm your email; a medical post administrator approves it.": "Konfirmasi email Anda; administrator pos medis akan menyetujuinya.",
	"Choose a login.": "Pilih login.",
	"You get a link to pick a username and password.": "Anda mendapat tautan untuk memilih username dan kata sandi.",
	"Send results.": "Kirim hasil.",
	"One PDF per worker. You can see the status of each one.": "Satu PDF per pekerja. Anda dapat melihat status masing-masing.",

	// Register
	"Request an ": "Ajukan ",
	"account":     "akun",
	"For company HR contacts and clinics who send medical checkup results.": "Untuk HR perusahaan dan klinik yang mengirim hasil medical check-up.",
	"Full name":  "Nama lengkap",
	"Work email": "Email kerja",
	"We send a link to confirm this address.": "Kami mengirim tautan untuk mengonfirmasi alamat ini.",
	"Company":             "Perusahaan",
	"Choose your company": "Pilih perusahaan Anda",
	"Company not listed? Ask the medical post administrator to add it.": "Perusahaan tidak ada di daftar? Minta administrator pos medis menambahkannya.",
	"Position":              "Jabatan",
	"Request account":       "Ajukan akun",
	"Already have a login?": "Sudah punya login?",

	// Confirm email
	"Confirm your ": "Konfirmasi ",
	"email":         "email Anda",
	"Press the button to confirm that this address is yours.": "Tekan tombol untuk mengonfirmasi bahwa alamat ini milik Anda.",
	"Confirm": "Konfirmasi",

	// Sign in
	"Sign ":                             "Masuk",
	"in":                                "",
	"Medical checkup submission portal": "Portal pengiriman medical check-up",
	"Username":                          "Username",
	"Password":                          "Kata sandi",
	"Forgot your password?":             "Lupa kata sandi Anda?",
	"No login yet?":                     "Belum punya login?",

	// Setup
	"Create your ": "Buat ",
	"login":        "login Anda",
	"Your request was approved. Choose a username and a password.":                   "Permintaan Anda disetujui. Pilih username dan kata sandi.",
	"3 to 40 letters, digits, dots or underscores.":                                  "3 sampai 40 huruf, angka, titik, atau garis bawah.",
	"At least 10 characters. Not your username or email, and not a common password.": "Minimal 10 karakter. Bukan username atau email Anda, dan bukan kata sandi yang umum.",
	"Repeat the password": "Ulangi kata sandi",
	"Create login":        "Buat login",

	// Forgot / reset
	"Forgot your ": "Lupa ",
	"password":     "kata sandi",
	"Enter the email address of your account. If it has one, we send a link to choose a new password.": "Masukkan alamat email akun Anda. Jika ada, kami mengirim tautan untuk memilih kata sandi baru.",
	"Email":           "Email",
	"Send the link":   "Kirim tautan",
	"Back to sign in": "Kembali ke halaman masuk",
	"Choose a new ":   "Pilih ",
	"New password":    "Kata sandi baru",
	"Change password": "Ubah kata sandi",

	// Email change
	"Confirm your new ": "Konfirmasi ",
	"login email":       "email login baru",

	// Messages from the handlers
	"Wrong username or password, or the account is not available. Five wrong passwords lock an account for 15 minutes.": "Username atau password salah, atau akun tidak tersedia. Lima kali salah memasukkan kata sandi mengunci akun selama 15 menit.",
	"Too many attempts. Please wait a few minutes.":                                                                     "Terlalu banyak percobaan. Harap tunggu beberapa menit.",
	"Too many requests":                                                                        "Terlalu banyak permintaan",
	"Please wait a while before trying again.":                                                 "Harap tunggu sebentar sebelum mencoba lagi.",
	"The security check failed. Please try again.":                                             "Pemeriksaan keamanan gagal. Silakan coba lagi.",
	"This account is locked for a few minutes after too many wrong passwords.":                 "Akun ini dikunci beberapa menit setelah terlalu banyak kata sandi yang salah.",
	"Enter a valid email address.":                                                             "Masukkan alamat email yang valid.",
	"Enter your full name (letters only).":                                                     "Masukkan nama lengkap Anda (hanya huruf).",
	"Enter your position.":                                                                     "Masukkan jabatan Anda.",
	"Choose your company from the list.":                                                       "Pilih perusahaan Anda dari daftar.",
	"That email domain cannot receive mail. Check the address.":                                "Domain email itu tidak dapat menerima surat. Periksa alamatnya.",
	"If this address can register, we sent a link to confirm it. The link works for 24 hours.": "Jika alamat ini dapat mendaftar, kami mengirim tautan untuk mengonfirmasinya. Tautan berlaku 24 jam.",
	"Check your email":                                                                         "Periksa email Anda",
	"Email confirmed":                                                                          "Email dikonfirmasi",
	"Thank you. A medpost administrator will review your request. You will get an email when it is decided.": "Terima kasih. Administrator pos medis akan meninjau permintaan Anda. Anda akan menerima email setelah ada keputusan.",
	"Link not valid": "Tautan tidak valid",
	"This link is invalid, has already been used, or has expired.": "Tautan ini tidak valid, sudah digunakan, atau sudah kedaluwarsa.",
	"Your login is ready": "Login Anda siap",
	"You can now sign in with your username and password.": "Sekarang Anda dapat masuk dengan username dan kata sandi Anda.",
	"That username is taken. Choose another.":              "Username itu sudah dipakai. Pilih yang lain.",
	"The two passwords are not the same.":                  "Kedua kata sandi tidak sama.",
	"If that address belongs to an active account, we have sent a link to choose a new password. It works for one hour.": "Jika alamat itu milik akun aktif, kami telah mengirim tautan untuk memilih kata sandi baru. Tautan berlaku satu jam.",
	"Password changed": "Kata sandi diubah",
	"Your password was changed and every device was signed out. You can sign in with the new password.": "Kata sandi Anda telah diubah dan semua perangkat dikeluarkan. Anda dapat masuk dengan kata sandi baru.",
	"Page not found":                    "Halaman tidak ditemukan",
	"There is nothing at this address.": "Tidak ada apa pun di alamat ini.",
	"Invalid request":                   "Permintaan tidak valid",
	"The form could not be read.":       "Formulir tidak dapat dibaca.",
	"Something went wrong.":             "Terjadi kesalahan.",
}
