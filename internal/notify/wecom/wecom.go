// Package wecom is the notify channel for a WeCom (企业微信) self-built app:
// template card messages, button callbacks and sign-in with WeCom.
package wecom

import (
	"ai-pm-system/internal/notify"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultAPI = "https://qyapi.weixin.qq.com"

// Spec is the plugin the server registers as "wecom".
func Spec() notify.Spec {
	return notify.Spec{
		Label: "企业微信",
		Fields: []notify.FieldSpec{
			{Key: "corp_id", Label: "企业 ID", Hint: "「我的企业 → 企业信息」页面底部的企业 ID", Required: true},
			{Key: "agent_id", Label: "应用 AgentId", Hint: "「应用管理 → 自建应用」中打开应用即可看到", Required: true},
			{Key: "secret", Label: "应用 Secret", Hint: "「应用管理 → 自建应用」中打开应用查看 Secret，需在企业微信客户端内接收", Secret: true, Required: true},
			{Key: "token", Label: "接收消息 Token", Hint: "应用页「接收消息 → 设置 API 接收」中的 Token", Secret: true, Required: true},
			{Key: "aes_key", Label: "接收消息 EncodingAESKey", Hint: "应用页「接收消息 → 设置 API 接收」中的 EncodingAESKey，共 43 位", Secret: true, Required: true},
			{Key: "api_base", Label: "接口地址", Hint: "通过代理访问企业微信接口时填写", Placeholder: defaultAPI},
		},
		Setup: setup,
		Open: func(cfg notify.Config) (notify.Channel, error) {
			c, err := open(cfg)
			if err != nil {
				return nil, err
			}
			return c, nil
		},
	}
}

func setup(publicURL string) []notify.Field {
	publicURL = strings.TrimRight(strings.TrimSpace(publicURL), "/")
	host := publicURL
	if u, err := url.Parse(publicURL); err == nil && u.Host != "" {
		host = u.Host
	}
	return []notify.Field{
		{Label: "接收消息 URL", Value: publicURL + "/integrations/wecom/callback"},
		{Label: "网页授权及 JS-SDK 可信域名", Value: host},
		{Label: "企业微信授权登录 Web 回调域名", Value: host},
		{Label: "应用主页", Value: publicURL + "/integrations/wecom/entry"},
		{Label: "企业可信 IP", Value: "服务器访问企业微信接口时的出口 IP"},
	}
}

type channel struct {
	corpID  string
	agentID int64
	secret  string
	token   string
	key     []byte // EncodingAESKey decoded, 32 bytes
	api     string
	client  *http.Client
	now     func() time.Time

	mu      sync.Mutex
	access  string
	expires time.Time
}

func open(cfg notify.Config) (*channel, error) {
	v := func(k string) string { return strings.TrimSpace(cfg.Values[k]) }
	c := &channel{corpID: v("corp_id"), secret: v("secret"), token: v("token"), api: strings.TrimRight(v("api_base"), "/"), client: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
	if c.corpID == "" {
		return nil, errors.New("请填写企业 ID")
	}
	agent, err := strconv.ParseInt(v("agent_id"), 10, 64)
	if err != nil || agent <= 0 {
		return nil, errors.New("应用 AgentId 需为正整数")
	}
	c.agentID = agent
	if c.secret == "" {
		return nil, errors.New("请填写应用 Secret")
	}
	if c.token == "" {
		return nil, errors.New("请填写接收消息 Token")
	}
	aesKey := v("aes_key")
	if c.key, err = base64.StdEncoding.DecodeString(aesKey + "="); len(aesKey) != 43 || err != nil || len(c.key) != 32 {
		return nil, errors.New("接收消息 EncodingAESKey 需为企业微信生成的 43 位字符")
	}
	if c.api == "" {
		c.api = defaultAPI
	} else if !httpURL(c.api) {
		return nil, errors.New("接口地址需为 http(s) 地址")
	}
	if !httpURL(cfg.PublicURL) {
		return nil, errors.New("访问地址（public_url）需为完整的 http(s) 地址")
	}
	return c, nil
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// AuthorizeURL signs in silently inside the WeCom client and otherwise uses
// WeCom web login, which offers a QR code and opening the desktop client.
func (c *channel) AuthorizeURL(r *http.Request, redirectURI, state string) string {
	appid, agent := url.QueryEscape(c.corpID), strconv.FormatInt(c.agentID, 10)
	redirect, state := url.QueryEscape(redirectURI), url.QueryEscape(state)
	if r != nil && strings.Contains(strings.ToLower(r.UserAgent()), "wxwork") {
		// WeCom documents this parameter order and the fragment.
		return "https://open.weixin.qq.com/connect/oauth2/authorize?appid=" + appid + "&redirect_uri=" + redirect + "&response_type=code&scope=snsapi_base&state=" + state + "&agentid=" + agent + "#wechat_redirect"
	}
	return "https://login.work.weixin.qq.com/wwlogin/sso/login?login_type=CorpApp&appid=" + appid + "&agentid=" + agent + "&redirect_uri=" + redirect + "&state=" + state
}

func (c *channel) Identify(ctx context.Context, code string) (string, error) {
	if code = strings.TrimSpace(code); code == "" || len(code) > 4096 {
		return "", errors.New("缺少企业微信授权码")
	}
	// JSON keys match case-insensitively, so these also read the older UserId and OpenId.
	var r struct {
		UserID string `json:"userid"`
		OpenID string `json:"openid"`
	}
	if err := c.call(ctx, http.MethodGet, "/cgi-bin/auth/getuserinfo", url.Values{"code": {code}}, nil, &r); err != nil {
		return "", err
	}
	if r.UserID != "" {
		return r.UserID, nil
	}
	if r.OpenID != "" {
		return "", errors.New("不是本企业成员，无法通过企业微信登录")
	}
	return "", errors.New("企业微信未返回成员 ID")
}
