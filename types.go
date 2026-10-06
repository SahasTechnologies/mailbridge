package main

import "context"

// Address is a name+email pair.
type Address struct {
	Name  string
	Email string
}

// String formats the address as "Name <email>" or just "email".
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return a.Name + " <" + a.Email + ">"
}

// Attachment is a provider-agnostic email attachment.
//
// Content contains the raw binary bytes. Individual providers are
// responsible for encoding it into whatever representation their API uses.
type Attachment struct {
	Filename    string
	ContentType string
	Content     []byte
	Inline      bool
	ContentID   string
}

// Message is the provider-agnostic email payload passed to every Sender.
type Message struct {
	From        Address
	To          Address
	ReplyTo     string
	Subject     string
	Text        string
	HTML        string
	InReplyTo   string // for threading: original Message-ID
	References  []string // for threading
	Attachments []Attachment
}

// Sender is implemented by every outbound provider.
type Sender interface {
	Name() string
	Send(ctx context.Context, msg Message) error
}
