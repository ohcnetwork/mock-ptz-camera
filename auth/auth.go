package auth

import (
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"time"
)

const (
	maxTimeDrift    = 5 * time.Minute
	passwordTextURI = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordText"
)

// ONVIF timestamp formats clients may use.
var createdTimeFormats = []string{
	time.RFC3339,
	"2006-01-02T15:04:05.000Z",
	"2006-01-02T15:04:05Z",
}

type Credentials struct {
	Username string
	Password string
}

// ValidateWSUsernameToken validates an ONVIF WS-UsernameToken.
func ValidateWSUsernameToken(creds Credentials, username, password, nonce, created, passwordType string) bool {
	if subtle.ConstantTimeCompare([]byte(username), []byte(creds.Username)) != 1 {
		return false
	}

	if passwordType == "PasswordText" || passwordType == passwordTextURI {
		return subtle.ConstantTimeCompare([]byte(password), []byte(creds.Password)) == 1
	}

	// PasswordDigest: Base64(SHA1(Nonce + Created + Password))
	t, ok := parseCreatedTime(created)
	if !ok || time.Since(t).Abs() > maxTimeDrift {
		return false
	}

	nonceRaw, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return false
	}

	h := sha1.New()
	h.Write(nonceRaw)
	h.Write([]byte(created))
	h.Write([]byte(creds.Password))
	expectedDigest := base64.StdEncoding.EncodeToString(h.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(password), []byte(expectedDigest)) == 1
}

func parseCreatedTime(s string) (time.Time, bool) {
	for _, layout := range createdTimeFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
