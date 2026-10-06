package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

type Config struct {
	Port              string
	Domain            string
	FromEmail         string
	ForwardFromEmail  string
	FromName          string
	MyEmail           string
	PrivilegedSenders []string
	WebhookKey        string

	// Dedicated Brother printer destination.
	// Example:
	// BROTHER_PRINTER_EMAIL=13035596380@print.brother.com
	BrotherPrinterEmail string

	// PrintEnabled toggles the dedicated print@ route.
	//
	// When false, print@ recipients fall through to normal
	// inbound handling instead of the Brother printer.
	PrintEnabled bool

	SendPulseClientID     string
	SendPulseClientSecret string
	SMTP2GOAPIKey         string
	PostmarkToken         string
	MailtrapToken         string
	MailerooAPIKey        string
	MailgunAPIKey         string
	MailgunDomain         string

	// SendOrder is the fallback chain for outbound mail.
	//
	// Example:
	// DEFAULT="POSTMARK SMTP2GO MAILTRAP MAILEROO MAILGUN"
	SendOrder []string

	// Routes maps a full lowercase recipient address to the mailbox
	// it should be delivered to.
	//
	// Example:
	// ROUTES=hello@example.com:first@example.net,support@example.com:second@example.net
	//
	// Addresses not listed here fall back to MyEmail.
	Routes map[string]string

	DBPath string
}

// validSenderNames lists every provider name DEFAULT can reference.
var validSenderNames = map[string]bool{
	"SMTP2GO":  true,
	"POSTMARK": true,
	"MAILTRAP": true,
	"MAILEROO": true,
	"MAILGUN":  true,
}

func LoadConfig() (*Config, error) {
	cfg := &Config{
		Port:                getenv("PORT", "6245"),
		Domain:              getenv("DOMAIN", ""),
		FromEmail:           getenv("FROM_EMAIL", ""),
		ForwardFromEmail:    getenv("FORWARD_FROM_EMAIL", ""),
		FromName:            getenv("FROM_NAME", ""),
		MyEmail:             getenv("MY_EMAIL", ""),
		WebhookKey:          getenv("WEBHOOK_KEY", ""),
		BrotherPrinterEmail: getenv("BROTHER_PRINTER_EMAIL", ""),

		PrintEnabled: strings.EqualFold(
			getenv("PRINT_ENABLED", "true"),
			"true",
		),

		SendPulseClientID:     getenv("SENDPULSE_CLIENT_ID", ""),
		SendPulseClientSecret: getenv("SENDPULSE_CLIENT_SECRET", ""),
		SMTP2GOAPIKey:         getenv("SMTP2GO_API_KEY", ""),
		PostmarkToken:         getenv("POSTMARK_TOKEN", ""),
		MailtrapToken:         getenv("MAILTRAP_TOKEN", ""),
		MailerooAPIKey:        getenv("MAILEROO_API_KEY", ""),
		MailgunAPIKey:         getenv("MAILGUN_API_KEY", ""),
		MailgunDomain:         getenv("MAILGUN_DOMAIN", ""),

		DBPath: getenv("DB_PATH", "/share/CACHEDEV1_DATA/mailbridge/data.json"),
	}

	for _, s := range strings.Split(getenv("PRIVILEGED_SENDERS", ""), ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			cfg.PrivilegedSenders = append(cfg.PrivilegedSenders, s)
		}
	}

	cfg.Routes = map[string]string{}

	for _, r := range strings.Split(getenv("ROUTES", ""), ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}

		parts := strings.SplitN(r, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			log.Printf(
				"[config] ignoring malformed ROUTES entry %q (want alias@domain:destination@domain)",
				r,
			)
			continue
		}

		key := strings.ToLower(strings.TrimSpace(parts[0]))
		dest := strings.TrimSpace(parts[1])

		if key != "" && dest != "" {
			cfg.Routes[key] = dest
		}
	}

	// Parse:
	// DEFAULT="POSTMARK SMTP2GO MAILTRAP MAILEROO MAILGUN"
	//
	// Both spaces and commas are accepted.
	defaultRaw := getenv(
		"DEFAULT",
		"SMTP2GO POSTMARK MAILTRAP MAILEROO",
	)

	fields := strings.FieldsFunc(defaultRaw, func(r rune) bool {
		return r == ' ' || r == ','
	})

	for _, f := range fields {
		name := strings.ToUpper(strings.TrimSpace(f))
		if validSenderNames[name] {
			cfg.SendOrder = append(cfg.SendOrder, name)
		}
	}

	if len(cfg.SendOrder) == 0 {
		return nil, fmt.Errorf(
			"DEFAULT must list at least one valid sender: SMTP2GO, POSTMARK, MAILTRAP, MAILEROO, MAILGUN",
		)
	}

	required := map[string]string{
		"DOMAIN":                  cfg.Domain,
		"FROM_EMAIL":              cfg.FromEmail,
		"FORWARD_FROM_EMAIL":      cfg.ForwardFromEmail,
		"MY_EMAIL":                cfg.MyEmail,
		"SENDPULSE_CLIENT_ID":     cfg.SendPulseClientID,
		"SENDPULSE_CLIENT_SECRET": cfg.SendPulseClientSecret,
	}

	for k, v := range required {
		if v == "" {
			return nil, fmt.Errorf("missing required env var: %s", k)
		}
	}

	if len(cfg.PrivilegedSenders) == 0 {
		return nil, fmt.Errorf("missing required env var: PRIVILEGED_SENDERS")
	}

	return cfg, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ResolveRoute returns the delivery address for toAddr, or "" when no
// explicit route exists (caller should fall back to MyEmail).
func (c *Config) ResolveRoute(toAddr string) string {
	if len(c.Routes) == 0 {
		return ""
	}

	return c.Routes[strings.ToLower(strings.TrimSpace(toAddr))]
}
