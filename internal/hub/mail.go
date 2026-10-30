package hub

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
)

// Mailer sends one plain-text email.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMTP sends mail through an SMTP server: smtp://user:pass@host:587 (STARTTLS, required) or
// smtps://user:pass@host:465 (TLS from the start).
type SMTP struct {
	Addr     string // host:port
	Implicit bool   // smtps
	User     string
	Password string
	From     string
	// TLS overrides the TLS config (tests); nil = verify the server's certificate for its host name.
	TLS *tls.Config
}

// ParseSMTP reads RETAKE_SMTP_URL. from is the sender; "" = retake@<public host>.
func ParseSMTP(raw, from, publicHost string) (*SMTP, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "smtp" && u.Scheme != "smtps") || u.Hostname() == "" {
		return nil, errors.New("the SMTP URL must look like smtp://user:password@host:587 or smtps://...:465")
	}
	m := &SMTP{Implicit: u.Scheme == "smtps"}
	port := u.Port()
	if port == "" {
		port = "587"
		if m.Implicit {
			port = "465"
		}
	}
	m.Addr = net.JoinHostPort(u.Hostname(), port)
	if u.User != nil {
		m.User = u.User.Username()
		m.Password, _ = u.User.Password()
	}
	m.From = strings.TrimSpace(from)
	if m.From == "" {
		host := publicHost
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == "" {
			host = "localhost"
		}
		m.From = "retake@" + host
	}
	if _, err := accounts.NormalizeEmail(m.From); err != nil {
		return nil, fmt.Errorf("the mail sender: %w", err)
	}
	return m, nil
}

// Send sends one message.
func (m *SMTP) Send(ctx context.Context, to, subject, body string) error {
	if strings.ContainsAny(to+subject, "\r\n") {
		return errors.New("a header can't have a line break")
	}
	host, _, _ := net.SplitHostPort(m.Addr)
	tc := m.TLS
	if tc == nil {
		tc = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	d := &net.Dialer{Timeout: 20 * time.Second}
	var conn net.Conn
	var err error
	if m.Implicit {
		conn, err = (&tls.Dialer{NetDialer: d, Config: tc}).DialContext(ctx, "tcp", m.Addr)
	} else {
		conn, err = d.DialContext(ctx, "tcp", m.Addr)
	}
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(time.Minute))
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()
	if !m.Implicit {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("the SMTP server doesn't offer STARTTLS; use smtps:// or a server that does")
		}
		if err := c.StartTLS(tc); err != nil {
			return err
		}
	}
	if m.User != "" {
		if err := c.Auth(smtp.PlainAuth("", m.User, m.Password, host)); err != nil {
			return err
		}
	}
	if err := c.Mail(m.From); err != nil { //nolint:gosec // From is checked as an address by ParseSMTP
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	msg := "From: Retake <" + m.From + ">\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nDate: " +
		time.Now().UTC().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n")
	if _, err := wc.Write([]byte(msg)); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// MailLink mails a sign-in link to u.
func MailLink(ctx context.Context, m Mailer, u accounts.User, link string, expires time.Time) error {
	if m == nil {
		return errors.New("mail isn't set up")
	}
	body := "Hello" + strings.TrimSpace(" "+u.Name) + ",\n\nHere is your link to sign in to Retake:\n\n" + link +
		"\n\nIt works once, until " + expires.UTC().Format("2 Jan 2006 15:04 MST") + ". If you didn't ask for it, " +
		"ignore this message.\n"
	return m.Send(ctx, u.Email, "Your Retake sign-in link", body)
}
