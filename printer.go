package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// printerWebhookPayload is the subset of Forward Email's webhook payload
// needed by the printer route.
//
// Forward Email uses mailparser-style parsed email objects. In particular:
//
//   from.value[].address
//   from.value[].name
//   subject
//   text
//   html
//   messageId
//   attachments[]
//
// Attachments contain Buffer-style content:
// {
//   "type": "Buffer",
//   "data": [ ... bytes ... ]
// }
type printerWebhookPayload struct {
	Subject   string `json:"subject"`
	Text      json.RawMessage `json:"text"`
	HTML      json.RawMessage `json:"html"`
	MessageID string `json:"messageId"`

	From struct {
		Value []struct {
			Address string `json:"address"`
			Name    string `json:"name"`
		} `json:"value"`
	} `json:"from"`

	Attachments []printerWebhookAttachment `json:"attachments"`
}

type printerWebhookAttachment struct {
	Type             string          `json:"type"`
	Content          json.RawMessage `json:"content"`
	ContentType      string          `json:"contentType"`
	Filename         string          `json:"filename"`
	ContentDisposition string       `json:"contentDisposition"`
}

// handlePrinter converts the Forward Email webhook into a new Maileroo
// message addressed directly to the configured Brother printer.
//
// This intentionally bypasses h.sendWithFallback() so that printer mail
// always uses Maileroo.
func (h *Handler) handlePrinter(
	ctx context.Context,
	body []byte,
) error {
	printer := strings.TrimSpace(h.cfg.BrotherPrinterEmail)

	if printer == "" {
		return fmt.Errorf(
			"BROTHER_PRINTER_EMAIL is not configured",
		)
	}

	if h.cfg.MailerooAPIKey == "" {
		return fmt.Errorf(
			"MAILEROO_API_KEY is not configured",
		)
	}

	var p printerWebhookPayload

	if err := json.Unmarshal(body, &p); err != nil {
		return fmt.Errorf(
			"parse printer webhook JSON: %w",
			err,
		)
	}

	sender := Address{}

	if len(p.From.Value) > 0 {
		sender.Email = strings.TrimSpace(
			p.From.Value[0].Address,
		)
		sender.Name = strings.TrimSpace(
			p.From.Value[0].Name,
		)
	}

	if sender.Email == "" {
		return fmt.Errorf(
			"printer webhook does not contain a sender address",
		)
	}

	text, err := webhookTextValue(p.Text)
	if err != nil {
		return fmt.Errorf(
			"decode printer webhook text: %w",
			err,
		)
	}

	html, err := webhookTextValue(p.HTML)
	if err != nil {
		return fmt.Errorf(
			"decode printer webhook HTML: %w",
			err,
		)
	}

	msg := Message{
		From: Address{
			Name:  h.cfg.FromName,
			Email: h.cfg.ForwardFromEmail,
		},
		To: Address{
			Email: printer,
		},
		Subject: p.Subject,
		Text:    text,
		HTML:    html,
	}

	totalAttachmentBytes := 0

	for _, incoming := range p.Attachments {
		if incoming.Filename == "" {
			log.Printf(
				"[printer] skipping attachment with no filename",
			)
			continue
		}

		content, err := webhookAttachmentBytes(
			incoming.Content,
		)
		if err != nil {
			return fmt.Errorf(
				"decode printer attachment %q: %w",
				incoming.Filename,
				err,
			)
		}

		contentType := strings.TrimSpace(
			incoming.ContentType,
		)
		if contentType == "" {
			contentType = "application/octet-stream"
		}

		inline := strings.EqualFold(
			strings.TrimSpace(incoming.ContentDisposition),
			"inline",
		)

		msg.Attachments = append(
			msg.Attachments,
			Attachment{
				Filename:    incoming.Filename,
				ContentType: contentType,
				Content:     content,
				Inline:      inline,
			},
		)

		totalAttachmentBytes += len(content)
	}

	if msg.Text == "" && msg.HTML == "" && len(msg.Attachments) == 0 {
		log.Printf(
			"[printer] warning: %q has no body and no attachments",
			p.Subject,
		)
	}

	if err := h.maileroo.Send(ctx, msg); err != nil {
		return fmt.Errorf(
			"send printer email via Maileroo: %w",
			err,
		)
	}

	log.Printf(
		"[printer] ✓ message=%q id=%q from=%s → %s attachments=%d bytes=%d",
		p.Subject,
		p.MessageID,
		sender.Email,
		printer,
		len(msg.Attachments),
		totalAttachmentBytes,
	)

	return nil
}

// webhookTextValue handles Forward Email's String-or-Buffer representation
// for text/html fields.
func webhookTextValue(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}

	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}

	var buffer struct {
		Type string `json:"type"`
		Data []int  `json:"data"`
	}

	if err := json.Unmarshal(raw, &buffer); err != nil {
		return "", err
	}

	if buffer.Type != "Buffer" {
		return "", fmt.Errorf(
			"unsupported content representation %q",
			buffer.Type,
		)
	}

	data, err := intBytes(buffer.Data)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// webhookAttachmentBytes decodes Forward Email's Buffer representation.
func webhookAttachmentBytes(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, fmt.Errorf("attachment has no content")
	}

	var buffer struct {
		Type string `json:"type"`
		Data []int  `json:"data"`
	}

	if err := json.Unmarshal(raw, &buffer); err != nil {
		return nil, err
	}

	if buffer.Type != "Buffer" {
		return nil, fmt.Errorf(
			"unsupported attachment content type %q",
			buffer.Type,
		)
	}

	return intBytes(buffer.Data)
}

// intBytes converts the JSON representation of a Node.js Buffer back into
// the original binary bytes.
func intBytes(values []int) ([]byte, error) {
	data := make([]byte, len(values))

	for i, value := range values {
		if value < 0 || value > 255 {
			return nil, fmt.Errorf(
				"invalid byte value %d at position %d",
				value,
				i,
			)
		}

		data[i] = byte(value)
	}

	return data, nil
}
