package mailer

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeSMTP is a minimal SMTP server: enough of the protocol for one message, and
// no STARTTLS, which is what a plaintext relay (MailHog in dev) looks like.
type fakeSMTP struct {
	listener net.Listener
	mu       sync.Mutex
	commands []string
	data     string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeSMTP{listener: listener}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (s *fakeSMTP) hostPort(t *testing.T) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	return host, number
}

func (s *fakeSMTP) serve() {
	conn, err := s.listener.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()

	reader := bufio.NewReader(conn)
	write := func(line string) { _, _ = fmt.Fprintf(conn, "%s\r\n", line) }
	write("220 fake ESMTP")

	inData := false
	var body strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false
				s.mu.Lock()
				s.data = body.String()
				s.mu.Unlock()
				write("250 2.0.0 Ok")
				continue
			}
			body.WriteString(line + "\n")
			continue
		}

		s.mu.Lock()
		s.commands = append(s.commands, line)
		s.mu.Unlock()

		switch {
		case strings.HasPrefix(line, "EHLO"):
			// No STARTTLS and no AUTH: a plaintext relay.
			write("250-fake")
			write("250 8BITMIME")
		case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
			write("250 2.0.0 Ok")
		case line == "DATA":
			inData = true
			write("354 End data with <CR><LF>.<CR><LF>")
		case line == "QUIT":
			write("221 2.0.0 Bye")
			return
		default:
			write("502 5.5.1 Command not implemented")
		}
	}
}

func (s *fakeSMTP) sawCommand(prefix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, command := range s.commands {
		if strings.HasPrefix(command, prefix) {
			return true
		}
	}
	return false
}

func (s *fakeSMTP) message() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data
}

// The MailHog-style path is unchanged by the mechanism switch: an empty username
// still sends without authenticating at all.
func TestSendDeliversWithoutAuthentication(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.hostPort(t)

	err := New().Send(Config{
		Host: host, Port: port,
		FromName: "Acme BV", FromAddress: "no-reply@acme.example",
	}, Message{
		To: "person@example.org", Subject: "Hello",
		TextBody: "plain body", HTMLBody: "<p>html body</p>",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if server.sawCommand("AUTH") {
		t.Error("an unauthenticated send issued an AUTH command")
	}
	if !strings.Contains(server.message(), "plain body") {
		t.Errorf("the relay did not receive the body:\n%s", server.message())
	}
}

// A relay that does not offer STARTTLS must not receive a bearer token: the send
// fails before AUTH, and nothing is handed over.
func TestSendRefusesXOAuth2WithoutStartTLS(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.hostPort(t)

	err := New().Send(Config{
		Host: host, Port: port, AuthMechanism: AuthXOAuth2,
		FromAddress: "no-reply@acme.example", AccessToken: "access-token-1",
	}, Message{To: "person@example.org", Subject: "Hello", TextBody: "plain body"})
	if err == nil {
		t.Fatal("the token was offered to a relay with no STARTTLS")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("err = %v, want it to name the missing STARTTLS", err)
	}
	if server.sawCommand("AUTH") {
		t.Error("an AUTH command was issued over an unencrypted connection")
	}
	if server.message() != "" {
		t.Error("a message was delivered despite the refusal")
	}
}

// An unusable config is refused before the transport dials, so a misconfigured
// org does not open a connection on every send to learn the same thing.
func TestSendRefusesAnUnusableConfigBeforeDialing(t *testing.T) {
	server := newFakeSMTP(t)
	host, port := server.hostPort(t)

	err := New().Send(Config{
		Host: host, Port: port, AuthMechanism: AuthXOAuth2,
		FromAddress: "no-reply@acme.example",
	}, Message{To: "person@example.org", Subject: "Hello"})
	if err == nil {
		t.Fatal("a config with no access token was sent with")
	}
	if server.sawCommand("EHLO") {
		t.Error("the transport dialed before validating the config")
	}
}
