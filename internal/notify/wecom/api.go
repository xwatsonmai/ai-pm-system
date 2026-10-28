package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type status struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// apiError is a non-zero errcode from the WeCom API.
type apiError status

func (e *apiError) Error() string {
	var hint string
	switch e.ErrCode {
	case 60020:
		hint = "服务器 IP 不在应用的企业可信 IP 中，请在「应用管理 → 自建应用 → 企业可信 IP」中添加"
		if _, rest, ok := strings.Cut(e.ErrMsg, "from ip: "); ok {
			ip, _, _ := strings.Cut(rest, ",")
			hint += " " + strings.TrimSpace(ip)
		}
	case 40013:
		hint = "企业 ID 不正确"
	case 40001, 40014, 42001, 41001:
		hint = "应用 Secret 不正确或已重置"
	case 40056:
		hint = "应用 AgentId 不正确"
	case 81013:
		hint = "成员不存在或不在应用可见范围"
	case 40029, 301002:
		hint = "授权码无效或已使用"
	default:
		return fmt.Sprintf("企业微信接口错误 %d：%s", e.ErrCode, e.ErrMsg)
	}
	return fmt.Sprintf("%s（企业微信错误码 %d）", hint, e.ErrCode)
}

func tokenRejected(err error) bool {
	var e *apiError
	return errors.As(err, &e) && (e.ErrCode == 40014 || e.ErrCode == 42001 || e.ErrCode == 40001)
}

// call runs an API request with the access token, fetching a new token and
// retrying once when WeCom rejects the cached one.
func (c *channel) call(ctx context.Context, method, path string, q url.Values, body, out any) error {
	for retried := false; ; retried = true {
		token, err := c.accessToken(ctx)
		if err != nil {
			return err
		}
		withToken := url.Values{"access_token": {token}}
		for k, v := range q {
			withToken[k] = v
		}
		err = c.do(ctx, method, path, withToken, body, out)
		if !retried && tokenRejected(err) {
			c.dropToken(token)
			continue
		}
		return err
	}
}

func (c *channel) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access != "" && c.now().Before(c.expires) {
		return c.access, nil
	}
	var r struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := c.do(ctx, http.MethodGet, "/cgi-bin/gettoken", url.Values{"corpid": {c.corpID}, "corpsecret": {c.secret}}, nil, &r); err != nil {
		return "", err
	}
	if r.AccessToken == "" {
		return "", errors.New("企业微信未返回 access_token")
	}
	life := time.Duration(r.ExpiresIn) * time.Second
	ttl := life - 5*time.Minute
	if ttl <= 0 {
		ttl = life / 2
	}
	c.access, c.expires = r.AccessToken, c.now().Add(ttl)
	return c.access, nil
}

// dropToken forgets token unless another call has already replaced it.
func (c *channel) dropToken(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.access == token {
		c.access = ""
	}
}

func (c *channel) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var payload io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path+"?"+q.Encode(), payload)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		// url.Error repeats the URL, which carries the secret or access token.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("无法连接企业微信接口：%v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("读取企业微信接口响应失败：%v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("企业微信接口返回 HTTP %d", resp.StatusCode)
	}
	var s status
	if json.Unmarshal(data, &s) != nil {
		return errors.New("企业微信接口返回了无法识别的内容")
	}
	if s.ErrCode != 0 {
		e := apiError(s)
		return &e
	}
	if out != nil && json.Unmarshal(data, out) != nil {
		return errors.New("企业微信接口返回了无法识别的内容")
	}
	return nil
}

// users is a list of user IDs, which WeCom returns as "a|b" or as an array.
type users []string

func (u *users) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*u = strings.Split(s, "|")
		return nil
	}
	var list []string
	if json.Unmarshal(b, &list) == nil {
		*u = list
	}
	return nil
}

func (u users) has(id string) bool {
	for _, v := range u {
		if strings.TrimSpace(v) == id {
			return true
		}
	}
	return false
}
