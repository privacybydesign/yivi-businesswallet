package auth

import "net/http"

const cookieName = "ybw_session" // Yivi Business Wallet

type CookieConfig struct {
	Secure bool // SESSION_COOKIE_SECURE
	MaxAge int  // SESSION_TTL
}

func setSessionCookie(w http.ResponseWriter, raw string, cfg CookieConfig) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    raw,
		Path:     "/",
		MaxAge:   cfg.MaxAge,
		Secure:   cfg.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, cfg CookieConfig) {
	// Attributes must match setSessionCookie: the browser only overwrites a
	// cookie when name/path/Secure match, so a mismatch silently fails logout.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   cfg.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func readSessionCookie(r *http.Request) (raw string, ok bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}
