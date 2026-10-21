package prefill

import (
	"context"
	"net"
	"strings"
	"time"
)

// MailServer is the SMTP/IMAP setup for a mailbox. Ports: SMTP 465 = implicit TLS,
// 587 = STARTTLS; IMAP 993 = TLS (what the email plugin speaks).
type MailServer struct {
	Provider string `json:"provider"` // id for the UI label: gmail, qq, exmail, …
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	IMAPHost string `json:"imap_host"`
	IMAPPort int    `json:"imap_port"`
	Note     string `json:"note,omitempty"` // i18n key suffix: app_password | auth_code | oauth
	Via      string `json:"via"`            // domain | mx
}

func ms(id, smtp string, sp int, imap, note string) MailServer {
	return MailServer{Provider: id, SMTPHost: smtp, SMTPPort: sp, IMAPHost: imap, IMAPPort: 993, Note: note}
}

// Values follow the providers' help pages and Mozilla's ISPDB.
var byDomain = map[string]MailServer{
	"gmail.com":      ms("gmail", "smtp.gmail.com", 465, "imap.gmail.com", "app_password"),
	"googlemail.com": ms("gmail", "smtp.gmail.com", 465, "imap.gmail.com", "app_password"),
	"qq.com":         ms("qq", "smtp.qq.com", 465, "imap.qq.com", "auth_code"),
	"foxmail.com":    ms("qq", "smtp.qq.com", 465, "imap.qq.com", "auth_code"),
	"vip.qq.com":     ms("qq", "smtp.qq.com", 465, "imap.qq.com", "auth_code"),
	"163.com":        ms("netease", "smtp.163.com", 465, "imap.163.com", "auth_code"),
	"126.com":        ms("netease", "smtp.126.com", 465, "imap.126.com", "auth_code"),
	"yeah.net":       ms("netease", "smtp.yeah.net", 465, "imap.yeah.net", "auth_code"),
	"vip.163.com":    ms("netease", "smtp.vip.163.com", 465, "imap.vip.163.com", "auth_code"),
	"outlook.com":    ms("outlook", "smtp-mail.outlook.com", 587, "outlook.office365.com", "oauth"),
	"hotmail.com":    ms("outlook", "smtp-mail.outlook.com", 587, "outlook.office365.com", "oauth"),
	"live.com":       ms("outlook", "smtp-mail.outlook.com", 587, "outlook.office365.com", "oauth"),
	"msn.com":        ms("outlook", "smtp-mail.outlook.com", 587, "outlook.office365.com", "oauth"),
	"icloud.com":     ms("icloud", "smtp.mail.me.com", 587, "imap.mail.me.com", "app_password"),
	"me.com":         ms("icloud", "smtp.mail.me.com", 587, "imap.mail.me.com", "app_password"),
	"mac.com":        ms("icloud", "smtp.mail.me.com", 587, "imap.mail.me.com", "app_password"),
	"aliyun.com":     ms("aliyun", "smtp.aliyun.com", 465, "imap.aliyun.com", ""),
	"sina.com":       ms("sina", "smtp.sina.com", 465, "imap.sina.com", "auth_code"),
	"sina.cn":        ms("sina", "smtp.sina.cn", 465, "imap.sina.cn", "auth_code"),
	"sohu.com":       ms("sohu", "smtp.sohu.com", 465, "imap.sohu.com", "auth_code"),
	"139.com":        ms("139", "smtp.139.com", 465, "imap.139.com", "auth_code"),
	"yahoo.com":      ms("yahoo", "smtp.mail.yahoo.com", 465, "imap.mail.yahoo.com", "app_password"),
	"zoho.com":       ms("zoho", "smtp.zoho.com", 465, "imap.zoho.com", ""),
	"zoho.com.cn":    ms("zoho", "smtp.zoho.com.cn", 465, "imap.zoho.com.cn", ""),
	"fastmail.com":   ms("fastmail", "smtp.fastmail.com", 465, "imap.fastmail.com", "app_password"),
	"yandex.com":     ms("yandex", "smtp.yandex.com", 465, "imap.yandex.com", "app_password"),
	"gmx.com":        ms("gmx", "mail.gmx.com", 587, "imap.gmx.com", ""),
	"proton.me":      ms("proton", "127.0.0.1", 1025, "127.0.0.1", "bridge"),
	"protonmail.com": ms("proton", "127.0.0.1", 1025, "127.0.0.1", "bridge"),
}

