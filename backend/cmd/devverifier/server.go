package main

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/privacybydesign/irmago/eudi/openid4vp"

	eudijwt "github.com/privacybydesign/irmago/eudi/jwt"

	"github.com/privacybydesign/yivi-businesswallet/backend/internal/devverifier"
)

const (
	sessionIDBytes  = 12
	maxFormBytes    = 4 << 20
	defaultQueryID  = "requested"
	defaultVCT      = "nl.kvk.registration"
	refreshSeconds  = 2
	sessionLifetime = 30 * time.Minute
)

// session is one Authorization Request and what came back for it.
type session struct {
	id            string
	created       time.Time
	vct           string
	claims        []string
	responseMode  string
	nonce         string
	state         string
	requestObject string
	fetched       bool
	encryptionKey *ecdsa.PrivateKey
	presented     []devverifier.Presented
	failure       string
	done          bool
}

type server struct {
	identity    devverifier.Identity
	issuerTrust eudijwt.X509VerificationContext
	publicURL   string
	internalURL string
	walletURL   string

	mu       sync.Mutex
	sessions map[string]*session
}

func newServer(identity devverifier.Identity, issuerTrust eudijwt.X509VerificationContext, publicURL, internalURL, walletURL string) *server {
	return &server{
		identity:    identity,
		issuerTrust: issuerTrust,
		publicURL:   publicURL,
		internalURL: internalURL,
		walletURL:   walletURL,
		sessions:    map[string]*session{},
	}
}

func (s *server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("POST /sessions", s.start)
	mux.HandleFunc("GET /sessions/{id}", s.show)
	mux.HandleFunc("GET /request/{id}", s.requestObject)
	mux.HandleFunc("POST /response/{id}", s.response)
	return mux
}

func (s *server) index(w http.ResponseWriter, _ *http.Request) {
	s.render(w, indexTemplate, map[string]any{
		"ClientID":   s.identity.ClientID(),
		"DefaultVCT": defaultVCT,
	})
}

