package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// Handler wires together the store and all providers.
type Handler struct {
	cfg       *Config
	store     *Store
	sendPulse *SendPulseSender
	smtp2go   *SMTP2GOSender
	postmark  *PostmarkSender
	mailtrap  *MailtrapSender
	maileroo  *MailerooSender
	mailgun   *MailgunSender

	// sendChain is the ordered list of outbound senders,
	// built from cfg.SendOrder.
	sendChain []Sender
}

// NewHandler builds a Handler and assembles the outbound sender chain
// according to cfg.SendOrder.
func NewHandler(
	cfg *Config,
	store *Store,
	sendPulse *SendPulseSender,
	smtp2go *SMTP2GOSender,
	postmark *PostmarkSender,
	mailtrap *MailtrapSender,
	maileroo *MailerooSender,
	mailgun *MailgunSender,
) *Handler {
	h := &Handler{
		cfg:       cfg,
		store:     store,
		sendPulse: sendPulse,
		smtp2go:   smtp2go,
		postmark:  postmark,
		mailtrap:  mailtrap,
		maileroo:  maileroo,
		mailgun:   mailgun,
	}

	byName := map[string]Sender{
		"SMTP2GO":  smtp2go,
		"POSTMARK": postmark,
		"MAILTRAP": mailtrap,
		"MAILEROO": maileroo,
		"MAILGUN":  mailgun,
	}

	configured := map[string]bool{
		"SMTP2GO":  cfg.SMTP2GOAPIKey != "",
		"POSTMARK": cfg.PostmarkToken != "",
		"MAILTRAP": cfg.MailtrapToken != "",
		"MAILEROO": cfg.MailerooAPIKey != "",
		"MAILGUN":  cfg.MailgunAPIKey != "" && cfg.MailgunDomain != "",
	}

	for _, name := range cfg.SendOrder {
		if configured[name] {
			h.sendChain = append(h.sendChain, byName[name])
		} else {
			log.Printf(
				"[config] DEFAULT lists %s but its credentials are unset — skipping",
				name,
			)
		}
	}

	if len(h.sendChain) == 0 {
		log.Printf(
			"[config] WARNING: no outbound senders have credentials configured — sending will fail",
		)
	} else {
		names := make([]string, len(h.sendChain))
		for i, s := range h.sendChain {
			names[i] = s.Name()
		}

		log.Printf(
			"[config] outbound send order: %s",
			strings.Join(names, " → "),
		)
	}

	return h
}

// ServeHTTP handles POST /webhook and POST /inbound.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(
			w,
			"method not allowed",
			http.StatusMethodNotAllowed,
		)
		return
	}

	body, err := io.ReadAll(
		io.LimitReader(r.Body, 10<<20),
	)
	if err != nil {
		http.Error(
			w,
			"read error",
			http.StatusBadRequest,
		)
		return
	}

	if !VerifyRequest(h.cfg.WebhookKey, r) {
		log.Printf(
			"[webhook] bad webhook key from %s",
			r.RemoteAddr,
		)

		http.Error(
			w,
			"unauthorized",
			http.StatusUnauthorized,
		)
		return
	}

	var payload FEPayload

	if err := json.Unmarshal(body, &payload); err != nil {
		log.Printf(
			"payload parse error: %v",
			err,
		)

		http.Error(
			w,
			"bad payload",
			http.StatusBadRequest,
		)
		return
	}

	// Determine which address this email was sent to.
	//
	// session.recipient is X-Original-To and is the most reliable source.
	toAddr := payload.Session.Recipient

	if toAddr == "" && len(payload.Recipients) > 0 {
		toAddr = payload.Recipients[0]
	}

	if toAddr == "" && len(payload.To.Value) > 0 {
		toAddr = payload.To.Value[0].Address
	}

	if toAddr == "" {
		log.Printf("[webhook] received message with no recipient")
		w.WriteHeader(http.StatusOK)
		return
	}

	local := LocalPart(
		strings.ToLower(toAddr),
	)

	// ------------------------------------------------------------
	// DEDICATED PRINTER ROUTE
	// ------------------------------------------------------------
	//
	// Printer delivery is deliberately synchronous.
	//
	// We only return HTTP 200 to Forward Email after Maileroo has
	// accepted the newly-created printer message.
	//
	// If Maileroo fails, return 502 so Forward Email can retry.
	if local == "print" && h.cfg.PrintEnabled {
		ctx, cancel := context.WithTimeout(
			r.Context(),
			15*time.Second,
		)
		defer cancel()

		if err := h.handlePrinter(ctx, body); err != nil {
			log.Printf(
				"[print] error: %v",
				err,
			)

			http.Error(
				w,
				"printer delivery failed",
				http.StatusBadGateway,
			)
			return
		}

		w.WriteHeader(http.StatusOK)
		return
	}

	if local == "print" && !h.cfg.PrintEnabled {
		log.Printf(
			"[print] disabled — treating message as normal inbound mail",
		)
	}

	// ------------------------------------------------------------
	// NORMAL ROUTES
	// ------------------------------------------------------------
	//
	// Existing inbound/reply/compose handling remains asynchronous.
	w.WriteHeader(http.StatusOK)

	go func() {
		ctx := context.Background()
		var routeErr error

		switch {
		case strings.HasPrefix(local, "emailreply-"):
			uuid := strings.TrimPrefix(
				local,
				"emailreply-",
			)

			routeErr = h.handleReply(
				ctx,
				payload,
				uuid,
			)

		case local == "compose":
			routeErr = h.handleCompose(
				ctx,
				payload,
			)

		default:
			routeErr = h.handleInbound(
				ctx,
				payload,
				toAddr,
			)
		}

		if routeErr != nil {
			log.Printf(
				"[%s] error: %v",
				local,
				routeErr,
			)
		}
	}()
}