// Hosted mail for company domains, recognized by MX host suffix.
var byMX = []struct {
	suffix string
	srv    MailServer
}{
	{"google.com", ms("gmail", "smtp.gmail.com", 465, "imap.gmail.com", "app_password")},
	{"googlemail.com", ms("gmail", "smtp.gmail.com", 465, "imap.gmail.com", "app_password")},
	{"mail.protection.outlook.com", ms("m365", "smtp.office365.com", 587, "outlook.office365.com", "oauth")},
	{"exmail.qq.com", ms("exmail", "smtp.exmail.qq.com", 465, "imap.exmail.qq.com", "")},
	{"qq.com", ms("exmail", "smtp.exmail.qq.com", 465, "imap.exmail.qq.com", "")}, // mxbiz1.qq.com
	{"feishu.cn", ms("feishu", "smtp.feishu.cn", 465, "imap.feishu.cn", "")},
	{"larksuite.com", ms("lark", "smtp.larksuite.com", 465, "imap.larksuite.com", "")},
	{"mxhichina.com", ms("aliyun_qiye", "smtp.qiye.aliyun.com", 465, "imap.qiye.aliyun.com", "")},
	{"qiye.163.com", ms("netease_qiye", "smtp.qiye.163.com", 465, "imap.qiye.163.com", "")},
	{"zoho.com", ms("zoho", "smtp.zoho.com", 465, "imap.zoho.com", "")},
	{"zoho.com.cn", ms("zoho", "smtp.zoho.com.cn", 465, "imap.zoho.com.cn", "")},
	{"messagingengine.com", ms("fastmail", "smtp.fastmail.com", 465, "imap.fastmail.com", "app_password")},
	{"icloud.com", ms("icloud", "smtp.mail.me.com", 587, "imap.mail.me.com", "app_password")},
}

// LookupMX is replaceable in tests.
var LookupMX = func(ctx context.Context, domain string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	mx, err := net.DefaultResolver.LookupMX(ctx, domain)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(mx))
	for _, m := range mx {
		out = append(out, strings.TrimSuffix(strings.ToLower(m.Host), "."))
	}
	return out, nil
}

// EmailDomain returns the lower-cased domain of an address ("" if it isn't one).
func EmailDomain(addr string) string {
	a := strings.TrimSpace(addr)
	if i := strings.LastIndex(a, "<"); i >= 0 { // "Name <me@x.com>"
		a = strings.TrimSuffix(a[i+1:], ">")
	}
	at := strings.LastIndex(a, "@")
	if at < 1 || at == len(a)-1 || strings.ContainsAny(a, " ,;") {
		return ""
	}
	d := strings.ToLower(a[at+1:])
	if !strings.Contains(d, ".") {
		return ""
	}
	return d
}

// Mail finds the servers for an address: the known-domain table first, then the
// domain's MX records (hosted company mail).
func Mail(ctx context.Context, addr string) (MailServer, bool) {
	d := EmailDomain(addr)
	if d == "" {
		return MailServer{}, false
	}
	if s, ok := byDomain[d]; ok {
		s.Via = "domain"
		return s, true
	}
	hosts, err := LookupMX(ctx, d)
	if err != nil {
		return MailServer{}, false
	}
	for _, h := range hosts {
		for _, m := range byMX {
			if h == m.suffix || strings.HasSuffix(h, "."+m.suffix) {
				s := m.srv
				s.Via = "mx"
				return s, true
			}
		}
	}
	return MailServer{}, false
}
