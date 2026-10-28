package wecom

import (
	"ai-pm-system/internal/notify"
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// seal is the sending side of WXBizMsgCrypt.
func seal(t *testing.T, key []byte, receiveID, msg string) string {
	t.Helper()
	var plain bytes.Buffer
	random := make([]byte, 16)
	rand.Read(random)
	plain.Write(random)
	binary.Write(&plain, binary.BigEndian, uint32(len(msg)))
	plain.WriteString(msg)
	plain.WriteString(receiveID)
	pad := 32 - plain.Len()%32
	plain.Write(bytes.Repeat([]byte{byte(pad)}, pad))
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	out := plain.Bytes()
	cipher.NewCBCEncrypter(block, key[:16]).CryptBlocks(out, out)
	return base64.StdEncoding.EncodeToString(out)
}

func noClick(context.Context, notify.Interaction) notify.Card { return notify.Card{} }

// push posts an encrypted message to the callback the way WeCom does.
func push(t *testing.T, c *channel, h http.Handler, encrypted string, at time.Time, sig string) *httptest.ResponseRecorder {
	t.Helper()
	ts, nonce := strconv.FormatInt(at.Unix(), 10), "1372623149"
	if sig == "" {
		sig = c.signature(ts, nonce, encrypted)
	}
	body := "<xml><ToUserName><![CDATA[" + testCorp + "]]></ToUserName><Encrypt><![CDATA[" + encrypted + "]]></Encrypt><AgentID><![CDATA[1000002]]></AgentID></xml>"
	r := httptest.NewRequest(http.MethodPost, "/integrations/wecom/callback?"+url.Values{"msg_signature": {sig}, "timestamp": {ts}, "nonce": {nonce}}.Encode(), strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCryptoRoundTrip(t *testing.T) {
	c, _ := newTestChannel(t)
	for _, msg := range []string{"", "x", strings.Repeat("长消息", 100), strings.Repeat("a", 64-20-len(testCorp))} {
		got, err := c.decrypt(seal(t, c.key, testCorp, msg))
		if err != nil || string(got) != msg {
			t.Fatalf("%q: %q %v", msg, got, err)
		}
	}
	if _, err := c.decrypt(seal(t, c.key, "ww_other_corp", "x")); err == nil {
		t.Fatal("foreign receiveid accepted")
	}
	for _, bad := range []string{"", "not base64!", base64.StdEncoding.EncodeToString([]byte("short")), base64.StdEncoding.EncodeToString(make([]byte, 32))} {
		if _, err := c.decrypt(bad); err == nil {
			t.Errorf("%q decrypted", bad)
		}
	}
}

// The sample from WeCom's 加解密方案说明.
func TestOfficialSample(t *testing.T) {
	c, err := open(notify.Config{Values: map[string]string{"corp_id": "wx5823bf96d3bd56c7", "agent_id": "218", "secret": "s", "token": "QDG6eK", "aes_key": testAESKey}, PublicURL: "https://pm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	h := c.Callback(func(context.Context, notify.Interaction) notify.Card {
		t.Error("text message treated as a click")
		return notify.Card{}
	})

	r := httptest.NewRequest(http.MethodGet, "/cgi-bin/wxpush?msg_signature=5c45ff5e21c57e6ad56bac8758b79b1d9ac89fd3&timestamp=1409659589&nonce=263014780&echostr=P9nAzCzyDtyTWESHep1vC5X9xho%2FqYX3Zpb4yKa9SKld1DsH3Iyt3tP3zNdtp%2B4RPcs8TgAE7OaBO%2BFZXvnaqQ%3D%3D", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "1616140317555161061" {
		t.Fatalf("verify: %d %q", w.Code, w.Body.String())
	}

	c.now = func() time.Time { return time.Unix(1409659813, 0) }
	body := "<xml><ToUserName><![CDATA[wx5823bf96d3bd56c7]]></ToUserName><Encrypt><![CDATA[RypEvHKD8QQKFhvQ6QleEB4J58tiPdvo+rtK1I9qca6aM/wvqnLSV5zEPeusUiX5L5X/0lWfrf0QADHHhGd3QczcdCUpj911L3vg3W/sYYvuJTs3TUUkSUXxaccAS0qhxchrRYt66wiSpGLYL42aM6A8dTT+6k4aSknmPj48kzJs8qLjvd4Xgpue06DOdnLxAUHzM6+kDZ+HMZfJYuR+LtwGc2hgf5gsijff0ekUNXZiqATP7PF5mZxZ3Izoun1s4zG4LUMnvw2r+KqCKIw+3IQH03v+BCA9nMELNqbSf6tiWSrXJB3LAVGUcallcrw8V2t9EL4EhzJWrQUax5wLVMNS0+rUPA3k22Ncx4XXZS9o0MBH27Bo6BpNelZpS+/uh9KsNlY6bHCmJU9p8g7m3fVKn28H3KDYA5Pl/T8Z1ptDAVe0lXdQ2YoyyH2uyPIGHBZZIs2pDBS8R07+qN+E7Q==]]></Encrypt>\n<AgentID><![CDATA[218]]></AgentID>\n</xml>"
	r = httptest.NewRequest(http.MethodPost, "/cgi-bin/wxpush?msg_signature=477715d11cdb4164915debcba66cb864d751f3e6&timestamp=1409659813&nonce=1372623149", strings.NewReader(body))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("push: %d %q", w.Code, w.Body.String())
	}
	msg, err := c.decrypt("RypEvHKD8QQKFhvQ6QleEB4J58tiPdvo+rtK1I9qca6aM/wvqnLSV5zEPeusUiX5L5X/0lWfrf0QADHHhGd3QczcdCUpj911L3vg3W/sYYvuJTs3TUUkSUXxaccAS0qhxchrRYt66wiSpGLYL42aM6A8dTT+6k4aSknmPj48kzJs8qLjvd4Xgpue06DOdnLxAUHzM6+kDZ+HMZfJYuR+LtwGc2hgf5gsijff0ekUNXZiqATP7PF5mZxZ3Izoun1s4zG4LUMnvw2r+KqCKIw+3IQH03v+BCA9nMELNqbSf6tiWSrXJB3LAVGUcallcrw8V2t9EL4EhzJWrQUax5wLVMNS0+rUPA3k22Ncx4XXZS9o0MBH27Bo6BpNelZpS+/uh9KsNlY6bHCmJU9p8g7m3fVKn28H3KDYA5Pl/T8Z1ptDAVe0lXdQ2YoyyH2uyPIGHBZZIs2pDBS8R07+qN+E7Q==")
	if err != nil || !strings.Contains(string(msg), "<Content><![CDATA[hello]]></Content>") {
		t.Fatalf("sample message: %q %v", msg, err)
	}
}

func TestURLVerification(t *testing.T) {
	c, _ := newTestChannel(t)
	h := c.Callback(noClick)
	echo := seal(t, c.key, testCorp, "8401958437509201")
	verify := func(sig, echostr string) *httptest.ResponseRecorder {
		q := url.Values{"msg_signature": {sig}, "timestamp": {"1409659589"}, "nonce": {"263014780"}, "echostr": {echostr}}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/integrations/wecom/callback?"+q.Encode(), nil))
		return w
	}
	if w := verify(c.signature("1409659589", "263014780", echo), echo); w.Code != http.StatusOK || w.Body.String() != "8401958437509201" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("verify: %d %q", w.Code, w.Body.String())
	}
	if w := verify(strings.Repeat("0", 40), echo); w.Code != http.StatusForbidden {
		t.Fatalf("bad signature: %d", w.Code)
	}
	foreign := seal(t, c.key, "ww_other_corp", "1")
	if w := verify(c.signature("1409659589", "263014780", foreign), foreign); w.Code != http.StatusForbidden {
		t.Fatalf("foreign: %d", w.Code)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/integrations/wecom/callback", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT: %d", w.Code)
	}
}

func cardEvent(user, task, key, code string) string {
	return fmt.Sprintf(`<xml><ToUserName><![CDATA[%s]]></ToUserName><FromUserName><![CDATA[%s]]></FromUserName><CreateTime>%d</CreateTime><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[template_card_event]]></Event><EventKey><![CDATA[%s]]></EventKey><TaskId><![CDATA[%s]]></TaskId><CardType><![CDATA[button_interaction]]></CardType><ResponseCode><![CDATA[%s]]></ResponseCode><AgentID>1000002</AgentID></xml>`, testCorp, user, time.Now().Unix(), key, task, code)
}

func TestCardEvent(t *testing.T) {
	c, f := newTestChannel(t)
	got := make(chan notify.Interaction, 4)
	h := c.Callback(func(_ context.Context, it notify.Interaction) notify.Card {
		got <- it
		if it.Key == "approve" {
			return notify.Card{ID: it.CardID, Done: "已由 陈晨 确认"}
		}
		return notify.Card{ID: it.CardID}
	})

	w := push(t, c, h, seal(t, c.key, testCorp, cardEvent("lin", "dlv_9", "approve", "RC9")), time.Now(), "")
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("push: %d %q", w.Code, w.Body.String())
	}
	if it := <-got; it != (notify.Interaction{CardID: "dlv_9", Key: "approve", ExternalID: "lin"}) {
		t.Fatalf("interaction = %+v", it)
	}
	select {
	case u := <-f.updates:
		if get(u, "response_code") != "RC9" || get(u, "button", "replace_name") != "已由 陈晨 确认" || get(u, "userids", 0) != "lin" || get(u, "agentid") != float64(1000002) {
			t.Fatalf("update = %v", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("card not updated")
	}

	// No Done: the card stays as it is. Other messages are ignored.
	if w := push(t, c, h, seal(t, c.key, testCorp, cardEvent("lin", "dlv_9", "later", "RC10")), time.Now(), ""); w.Code != http.StatusOK || (<-got).Key != "later" {
		t.Fatalf("no done: %d", w.Code)
	}
	other := `<xml><ToUserName><![CDATA[x]]></ToUserName><FromUserName><![CDATA[lin]]></FromUserName><MsgType><![CDATA[event]]></MsgType><Event><![CDATA[enter_agent]]></Event></xml>`
	if w := push(t, c, h, seal(t, c.key, testCorp, other), time.Now(), ""); w.Code != http.StatusOK || len(got) != 0 {
		t.Fatalf("other event: %d", w.Code)
	}
	select {
	case u := <-f.updates:
		t.Fatalf("unexpected update %v", u)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCallbackRejects(t *testing.T) {
	c, _ := newTestChannel(t)
	h := c.Callback(func(context.Context, notify.Interaction) notify.Card {
		t.Error("rejected push reached on")
		return notify.Card{}
	})
	event := cardEvent("lin", "dlv_9", "approve", "RC9")
	if w := push(t, c, h, seal(t, c.key, testCorp, event), time.Now(), strings.Repeat("ab", 20)); w.Code != http.StatusForbidden {
		t.Errorf("bad signature: %d", w.Code)
	}
	if w := push(t, c, h, seal(t, c.key, "ww_other_corp", event), time.Now(), ""); w.Code != http.StatusForbidden {
		t.Errorf("foreign receiveid: %d", w.Code)
	}
	if w := push(t, c, h, seal(t, c.key, testCorp, event), time.Now().Add(-6*time.Minute), ""); w.Code != http.StatusForbidden {
		t.Errorf("stale timestamp: %d", w.Code)
	}
	if w := push(t, c, h, seal(t, c.key, testCorp, event), time.Now().Add(6*time.Minute), ""); w.Code != http.StatusForbidden {
		t.Errorf("future timestamp: %d", w.Code)
	}
	big := httptest.NewRequest(http.MethodPost, "/integrations/wecom/callback", strings.NewReader("<xml><Encrypt>"+strings.Repeat("A", maxCallbackBody)+"</Encrypt></xml>"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, big)
	if w.Code != http.StatusBadRequest {
		t.Errorf("oversized: %d", w.Code)
	}
}
