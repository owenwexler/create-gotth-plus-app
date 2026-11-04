package mail

import (
	"context"
	"testing"
)

func TestHeaderInjectionIsRejected(t *testing.T) {
	for _, m := range []Message{
		{To: "ann@example.com\r\nBcc: everyone@example.com", Subject: "Hi"},
		{To: "ann@example.com", Subject: "Hi\nBcc: everyone@example.com"},
	} {
		for name, mailer := range map[string]Mailer{"log": LogMailer{}, "recorder": &Recorder{}, "smtp": SMTPMailer{Host: "127.0.0.1", Port: "1", From: "app@example.com"}} {
			if err := mailer.Send(context.Background(), m); err != errHeaderInjection {
				t.Errorf("%s: Send(%q) = %v, want errHeaderInjection", name, m.To+m.Subject, err)
			}
		}
	}
}

func TestRecorder(t *testing.T) {
	r := &Recorder{}
	_ = r.Send(context.Background(), Message{To: "ann@example.com", Subject: "Hi", Text: "Hello"})
	if sent := r.Sent(); len(sent) != 1 || sent[0].Text != "Hello" {
		t.Errorf("sent %+v", sent)
	}
}
