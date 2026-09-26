// Package delivery sends a completed digest. Delivery failure is returned to
// the caller so a scheduled run exits non-zero instead of failing silently.
package delivery

import (
	"fmt"
	"net/smtp"
	"strings"
)

type Sender interface {
	Send(subject, body string) error
}
type SMTP struct{ Host, From, To, Username, Password string }

func (s SMTP) Send(subject, body string) error {
	if s.Host == "" || s.From == "" || s.To == "" {
		return fmt.Errorf("SMTP host, from, and to are required")
	}
	host := strings.Split(s.Host, ":")[0]
	var auth smtp.Auth
	if s.Username != "" {
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}
	msg := []byte("To: " + s.To + "\r\nFrom: " + s.From + "\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	return smtp.SendMail(s.Host, auth, s.From, strings.Split(s.To, ","), msg)
}
