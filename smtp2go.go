package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const smtp2goSendURL = "https://api.smtp2go.com/v3/email/send"

// SMTP2GOSender sends email via the SMTP2GO REST API.
type SMTP2GOSender struct {
	apiKey string
}

func NewSMTP2GOSender(apiKey string) *SMTP2GOSender {
	return &SMTP2GOSender{apiKey: apiKey}
}

func (s *SMTP2GOSender) Name() string { return "SMTP2GO" }

func (s *SMTP2GOSender) Send(ctx context.Context, msg Message) error {
	type customHeader struct {
		Header string `json:"header"`
		Value  string `json:"value"`
	}
	type body struct {
		APIKey        string         `json:"api_key"`
		To            []string       `json:"to"`
		Sender        string         `json:"sender"`
		Subject       string         `json:"subject"`
		TextBody      string         `json:"text_body,omitempty"`
		HTMLBody      string         `json:"html_body,omitempty"`
		CustomHeaders []customHeader `json:"custom_headers,omitempty"`
	}

	payload := body{
		APIKey:  s.apiKey,
		To:      []string{msg.To.String()},
		Sender:  msg.From.String(),
		Subject: msg.Subject,
		TextBody: msg.Text,
		HTMLBody: msg.HTML,
	}

	// Reply-To header
	if msg.ReplyTo != "" {
		payload.CustomHeaders = append(payload.CustomHeaders,
			customHeader{"Reply-To", msg.ReplyTo},
		)
	}

	// Threading headers
	if msg.InReplyTo != "" {
		payload.CustomHeaders = append(payload.CustomHeaders,
			customHeader{"In-Reply-To", msg.InReplyTo},
			customHeader{"References", strings.Join(msg.References, " ")},
		)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, smtp2goSendURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("smtp2go %d: %s", resp.StatusCode, b)
	}
	return nil
}
