package sofi

// Simplified Chinese (简体中文) for /sofi. Same idea as messages in i18n.go: the English text is the
// key. The 20 words are a plain translation of the English SOFI words; the clinic's own explanations
// exist in Indonesian only, so the Chinese words are for the medical team to read through before
// they are relied on.

// zhItems are the 20 words in form order, the same order as the English ones in buildItems.
var zhItems = [ItemCount]string{
	"工作过度", "精疲力竭", "体力耗尽", "精力被耗光",
	"出汗", "呼吸沉重", "心悸", "上气不接下气",
	"肌肉紧绷", "关节僵硬", "身体麻木", "身体酸痛",
	"对工作提不起兴趣", "不爱活动", "无精打采", "漠不关心",
	"困倦", "想马上睡觉", "工作让人昏昏欲睡", "频繁打哈欠",
}

// zhMessages maps the English key to its Chinese text.
var zhMessages = map[string]string{
	"Fatigue screening":       "疲劳筛查",
	"No account needed.":      "无需账号。",
	"1. Tick the box":         "1. 勾选方框",
	"Your name":               "您的姓名",
	"Type at least 3 letters": "请至少输入 3 个字母",
	"Date of birth":           "出生日期",
	"Pick a date":             "选择日期",
	"Sign in":                 "登录",
	"Sign out":                "退出",
	"No match":                "未找到",
	"Language":                "语言",
	"Name search is not available right now.":                            "姓名搜索暂时不可用。",
	"Choose your name from the list.":                                    "请从列表中选择您的姓名。",
	"Enter your date of birth as dd/mm/yyyy.":                            "请按 dd/mm/yyyy 格式输入出生日期。",
	"Details not found, please check and try again.":                     "未找到相关信息，请检查后重试。",
	"Check failed, please try again.":                                    "验证失败，请重试。",
	"The check could not load. Please check your connection and reload.": "验证无法加载。请检查网络连接后刷新页面。",
	"Please complete the check first.":                                   "请先完成验证。",
	"Please wait a moment and try again.":                                "请稍等片刻后重试。",
	"Something went wrong.":                                              "出错了。",
	"Too many searches, please wait a minute.":                           "搜索次数过多，请等待一分钟。",
	"Sign-in is not available right now. Please try again later.":        "暂时无法登录，请稍后再试。",
	"Please sign in first.":                                              "请先登录。",
	"Enter the weeks on site and answer every word.":                     "请填写在场周数并回答每一个词。",
	"Too many submissions, please try again later.":                      "提交次数过多，请稍后再试。",

	"Signed in as":      "当前登录：",
	"Name":              "姓名",
	"Work position":     "职位",
	"Company":           "公司",
	"Age":               "年龄",
	"years":             "岁",
	"MCU review status": "体检结果状态",
	"Weeks on site":     "在场周数",
	"Fit":               "适合工作",
	"Fit with note":     "适合工作（附说明）",
	"Unfit":             "不适合工作",
	"Temporary unfit":   "暂时不适合工作",
	"Need follow up":    "需要跟进",
	noMcuStatus:         "暂无体检结果",

	"How much does each word describe how you feel? Choose 0 to 6.": "每个词在多大程度上描述了您的感受？请选择 0 到 6。",
	"Not at all":                "完全没有",
	"To a very large extent":    "非常强烈",
	"Submit":                    "提交",
	"Please answer every word.": "请回答每一个词。",
	"Choose the weeks on site.": "请选择在场周数。",
	"More than 1 year":          "超过 1 年",
	"Choose":                    "请选择",
	"Your result":               "您的结果",
	msgAlreadyDone:              "您已填写过本期问卷。",
	"Your earlier results":      "您之前的结果",
	"Date":                      "日期",
	"Total score":               "总分",
	"Low":                       "低",
	"Moderate":                  "中",
	"High":                      "高",
	"Your fatigue is low. Keep resting well.":                                                      "您的疲劳程度较低。请继续保证充分休息。",
	"Your fatigue is moderate. Rest and drink water, and tell your supervisor if it gets worse.":   "您的疲劳程度中等。请休息并多喝水，如果加重请告知主管。",
	"Your fatigue is high. Tell your supervisor and the Medical Team before doing high-risk work.": "您的疲劳程度较高。从事高风险工作前，请告知主管和医疗团队。",
	"This is a screening result, not a diagnosis.":                                                 "这是筛查结果，不是诊断。",

	msgClosed:                           "问卷目前已关闭。",
	"The questionnaire is closed":       "问卷已关闭",
	"It opens on":                       "开放时间：",
	"Please come back then.":            "请届时再来。",
	"No opening date has been set yet.": "尚未设定开放日期。",
	"Questions? Ask the Medical Team.":  "有疑问？请咨询医疗团队。",
	msgPreviewOn:                        "预览：您的答案不会被保存。",
}
