package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arsykor/loyalty-system/internal/middleware"
	"github.com/arsykor/loyalty-system/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

// --- mock repository ---

type mockUser struct {
	id   string
	hash string
}

type mockOrder struct {
	userID     string
	status     string
	accrual    *float64
	uploadedAt time.Time
}

type mockRepo struct {
	mu          sync.Mutex
	users       map[string]mockUser
	orders      map[string]mockOrder
	balances    map[string]float64
	withdrawn   map[string]float64
	withdrawals map[string][]service.Withdrawal
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		users:       make(map[string]mockUser),
		orders:      make(map[string]mockOrder),
		balances:    make(map[string]float64),
		withdrawn:   make(map[string]float64),
		withdrawals: make(map[string][]service.Withdrawal),
	}
}

func (m *mockRepo) CreateUser(_ context.Context, login, hash string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.users[login]; exists {
		return "", service.ErrLoginConflict
	}
	id := "user-" + login
	m.users[login] = mockUser{id: id, hash: hash}
	m.balances[id] = 0
	m.withdrawn[id] = 0
	return id, nil
}

func (m *mockRepo) GetUser(_ context.Context, login string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[login]
	if !ok {
		return "", "", service.ErrInvalidCredentials
	}
	return u.id, u.hash, nil
}

func (m *mockRepo) AddOrder(_ context.Context, number, userID string) (bool, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, exists := m.orders[number]
	if exists {
		if o.userID == userID {
			return true, false, nil
		}
		return false, true, nil
	}
	m.orders[number] = mockOrder{userID: userID, status: service.StatusNew, uploadedAt: time.Now()}
	return false, false, nil
}

func (m *mockRepo) GetOrders(_ context.Context, userID string) ([]service.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []service.Order
	for number, o := range m.orders {
		if o.userID == userID {
			result = append(result, service.Order{
				Number:     number,
				Status:     o.status,
				Accrual:    o.accrual,
				UploadedAt: o.uploadedAt,
			})
		}
	}
	return result, nil
}

func (m *mockRepo) GetBalance(_ context.Context, userID string) (float64, float64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.balances[userID], m.withdrawn[userID], nil
}

func (m *mockRepo) Withdraw(_ context.Context, userID, orderNumber string, sum float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.balances[userID] < sum {
		return service.ErrInsufficientFunds
	}
	m.balances[userID] -= sum
	m.withdrawn[userID] += sum
	m.withdrawals[userID] = append(m.withdrawals[userID], service.Withdrawal{
		OrderNumber: orderNumber,
		Sum:         sum,
		ProcessedAt: time.Now(),
	})
	return nil
}

func (m *mockRepo) GetWithdrawals(_ context.Context, userID string) ([]service.Withdrawal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.withdrawals[userID], nil
}

func (m *mockRepo) GetPendingOrders(_ context.Context) ([]string, error)                              { return nil, nil }
func (m *mockRepo) UpdateOrderStatus(_ context.Context, _, _ string, _ *float64) error               { return nil }

// --- helpers ---

// newRouter builds a ready-to-use router backed by the given mock repo.
func newRouter(repo *mockRepo) http.Handler {
	svc := service.New(repo)
	return New(svc, zap.NewNop().Sugar()).Router()
}

// authCookie creates a valid signed auth cookie for the given userID.
func authCookie(userID string) *http.Cookie {
	w := httptest.NewRecorder()
	middleware.SetAuthCookie(w, userID)
	return w.Result().Cookies()[0]
}

// bcryptHash hashes a password using bcrypt MinCost (fast in tests).
func bcryptHash(t *testing.T, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	return string(h)
}

// --- tests ---

func TestHandleRegister(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		setup    func(*mockRepo)
		wantCode int
	}{
		{
			name:     "success",
			body:     `{"login":"alice","password":"secret"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "empty body",
			body:     `{}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "empty password",
			body:     `{"login":"alice","password":""}`,
			wantCode: http.StatusBadRequest,
		},
		{
			name: "duplicate login",
			body: `{"login":"alice","password":"secret"}`,
			setup: func(r *mockRepo) {
				r.users["alice"] = mockUser{id: "user-alice", hash: "hash"}
			},
			wantCode: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			if tt.setup != nil {
				tt.setup(repo)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			newRouter(repo).ServeHTTP(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
			if tt.wantCode == http.StatusOK {
				// Auth cookie must be set on success
				assert.NotEmpty(t, w.Result().Cookies())
			}
		})
	}
}

func TestHandleLogin(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantCode int
	}{
		{
			name:     "success",
			body:     `{"login":"alice","password":"secret"}`,
			wantCode: http.StatusOK,
		},
		{
			name:     "wrong password",
			body:     `{"login":"alice","password":"wrong"}`,
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "unknown user",
			body:     `{"login":"nobody","password":"x"}`,
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "empty body",
			body:     `{}`,
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			repo.users["alice"] = mockUser{id: "user-alice", hash: bcryptHash(t, "secret")}

			req := httptest.NewRequest(http.MethodPost, "/api/user/login", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			newRouter(repo).ServeHTTP(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
		})
	}
}