// handleInbound: someone emailed *@shimpi.dev → forward to the
// configured destination.
func (h *Handler) handleInbound(
	ctx context.Context,
	p FEPayload,
	toAddr string,
) error {
	sender := p.From.First()

	// Prevent loops — ignore emails from privileged senders.
	for _, priv := range h.cfg.PrivilegedSenders {
		if strings.EqualFold(sender.Email, priv) {
			log.Printf(
				"[inbound] skipping loop from privileged sender %s",
				sender.Email,
			)
			return nil
		}
	}

	id, err := newID()
	if err != nil {
		return fmt.Errorf("uuid: %w", err)
	}

	// Reply-To uses the same domain the email arrived on,
	// so multi-domain mail can continue to work.
	incomingDomain := h.cfg.Domain

	if at := strings.Index(toAddr, "@"); at >= 0 {
		incomingDomain = toAddr[at+1:]
	}

	replyTo := "emailreply-" +
		id +
		"@" +
		incomingDomain

	if err := h.store.SaveMapping(
		id,
		sender.Email,
		sender.Name,
		p.Subject,
		p.MessageID,
	); err != nil {
		return fmt.Errorf(
			"save mapping: %w",
			err,
		)
	}

	text := fmt.Sprintf(
		"To: %s\nFrom: %s\n\n%s",
		toAddr,
		sender.String(),
		p.Text,
	)

	htmlStr := string(p.HTML)

	if htmlStr == "" && looksLikeHtml(p.Text) {
		log.Printf(
			"[inbound] text body looks like HTML — using it directly",
		)

		htmlStr = p.Text
	}

	card := senderCard(
		sender,
		toAddr,
	)

	if htmlStr != "" {
		htmlStr = htmlWrap(
			card + htmlStr,
		)
	} else if p.Text != "" {
		htmlStr = htmlWrap(
			card +
				`<div style="font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;font-size:15px;color:#1d1c1d;white-space:pre-wrap;">` +
				htmlEscape(p.Text) +
				`</div>`,
		)
	}

	// Explicit ROUTES entry wins; otherwise deliver to the master inbox.
	recipient := h.cfg.ResolveRoute(toAddr)

	fallback := false

	if recipient == "" {
		recipient = h.cfg.MyEmail
		fallback = true
	}

	// Construct the normal forwarded message.
	//
	// Attachments are handled separately by the provider layer once the
	// FEPayload attachment structure is wired into this generic path.
	msg := Message{
		From: Address{
			Email: h.cfg.ForwardFromEmail,
		},
		To: Address{
			Email: recipient,
		},
		ReplyTo:    replyTo,
		Subject:    p.Subject,
		Text:       text,
		HTML:       htmlStr,
		InReplyTo:  "",
		References: nil,
	}

	if err := h.sendWithFallback(
		ctx,
		msg,
	); err != nil {
		return err
	}

	routeTag := "route"

	if fallback {
		routeTag = "fallback"
	}

	log.Printf(
		"[inbound] ✓ [%s] %q from %s → %s to %s (reply-to: %s)",
		toAddr,
		p.Subject,
		sender.Email,
		routeTag,
		recipient,
		replyTo,
	)

	return nil
}

