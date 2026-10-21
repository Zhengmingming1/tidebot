package webui

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ---- task form: name + schedule from the instruction ----

// TaskGuess is what the new-task form can fill in from the instruction text.
type TaskGuess struct {
	Name string `json:"name"`
	Cron string `json:"cron"` // "" = nothing recognized (leave the schedule alone)
}

var (
	reEveryMin  = regexp.MustCompile(`(?i)每\s*(?:隔)?\s*(\d{1,2})\s*分钟|every\s+(\d{1,2})\s*min(?:ute)?s?`)
	reHourly    = regexp.MustCompile(`(?i)每\s*(?:个)?\s*小时|每小时|hourly|every\s+hour`)
	reWeekdays  = regexp.MustCompile(`(?i)(?:每个?)?工作日|(?:每)?周一(?:到|至|-|~)周五|weekdays?|every\s+weekday|mon(?:day)?\s*(?:-|to|through)\s*fri(?:day)?`)
	reWeeklyZh  = regexp.MustCompile(`每(?:个)?(?:周|星期|礼拜)([一二三四五六日天])`)
	reWeeklyEn  = regexp.MustCompile(`(?i)every\s+(sun|mon|tue|wed|thu|fri|sat)[a-z]*`)
	reMonthly   = regexp.MustCompile(`(?i)每(?:个)?月\s*(\d{1,2})\s*[号日]|(?:monthly|every\s+month)\s+on\s+(?:the\s+)?(\d{1,2})(?:st|nd|rd|th)?|on\s+the\s+(\d{1,2})(?:st|nd|rd|th)?\s+of\s+every\s+month`)
	reDaily     = regexp.MustCompile(`(?i)每天|每日|天天|每晚|每早|每天早上|daily|every\s*day|every\s+(?:morning|evening|night)|each\s+day`)
	reTimeZh    = regexp.MustCompile(`(凌晨|早上|早晨|上午|中午|下午|傍晚|晚上|夜里)?\s*(\d{1,2})\s*(?:[点时:：])\s*(半|\d{1,2})?\s*分?`)
	reTimeEn    = regexp.MustCompile(`(?i)\bat\s+(\d{1,2})(?::(\d{2}))?\s*(am|pm|a\.m\.|p\.m\.)?|\b(\d{1,2})(?::(\d{2}))?\s*(am|pm)\b|\b(\d{1,2}):(\d{2})\b`)
	reEvening   = regexp.MustCompile(`(?i)每晚|晚上|evening|night|tonight`)
	reSchedWord = regexp.MustCompile(`(?i)^(?:请|帮我|帮忙|麻烦|please|kindly)\s*`)
	reAmPm      = regexp.MustCompile(`(?i)\b(?:am|pm|a\.m\.|p\.m\.)(?:\s|$)`)
	reLeadPrep  = regexp.MustCompile(`(?i)^(?:at|on|in)\s+`)
	reSplit     = regexp.MustCompile(`[。．!！?？;；\n,，、：]|\.(?:\s|$)`)
)

var zhDow = map[string]int{"日": 0, "天": 0, "一": 1, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6}
var enDow = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// InferTask guesses a short task name and a cron schedule from the instruction.
// The schedule is only filled when a frequency word is present ("每天", "every Monday"…);
// a bare time like "9 点" alone is not enough.
func InferTask(prompt string) TaskGuess {
	p := strings.TrimSpace(prompt)
	if p == "" {
		return TaskGuess{}
	}
	if len(p) > 4000 {
		p = p[:4000]
	}
	g := TaskGuess{Cron: inferCron(p)}
	g.Name = inferName(p)
	return g
}

func inferCron(p string) string {
	h, m, hasTime := inferTime(p)
	if !hasTime {
		h, m = 9, 0
		if reEvening.MatchString(p) {
			h = 21
		}
	}
	switch {
	case reEveryMin.MatchString(p):
		sm := reEveryMin.FindStringSubmatch(p)
		n, _ := strconv.Atoi(sm[1] + sm[2])
		if n < 1 || n > 59 {
			return ""
		}
		return "*/" + strconv.Itoa(n) + " * * * *"
	case reHourly.MatchString(p):
		return strconv.Itoa(m) + " * * * *"
	case reMonthly.MatchString(p):
		sm := reMonthly.FindStringSubmatch(p)
		d, _ := strconv.Atoi(sm[1] + sm[2] + sm[3])
		if d < 1 || d > 28 {
			return ""
		}
		return strconv.Itoa(m) + " " + strconv.Itoa(h) + " " + strconv.Itoa(d) + " * *"
	case reWeekdays.MatchString(p):
		return strconv.Itoa(m) + " " + strconv.Itoa(h) + " * * 1-5"
	case reWeeklyZh.MatchString(p):
		return strconv.Itoa(m) + " " + strconv.Itoa(h) + " * * " + strconv.Itoa(zhDow[reWeeklyZh.FindStringSubmatch(p)[1]])
	case reWeeklyEn.MatchString(p):
		return strconv.Itoa(m) + " " + strconv.Itoa(h) + " * * " + strconv.Itoa(enDow[strings.ToLower(reWeeklyEn.FindStringSubmatch(p)[1])])
	case reDaily.MatchString(p):
		return strconv.Itoa(m) + " " + strconv.Itoa(h) + " * * *"
	}
	return ""
}

