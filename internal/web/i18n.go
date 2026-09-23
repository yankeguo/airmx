package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// langCookieName stores the user's explicit language choice; it overrides
// Accept-Language negotiation.
const langCookieName = "airmx_lang"

const defaultLang = "zh"

// catalog holds every user-visible string of the web UI in one language.
type catalog struct {
	Lang string // "zh" or "en"

	// nav
	Logout     string
	LangSwitch string // label of the link switching to the other language

	// message list
	Inbox      string
	ListCount  string // printf format: total
	ColSubject string
	ColFrom    string
	ColDate    string
	SpamBadge  string
	NoSubject  string
	Empty      string
	PrevPage   string
	NextPage   string
	PageInfo   string // printf format: page, pages, total

	// web push toggle (also consumed by push.js via data attributes)
	PushToggle        string
	PushOn            string
	PushOff           string
	PushErrDenied     string
	PushErrDeniedHint string
	PushErrFailed     string

	// shared modal
	ModalOK     string
	ModalCancel string

	// login
	LoginTitle        string
	LoginHeading      string
	LoginHint         string
	Username          string
	Password          string
	ShowPassword      string
	HidePassword      string
	SignIn            string
	ErrInvalidRequest string
	ErrBadCredentials string

	// message view
	Back               string
	ToLabel            string
	TabText            string
	Delete             string
	ConfirmDeleteTitle string
	ConfirmDelete      string
	NoTextContent      string
	Attachments        string
}

var catalogs = map[string]catalog{
	"zh": {
		Lang:               "zh",
		Logout:             "退出",
		LangSwitch:         "EN",
		Inbox:              "收件箱",
		ListCount:          "共 %d 封",
		ColSubject:         "主题",
		ColFrom:            "发件人",
		ColDate:            "日期",
		SpamBadge:          "垃圾",
		NoSubject:          "(无主题)",
		Empty:              "没有邮件",
		PrevPage:           "上一页",
		NextPage:           "下一页",
		PageInfo:           "第 %d / %d 页 · 共 %d 封",
		PushToggle:         "Web 通知",
		PushOn:             "打开 Web 通知",
		PushOff:            "关闭 Web 通知",
		PushErrDenied:      "通知权限被拒绝",
		PushErrDeniedHint:  "请在浏览器设置里允许本站通知，然后再试一次。",
		PushErrFailed:      "操作失败",
		ModalOK:            "确定",
		ModalCancel:        "取消",
		LoginTitle:         "登录 - AirMX",
		LoginHeading:       "AirMX 邮箱",
		LoginHint:          "登录以查看你的邮件",
		Username:           "用户名",
		Password:           "密码",
		ShowPassword:       "显示密码",
		HidePassword:       "隐藏密码",
		SignIn:             "登录",
		ErrInvalidRequest:  "请求无效",
		ErrBadCredentials:  "用户名或密码错误",
		Back:               "返回",
		ToLabel:            "发给",
		TabText:            "纯文本",
		Delete:             "删除",
		ConfirmDeleteTitle: "删除这封邮件",
		ConfirmDelete:      "删除后无法恢复。",
		NoTextContent:      "(无法提取纯文本内容)",
		Attachments:        "附件",
	},
	"en": {
		Lang:               "en",
		Logout:             "Log out",
		LangSwitch:         "中文",
		Inbox:              "Inbox",
		ListCount:          "%d messages",
		ColSubject:         "Subject",
		ColFrom:            "From",
		ColDate:            "Date",
		SpamBadge:          "Spam",
		NoSubject:          "(no subject)",
		Empty:              "No mail",
		PrevPage:           "Prev",
		NextPage:           "Next",
		PageInfo:           "Page %d of %d · %d messages",
		PushToggle:         "Notifications",
		PushOn:             "Enable notifications",
		PushOff:            "Disable notifications",
		PushErrDenied:      "Notification permission denied",
		PushErrDeniedHint:  "Allow notifications for this site in your browser settings, then try again.",
		PushErrFailed:      "Operation failed",
		ModalOK:            "OK",
		ModalCancel:        "Cancel",
		LoginTitle:         "Log in - AirMX",
		LoginHeading:       "AirMX Mail",
		LoginHint:          "Sign in to read your mail",
		Username:           "Username",
		Password:           "Password",
		ShowPassword:       "Show password",
		HidePassword:       "Hide password",
		SignIn:             "Log in",
		ErrInvalidRequest:  "Invalid request",
		ErrBadCredentials:  "Invalid username or password",
		Back:               "Back",
		ToLabel:            "To",
		TabText:            "Plain text",
		Delete:             "Delete",
		ConfirmDeleteTitle: "Delete this message",
		ConfirmDelete:      "This cannot be undone.",
		NoTextContent:      "(could not extract plain text)",
		Attachments:        "Attachments",
	},
}

// catalogFor resolves the UI language for a request: explicit cookie choice
// first, then Accept-Language, then the default.
func catalogFor(r *http.Request) catalog {
	if c, err := r.Cookie(langCookieName); err == nil {
		if cat, ok := catalogs[c.Value]; ok {
			return cat
		}
	}
	if l := negotiateLang(r.Header.Get("Accept-Language")); l != "" {
		return catalogs[l]
	}
	return catalogs[defaultLang]
}

// negotiateLang picks "zh" or "en" from an Accept-Language header by
// q-value; it returns "" when neither appears.
func negotiateLang(header string) string {
	bestLang := ""
	bestQ := -1.0
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		tag, q := part, 1.0
		if i := strings.IndexByte(part, ';'); i >= 0 {
			tag = strings.TrimSpace(part[:i])
			q = 0
			for _, p := range strings.Split(part[i+1:], ";") {
				if p = strings.TrimSpace(p); strings.HasPrefix(p, "q=") {
					q, _ = strconv.ParseFloat(p[2:], 64)
				}
			}
		}
		var lang string
		switch tag = strings.ToLower(tag); {
		case strings.HasPrefix(tag, "zh"):
			lang = "zh"
		case strings.HasPrefix(tag, "en"):
			lang = "en"
		default:
			continue
		}
		if q > bestQ {
			bestQ, bestLang = q, lang
		}
	}
	return bestLang
}

// handleLang records the language choice from /lang?set=zh|en and redirects
// back to the referring page (same host only).
func (s *Server) handleLang(w http.ResponseWriter, r *http.Request) {
	l := r.URL.Query().Get("set")
	if _, ok := catalogs[l]; !ok {
		l = defaultLang
	}
	http.SetCookie(w, &http.Cookie{
		Name:     langCookieName,
		Value:    l,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	back := "/"
	if ref := r.Referer(); ref != "" {
		if u, err := url.Parse(ref); err == nil && u.Host == r.Host {
			back = u.RequestURI()
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
