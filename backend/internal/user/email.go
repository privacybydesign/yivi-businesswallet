package user

import (
	"net/mail"
	"strings"
)

type Email string

func ParseEmail(raw string) (Email, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	return Email(strings.ToLower(addr.Address)), nil
}
