// Package mail sends the app's emails (email confirmation, password reset): over SMTP in
// production, and to the log in development.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Message is a plain-text email.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Mailer sends messages.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

var errHeaderInjection = errors.New("mail: line break in an address or subject")

// check rejects line breaks in header values, which would let input add headers or recipients.
func (m Message) check(from string) error {
	for _, v := range []string{m.To, m.Subject, from} {
		if strings.ContainsAny(v, "\r\n") {
			return errHeaderInjection
		}
	}
	return nil
}

// LogMailer writes messages, links included, to the log instead of sending them. It is for
// development only: links in emails are credentials, and logs are not the place for them.
type LogMailer struct{}

func (LogMailer) Send(ctx context.Context, m Message) error {
	if err := m.check(""); err != nil {
		return err
	}
	slog.InfoContext(ctx, "email (development: not sent)", "to", m.To, "subject", m.Subject, "text", m.Text)
	return nil
}

// SMTPMailer sends through an SMTP server. It always uses TLS (STARTTLS), except to a server on
// localhost such as a local mail catcher.
type SMTPMailer struct {
	Host, Port         string
	Username, Password string
	From               string
}

// sendTimeout bounds the whole SMTP conversation.
const sendTimeout = 30 * time.Second

func (s SMTPMailer) Send(ctx context.Context, m Message) error {
	if err := m.check(s.From); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(s.Host, s.Port))
	if err != nil {
		return fmt.Errorf("mail: connect: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("mail: %w", err)
	}
	defer c.Close()

	local := s.Host == "localhost" || s.Host == "127.0.0.1" || s.Host == "::1"
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	} else if !local {
		return errors.New("mail: the server does not offer STARTTLS; refusing to send credentials and links in the clear")
	}
	if s.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return fmt.Errorf("mail: from: %w", err)
	}
	if err := c.Rcpt(m.To); err != nil {
		return fmt.Errorf("mail: to: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	headers := "From: " + s.From + "\r\n" +
		"To: " + m.To + "\r\n" +
		"Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n" +
		"Date: " + time.Now().Format(time.RFC1123Z) + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n"
	body := strings.ReplaceAll(strings.ReplaceAll(m.Text, "\r\n", "\n"), "\n", "\r\n")
	if _, err := w.Write([]byte(headers + body)); err != nil {
		return fmt.Errorf("mail: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: send: %w", err)
	}
	return c.Quit()
}

// Recorder keeps messages instead of sending them, for tests.
type Recorder struct {
	mu   sync.Mutex
	sent []Message
}

func (r *Recorder) Send(_ context.Context, m Message) error {
	if err := m.check(""); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, m)
	return nil
}

// Sent returns the messages sent so far.
func (r *Recorder) Sent() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.sent...)
}
