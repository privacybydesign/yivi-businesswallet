package mailer

import (
	"errors"
	"fmt"
	"net/smtp"
	"strings"
)

const (
	// xoauth2Mechanism is the SASL mechanism name as it goes out in the AUTH command.
	xoauth2Mechanism = "XOAUTH2"

	// xoauth2Separator is the SASL field separator, CTRL-A. It goes between the two
	// fields of the client response and twice more to terminate it, which is why a
	// stray one in either field would forge a field rather than corrupt one.
	xoauth2Separator = '\x01'
)

// xoauth2Auth is the SASL XOAUTH2 client (Google's mechanism, and the one
// Microsoft 365 requires now that it has turned off Basic Authentication for
// SMTP AUTH). The whole exchange is a single client response:
//
//	user=<mailbox>^Aauth=Bearer <token>^A^A
//
// net/smtp base64-encodes what Start returns, so this builds the raw form.
type xoauth2Auth struct {
	username string
	token    string
}

// XOAuth2Auth returns an smtp.Auth that authenticates with an OAuth2 bearer
// token for username. It is the token-carrying counterpart of smtp.PlainAuth and
// keeps that type's posture: the credential only goes out over an encrypted
// connection.
func XOAuth2Auth(username, token string) smtp.Auth {
	return &xoauth2Auth{username: username, token: token}
}

func (a *xoauth2Auth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	// The same refusal smtp.PlainAuth makes, for the same reason and with no
	// localhost exemption: a bearer token is a live credential for the whole
	// mailbox, and the only server that speaks XOAUTH2 is a hosted one reached
	// over the internet. Send() also requires STARTTLS before it gets here; this
	// is the check that cannot be skipped by a future caller.
	if !server.TLS {
		return "", nil, errors.New("mailer: refusing to send an XOAUTH2 token over an unencrypted connection")
	}
	if a.username == "" || a.token == "" {
		return "", nil, errors.New("mailer: XOAUTH2 needs both a username and an access token")
	}
	// The username and the token are ours, not a peer's, but a stray ^A would
	// forge an extra field in the SASL response, so refuse rather than truncate.
	if strings.ContainsRune(a.username, xoauth2Separator) || strings.ContainsRune(a.token, xoauth2Separator) {
		return "", nil, errors.New("mailer: XOAUTH2 username and access token must not contain a separator byte")
	}
	return xoauth2Mechanism, fmt.Appendf(nil, "user=%s%cauth=Bearer %s%c%c",
		a.username, xoauth2Separator, a.token, xoauth2Separator, xoauth2Separator), nil
}

// Next answers the server's challenge. XOAUTH2 has no second client message: a
// server that rejects the token sends one base64 challenge carrying its reason
// and expects an empty response, after which it issues the real failure status —
// which is the error the caller sees. Anything else is a server that is not
// speaking this mechanism.
func (a *xoauth2Auth) Next(_ []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	return []byte{}, nil
}
