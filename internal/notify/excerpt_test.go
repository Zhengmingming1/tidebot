package notify

import (
	"io"
	"mime"
	"mime/quotedprintable"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestAnswerExcerpt(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# 今日摘要\r\n\r\n**三条**新闻：\r\n- 第一条 [链接](https://example.com/a)\r\n- [ ] 待办\r\n- [x] 已办", "今日摘要\n\n三条新闻：\n• 第一条 链接 (https://example.com/a)\n☐ 待办\n☑ 已办"},
		{"> quoted *em* and `code` ~~gone~~\n\n---\n\n| a | b |\n|---|:-:|\n| 1 | 2 |", "quoted em and code gone\n\na · b\n1 · 2"},
		{"```bash\necho **hi**\n```\nafter", "echo **hi**\nafter"},
		{"![logo](x.png) [rel](../x.md) [js](javascript:void)", "logo rel js"},
		{"snake_case_name and 2*3*4 stay", "snake_case_name and 2*3*4 stay"},
		{"\n\n  \n", ""},
	}
	for _, c := range cases {
		if got, cut := AnswerExcerpt(c.in); got != c.want || cut {
			t.Errorf("AnswerExcerpt(%q)\n got %q (cut %v)\nwant %q", c.in, got, cut, c.want)
		}
	}
	// line cap
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("- item\n")
	}
	got, cut := AnswerExcerpt(b.String())
	if !cut || strings.Count(got, "\n") != ExcerptLines-1 {
		t.Fatalf("lines: cut=%v %q", cut, got)
	}
	// rune cap, CJK, ends with an ellipsis on a rune boundary
	got, cut = AnswerExcerpt(strings.Repeat("潮汐往复。", 200))
	if !cut || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") || utf8.RuneCountInString(got) > ExcerptRunes+1 {
		t.Fatalf("runes: cut=%v len=%d", cut, utf8.RuneCountInString(got))
	}
	// far below WeCom's 2048-byte text limit even in CJK with a title and cost line
	if len(got)+200 > 2048 {
		t.Fatalf("too long for WeCom: %d bytes", len(got))
	}
	// a short last line that doesn't fit is marked on the line before
	got, cut = AnswerExcerpt(strings.Repeat("a", ExcerptRunes-10) + "\n" + strings.Repeat("b", 30))
	if !cut || got != strings.Repeat("a", ExcerptRunes-10)+" …" {
		t.Fatalf("tail: %q", got)
	}
}

func TestDigestLine(t *testing.T) {
	if got := digestLine("运行完成：日报\n花费 $0.0100\n\n第一行\n第二行"); got != "运行完成：日报 — 花费 $0.0100 — 第一行 — 第二行" {
		t.Fatal(got)
	}
	long := digestLine("t\n" + strings.Repeat("长", 500))
	if utf8.RuneCountInString(long) != DigestLineRunes+1 || !strings.HasSuffix(long, "…") {
		t.Fatal(utf8.RuneCountInString(long))
	}
}

func TestBuildMailLongAnswer(t *testing.T) {
	body := "花费 $0.0100\n\n" + strings.Repeat("回答很长，没有换行。", 300) + "\n.\nend"
	raw := string(BuildMail("a@x", "b@y", Message{Title: "运行完成：日报", Body: body}))
	head, enc, ok := strings.Cut(raw, "\r\n\r\n")
	if !ok || !strings.Contains(head, "Content-Transfer-Encoding: quoted-printable") {
		t.Fatal(head)
	}
	subj := ""
	for _, l := range strings.Split(head, "\r\n") {
		if s, ok := strings.CutPrefix(l, "Subject: "); ok {
			subj = s
		}
	}
	if dec, err := new(mime.WordDecoder).DecodeHeader(subj); err != nil || dec != "[Tidebot] 运行完成：日报" {
		t.Fatalf("subject %q -> %q %v", subj, dec, err)
	}
	for _, l := range strings.Split(enc, "\r\n") {
		if len(l) > 78 {
			t.Fatalf("line of %d octets", len(l))
		}
	}
	if strings.Contains(strings.ReplaceAll(enc, "\r\n", ""), "\n") {
		t.Fatal("bare LF in body")
	}
	dec, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(enc)))
	if err != nil || strings.TrimRight(strings.ReplaceAll(string(dec), "\r\n", "\n"), "\n") != body {
		t.Fatalf("round trip: %v", err)
	}
}
