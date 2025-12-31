package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"log/slog"
)

type Client struct {
	logger     *slog.Logger
	appID      string
	appSecret  string
	httpClient *http.Client

	mu           sync.Mutex
	accessToken  string
	accessExpiry time.Time
}

func NewClient(logger *slog.Logger, appID, appSecret string) *Client {
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		logger:    logger.With("component", "wechat"),
		appID:     appID,
		appSecret: appSecret,
		httpClient: &http.Client{
			Timeout: 8 * time.Second,
		},
	}
}

type CodeSession struct {
	OpenID     string  `json:"openid"`
	SessionKey string  `json:"session_key"`
	UnionID    *string `json:"unionid,omitempty"`
	ErrCode    int     `json:"errcode"`
	ErrMsg     string  `json:"errmsg"`
}

func (c *Client) ExchangeCode(ctx context.Context, code string) (CodeSession, error) {
	if stringsTrim(code) == "" {
		return CodeSession{}, fmt.Errorf("missing code")
	}
	if stringsTrim(c.appID) == "" || stringsTrim(c.appSecret) == "" {
		return CodeSession{}, fmt.Errorf("wechat app credentials not configured")
	}

	u, _ := url.Parse("https://api.weixin.qq.com/sns/jscode2session")
	q := u.Query()
	q.Set("appid", c.appID)
	q.Set("secret", c.appSecret)
	q.Set("js_code", code)
	q.Set("grant_type", "authorization_code")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return CodeSession{}, err
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return CodeSession{}, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return CodeSession{}, err
	}

	var cs CodeSession
	if err := json.Unmarshal(body, &cs); err != nil {
		return CodeSession{}, fmt.Errorf("decode wechat response: %w", err)
	}

	if cs.ErrCode != 0 {
		return CodeSession{}, fmt.Errorf("wechat jscode2session errcode=%d errmsg=%q", cs.ErrCode, cs.ErrMsg)
	}
	if cs.OpenID == "" || cs.SessionKey == "" {
		return CodeSession{}, errors.New("wechat response missing openid/session_key")
	}

	return cs, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

func (c *Client) GetAccessToken(ctx context.Context) (string, error) {
	if stringsTrim(c.appID) == "" || stringsTrim(c.appSecret) == "" {
		return "", fmt.Errorf("wechat app credentials not configured")
	}

	now := time.Now()
	c.mu.Lock()
	if c.accessToken != "" && now.Before(c.accessExpiry.Add(-30*time.Second)) {
		tok := c.accessToken
		c.mu.Unlock()
		return tok, nil
	}
	c.mu.Unlock()

	u, _ := url.Parse("https://api.weixin.qq.com/cgi-bin/token")
	q := u.Query()
	q.Set("grant_type", "client_credential")
	q.Set("appid", c.appID)
	q.Set("secret", c.appSecret)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", fmt.Errorf("decode wechat token response: %w", err)
	}
	if tr.ErrCode != 0 {
		return "", fmt.Errorf("wechat token errcode=%d errmsg=%q", tr.ErrCode, tr.ErrMsg)
	}
	if tr.AccessToken == "" || tr.ExpiresIn <= 0 {
		return "", errors.New("wechat token response missing access_token/expires_in")
	}

	c.mu.Lock()
	c.accessToken = tr.AccessToken
	c.accessExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	c.mu.Unlock()

	return tr.AccessToken, nil
}

type subscribeSendResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

type SubscribeSendRequest struct {
	ToUser     string         `json:"touser"`
	TemplateID string         `json:"template_id"`
	Page       string         `json:"page,omitempty"`
	Data       map[string]any `json:"data"`
}

func (c *Client) SendSubscribeMessage(ctx context.Context, accessToken string, req SubscribeSendRequest) error {
	if stringsTrim(accessToken) == "" {
		return fmt.Errorf("missing access token")
	}
	if stringsTrim(req.ToUser) == "" || stringsTrim(req.TemplateID) == "" {
		return fmt.Errorf("missing touser/template_id")
	}
	if req.Data == nil {
		req.Data = map[string]any{}
	}

	u, _ := url.Parse("https://api.weixin.qq.com/cgi-bin/message/subscribe/send")
	q := u.Query()
	q.Set("access_token", accessToken)
	u.RawQuery = q.Encode()

	b, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(b))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}

	var sr subscribeSendResponse
	if err := json.Unmarshal(body, &sr); err != nil {
		return fmt.Errorf("decode wechat subscribe response: %w", err)
	}
	if sr.ErrCode != 0 {
		return fmt.Errorf("wechat subscribe send errcode=%d errmsg=%q", sr.ErrCode, sr.ErrMsg)
	}
	return nil
}

type WxaCodeUnlimitRequest struct {
	Scene     string `json:"scene"`
	Page      string `json:"page,omitempty"`
	CheckPath bool   `json:"check_path"`
	EnvVersion string `json:"env_version,omitempty"` // develop|trial|release
	Width     int    `json:"width,omitempty"`
}

type wxaCodeErrorResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func (c *Client) GetWxaCodeUnlimit(ctx context.Context, accessToken string, req WxaCodeUnlimitRequest) ([]byte, error) {
	if stringsTrim(accessToken) == "" {
		return nil, fmt.Errorf("missing access token")
	}
	if stringsTrim(req.Scene) == "" {
		return nil, fmt.Errorf("missing scene")
	}
	if req.Width <= 0 {
		req.Width = 430
	}

	u, _ := url.Parse("https://api.weixin.qq.com/wxa/getwxacodeunlimit")
	q := u.Query()
	q.Set("access_token", accessToken)
	u.RawQuery = q.Encode()

	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 5<<20))
	if err != nil {
		return nil, err
	}

	// When failing, WeChat returns JSON: {"errcode":...,"errmsg":...}
	if len(body) > 0 && body[0] == '{' {
		var er wxaCodeErrorResponse
		if err := json.Unmarshal(body, &er); err == nil && er.ErrCode != 0 {
			return nil, fmt.Errorf("wechat getwxacodeunlimit errcode=%d errmsg=%q", er.ErrCode, er.ErrMsg)
		}
	}

	return body, nil
}

func stringsTrim(s string) string {
	var start int
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	var end = len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
