package wecom

import (
	"ai-pm-system/internal/notify"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Display limits WeCom recommends for template cards, in characters.
const (
	sourceLimit   = 13
	titleLimit    = 36
	descLimit     = 44
	subTitleLimit = 112
	keyLimit      = 5
	valueLimit    = 30
	buttonLimit   = 10
	jumpLimit     = 13
	replaceLimit  = 20
	maxFields     = 6
	maxJumps      = 3
)

// Button styles 1-4; 1 is the filled primary style.
const (
	stylePrimary   = 1
	styleSecondary = 2
)

// codeTTL is how long a response_code stays usable: WeCom allows 72 hours,
// minus a margin for clock skew.
const codeTTL = 72*time.Hour - 10*time.Minute

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9_@-]{1,128}$`)

type templateCard struct {
	CardType     string      `json:"card_type"`
	Source       source      `json:"source"`
	MainTitle    mainTitle   `json:"main_title"`
	SubTitleText string      `json:"sub_title_text,omitempty"`
	Horizontal   []keyValue  `json:"horizontal_content_list,omitempty"`
	JumpList     []jump      `json:"jump_list,omitempty"`
	CardAction   *cardAction `json:"card_action,omitempty"`
	TaskID       string      `json:"task_id,omitempty"`
	ButtonList   []button    `json:"button_list,omitempty"`
}

type source struct {
	Desc string `json:"desc"`
}

type mainTitle struct {
	Title string `json:"title,omitempty"`
	Desc  string `json:"desc,omitempty"`
}

type keyValue struct {
	KeyName string `json:"keyname"`
	Value   string `json:"value"`
}

type jump struct {
	Type  int    `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

type cardAction struct {
	Type int    `json:"type"`
	URL  string `json:"url"`
}

type button struct {
	Type  int    `json:"type,omitempty"` // 0 reports a click, 1 opens url
	Text  string `json:"text"`
	Style int    `json:"style,omitempty"`
	Key   string `json:"key,omitempty"`
	URL   string `json:"url,omitempty"`
}

// reference is what Send returns and Update reads back.
type reference struct {
	MsgID  string `json:"msgid"`
	Code   string `json:"code,omitempty"` // response_code, for button cards
	User   string `json:"user"`
	At     int64  `json:"at"` // sent, Unix seconds
	Button bool   `json:"button,omitempty"`
}

// cut keeps s on one line within n characters, ending in "…" when shortened.
func cut(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// render builds the template card for c, or nil when it can only be plain
// text: a text_notice card must open a link.
func render(c notify.Card) (*templateCard, error) {
	t := &templateCard{Source: source{Desc: cut(c.Project, sourceLimit)}}
	if t.Source.Desc == "" {
		t.Source.Desc = "智驱"
	}
	t.MainTitle = mainTitle{Title: cut(c.Title, titleLimit), Desc: cut(c.Subject, descLimit)}
	if t.MainTitle.Title == "" {
		t.MainTitle = mainTitle{Title: cut(c.Subject, titleLimit)}
	}
	for _, f := range c.Fields {
		if k, v := cut(f.Label, keyLimit), cut(f.Value, valueLimit); k != "" && v != "" && len(t.Horizontal) < maxFields {
			t.Horizontal = append(t.Horizontal, keyValue{KeyName: k, Value: v})
		}
	}
	if c.URL != "" {
		t.CardAction = &cardAction{Type: 1, URL: c.URL}
	}
	interactive := false
	for _, b := range c.Buttons {
		interactive = interactive || (b.Key != "" && b.Text != "")
	}
	if interactive && c.Done == "" {
		if !taskIDPattern.MatchString(c.ID) {
			return nil, errors.New("卡片 ID 需为 1-128 位字母、数字或 _-@，才能用作企业微信 task_id")
		}
		t.CardType, t.TaskID = "button_interaction", c.ID
		for _, b := range c.Buttons {
			style := styleSecondary
			if b.Primary {
				style = stylePrimary
			}
			switch text := cut(b.Text, buttonLimit); {
			case text == "":
			case b.Key != "":
				t.ButtonList = append(t.ButtonList, button{Text: text, Style: style, Key: b.Key})
			case b.URL != "":
				t.ButtonList = append(t.ButtonList, button{Type: 1, Text: text, Style: style, URL: b.URL})
			}
		}
		return t, nil
	}
	t.CardType, t.SubTitleText = "text_notice", cut(c.Done, subTitleLimit)
	for _, b := range c.Buttons {
		if b.Key == "" && b.URL != "" && b.Text != "" && len(t.JumpList) < maxJumps {
			t.JumpList = append(t.JumpList, jump{Type: 1, Title: cut(b.Text, jumpLimit), URL: b.URL})
		}
	}
	if t.CardAction == nil && len(t.JumpList) > 0 {
		t.CardAction = &cardAction{Type: 1, URL: t.JumpList[0].URL}
	}
	if t.CardAction == nil {
		return nil, nil
	}
	return t, nil
}

func plainText(c notify.Card) string {
	var lines []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, s)
		}
	}
	add(c.Title)
	add(c.Subject)
	if c.Project != "" {
		add("项目：" + c.Project)
	}
	for _, f := range c.Fields {
		if f.Label != "" && f.Value != "" {
			add(f.Label + "：" + f.Value)
		}
	}
	add(c.Done)
	return strings.Join(lines, "\n")
}

