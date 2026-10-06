package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

// runTestProviders sends one test email through every provider that has
// credentials configured, using the exact same Sender implementations
// MailBridge uses in production. Invoked via: ./mailbridge test
func runTestProviders(cfg *Config) {
	to := "sahas.shimpi@gmail.com"
	if len(os.Args) > 2 {
		to = os.Args[2] // allow: ./mailbridge test someone@else.com
	}

	fmt.Println("==================================================")
	fmt.Printf(" MailBridge Provider Test — sending to %s\n", to)
	fmt.Println("==================================================")

	msg := Message{
		From:    Address{Name: cfg.FromName, Email: cfg.FromEmail},
		To:      Address{Email: to},
		Subject: "[Test] MailBridge provider check",
		Text:    "This is a test email sent directly through MailBridge's Go sender code.",
		HTML:    "<p>This is a test email sent directly through MailBridge's <strong>Go sender code</strong>.</p>",
	}

	type testCase struct {
		name      string
		configured bool
		sender    Sender
	}

	cases := []testCase{
		{"SMTP2GO", cfg.SMTP2GOAPIKey != "", NewSMTP2GOSender(cfg.SMTP2GOAPIKey)},
		{"Postmark", cfg.PostmarkToken != "", NewPostmarkSender(cfg.PostmarkToken)},
		{"Mailtrap", cfg.MailtrapToken != "", NewMailtrapSender(cfg.MailtrapToken)},
		{"Maileroo", cfg.MailerooAPIKey != "", NewMailerooSender(cfg.MailerooAPIKey)},
		{"Mailgun", cfg.MailgunAPIKey != "" && cfg.MailgunDomain != "", NewMailgunSender(cfg.MailgunAPIKey, cfg.MailgunDomain)},
	}

	for _, tc := range cases {
		fmt.Printf("\n--- %s ---\n", tc.name)
		if !tc.configured {
			fmt.Println("  ⏭  SKIPPED (credentials not set)")
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := tc.sender.Send(ctx, msg)
		cancel()
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
			fmt.Println("  ❌ FAIL")
		} else {
			fmt.Println("  ✅ PASS")
		}
	}

	// SendPulse is special-cased: it's the inbound-forwarding sender,
	// with a different From address (ForwardFromEmail) and Reply-To.
	fmt.Printf("\n--- SendPulse ---\n")
	if cfg.SendPulseClientID == "" || cfg.SendPulseClientSecret == "" {
		fmt.Println("  ⏭  SKIPPED (credentials not set)")
	} else {
		sp := NewSendPulseSender(cfg.SendPulseClientID, cfg.SendPulseClientSecret)
		spMsg := msg
		spMsg.From = Address{Name: cfg.FromName, Email: cfg.ForwardFromEmail}
		spMsg.ReplyTo = "test-reply@" + cfg.Domain
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := sp.Send(ctx, spMsg)
		cancel()
		if err != nil {
			fmt.Printf("  Error: %v\n", err)
			fmt.Println("  ❌ FAIL")
		} else {
			fmt.Println("  ✅ PASS")
		}
	}

	fmt.Println("\n==================================================")
	fmt.Printf(" Done — check %s for however many arrived.\n", to)
	fmt.Println("==================================================")
}
