package wecom

import (
	"ai-pm-system/internal/notify"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testCorp   = "ww0123456789abcdef"
	testSecret = "s3cret-value"
	testToken  = "QDG6eK"
	testAESKey = "jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAj3ykkR6q2B2C"
)

// fakeAPI is a minimal WeCom API. Handlers return the JSON reply.
type fakeAPI struct {
	*httptest.Server
	mu       sync.Mutex
	issued   int
	valid    string
	gettoken func(q url.Values) string
	send     func(body map[string]any) string
	sends    []map[string]any
	updates  chan map[string]any
	userinfo func(code string) string
}

func newFake(t *testing.T) *fakeAPI {
	f := &fakeAPI{updates: make(chan map[string]any, 8)}
	f.send = func(body map[string]any) string {
		return `{"errcode":0,"errmsg":"ok","invaliduser":"","msgid":"msg1","response_code":"rc1"}`
	}
	f.userinfo = func(code string) string {
		switch code {
		case "good":
			return `{"errcode":0,"errmsg":"ok","userid":"lin","user_ticket":"t"}`
		case "legacy":
			return `{"errcode":0,"errmsg":"ok","UserId":"old"}`
		case "outsider":
			return `{"errcode":0,"errmsg":"ok","openid":"o1","external_userid":"wm1"}`
		}
		return `{"errcode":40029,"errmsg":"invalid code"}`
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		q := r.URL.Query()
		if r.URL.Path == "/cgi-bin/gettoken" {
			if f.gettoken != nil {
				fmt.Fprint(w, f.gettoken(q))
				return
			}
			if q.Get("corpid") != testCorp || q.Get("corpsecret") != testSecret {
				fmt.Fprint(w, `{"errcode":40001,"errmsg":"invalid credential"}`)
				return
			}
			f.issued++
			f.valid = fmt.Sprintf("tok%d", f.issued)
			fmt.Fprintf(w, `{"errcode":0,"errmsg":"ok","access_token":%q,"expires_in":7200}`, f.valid)
			return
		}
		if q.Get("access_token") == "" || q.Get("access_token") != f.valid {
			fmt.Fprint(w, `{"errcode":42001,"errmsg":"access_token expired"}`)
			return
		}
		var body map[string]any
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("%s: bad JSON: %v", r.URL.Path, err)
			}
		}
		switch r.URL.Path {
		case "/cgi-bin/message/send":
			f.sends = append(f.sends, body)
			fmt.Fprint(w, f.send(body))
		case "/cgi-bin/message/update_template_card":
			f.updates <- body
			fmt.Fprint(w, `{"errcode":0,"errmsg":"ok","invaliduser":[]}`)
		case "/cgi-bin/auth/getuserinfo":
			fmt.Fprint(w, f.userinfo(q.Get("code")))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

// locked runs fn while the server is not handling a request.
func (f *fakeAPI) locked(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

func (f *fakeAPI) lastSend(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		t.Fatal("no message sent")
	}
	return f.sends[len(f.sends)-1]
}

func testValues(api string) map[string]string {
	return map[string]string{"corp_id": testCorp, "agent_id": "1000002", "secret": testSecret, "token": testToken, "aes_key": testAESKey, "api_base": api}
}

func newTestChannel(t *testing.T) (*channel, *fakeAPI) {
	t.Helper()
	f := newFake(t)
	c, err := open(notify.Config{Values: testValues(f.URL), PublicURL: "https://pm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	return c, f
}

// get walks decoded JSON by object keys and array indexes.
func get(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			a, _ := v.([]any)
			if k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func TestSpecAndOpen(t *testing.T) {
	s := Spec()
	if s.Label != "企业微信" || len(s.Fields) != 6 {
		t.Fatalf("spec = %+v", s)
	}
	for _, f := range s.Fields {
		if f.Hint == "" {
			t.Errorf("%s has no hint", f.Key)
		}
		if secret := f.Key == "secret" || f.Key == "token" || f.Key == "aes_key"; f.Secret != secret || f.Required != (f.Key != "api_base") {
			t.Errorf("%s: secret=%v required=%v", f.Key, f.Secret, f.Required)
		}
	}
	setup := map[string]string{}
	for _, f := range s.Setup("https://pm.example.com:8443") {
		setup[f.Label] = f.Value
	}
	if setup["接收消息 URL"] != "https://pm.example.com:8443/integrations/wecom/callback" || setup["应用主页"] != "https://pm.example.com:8443/integrations/wecom/entry" ||
		setup["网页授权及 JS-SDK 可信域名"] != "pm.example.com:8443" || setup["企业微信授权登录 Web 回调域名"] != "pm.example.com:8443" || setup["企业可信 IP"] == "" {
		t.Fatalf("setup = %v", setup)
	}

	ch, err := s.Open(notify.Config{Values: testValues(""), PublicURL: "https://pm.example.com"})
	if err != nil || ch.(*channel).api != defaultAPI || ch.(*channel).agentID != 1000002 {
		t.Fatalf("open: %v %+v", err, ch)
	}
	for _, tc := range []struct{ key, value, public, want string }{
		{"corp_id", " ", "", "企业 ID"},
		{"agent_id", "abc", "", "AgentId"},
		{"agent_id", "0", "", "AgentId"},
		{"secret", "", "", "Secret"},
		{"token", "", "", "Token"},
		{"aes_key", "short", "", "EncodingAESKey"},
		{"aes_key", testAESKey[:42] + "!", "", "EncodingAESKey"},
		{"api_base", "ftp://proxy", "", "接口地址"},
		{"api_base", "proxy.internal", "", "接口地址"},
		{"", "", "pm.example.com", "public_url"},
		{"", "", "https://user:pw@pm.example.com", "public_url"},
	} {
		v := testValues("")
		if tc.key != "" {
			v[tc.key] = tc.value
		}
		public := "https://pm.example.com"
		if tc.public != "" {
			public = tc.public
		}
		ch, err := s.Open(notify.Config{Values: v, PublicURL: public})
		if err == nil || !strings.Contains(err.Error(), tc.want) || ch != nil {
			t.Errorf("%s=%q public=%q: err = %v, want %q", tc.key, tc.value, public, err, tc.want)
		}
	}
}

func TestTokenCachedAndRefreshed(t *testing.T) {
	c, f := newTestChannel(t)
	ctx := context.Background()
	card := notify.Card{ID: "d1", Title: "待办", URL: "https://pm.example.com/x"}
	for i := 0; i < 2; i++ {
		if _, err := c.Send(ctx, "lin", card); err != nil {
			t.Fatal(err)
		}
	}
	issued := func() (n int) { f.locked(func() { n = f.issued }); return }
	if n := issued(); n != 1 {
		t.Fatalf("gettoken called %d times, want 1", n)
	}
	f.locked(func() { f.valid = "" }) // the server expired the token early
	if _, err := c.Send(ctx, "lin", card); err != nil {
		t.Fatal(err)
	}
	if n := issued(); n != 2 {
		t.Fatalf("issued=%d after 42001", n)
	}
	base := time.Now()
	c.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, err := c.Send(ctx, "lin", card); err != nil || issued() != 3 {
		t.Fatalf("expired cache: err=%v issued=%d", err, issued())
	}

	// A token rejected even after a refresh is retried only once.
	calls := 0
	f.locked(func() {
		f.send = func(map[string]any) string { calls++; return `{"errcode":40014,"errmsg":"invalid access_token"}` }
	})
	_, err := c.Send(ctx, "lin", card)
	f.locked(func() {
		if err == nil || !strings.Contains(err.Error(), "Secret") || calls != 2 {
			t.Fatalf("err=%v calls=%d", err, calls)
		}
	})
}

func TestSendButtonCard(t *testing.T) {
	c, f := newTestChannel(t)
	ref, err := c.Send(context.Background(), "lin", notify.Card{
		ID: "dlv_0a1b2c", Title: "需求评审等你回复", Subject: "登录页改版", Project: "智驱",
		Fields:  []notify.Field{{Label: "版本", Value: "v2.3"}, {Label: "环境", Value: strings.Repeat("长", 40)}, {Label: "", Value: "skip"}},
		URL:     "https://pm.example.com/integrations/wecom/entry?todo=1",
		Buttons: []notify.Button{{Key: "approve", Text: "通过", Primary: true}, {Key: "reject", Text: "驳回"}, {Text: "查看详情", URL: "https://pm.example.com/r/1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := f.lastSend(t)
	if m["touser"] != "lin" || m["agentid"] != float64(1000002) || m["msgtype"] != "template_card" {
		t.Fatalf("message = %v", m)
	}
	tc := m["template_card"]
	checks := map[string][2]any{
		"card_type":    {get(tc, "card_type"), "button_interaction"},
		"task_id":      {get(tc, "task_id"), "dlv_0a1b2c"},
		"source":       {get(tc, "source", "desc"), "智驱"},
		"title":        {get(tc, "main_title", "title"), "需求评审等你回复"},
		"desc":         {get(tc, "main_title", "desc"), "登录页改版"},
		"field key":    {get(tc, "horizontal_content_list", 0, "keyname"), "版本"},
		"field cut":    {get(tc, "horizontal_content_list", 1, "value"), strings.Repeat("长", 29) + "…"},
		"button 0 key": {get(tc, "button_list", 0, "key"), "approve"},
		"button 0":     {get(tc, "button_list", 0, "style"), float64(stylePrimary)},
		"button 1 key": {get(tc, "button_list", 1, "key"), "reject"},
		"button 1":     {get(tc, "button_list", 1, "style"), float64(styleSecondary)},
		"link type":    {get(tc, "button_list", 2, "type"), float64(1)},
		"link url":     {get(tc, "button_list", 2, "url"), "https://pm.example.com/r/1"},
		"action type":  {get(tc, "card_action", "type"), float64(1)},
		"action url":   {get(tc, "card_action", "url"), "https://pm.example.com/integrations/wecom/entry?todo=1"},
	}
	for name, v := range checks {
		if v[0] != v[1] {
			t.Errorf("%s = %v, want %v", name, v[0], v[1])
		}
	}
	if n := len(get(tc, "horizontal_content_list").([]any)); n != 2 {
		t.Errorf("fields = %d", n)
	}
	if get(tc, "button_list", 0, "type") != nil || get(tc, "button_list", 2, "key") != nil {
		t.Errorf("buttons = %v", get(tc, "button_list"))
	}
	var r reference
	if json.Unmarshal([]byte(ref), &r) != nil || r.MsgID != "msg1" || r.Code != "rc1" || r.User != "lin" || !r.Button || r.At == 0 {
		t.Fatalf("ref = %s", ref)
	}

	if _, err := c.Send(context.Background(), "lin", notify.Card{ID: "bad id!", Title: "x", Buttons: []notify.Button{{Key: "k", Text: "t"}}}); err == nil || !strings.Contains(err.Error(), "task_id") {
		t.Fatalf("bad task id: %v", err)
	}
}

func TestSendTextNotice(t *testing.T) {
	c, f := newTestChannel(t)
	ctx := context.Background()
	f.locked(func() { f.send = func(map[string]any) string { return `{"errcode":0,"errmsg":"ok","msgid":"msg2"}` } })

	ref, err := c.Send(ctx, "lin", notify.Card{ID: "d2", Title: "缺陷待验证", Subject: "导出报错", URL: "https://pm.example.com/b/2", Buttons: []notify.Button{{Text: "打开", URL: "https://pm.example.com/b/2/open"}}})
	if err != nil {
		t.Fatal(err)
	}
	tc := f.lastSend(t)["template_card"]
	if get(tc, "card_type") != "text_notice" || get(tc, "card_action", "url") != "https://pm.example.com/b/2" || get(tc, "task_id") != nil || get(tc, "button_list") != nil ||
		get(tc, "jump_list", 0, "url") != "https://pm.example.com/b/2/open" || get(tc, "source", "desc") != "智驱" {
		t.Fatalf("card = %v", tc)
	}
	if err := c.Update(ctx, ref, notify.Card{Done: "已处理"}); !errors.Is(err, notify.ErrNotUpdatable) {
		t.Fatalf("update text notice: %v", err)
	}

	// Already handled: no buttons, Done shown as the sub title.
	if _, err = c.Send(ctx, "lin", notify.Card{ID: "d3", Title: "评审", URL: "https://pm.example.com/r/3", Done: "已由 陈晨 确认", Buttons: []notify.Button{{Key: "ok", Text: "确认"}}}); err != nil {
		t.Fatal(err)
	}
	tc = f.lastSend(t)["template_card"]
	if get(tc, "card_type") != "text_notice" || get(tc, "sub_title_text") != "已由 陈晨 确认" || get(tc, "button_list") != nil {
		t.Fatalf("done card = %v", tc)
	}

	// Nowhere to link to: plain text.
	if _, err = c.Send(ctx, "lin", notify.Card{ID: "d4", Title: "提醒", Subject: "登录页改版", Fields: []notify.Field{{Label: "版本", Value: "v1"}}}); err != nil {
		t.Fatal(err)
	}
	m := f.lastSend(t)
	if m["msgtype"] != "text" || get(m, "text", "content") != "提醒\n登录页改版\n版本：v1" {
		t.Fatalf("text = %v", m)
	}
}

func TestSendErrors(t *testing.T) {
	c, f := newTestChannel(t)
	ctx := context.Background()
	card := notify.Card{ID: "d1", Title: "待办", URL: "https://pm.example.com/x"}
	if _, err := c.Send(ctx, " ", card); err == nil {
		t.Fatal("empty user accepted")
	}
	reply := func(f *fakeAPI, s string) { f.locked(func() { f.send = func(map[string]any) string { return s } }) }
	reply(f, `{"errcode":0,"errmsg":"ok","invaliduser":"zhang|lin","msgid":"m"}`)
	if _, err := c.Send(ctx, "lin", card); err == nil || !strings.Contains(err.Error(), "不在应用可见范围") {
		t.Fatalf("invaliduser: %v", err)
	}
	reply(f, `{"errcode":81013,"errmsg":"user & party & tag all invalid"}`)
	if _, err := c.Send(ctx, "lin", card); err == nil || !strings.Contains(err.Error(), "不在应用可见范围") || !strings.Contains(err.Error(), "81013") {
		t.Fatalf("81013: %v", err)
	}
	reply(f, `{"errcode":40058,"errmsg":"invalid parameter"}`)
	if _, err := c.Send(ctx, "lin", card); err == nil || err.Error() != "企业微信接口错误 40058：invalid parameter" {
		t.Fatalf("generic: %v", err)
	}

	c2, f2 := newTestChannel(t)
	f2.locked(func() {
		f2.gettoken = func(url.Values) string {
			return `{"errcode":60020,"errmsg":"not allow to access from your ip, hint: [1700000000], from ip: 203.0.113.7, more info at https://open.work.weixin.qq.com/devtool/query?e=60020"}`
		}
	})
	_, err := c2.Send(ctx, "lin", card)
	if err == nil || !strings.Contains(err.Error(), "企业可信 IP") || !strings.Contains(err.Error(), "203.0.113.7") || !strings.Contains(err.Error(), "60020") {
		t.Fatalf("60020: %v", err)
	}
	f2.locked(func() { f2.gettoken = func(url.Values) string { return `{"errcode":40013,"errmsg":"invalid corpid"}` } })
	if _, err = c2.Send(ctx, "lin", card); err == nil || !strings.Contains(err.Error(), "企业 ID 不正确") {
		t.Fatalf("40013: %v", err)
	}

	// Transport errors must not repeat the URL with the secret.
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	c3, err := open(notify.Config{Values: testValues(down.URL), PublicURL: "https://pm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c3.Send(ctx, "lin", card); err == nil || strings.Contains(err.Error(), testSecret) || !strings.Contains(err.Error(), "无法连接") {
		t.Fatalf("transport: %v", err)
	}
}

func TestUpdate(t *testing.T) {
	c, f := newTestChannel(t)
	ctx := context.Background()
	card := notify.Card{ID: "dlv_1", Title: "评审", URL: "https://pm.example.com/r/1", Buttons: []notify.Button{{Key: "ok", Text: "确认", Primary: true}}}
	ref, err := c.Send(ctx, "lin", card)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Update(ctx, ref, notify.Card{Done: "已由 陈晨 确认"}); err != nil {
		t.Fatal(err)
	}
	u := <-f.updates
	if get(u, "userids", 0) != "lin" || get(u, "agentid") != float64(1000002) || get(u, "response_code") != "rc1" || get(u, "button", "replace_name") != "已由 陈晨 确认" || u["template_card"] != nil {
		t.Fatalf("update = %v", u)
	}

	// Without Done the whole card is replaced.
	card.Title = "评审（已改期）"
	if err = c.Update(ctx, ref, card); err != nil {
		t.Fatal(err)
	}
	u = <-f.updates
	if get(u, "template_card", "card_type") != "button_interaction" || get(u, "template_card", "main_title", "title") != "评审（已改期）" || get(u, "template_card", "task_id") != "dlv_1" || u["button"] != nil {
		t.Fatalf("full update = %v", u)
	}

	for name, r := range map[string]string{
		"garbage":    "not json",
		"no code":    `{"msgid":"m","user":"lin","at":` + fmt.Sprint(time.Now().Unix()) + `,"button":true}`,
		"not button": `{"msgid":"m","code":"rc","user":"lin","at":` + fmt.Sprint(time.Now().Unix()) + `}`,
	} {
		if err := c.Update(ctx, r, notify.Card{Done: "x"}); !errors.Is(err, notify.ErrNotUpdatable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	base := time.Now()
	c.now = func() time.Time { return base.Add(73 * time.Hour) }
	if err = c.Update(ctx, ref, notify.Card{Done: "x"}); !errors.Is(err, notify.ErrNotUpdatable) {
		t.Fatalf("after 72h: %v", err)
	}
	select {
	case u := <-f.updates:
		t.Fatalf("unexpected update %v", u)
	default:
	}
}

func TestIdentify(t *testing.T) {
	c, _ := newTestChannel(t)
	ctx := context.Background()
	for code, want := range map[string]string{"good": "lin", "legacy": "old"} {
		if id, err := c.Identify(ctx, code); err != nil || id != want {
			t.Errorf("%s: %q %v", code, id, err)
		}
	}
	for code, want := range map[string]string{"outsider": "不是本企业成员", "bad": "授权码无效", "": "授权码"} {
		if id, err := c.Identify(ctx, code); err == nil || id != "" || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %q %v", code, id, err)
		}
	}
}

func TestAuthorizeURL(t *testing.T) {
	c, _ := newTestChannel(t)
	redirect := "https://pm.example.com/integrations/wecom/auth?next=/todo&x=1"
	state := "st+/=ate"
	for _, tc := range []struct{ ua, host, path string }{
		{"Mozilla/5.0 (iPhone) AppleWebKit/605.1.15 wxwork/4.1.20 MicroMessenger/7.0.1 Language/zh", "open.weixin.qq.com", "/connect/oauth2/authorize"},
		{"Mozilla/5.0 (Windows NT 10.0) WXWork/4.1.0 Chrome/86", "open.weixin.qq.com", "/connect/oauth2/authorize"},
		{"Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 Safari/605.1.15", "login.work.weixin.qq.com", "/wwlogin/sso/login"},
	} {
		r := httptest.NewRequest(http.MethodGet, "/integrations/wecom/entry", nil)
		r.Header.Set("User-Agent", tc.ua)
		raw := c.AuthorizeURL(r, redirect, state)
		u, err := url.Parse(raw)
		if err != nil || u.Host != tc.host || u.Path != tc.path {
			t.Fatalf("%s: %s", tc.ua, raw)
		}
		q := u.Query()
		if q.Get("appid") != testCorp || q.Get("agentid") != "1000002" || q.Get("redirect_uri") != redirect || q.Get("state") != state || strings.Contains(raw, "next=/todo") {
			t.Errorf("%s: %s", tc.ua, raw)
		}
		if tc.host == "open.weixin.qq.com" {
			if q.Get("scope") != "snsapi_base" || q.Get("response_type") != "code" || u.Fragment != "wechat_redirect" {
				t.Errorf("silent: %s", raw)
			}
		} else if q.Get("login_type") != "CorpApp" {
			t.Errorf("web: %s", raw)
		}
	}
}

func TestErrorMessages(t *testing.T) {
	for code, want := range map[int]string{
		40001: "Secret", 42001: "Secret", 40013: "企业 ID", 40056: "AgentId", 81013: "可见范围", 40029: "授权码", 301002: "授权码", 60020: "企业可信 IP",
	} {
		if msg := (&apiError{ErrCode: code, ErrMsg: "m"}).Error(); !strings.Contains(msg, want) || !strings.Contains(msg, fmt.Sprint(code)) {
			t.Errorf("%d: %s", code, msg)
		}
	}
}
