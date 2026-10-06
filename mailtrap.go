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

const mailtrapSendURL = "https://send.api.mailtrap.io/api/send"

// MailtrapSender sends email via the Mailtrap Sending API.
type MailtrapSender struct {
	token string
}

func NewMailtrapSender(token string) *MailtrapSender {
	return &MailtrapSender{token: token}
}

func (m *MailtrapSender) Name() string { return "Mailtrap" }

func (m *MailtrapSender) Send(ctx context.Context, msg Message) error {
	type mtAddress struct {
		Email string `json:"email"`
		Name  string `json:"name,omitempty"`
	}
	type mtBody struct {
		From    mtAddress         `json:"from"`
		To      []mtAddress       `json:"to"`
		Subject string            `json:"subject"`
		Text    string            `json:"text,omitempty"`
		HTML    string            `json:"html,omitempty"`
		Headers map[string]string `json:"headers,omitempty"`
	}

	payload := mtBody{
		From:    mtAddress{Email: msg.From.Email, Name: msg.From.Name},
		To:      []mtAddress{{Email: msg.To.Email, Name: msg.To.Name}},
		Subject: msg.Subject,
		Text:    msg.Text,
		HTML:    msg.HTML,
	}

	// Threading headers
	if msg.InReplyTo != "" {
		payload.Headers = map[string]string{
			"In-Reply-To": msg.InReplyTo,
			"References":  strings.Join(msg.References, " "),
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mailtrapSendURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mailtrap %d: %s", resp.StatusCode, b)
	}
	return nil
}
