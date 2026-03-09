package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetUserID(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), UserIDKey, "abc-123")
		id, err := GetUserID(ctx)
		require.NoError(t, err)
		assert.Equal(t, "abc-123", id)
	})

	t.Run("missing", func(t *testing.T) {
		_, err := GetUserID(context.Background())
		assert.ErrorIs(t, err, ErrNoUserIDInContext)
	})

	t.Run("empty string", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), UserIDKey, "")
		_, err := GetUserID(ctx)
		assert.ErrorIs(t, err, ErrNoUserIDInContext)
	})
}

func TestRequireAuth(t *testing.T) {
	// A simple next handler that echoes back the userID from context.
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID, err := GetUserID(r.Context())
		require.NoError(t, err)
		w.Write([]byte(userID))
	})

	handler := RequireAuth(next)

	t.Run("valid cookie passes through", func(t *testing.T) {
		userID := "test-user-42"
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{
			Name:  cookieName,
			Value: buildCookieValue(userID),
		})
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, userID, w.Body.String())
	})

	t.Run("no cookie returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("tampered signature returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{
			Name:  cookieName,
			Value: "some-user|invalidsignature",
		})
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("no separator returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.AddCookie(&http.Cookie{
			Name:  cookieName,
			Value: "noseparatoratall",
		})
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestSetAuthCookie(t *testing.T) {
	userID := "user-777"
	w := httptest.NewRecorder()
	SetAuthCookie(w, userID)

	resp := w.Result()
	defer resp.Body.Close()

	cookies := resp.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, cookieName, cookies[0].Name)

	// Cookie value must encode the correct userID
	assert.Equal(t, userID, parseCookieValue(cookies[0].Value))
}

func TestBuildAndParseCookieValue(t *testing.T) {
	userID := "hello-world"
	value := buildCookieValue(userID)

	t.Run("round-trip", func(t *testing.T) {
		assert.Equal(t, userID, parseCookieValue(value))
	})

	t.Run("wrong signature", func(t *testing.T) {
		assert.Empty(t, parseCookieValue(userID+"|badsig"))
	})

	t.Run("no separator", func(t *testing.T) {
		assert.Empty(t, parseCookieValue("noseparator"))
	})
}
