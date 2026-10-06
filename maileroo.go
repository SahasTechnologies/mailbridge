package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const mailerooSendURL = "https://smtp.maileroo.com/api/v2/emails"

// MailerooSender sends email via the Maileroo API v2.
type MailerooSender struct {
	apiKey string
}

func NewMailerooSender(apiKey string) *MailerooSender {
	return &MailerooSender{apiKey: apiKey}
}

func (m *MailerooSender) Name() string {
	return "Maileroo"
}

// mrAddress matches Maileroo's expected address object shape:
//
//	{
//	  "address": "...",
//	  "display_name": "..."
//	}
type mrAddress struct {
	Address     string `json:"address"`
	DisplayName string `json:"display_name,omitempty"`
}

// mrAttachment matches Maileroo's API attachment shape.
//
// Maileroo expects the attachment content as a base64-encoded string.
type mrAttachment struct {
	FileName    string `json:"file_name"`
	ContentType string `json:"content_type"`
	Content     string `json:"content"`
	Inline      bool   `json:"inline,omitempty"`
}

func (m *MailerooSender) Send(ctx context.Context, msg Message) error {
	if m.apiKey == "" {
		return fmt.Errorf("maileroo API key is not configured")
	}

	type mrBody struct {
		From        mrAddress       `json:"from"`
		To          []mrAddress     `json:"to"`
		Subject     string          `json:"subject"`
		Plain       string          `json:"plain,omitempty"`
		HTML        string          `json:"html,omitempty"`
		ReplyTo     *mrAddress      `json:"reply_to,omitempty"`
		Attachments []mrAttachment `json:"attachments,omitempty"`
	}

	payload := mrBody{
		From:    mrAddress{Address: msg.From.Email, DisplayName: msg.From.Name},
		To:      []mrAddress{{Address: msg.To.Email, DisplayName: msg.To.Name}},
		Subject: msg.Subject,
		Plain:   msg.Text,
		HTML:    msg.HTML,
	}

	if msg.ReplyTo != "" {
		payload.ReplyTo = &mrAddress{Address: msg.ReplyTo}
	}

	for _, attachment := range msg.Attachments {
		if attachment.Filename == "" {
			return fmt.Errorf("maileroo attachment has no filename")
		}

		contentType := attachment.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		payload.Attachments = append(payload.Attachments, mrAttachment{
			FileName:    attachment.Filename,
			ContentType: contentType,
			Content:     base64.StdEncoding.EncodeToString(attachment.Content),
			Inline:      attachment.Inline,
		})
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal Maileroo payload: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		mailerooSendURL,
		bytes.NewReader(data),
	)
	if err != nil {
		return fmt.Errorf("create Maileroo request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", m.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("Maileroo request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)

		return fmt.Errorf(
			"Maileroo %d: %s",
			resp.StatusCode,
			b,
		)
	}

	return nil
}
