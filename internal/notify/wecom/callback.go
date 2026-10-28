package wecom

import (
	"ai-pm-system/internal/notify"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxCallbackBody = 64 << 10
	callbackSkew    = 5 * time.Minute
)

var errCipher = errors.New("企业微信消息无法解密，请检查 EncodingAESKey")

// Callback serves WeCom's 接收消息 URL: URL verification on GET and
// encrypted event pushes on POST.
func (c *channel) Callback(on func(context.Context, notify.Interaction) notify.Card) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sig, ts, nonce := q.Get("msg_signature"), q.Get("timestamp"), q.Get("nonce")
		switch r.Method {
		case http.MethodGet:
			// Base64 has no spaces; a "+" sent unescaped arrives as one.
			echo := strings.ReplaceAll(q.Get("echostr"), " ", "+")
			if !c.signed(sig, ts, nonce, echo) {
				log.Printf("企业微信回调地址校验失败：签名不符，请检查接收消息 Token")
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			msg, err := c.decrypt(echo)
			if err != nil {
				log.Printf("企业微信回调地址校验失败：%v", err)
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(msg)
		case http.MethodPost:
			c.receive(w, r, on, sig, ts, nonce)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func (c *channel) receive(w http.ResponseWriter, r *http.Request, on func(context.Context, notify.Interaction) notify.Card, sig, ts, nonce string) {
	var envelope struct {
		Encrypt string `xml:"Encrypt"`
	}
	if xml.NewDecoder(http.MaxBytesReader(w, r.Body, maxCallbackBody)).Decode(&envelope) != nil || envelope.Encrypt == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !c.fresh(ts) || !c.signed(sig, ts, nonce, envelope.Encrypt) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	plain, err := c.decrypt(envelope.Encrypt)
	if err != nil {
		log.Printf("企业微信回调消息被拒绝：%v", err)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var ev struct {
		FromUserName string `xml:"FromUserName"`
		MsgType      string `xml:"MsgType"`
		Event        string `xml:"Event"`
		EventKey     string `xml:"EventKey"`
		TaskID       string `xml:"TaskId"`
		ResponseCode string `xml:"ResponseCode"`
	}
	if xml.Unmarshal(plain, &ev) != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// An empty 200 tells WeCom there is no reply. Retries of the same push are
	// harmless: the core handles clicks idempotently and a used response_code
	// only makes the second card update fail.
	if ev.MsgType != "event" || ev.Event != "template_card_event" || ev.TaskID == "" || ev.FromUserName == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	card := on(r.Context(), notify.Interaction{CardID: ev.TaskID, Key: ev.EventKey, ExternalID: ev.FromUserName})
	w.WriteHeader(http.StatusOK)
	if card.Done != "" && ev.ResponseCode != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := c.replaceButtons(ctx, ev.FromUserName, ev.ResponseCode, card.Done); err != nil {
				log.Printf("企业微信卡片 %s 更新失败：%v", ev.TaskID, err)
			}
		}()
	}
}

func (c *channel) fresh(ts string) bool {
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	d := c.now().Sub(time.Unix(t, 0))
	return d > -callbackSkew && d < callbackSkew
}

func (c *channel) signature(ts, nonce, encrypted string) string {
	parts := []string{c.token, ts, nonce, encrypted}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

func (c *channel) signed(sig, ts, nonce, encrypted string) bool {
	return sig != "" && encrypted != "" && subtle.ConstantTimeCompare([]byte(c.signature(ts, nonce, encrypted)), []byte(strings.ToLower(sig))) == 1
}

// decrypt opens a WXBizMsgCrypt payload: AES-256-CBC with IV = key[:16] and
// PKCS#7 padding to 32 bytes over
// random(16) ‖ len(msg) as uint32 big-endian ‖ msg ‖ receiveid.
func (c *channel) decrypt(encrypted string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errCipher
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, errCipher
	}
	cipher.NewCBCDecrypter(block, c.key[:aes.BlockSize]).CryptBlocks(data, data)
	pad := int(data[len(data)-1])
	if pad < 1 || pad > 32 || pad > len(data) {
		return nil, errCipher
	}
	data = data[:len(data)-pad]
	if len(data) < 20 {
		return nil, errCipher
	}
	n := binary.BigEndian.Uint32(data[16:20])
	if uint64(n) > uint64(len(data)-20) {
		return nil, errCipher
	}
	msg, receiver := data[20:20+int(n)], data[20+int(n):]
	if subtle.ConstantTimeCompare(receiver, []byte(c.corpID)) != 1 {
		return nil, errors.New("企业微信消息不属于本企业，请检查企业 ID")
	}
	return msg, nil
}
