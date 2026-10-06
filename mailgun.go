package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// MailgunSender sends email via the Mailgun HTTP API.
type MailgunSender struct {
	apiKey string
	domain string
}

func NewMailgunSender(apiKey, domain string) *MailgunSender {
	return &MailgunSender{apiKey: apiKey, domain: domain}
}

func (m *MailgunSender) Name() string { return "Mailgun" }

func (m *MailgunSender) Send(ctx context.Context, msg Message) error {
	endpoint := fmt.Sprintf("https://api.mailgun.net/v3/%s/messages", m.domain)

	form := url.Values{}
	form.Set("from", msg.From.String())
	form.Set("to", msg.To.String())
	form.Set("subject", msg.Subject)
	if msg.Text != "" {
		form.Set("text", msg.Text)
	}
	if msg.HTML != "" {
		form.Set("html", msg.HTML)
	}
	if msg.ReplyTo != "" {
		form.Set("h:Reply-To", msg.ReplyTo)
	}
	if msg.InReplyTo != "" {
		form.Set("h:In-Reply-To", msg.InReplyTo)
		form.Set("h:References", strings.Join(msg.References, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("api", m.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mailgun %d: %s", resp.StatusCode, b)
	}
	return nil
}