func inferTime(p string) (h, m int, ok bool) {
	if sm := reTimeZh.FindStringSubmatch(p); sm != nil {
		h, _ = strconv.Atoi(sm[2])
		switch sm[3] {
		case "":
		case "半":
			m = 30
		default:
			m, _ = strconv.Atoi(sm[3])
		}
		switch sm[1] {
		case "下午", "傍晚", "晚上", "夜里":
			if h < 12 {
				h += 12
			}
		case "中午":
			if h < 6 {
				h += 12
			}
		}
		return h % 24, m % 60, h < 24 && m < 60
	}
	if sm := reTimeEn.FindStringSubmatch(p); sm != nil {
		hs, ms, ap := sm[1], sm[2], strings.ToLower(sm[3])
		if hs == "" {
			hs, ms, ap = sm[4], sm[5], strings.ToLower(sm[6])
		}
		if hs == "" {
			hs, ms = sm[7], sm[8]
		}
		h, _ = strconv.Atoi(hs)
		m, _ = strconv.Atoi(ms)
		if strings.HasPrefix(ap, "p") && h < 12 {
			h += 12
		}
		if strings.HasPrefix(ap, "a") && h == 12 {
			h = 0
		}
		if h > 23 || m > 59 {
			return 0, 0, false
		}
		return h, m, true
	}
	return 0, 0, false
}

// inferName: the first clause with schedule words and polite openers removed,
// cut to 24 characters (CJK) or 6 words.
var dangling = map[string]bool{"a": true, "an": true, "the": true, "into": true, "to": true, "of": true, "for": true, "and": true, "or": true, "with": true, "in": true, "on": true, "from": true, "by": true, "at": true, "my": true, "our": true}

func inferName(p string) string {
	strip := func(s string) string {
		for _, re := range []*regexp.Regexp{reEveryMin, reHourly, reMonthly, reWeekdays, reWeeklyZh, reWeeklyEn, reDaily, reTimeEn, reTimeZh} {
			s = re.ReplaceAllString(s, " ")
		}
		s = reAmPm.ReplaceAllString(s, " ")
		s = reSchedWord.ReplaceAllString(strings.TrimSpace(s), "")
		s = reLeadPrep.ReplaceAllString(s, "")
		s = strings.TrimLeft(strings.TrimSpace(s), "的在,， ")
		return strings.Join(strings.Fields(s), " ")
	}
	var name string
	for _, part := range reSplit.Split(p, -1) {
		if n := strip(part); utf8.RuneCountInString(n) >= 2 {
			name = n
			break
		}
	}
	if name == "" {
		return ""
	}
	if cjk(name) {
		if r := []rune(name); len(r) > 24 {
			name = string(r[:24])
		}
	} else {
		w := strings.Fields(name)
		if len(w) > 6 {
			w = w[:6]
		}
		// A cut at six words shouldn't end on "into a" / "for the".
		for len(w) > 1 && dangling[strings.ToLower(w[len(w)-1])] {
			w = w[:len(w)-1]
		}
		name = strings.Join(w, " ")
		if len(name) > 0 {
			name = strings.ToUpper(name[:1]) + name[1:]
		}
		if utf8.RuneCountInString(name) > 48 {
			name = string([]rune(name)[:48])
		}
	}
	return strings.TrimSpace(name)
}

func cjk(s string) bool {
	for _, r := range s {
		if r >= 0x3000 {
			return true
		}
	}
	return false
}

// handleTaskInfer: POST /api/tasks/infer {prompt} → TaskGuess. POST keeps the prompt out of URLs and logs.
func (s *Server) handleTaskInfer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Prompt   string `json:"prompt"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
		Cron     string `json:"cron"` // the form's current schedule (for runs per day)
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		writeErr(w, 400, "bad json")
		return
	}
	g := InferTask(body.Prompt)
	out := map[string]any{"name": g.Name, "cron": g.Cron}
	if body.Provider != "" {
		n := body.Name
		if n == "" {
			n = g.Name
		}
		c := body.Cron
		if c == "" && g.Cron != "" {
			c = g.Cron
		}
		out["budget"] = s.suggestBudget(r.Context(), n, body.Prompt, body.Provider, c)
	}
	writeJSON(w, 200, out)
}

func postOnly(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeErr(w, 405, "POST only")
			return
		}
		fn(w, r)
	}
}