// handleReply: you hit Reply in Gmail → look up UUID,
// then send to original sender.
func (h *Handler) handleReply(
	ctx context.Context,
	p FEPayload,
	uuid string,
) error {
	from := p.From.First()

	if !h.isPrivileged(from.Email) {
		return fmt.Errorf(
			"unauthorized reply from %s",
			from.Email,
		)
	}

	mapping, err := h.store.LookupMapping(uuid)
	if err != nil {
		return fmt.Errorf(
			"lookup uuid %s: %w",
			uuid,
			err,
		)
	}

	htmlBody := string(p.HTML)

	if htmlBody == "" && looksLikeHtml(p.Text) {
		log.Printf(
			"[compose] text body looks like HTML — using it directly",
		)

		htmlBody = p.Text
	}

	msg := Message{
		From: Address{
			Name:  h.cfg.FromName,
			Email: h.cfg.FromEmail,
		},
		To: Address{
			Name:  mapping.SenderName,
			Email: mapping.SenderEmail,
		},
		Subject:    reSubject(mapping.Subject),
		Text:       p.Text,
		HTML:       htmlBody,
		InReplyTo:  mapping.MessageID,
		References: []string{mapping.MessageID},
	}

	if err := h.sendWithFallback(
		ctx,
		msg,
	); err != nil {
		return err
	}

	log.Printf(
		"[reply] ✓ %q → %s",
		mapping.Subject,
		mapping.SenderEmail,
	)

	return nil
}

// handleCompose: email compose@shimpi.dev with subject
// "recipient@x.com | Real subject".
func (h *Handler) handleCompose(
	ctx context.Context,
	p FEPayload,
) error {
	from := p.From.First()

	if !h.isPrivileged(from.Email) {
		return fmt.Errorf(
			"unauthorized compose from %s",
			from.Email,
		)
	}

	parts := strings.SplitN(
		p.Subject,
		"|",
		2,
	)

	if len(parts) != 2 {
		return fmt.Errorf(
			"compose subject must be 'recipient@x | Subject', got: %q",
			p.Subject,
		)
	}

	recipientsRaw := strings.TrimSpace(parts[0])
	subject := strings.TrimSpace(parts[1])

	htmlBody := string(p.HTML)

	if htmlBody == "" && looksLikeHtml(p.Text) {
		log.Printf(
			"[compose] text body looks like HTML — using it directly",
		)

		htmlBody = p.Text
	}

	recipients := strings.FieldsFunc(
		recipientsRaw,
		func(r rune) bool {
			return r == ',' ||
				r == ' ' ||
				r == '\n' ||
				r == '\t'
		},
	)

	if len(recipients) == 0 {
		return fmt.Errorf(
			"no recipients found in subject",
		)
	}

	sent := 0

	for _, addr := range recipients {
		addr = strings.TrimSpace(addr)

		if addr == "" {
			continue
		}

		id, err := newID()
		if err != nil {
			return fmt.Errorf(
				"uuid: %w",
				err,
			)
		}

		replyTo := "emailreply-" +
			id +
			"@" +
			h.cfg.Domain

		if err := h.store.SaveMapping(
			id,
			addr,
			"",
			subject,
			"",
		); err != nil {
			return fmt.Errorf(
				"save mapping: %w",
				err,
			)
		}

		msg := Message{
			From: Address{
				Name:  h.cfg.FromName,
				Email: h.cfg.FromEmail,
			},
			To: Address{
				Email: addr,
			},
			ReplyTo: replyTo,
			Subject: subject,
			Text:    p.Text,
			HTML:    htmlBody,
		}

		if err := h.sendWithFallback(
			ctx,
			msg,
		); err != nil {
			log.Printf(
				"[compose] ✗ %q → %s: %v",
				subject,
				addr,
				err,
			)
			continue
		}

		sent++
	}

	if sent == 0 {
		return fmt.Errorf(
			"compose: all recipients failed",
		)
	}

	log.Printf(
		"[compose] ✓ %q → %d/%d recipients",
		subject,
		sent,
		len(recipients),
	)

	return nil
}