func (c *channel) Send(ctx context.Context, externalID string, card notify.Card) (string, error) {
	if externalID = strings.TrimSpace(externalID); externalID == "" {
		return "", errors.New("缺少企业微信成员 ID")
	}
	t, err := render(card)
	if err != nil {
		return "", err
	}
	msg := map[string]any{"touser": externalID, "agentid": c.agentID}
	if t != nil {
		msg["msgtype"], msg["template_card"] = "template_card", t
	} else {
		msg["msgtype"], msg["text"] = "text", map[string]string{"content": plainText(card)}
	}
	var r struct {
		InvalidUser  users  `json:"invaliduser"`
		MsgID        string `json:"msgid"`
		ResponseCode string `json:"response_code"`
	}
	if err := c.call(ctx, http.MethodPost, "/cgi-bin/message/send", nil, msg, &r); err != nil {
		return "", err
	}
	if r.InvalidUser.has(externalID) {
		return "", fmt.Errorf("企业微信成员 %s 不存在或不在应用可见范围", externalID)
	}
	ref, _ := json.Marshal(reference{MsgID: r.MsgID, Code: r.ResponseCode, User: externalID, At: c.now().Unix(), Button: t != nil && t.CardType == "button_interaction"})
	return string(ref), nil
}

// Update replaces the buttons of a button card with c.Done, or the whole
// card when Done is empty. A response_code works once within 72 hours.
func (c *channel) Update(ctx context.Context, ref string, card notify.Card) error {
	var r reference
	if json.Unmarshal([]byte(ref), &r) != nil || !r.Button || r.Code == "" || r.User == "" || c.now().Sub(time.Unix(r.At, 0)) >= codeTTL {
		return notify.ErrNotUpdatable
	}
	if card.Done != "" {
		return c.replaceButtons(ctx, r.User, r.Code, card.Done)
	}
	t, err := render(card)
	if err != nil {
		return err
	}
	if t == nil {
		return notify.ErrNotUpdatable
	}
	return c.updateCard(ctx, r.User, map[string]any{"userids": []string{r.User}, "agentid": c.agentID, "response_code": r.Code, "template_card": t})
}

func (c *channel) replaceButtons(ctx context.Context, user, code, done string) error {
	return c.updateCard(ctx, user, map[string]any{"userids": []string{user}, "agentid": c.agentID, "response_code": code, "button": map[string]string{"replace_name": cut(done, replaceLimit)}})
}

func (c *channel) updateCard(ctx context.Context, user string, body map[string]any) error {
	var r struct {
		InvalidUser users `json:"invaliduser"`
	}
	if err := c.call(ctx, http.MethodPost, "/cgi-bin/message/update_template_card", nil, body, &r); err != nil {
		// An expired or used response_code is reported by message, not by a
		// documented errcode.
		var e *apiError
		if errors.As(err, &e) && strings.Contains(strings.ToLower(e.ErrMsg), "response_code") {
			return fmt.Errorf("%w：%v", notify.ErrNotUpdatable, err)
		}
		return err
	}
	if r.InvalidUser.has(user) {
		return fmt.Errorf("企业微信成员 %s 不存在或不在应用可见范围", user)
	}
	return nil
}
