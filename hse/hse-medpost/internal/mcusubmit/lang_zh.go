package mcusubmit

// simplifiedChineseText maps the same English phrases as indonesianText to Simplified Chinese
// (owner, 2026-11-05: a Chinese switch for the submitter portal). Same rules: keys are exactly what
// the template or handler writes, split headings are separate entries (a trailing space in a key
// is kept so the entry reads like the template), and a phrase missing here simply stays English.
// Like the Indonesian text, this is written for plain office readers, not as legal text, and the
// owner should have a Chinese reader go through it, the ROMIF statement especially.
var simplifiedChineseText = map[string]string{
	// ROMIF on the sign-up page
	"Release of medical information (ROMIF)": "医疗信息披露同意书(ROMIF)",
	"By requesting this account I understand that the Medical Team of the PT Layar Nusantara Gas project area will store and review the medical checkup results I send, and that a worker's health information may be shared with company management or authorised workplace representatives when reasonably needed for workplace safety, company policy or other legitimate medical purposes.": "申请此账户即表示我理解：PT Layar Nusantara Gas 项目区域的医疗团队将保存并审阅我发送的体检结果；为了工作场所安全、公司政策或其他正当的医疗目的，在合理必要时，员工的健康信息可能会提供给公司管理层或经授权的工作场所代表。",
	"I confirm that the workers whose results I send know about this and agree to it, and that my company has authorised me to send them.":                                                                      "我确认，我所发送结果的员工已知晓并同意此事，且我所在的公司已授权我发送这些结果。",
	"I also understand that the information may be shared with other healthcare facilities for referral or continuity of care, in line with the Medical Team's policies and the law that applies in Indonesia.": "我也理解，为了转诊或延续治疗，这些信息可能会提供给其他医疗机构，并遵循医疗团队的政策以及印度尼西亚适用的法律。",
	"If you do not agree, do not request an account.":                                          "如果您不同意，请勿申请账户。",
	"I have read the statement above and I agree.":                                             "我已阅读以上声明并表示同意。",
	"Sign by typing your full name":                                                            "请输入您的全名作为签名",
	"It must match the full name above. It is saved with the date and time as your signature.": "必须与上方填写的全名一致。它将连同日期和时间一起保存，作为您的签名。",
	"Read the ROMIF statement and tick the box to agree.":                                      "请阅读 ROMIF 声明并勾选方框表示同意。",
	"Sign by typing your full name exactly as entered above.":                                  "请按上方填写的全名原样输入作为签名。",
	"Release of medical ":                                                                      "医疗信息",
	"information":                                                                              "披露同意书",
	"Please read and sign this once to keep using the portal.":                                 "请阅读并签署一次，以便继续使用本门户。",
	"Sign and continue":                                                                        "签署并继续",
	"Withdraw this document? The medical team will no longer see it and the file is deleted. You can upload a corrected one in a new batch.": "撤回此文件？医疗团队将不再看到它，文件会被删除。您可以在新的批次中上传更正后的文件。",

	// Header and shared
	"Skip to content":            "跳到正文",
	"Main navigation":            "主导航",
	"Home":                       "首页",
	"New batch":                  "新建批次",
	"My documents":               "我的文件",
	"Settings":                   "设置",
	"Admin":                      "管理",
	"Sign out":                   "退出",
	"Sign in":                    "登录",
	"Request an account":         "申请账户",
	"Medical checkup submission": "体检结果提交",
	"Error:":                     "错误：",
	"Back to Home":               "返回首页",
	"Language":                   "语言",

	// Landing
	"Medical checkup ": "体检结果",
	"submission":       "提交",
	"Send a worker's medical checkup result to the medical team as a PDF, and follow what happens to it.": "以 PDF 形式向医疗团队发送员工的体检结果，并跟踪处理进度。",
	"How it works":        "使用方法",
	"Request an account.": "申请账户。",
	"Confirm your email; a medical team administrator approves it.": "确认您的邮箱；医疗团队管理员会审批。",
	"Choose a login.": "设置登录信息。",
	"You get a link to pick a username and password.": "您会收到一个链接，用来设置用户名和密码。",
	"Send results.": "发送结果。",
	"One PDF per worker. You can see the status of each one.": "每位员工一份 PDF。您可以查看每份文件的状态。",

	// Register
	"Request an ": "申请",
	"account":     "账户",
	"For company HR contacts and clinics who send medical checkup results.": "适用于发送体检结果的公司人事联系人和诊所。",
	"Full name":  "全名",
	"Work email": "工作邮箱",
	"We send a link to confirm this address.": "我们会发送一个链接来确认此邮箱地址。",
	"Company":             "公司",
	"Choose your company": "请选择您的公司",
	"Company not listed? Ask the medical team administrator to add it.": "没有找到您的公司？请联系医疗团队管理员添加。",
	"Position":              "职位",
	"Request account":       "申请账户",
	"Already have a login?": "已有登录账号？",

	// Confirm email
	"Confirm your ": "确认",
	"email":         "邮箱",
	"Press the button to confirm that this address is yours.": "请点击按钮，确认此邮箱地址属于您。",
	"Confirm": "确认",

	// Sign in
	"Sign ":                             "登录",
	"in":                                "",
	"Medical checkup submission portal": "体检结果提交门户",
	"Username":                          "用户名",
	"Password":                          "密码",
	"Forgot your password?":             "忘记密码？",
	"No login yet?":                     "还没有登录账号？",

	// Setup
	"Create your ": "创建",
	"login":        "登录账号",
	"Your request was approved. Choose a username and a password.":                  "您的申请已通过。请设置用户名和密码。",
	"3 to 40 letters, digits, dots or underscores.":                                 "3 到 40 个字母、数字、点或下划线。",
	"At least 6 characters. Not your username or email, and not a common password.": "至少 6 个字符。不能是您的用户名或邮箱，也不能是常见密码。",
	"Repeat the password": "再次输入密码",
	"Create login":        "创建登录账号",

	// Forgot / reset
	"Forgot your ": "忘记",
	"password":     "密码",
	"Enter the email address of your account. If it has one, we send a link to choose a new password.": "请输入您账户的邮箱地址。如果该账户存在，我们会发送一个用于设置新密码的链接。",
	"Email":           "邮箱",
	"Send the link":   "发送链接",
	"Back to sign in": "返回登录",
	"Choose a new ":   "设置新",
	"New password":    "新密码",
	"Change password": "修改密码",

	// Email change
	"Confirm your new ": "确认新的",
	"login email":       "登录邮箱",

	// Messages from the handlers
	"Wrong username or password, or the account is not available. Five wrong passwords lock an account for 15 minutes.": "用户名或密码错误，或账户不可用。连续输错五次密码，账户将被锁定 15 分钟。",
	"Too many attempts. Please wait a few minutes.":                                                                     "尝试次数过多，请等待几分钟。",
	"Too many requests":                                                                        "请求过多",
	"Please wait a while before trying again.":                                                 "请稍等片刻再试。",
	"The security check failed. Please try again.":                                             "安全验证失败，请重试。",
	"This account is locked for a few minutes after too many wrong passwords.":                 "由于密码输错次数过多，此账户已被锁定几分钟。",
	"Enter a valid email address.":                                                             "请输入有效的邮箱地址。",
	"Enter your full name (letters only).":                                                     "请输入您的全名(仅限字母)。",
	"Enter your position.":                                                                     "请输入您的职位。",
	"Choose your company from the list.":                                                       "请从列表中选择您的公司。",
	"That email domain cannot receive mail. Check the address.":                                "该邮箱域名无法接收邮件，请检查地址。",
	"If this address can register, we sent a link to confirm it. The link works for 24 hours.": "如果此地址可以注册，我们已发送确认链接。链接 24 小时内有效。",
	"Check your email":                                                                         "请查收邮件",
	"Email confirmed":                                                                          "邮箱已确认",
	"Thank you. A medpost administrator will review your request. You will get an email when it is decided.": "谢谢。医疗团队管理员将审核您的申请，有结果时您会收到邮件。",
	"Link not valid": "链接无效",
	"This link is invalid, has already been used, or has expired.": "此链接无效、已被使用或已过期。",
	"Your login is ready": "您的登录账号已就绪",
	"You can now sign in with your username and password.": "现在您可以使用用户名和密码登录了。",
	"That username is taken. Choose another.":              "该用户名已被占用，请换一个。",
	"The two passwords are not the same.":                  "两次输入的密码不一致。",
	"If that address belongs to an active account, we have sent a link to choose a new password. It works for one hour.": "如果该地址属于有效账户，我们已发送设置新密码的链接。链接 1 小时内有效。",
	"Password changed": "密码已修改",
	"Your password was changed and every device was signed out. You can sign in with the new password.": "您的密码已修改，所有设备均已退出登录。您可以使用新密码登录。",
	"Page not found":                    "页面未找到",
	"There is nothing at this address.": "此地址没有内容。",
	"Invalid request":                   "无效的请求",
	"The form could not be read.":       "无法读取表单。",
	"This company already has two accounts. Ask your colleague to sign in, or ask the medical team administrator.": "该公司已有两个账户。请联系您的同事登录，或联系医疗团队管理员。",
	"Something went wrong.": "出错了。",
}