// sendWithFallback tries each configured sender in the order set by DEFAULT.
func (h *Handler) sendWithFallback(
	ctx context.Context,
	msg Message,
) error {
	if len(h.sendChain) == 0 {
		return fmt.Errorf(
			"no outbound senders configured",
		)
	}

	for i, sender := range h.sendChain {
		if err := sender.Send(
			ctx,
			msg,
		); err == nil {
			log.Printf(
				"[%s] sent to %s",
				sender.Name(),
				msg.To.Email,
			)

			if sender.Name() == "Postmark" {
				_ = h.store.IncrPostmarkCount()
			}

			return nil
		} else {
			if i == len(h.sendChain)-1 {
				return fmt.Errorf(
					"all senders failed, last error (%s): %w",
					sender.Name(),
					err,
				)
			}

			log.Printf(
				"[%s] failed (%v), trying next",
				sender.Name(),
				err,
			)
		}
	}

	return nil
}

func (h *Handler) isPrivileged(email string) bool {
	for _, s := range h.cfg.PrivilegedSenders {
		if strings.EqualFold(email, s) {
			return true
		}
	}

	return false
}

func reSubject(subject string) string {
	if strings.HasPrefix(
		strings.ToLower(subject),
		"re:",
	) {
		return subject
	}

	return "Re: " + subject
}

// looksLikeHtml reports whether a plain-text email actually contains
// a full HTML document.
func looksLikeHtml(s string) bool {
	t := strings.TrimSpace(s)

	return strings.HasPrefix(t, "<!DOCTYPE") ||
		strings.HasPrefix(t, "<!doctype") ||
		strings.HasPrefix(t, "<html")
}

// newID creates a random UUID-like identifier used for reply mappings.
func newID() (string, error) {
	b := make([]byte, 16)

	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return fmt.Sprintf(
		"%x-%x-%x-%x-%x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:],
	), nil
}

// htmlWrap wraps content in a full HTML document for maximum
// email client compatibility.
func htmlWrap(body string) string {
	return `<!DOCTYPE html><html><head><meta charset="UTF-8"></head><body style="margin:0;padding:16px;background:#fff;">` +
		body +
		`</body></html>`
}

// htmlEscape escapes the five special HTML characters so plain text
// can safely be embedded inside an HTML element.
func htmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	s = strings.ReplaceAll(s, "'", "&#39;")
	return s
}

// senderCard returns a styled HTML card prepended to every forwarded email.
func senderCard(
	sender Address,
	toAddr string,
) string {
	initial := "?"

	if len([]rune(sender.Name)) > 0 {
		initial = strings.ToUpper(
			string([]rune(sender.Name)[0]),
		)
	} else if len(sender.Email) > 0 {
		initial = strings.ToUpper(
			string(sender.Email[0]),
		)
	}

	colors := []string{
		"#3b82f6",
		"#8b5cf6",
		"#ec4899",
		"#f97316",
		"#10b981",
		"#ef4444",
	}

	colorIdx := 0

	for _, c := range sender.Email {
		colorIdx += int(c)
	}

	color := colors[colorIdx%len(colors)]

	return `` +
		`<div style="margin-bottom:16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;">` +
		`<table cellpadding="0" cellspacing="0" border="0" style="border-radius:8px;border:1px solid #e0e0e0;background:#f9f9fb;max-width:480px;">` +
		`<tr><td style="padding:12px 16px;">` +
		`<table cellpadding="0" cellspacing="0" border="0"><tr>` +
		`<td style="padding-right:12px;vertical-align:top;">` +
		`<div style="width:40px;height:40px;border-radius:50%;background-color:` +
		color +
		`;color:#fff;text-align:center;line-height:40px;font-size:18px;font-weight:600;">` +
		initial +
		`</div>` +
		`</td>` +
		`<td style="vertical-align:top;">` +
		`<div style="font-size:14px;color:#555;"><strong>From:</strong> ` +
		htmlEscape(sender.Email) +
		`</div>` +
		`<div style="font-size:13px;color:#666;margin-top:2px;"><strong>To:</strong> ` +
		htmlEscape(toAddr) +
		`</div>` +
		`</td>` +
		`</tr></table>` +
		`</td></tr></table></div>` +
		`<hr style="border:none;border-top:1px solid #e0e0e0;margin:0 0 16px;">`
}