// start creates a session and signs its Request Object.
func (s *server) start(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vct := strings.TrimSpace(r.PostFormValue("vct"))
	if vct == "" {
		http.Error(w, "vct is required", http.StatusBadRequest)
		return
	}
	var claims []string
	for _, c := range strings.Split(r.PostFormValue("claims"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			claims = append(claims, c)
		}
	}
	mode := r.PostFormValue("response_mode")
	if mode != string(openid4vp.ResponseMode_DirectPostJwt) {
		mode = string(openid4vp.ResponseMode_DirectPost)
	}

	sess, err := s.newSession(vct, claims, mode)
	if err != nil {
		slog.Error("start session", slog.String("error", err.Error()))
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/sessions/"+sess.id, http.StatusSeeOther)
}

func (s *server) newSession(vct string, claims []string, mode string) (*session, error) {
	idBytes := make([]byte, sessionIDBytes)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	nonce, err := devverifier.RandomToken()
	if err != nil {
		return nil, err
	}
	state, err := devverifier.RandomToken()
	if err != nil {
		return nil, err
	}
	sess := &session{
		id:           hex.EncodeToString(idBytes),
		created:      time.Now(),
		vct:          vct,
		claims:       claims,
		responseMode: mode,
		nonce:        nonce,
		state:        state,
	}
	dcql, err := devverifier.SimpleDCQL(defaultQueryID, vct, claims)
	if err != nil {
		return nil, err
	}
	req := devverifier.Request{
		Nonce:        nonce,
		State:        state,
		ResponseURI:  s.internalURL + "/response/" + sess.id,
		ResponseMode: mode,
		DCQLQuery:    dcql,
		ClientName:   "Dev Verifier",
	}
	if mode == string(openid4vp.ResponseMode_DirectPostJwt) {
		key, err := devverifier.NewEncryptionKey()
		if err != nil {
			return nil, err
		}
		sess.encryptionKey = key
		req.EncryptionKey = &key.PublicKey
	}
	jar, err := devverifier.SignRequestObject(s.identity, req)
	if err != nil {
		return nil, err
	}
	sess.requestObject = jar

	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune()
	s.sessions[sess.id] = sess
	return sess, nil
}

func (s *server) prune() {
	for id, sess := range s.sessions {
		if time.Since(sess.created) > sessionLifetime {
			delete(s.sessions, id)
		}
	}
}

func (s *server) get(id string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

// show is the session page: the link that invokes the wallet while the response
// is outstanding, the verified disclosure once it arrived.
func (s *server) show(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	view := map[string]any{
		"ID":           sess.id,
		"VCT":          sess.vct,
		"Claims":       strings.Join(sess.claims, ", "),
		"ResponseMode": sess.responseMode,
		"Fetched":      sess.fetched,
		"Done":         sess.done,
		"Failure":      sess.failure,
		"Presented":    sess.presented,
		"Refresh":      refreshSeconds,
		"Invocation":   s.invocationLink(sess.id),
	}
	s.mu.Unlock()
	s.render(w, sessionTemplate, view)
}

// invocationLink is what a verifier would put behind its "share with your
// business wallet" button: the wallet's entry point with client_id and the
// request_uri the wallet backend fetches.
func (s *server) invocationLink(id string) string {
	q := url.Values{}
	q.Set("client_id", s.identity.ClientID())
	q.Set("request_uri", s.internalURL+"/request/"+id)
	q.Set("request_uri_method", string(openid4vp.RequestUriMethod_Get))
	return s.walletURL + "/openid4vp?" + q.Encode()
}

// requestObject serves the JAR once, like the hosted EUDI verifier: a second
// fetch is refused so a replayed link fails visibly.
func (s *server) requestObject(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess.fetched {
		http.Error(w, "request object already retrieved", http.StatusBadRequest)
		return
	}
	sess.fetched = true
	w.Header().Set("Content-Type", "application/oauth-authz-req+jwt")
	if _, err := w.Write([]byte(sess.requestObject)); err != nil {
		slog.Error("write request object", slog.String("error", err.Error()))
	}
}

// response receives the Authorization Response (direct_post form or
// direct_post.jwt JWE), verifies it, and tells the wallet where to send the
// browser next: back here, to the result.
func (s *server) response(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	token, state, err := s.decodeResponse(sess, r.PostForm)
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.done = true
	switch {
	case err != nil:
		sess.failure = err.Error()
	case state != sess.state:
		sess.failure = fmt.Sprintf("state mismatch: got %q", state)
	case len(token) == 0:
		sess.failure = devverifier.ErrNoPresentations.Error()
	default:
		sess.presented = devverifier.VerifyVPToken(token, s.issuerTrust, sess.nonce, s.identity.ClientID())
	}
	if sess.failure != "" {
		slog.Warn("response refused", slog.String("session", sess.id), slog.String("error", sess.failure))
		http.Error(w, sess.failure, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"redirect_uri": s.publicURL + "/sessions/" + sess.id}); err != nil {
		slog.Error("write response ack", slog.String("error", err.Error()))
	}
}

func (s *server) decodeResponse(sess *session, form url.Values) (map[string][]string, string, error) {
	if sess.responseMode == string(openid4vp.ResponseMode_DirectPostJwt) {
		if form.Get("response") == "" {
			return nil, "", errors.New("direct_post.jwt response without `response`")
		}
		return devverifier.DecryptResponse(form.Get("response"), sess.encryptionKey)
	}
	if form.Get("vp_token") == "" {
		return nil, "", errors.New("direct_post response without `vp_token`")
	}
	token, err := devverifier.ParseVPToken(form.Get("vp_token"))
	return token, form.Get("state"), err
}

func (s *server) render(w http.ResponseWriter, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("render", slog.String("error", err.Error()))
	}
}
