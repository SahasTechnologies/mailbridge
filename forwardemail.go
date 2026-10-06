package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// FEPayload is the JSON body ForwardEmail POSTs to your webhook.
// Structure confirmed from https://forwardemail.net/en/faq#do-you-support-webhooks
type FEPayload struct {
	From       FEAddressField `json:"from"`
	To         FEAddressField `json:"to"`
	CC         FEAddressField `json:"cc"`
	Subject    string         `json:"subject"`
	Text       string         `json:"text"`
	HTML       FEHTMLField    `json:"html"`      // mailparser returns false (no HTML) or a string
	Date       string         `json:"date"`
	MessageID  string         `json:"messageId"`
	InReplyTo  string         `json:"inReplyTo"`
	References FERefsField    `json:"references"` // string or []string from mailparser
	Recipients []string       `json:"recipients"` // envelope recipients grouped for this webhook
	Session    FESession      `json:"session"`
}

// FESession contains SMTP session info sent by ForwardEmail.
// session.recipient is the value of X-Original-To — the most reliable
// way to know which alias was actually hit.
type FESession struct {
	Recipient      string `json:"recipient"`
	RemoteAddress  string `json:"remoteAddress"`
	ClientHostname string `json:"clientHostname"`
	Sender         string `json:"sender"`
}

// FEAddressField holds a parsed address list from mailparser.
type FEAddressField struct {
	Value []FEAddress `json:"value"`
	Text  string      `json:"text"`
}

// First returns the first address in the field, or a zero Address.
func (f FEAddressField) First() Address {
	if len(f.Value) == 0 {
		return Address{}
	}
	return Address{Name: f.Value[0].Name, Email: f.Value[0].Address}
}

// FEAddress is a single parsed email address.
type FEAddress struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// FEHTMLField handles mailparser's html field which is either a string or `false`.
type FEHTMLField string

func (h *FEHTMLField) UnmarshalJSON(data []byte) error {
	if string(data) == "false" || string(data) == "null" {
		*h = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*h = FEHTMLField(s)
	return nil
}

// FERefsField handles mailparser's references field which is either a string or []string.
type FERefsField []string

func (r *FERefsField) UnmarshalJSON(data []byte) error {
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*r = arr
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return nil // field is optional, ignore
	}
	if s != "" {
		*r = []string{s}
	}
	return nil
}

// LocalPart extracts the local part of an email address (the part before @).
func LocalPart(email string) string {
	at := strings.Index(email, "@")
	if at < 0 {
		return email
	}
	return email[:at]
}

// VerifyRequest checks the ?key= query parameter against the configured webhook key.
// ForwardEmail's HMAC signing requires a paid plan; the secret URL param is
// secure over HTTPS since CF Tunnel encrypts the connection end-to-end.
// Leave secret empty to skip verification (dev only).
func VerifyRequest(secret string, r *http.Request) bool {
	if secret == "" {
		return true
	}
	return r.URL.Query().Get("key") == secret
}
