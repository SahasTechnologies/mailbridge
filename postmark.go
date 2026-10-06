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

const postmarkAPI = "https://api.postmarkapp.com/email"

// PostmarkSender sends email via the Postmark API.
type PostmarkSender struct {
	token string
}

func NewPostmarkSender(token string) *PostmarkSender {
	return &PostmarkSender{token: token}
}

func (p *PostmarkSender) Name() string { return "Postmark" }

func (p *PostmarkSender) Send(ctx context.Context, msg Message) error {
	type header struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	}

	type body struct {
		From        string   `json:"From"`
		To          string   `json:"To"`
		Subject     string   `json:"Subject"`
		TextBody    string   `json:"TextBody,omitempty"`
		HTMLBody    string   `json:"HtmlBody,omitempty"`
		ReplyTo     string   `json:"ReplyTo,omitempty"`
		Headers     []header `json:"Headers,omitempty"`
		MessageStream string `json:"MessageStream"`
	}

	payload := body{
		From:          msg.From.String(),
		To:            msg.To.Email,
		Subject:       msg.Subject,
		TextBody:      msg.Text,
		HTMLBody:      msg.HTML,
		ReplyTo:       msg.ReplyTo,
		MessageStream: "outbound",
	}

	// Threading headers
	if msg.InReplyTo != "" {
		payload.Headers = append(payload.Headers,
			header{"In-Reply-To", msg.InReplyTo},
			header{"References", strings.Join(msg.References, " ")},
		)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postmarkAPI, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Postmark-Server-Token", p.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("postmark %d: %s", resp.StatusCode, b)
	}
	return nil
}
