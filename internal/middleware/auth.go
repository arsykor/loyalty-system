package middleware

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
)

type contextKey string

const (
	UserIDKey  contextKey = "userID"
	cookieName            = "loyalty_token"
	secretKey             = "super-secret-key"
)

var (
	ErrNoUserIDInContext = errors.New("user ID not found in context")
	ErrInvalidUserIDType = errors.New("user ID in context has invalid type")
)

func GetUserID(ctx context.Context) (string, error) {
	val := ctx.Value(UserIDKey)
	if val == nil {
		return "", ErrNoUserIDInContext
	}
	userID, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%w: %T", ErrInvalidUserIDType, val)
	}
	if userID == "" {
		return "", ErrNoUserIDInContext
	}
	return userID, nil
}

func signValue(value string) string {
	h := hmac.New(sha256.New, []byte(secretKey))
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

func buildCookieValue(userID string) string {
	return userID + "|" + signValue(userID)
}

func parseCookieValue(cookieValue string) string {
	for i := len(cookieValue) - 1; i >= 0; i-- {
		if cookieValue[i] == '|' {
			userID := cookieValue[:i]
			signature := cookieValue[i+1:]
			if hmac.Equal([]byte(signature), []byte(signValue(userID))) {
				return userID
			}
			return ""
		}
	}
	return ""
}

// WithAuth sets a signed cookie if none exists. Does NOT enforce auth.
//func WithAuth(next http.Handler) http.Handler {
//	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
//		var userID string
//
//		cookie, err := r.Cookie(cookieName)
//		if err == nil {
//			userID = parseCookieValue(cookie.Value)
//		}
//
//		if userID == "" {
//			userID = uuid.New().String()
//			http.SetCookie(w, &http.Cookie{
//				Name:  cookieName,
//				Value: buildCookieValue(userID),
//				Path:  "/",
//			})
//		}
//
//		ctx := context.WithValue(r.Context(), UserIDKey, userID)
//		next.ServeHTTP(w, r.WithContext(ctx))
//	})
//}

// RequireAuth is a middleware that enforces authentication, returning 401 if no valid cookie.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(cookieName)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		userID := parseCookieValue(cookie.Value)
		if userID == "" {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), UserIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// SetAuthCookie writes a signed auth cookie for the given userID.
func SetAuthCookie(w http.ResponseWriter, userID string) {
	http.SetCookie(w, &http.Cookie{
		Name:  cookieName,
		Value: buildCookieValue(userID),
		Path:  "/",
	})
}