func TestHandleAddOrder(t *testing.T) {
	const userID = "user-alice"
	const otherUser = "user-bob"

	tests := []struct {
		name     string
		body     string
		setup    func(*mockRepo)
		wantCode int
	}{
		{
			name:     "new order accepted",
			body:     "12345678903", // valid Luhn
			wantCode: http.StatusAccepted,
		},
		{
			name: "same user re-uploads",
			body: "12345678903",
			setup: func(r *mockRepo) {
				r.orders["12345678903"] = mockOrder{userID: userID, status: service.StatusNew, uploadedAt: time.Now()}
			},
			wantCode: http.StatusOK,
		},
		{
			name: "another user already uploaded",
			body: "12345678903",
			setup: func(r *mockRepo) {
				r.orders["12345678903"] = mockOrder{userID: otherUser, status: service.StatusNew, uploadedAt: time.Now()}
			},
			wantCode: http.StatusConflict,
		},
		{
			name:     "invalid Luhn",
			body:     "12345678900",
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "empty body",
			body:     "",
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			if tt.setup != nil {
				tt.setup(repo)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/user/orders", strings.NewReader(tt.body))
			req.AddCookie(authCookie(userID))
			w := httptest.NewRecorder()

			newRouter(repo).ServeHTTP(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
		})
	}
}

func TestHandleAddOrder_Unauthenticated(t *testing.T) {
	repo := newMockRepo()
	req := httptest.NewRequest(http.MethodPost, "/api/user/orders", strings.NewReader("12345678903"))
	w := httptest.NewRecorder()

	newRouter(repo).ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleGetOrders(t *testing.T) {
	const userID = "user-alice"

	t.Run("has orders", func(t *testing.T) {
		repo := newMockRepo()
		repo.orders["12345678903"] = mockOrder{userID: userID, status: service.StatusProcessed, uploadedAt: time.Now()}

		req := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)
		req.AddCookie(authCookie(userID))
		w := httptest.NewRecorder()

		newRouter(repo).ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

		var orders []orderResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&orders))
		require.Len(t, orders, 1)
		assert.Equal(t, "12345678903", orders[0].Number)
		assert.Equal(t, service.StatusProcessed, orders[0].Status)
	})

	t.Run("no orders", func(t *testing.T) {
		repo := newMockRepo()

		req := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)
		req.AddCookie(authCookie(userID))
		w := httptest.NewRecorder()

		newRouter(repo).ServeHTTP(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("unauthenticated", func(t *testing.T) {
		repo := newMockRepo()

		req := httptest.NewRequest(http.MethodGet, "/api/user/orders", nil)
		w := httptest.NewRecorder()

		newRouter(repo).ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestHandleGetBalance(t *testing.T) {
	const userID = "user-alice"

	t.Run("returns balance", func(t *testing.T) {
		repo := newMockRepo()
		repo.balances[userID] = 150.5
		repo.withdrawn[userID] = 50

		req := httptest.NewRequest(http.MethodGet, "/api/user/balance", nil)
		req.AddCookie(authCookie(userID))
		w := httptest.NewRecorder()

		newRouter(repo).ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)

		var resp balanceResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, 150.5, resp.Current)
		assert.Equal(t, 50.0, resp.Withdrawn)
	})

	t.Run("unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/user/balance", nil)
		w := httptest.NewRecorder()
		newRouter(newMockRepo()).ServeHTTP(w, req)
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestHandleWithdraw(t *testing.T) {
	const userID = "user-alice"

	tests := []struct {
		name     string
		body     string
		balance  float64
		wantCode int
	}{
		{
			name:     "success",
			body:     `{"order":"2377225624","sum":50}`,
			balance:  100,
			wantCode: http.StatusOK,
		},
		{
			name:     "insufficient funds",
			body:     `{"order":"2377225624","sum":200}`,
			balance:  100,
			wantCode: http.StatusPaymentRequired,
		},
		{
			name:     "invalid order number",
			body:     `{"order":"1234567890","sum":10}`,
			balance:  100,
			wantCode: http.StatusUnprocessableEntity,
		},
		{
			name:     "empty body",
			body:     `{}`,
			balance:  100,
			wantCode: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockRepo()
			repo.balances[userID] = tt.balance

			req := httptest.NewRequest(http.MethodPost, "/api/user/balance/withdraw", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(authCookie(userID))
			w := httptest.NewRecorder()

			newRouter(repo).ServeHTTP(w, req)

			assert.Equal(t, tt.wantCode, w.Code)
		})
	}
}

func TestHandleGetWithdrawals(t *testing.T) {
	const userID = "user-alice"

	t.Run("has withdrawals", func(t *testing.T) {
		repo := newMockRepo()
		repo.withdrawals[userID] = []service.Withdrawal{
			{OrderNumber: "2377225624", Sum: 100, ProcessedAt: time.Now()},
		}

		req := httptest.NewRequest(http.MethodGet, "/api/user/withdrawals", nil)
		req.AddCookie(authCookie(userID))
		w := httptest.NewRecorder()

		newRouter(repo).ServeHTTP(w, req)

		require.Equal(t, http.StatusOK, w.Code)

		body, err := io.ReadAll(w.Body)
		require.NoError(t, err)

		var resp []withdrawalResponse
		require.NoError(t, json.Unmarshal(body, &resp))
		require.Len(t, resp, 1)
		assert.Equal(t, "2377225624", resp[0].Order)
		assert.Equal(t, 100.0, resp[0].Sum)
	})

	t.Run("no withdrawals", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/user/withdrawals", nil)
		req.AddCookie(authCookie(userID))
		w := httptest.NewRecorder()
		newRouter(newMockRepo()).ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
	})
}

func TestLuhn(t *testing.T) {
	tests := []struct {
		number string
		valid  bool
	}{
		{"12345678903", true},
		{"2377225624", true},
		{"1000000024", true},
		{"9278923470", true},
		{"12345678900", false}, // wrong check digit
		{"1234567890", false},  // wrong check digit
		{"abc", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.number, func(t *testing.T) {
			assert.Equal(t, tt.valid, luhn(tt.number))
		})
	}
}
