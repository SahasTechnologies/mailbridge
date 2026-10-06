package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	sendpulseTokenURL = "https://api.sendpulse.com/oauth/access_token"
	sendpulseSendURL  = "https://api.sendpulse.com/smtp/emails"
)

// SendPulseSender sends email via the SendPulse REST API.
// It caches the OAuth token and refreshes it automatically on expiry.
type SendPulseSender struct {
	clientID     string
	clientSecret string

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

func NewSendPulseSender(clientID, clientSecret string) *SendPulseSender {
	return &SendPulseSender{clientID: clientID, clientSecret: clientSecret}
}

func (s *SendPulseSender) Name() string { return "SendPulse" }

func (s *SendPulseSender) token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cachedToken != "" && time.Now().Before(s.tokenExpiry) {
		return s.cachedToken, nil
	}

	type tokenReq struct {
		GrantType    string `json:"grant_type"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	type tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}

	body, _ := json.Marshal(tokenReq{"client_credentials", s.clientID, s.clientSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendpulseTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("sendpulse token request: %w", err)
	}
	defer resp.Body.Close()

	var tr tokenResp
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	if tr.Error != "" {
		return "", fmt.Errorf("sendpulse token error: %s", tr.Error)
	}

	s.cachedToken = tr.AccessToken
	s.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn-60) * time.Second)
	return s.cachedToken, nil
}

func (s *SendPulseSender) Send(ctx context.Context, msg Message) error {
	tok, err := s.token(ctx)
	if err != nil {
		return err
	}

	type spAddress struct {
		Name  string `json:"name,omitempty"`
		Email string `json:"email"`
	}
	type spEmail struct {
		From    spAddress  `json:"from"`
		To      []spAddress `json:"to"`
		ReplyTo *spAddress `json:"reply_to,omitempty"` // critical: tells Gmail where Reply goes
		Subject string     `json:"subject"`
		HTML    string     `json:"html,omitempty"`
		Text    string     `json:"text,omitempty"`
	}
	type spRequest struct {
		Email spEmail `json:"email"`
	}

	email := spEmail{
		From:    spAddress{Name: msg.From.Name, Email: msg.From.Email},
		To:      []spAddress{{Name: msg.To.Name, Email: msg.To.Email}},
		Subject: msg.Subject,
		Text:    msg.Text,
	}

	// SendPulse requires HTML to be Base64-encoded (per their API docs)
	if msg.HTML != "" {
		email.HTML = base64.StdEncoding.EncodeToString([]byte(msg.HTML))
	}

	// Set Reply-To so Gmail threads replies to the UUID address,
	// not back to forward.shimpi.dev which has no inbound server
	if msg.ReplyTo != "" {
		email.ReplyTo = &spAddress{Email: msg.ReplyTo}
	}

	payload := spRequest{Email: email}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	log.Printf("[sendpulse] sending — html_len=%d text_len=%d", len(msg.HTML), len(msg.Text))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sendpulseSendURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sendpulse %d: %s", resp.StatusCode, b)
	}
	log.Printf("[sendpulse] response %d: %s", resp.StatusCode, b)
	return nil
}
