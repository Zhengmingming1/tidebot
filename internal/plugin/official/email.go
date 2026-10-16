package official

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

// TLSConfig is used for SMTP and IMAP (tests swap in their own roots).
var TLSConfig = func(host string) *tls.Config { return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12} }

func parseRecipients(s string) ([]string, error) {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		a, err := mail.ParseAddress(p)
		if err != nil {
			return nil, fmt.Errorf("bad address %q", p)
		}
		out = append(out, a.Address)
	}
	if len(out) == 0 {
		return nil, errors.New("no recipients")
	}
	if len(out) > 50 {
		return nil, errors.New("at most 50 recipients")
	}
	return out, nil
}

// BuildEmail renders a UTF-8 plain-text message with encoded headers.
func BuildEmail(from string, to []string, subject, body string, now time.Time) []byte {
	clean := func(s string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(s) }
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\nX-Mailer: tidebot\r\n\r\n",
		clean(from), clean(strings.Join(to, ", ")), mime.QEncoding.Encode("utf-8", clean(subject)), now.Format(time.RFC1123Z))
	body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	b.WriteString(body)
	b.WriteString("\r\n")
	return []byte(b.String())
}

// SendEmail sends over SMTP with TLS required: implicit TLS on 465, STARTTLS otherwise
// (a server that does not offer STARTTLS is refused, never downgraded to plain text).
func SendEmail(ctx context.Context, cfg map[string]any, to, subject, body string) error {
	if err := need(cfg, "smtp_host", "from"); err != nil {
		return errors.New("set SMTP host and From in the plugin settings")
	}
	host := str(cfg, "smtp_host")
	port := int(num(cfg, "smtp_port", 465))
	if to == "" {
		to = str(cfg, "default_to")
	}
	rcpt, err := parseRecipients(to)
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(str(cfg, "from"))
	if err != nil {
		return errors.New("bad From address")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	d := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	if port == 465 {
		conn, err = tls.DialWithDialer(d, "tcp", addr, TLSConfig(host))
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("connect %s: %v", addr, err)
	}
	_ = conn.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if port != 465 {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("server does not offer STARTTLS; refusing to send without TLS")
		}
		if err := c.StartTLS(TLSConfig(host)); err != nil {
			return fmt.Errorf("starttls: %v", err)
		}
	}
	if u := str(cfg, "smtp_username"); u != "" {
		if err := c.Auth(smtp.PlainAuth("", u, str(cfg, "smtp_password"), host)); err != nil {
			return errors.New("SMTP login failed")
		}
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	for _, r := range rcpt {
		if err := c.Rcpt(r); err != nil {
			return fmt.Errorf("rcpt %s: %v", r, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(BuildEmail(from.String(), rcpt, subject, body, time.Now())); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// MailMatch applies the trigger filters (case-insensitive substring).
func MailMatch(cfg map[string]any, from, subject, body string) bool {
	has := func(hay, needle string) bool {
		return needle == "" || strings.Contains(strings.ToLower(hay), strings.ToLower(strings.TrimSpace(needle)))
	}
	return has(from, str(cfg, "from_contains")) && has(subject, str(cfg, "subject_contains")) && has(subject+"\n"+body, str(cfg, "keyword"))
}

func pollIMAP(ctx context.Context, cfg map[string]any, st *State, first bool) ([]Item, error) {
	if err := need(cfg, "imap_host", "imap_username"); err != nil {
		return nil, errors.New("set IMAP host and username in the plugin settings")
	}
	host := str(cfg, "imap_host")
	addr := net.JoinHostPort(host, strconv.Itoa(int(num(cfg, "imap_port", 993))))
	c, err := client.DialWithDialerTLS(&net.Dialer{Timeout: 20 * time.Second}, addr, TLSConfig(host))
	if err != nil {
		return nil, fmt.Errorf("connect %s (TLS): %v", addr, err)
	}
	c.Timeout = 60 * time.Second
	defer c.Logout()
	if err := c.Login(str(cfg, "imap_username"), str(cfg, "imap_password")); err != nil {
		return nil, errors.New("IMAP login failed")
	}
	folder := firstNonEmpty(str(cfg, "folder"), "INBOX")
	mb, err := c.Select(folder, true)
	if err != nil {
		return nil, fmt.Errorf("select %s: %v", folder, err)
	}
	if st.preview {
		first = false
		if mb.UidNext > 20 {
			st.UID = mb.UidNext - 20
		}
		st.UIDValidity = mb.UidValidity
	}
	if first || st.UIDValidity != mb.UidValidity {
		st.UIDValidity = mb.UidValidity
		if mb.UidNext > 0 {
			st.UID = mb.UidNext - 1
		}
		return nil, nil
	}
	seq := new(imap.SeqSet)
	seq.AddRange(st.UID+1, 0)
	sec := &imap.BodySectionName{Peek: true, BodyPartName: imap.BodyPartName{Specifier: imap.TextSpecifier}, Partial: []int{0, 4000}}
	ch := make(chan *imap.Message, 20)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(seq, []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, sec.FetchItem()}, ch)
	}()
	var out []Item
	maxUID := st.UID
	for m := range ch {
		if m.Uid <= st.UID { // "n:*" always returns the last message
			continue
		}
		if m.Uid > maxUID {
			maxUID = m.Uid
		}
		var from, subject, date string
		if e := m.Envelope; e != nil {
			subject = e.Subject
			date = e.Date.UTC().Format(time.RFC3339)
			if len(e.From) > 0 {
				from = e.From[0].Address()
				if e.From[0].PersonalName != "" {
					from = e.From[0].PersonalName + " <" + from + ">"
				}
			}
		}
		body := ""
		if r := m.GetBody(sec); r != nil {
			b, _ := io.ReadAll(io.LimitReader(r, 8000))
			body = string(b)
		}
		if MailMatch(cfg, from, subject, body) && len(out) < 20 {
			out = append(out, Item{"uid": m.Uid, "from": from, "subject": subject, "date": date, "snippet": snippet(body, 1500)})
		}
	}
	if err := <-done; err != nil {
		return nil, err
	}
	if !st.preview {
		st.UID = maxUID
	}
	return out, nil
}

func init() {
	register(&Runtime{
		Name:     "email",
		Poll:     pollIMAP,
		Interval: func(cfg map[string]any) time.Duration { return minutes(cfg, "interval_minutes", 5, 1) },
		Tools: func(cfg map[string]any) []Tool {
			return []Tool{{Name: "send_email", Description: "Send a plain-text email over SMTP (TLS). Recipients default to the plugin's default_to.",
				InputSchema: schema(map[string]any{"to": map[string]any{"type": "string", "description": "Comma-separated addresses (optional)"},
					"subject": map[string]any{"type": "string"}, "body": map[string]any{"type": "string"}}, "subject", "body"),
				Call: func(a map[string]any) (string, error) {
					ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
					defer cancel()
					if err := SendEmail(ctx, cfg, str(a, "to"), str(a, "subject"), str(a, "body")); err != nil {
						return "", err
					}
					return "sent", nil
				}}}
		},
		Test: func(ctx context.Context, cfg map[string]any) (string, error) {
			return "sent", SendEmail(ctx, cfg, "", "tidebot 测试邮件 / test", "这是一封测试邮件。This is a test message from tidebot.")
		},
	})
}
