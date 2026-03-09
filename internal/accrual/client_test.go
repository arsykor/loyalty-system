package accrual

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/arsykor/loyalty-system/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// --- minimal mock for service.Repository ---
// PS не стал использовать кодогенерацию mockery, так как в данном случае нужен минимальный мок
type mockRepo struct {
	pendingOrders  []string
	updatedNumber  string
	updatedStatus  string
	updatedAccrual *float64
}

func (m *mockRepo) GetPendingOrders(_ context.Context) ([]string, error) {
	return m.pendingOrders, nil
}

func (m *mockRepo) UpdateOrderStatus(_ context.Context, number, status string, accrual *float64) error {
	m.updatedNumber = number
	m.updatedStatus = status
	m.updatedAccrual = accrual
	return nil
}

func (m *mockRepo) CreateUser(_ context.Context, _, _ string) (string, error)   { return "", nil }
func (m *mockRepo) GetUser(_ context.Context, _ string) (string, string, error) { return "", "", nil }
func (m *mockRepo) AddOrder(_ context.Context, _, _ string) (bool, bool, error) {
	return false, false, nil
}
func (m *mockRepo) GetOrders(_ context.Context, _ string) ([]service.Order, error) { return nil, nil }
func (m *mockRepo) GetBalance(_ context.Context, _ string) (float64, float64, error) {
	return 0, 0, nil
}
func (m *mockRepo) Withdraw(_ context.Context, _, _ string, _ float64) error { return nil }
func (m *mockRepo) GetWithdrawals(_ context.Context, _ string) ([]service.Withdrawal, error) {
	return nil, nil
}

// --- tests ---

func TestNewWorker_URLNormalization(t *testing.T) {
	svc := service.New(&mockRepo{})
	logger := zap.NewNop().Sugar()

	tests := []struct {
		input string
		want  string
	}{
		{"localhost:8085", "http://localhost:8085"},
		{"http://localhost:8085", "http://localhost:8085"},
		{"https://accrual.example.com", "https://accrual.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			w := NewWorker(tt.input, svc, logger)
			assert.Equal(t, tt.want, w.baseURL)
		})
	}
}

func TestMapStatus(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"REGISTERED", service.StatusNew},
		{"PROCESSING", service.StatusProcessing},
		{"INVALID", service.StatusInvalid},
		{"PROCESSED", service.StatusProcessed},
		{"UNKNOWN", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, mapStatus(tt.input))
		})
	}
}

func TestProcessOrder_200_Processed(t *testing.T) {
	accrualVal := 500.0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/orders/12345678903", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(accrualResponse{
			Order:   "12345678903",
			Status:  "PROCESSED",
			Accrual: &accrualVal,
		})
	}))
	defer server.Close()

	repo := &mockRepo{}
	svc := service.New(repo)
	worker := NewWorker(server.URL, svc, zap.NewNop().Sugar())

	err := worker.processOrder(context.Background(), "12345678903")
	require.NoError(t, err)

	assert.Equal(t, "12345678903", repo.updatedNumber)
	assert.Equal(t, service.StatusProcessed, repo.updatedStatus)
	require.NotNil(t, repo.updatedAccrual)
	assert.Equal(t, 500.0, *repo.updatedAccrual)
}

func TestProcessOrder_200_Processing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(accrualResponse{
			Order:  "12345678903",
			Status: "PROCESSING",
		})
	}))
	defer server.Close()

	repo := &mockRepo{}
	svc := service.New(repo)
	worker := NewWorker(server.URL, svc, zap.NewNop().Sugar())

	err := worker.processOrder(context.Background(), "12345678903")
	require.NoError(t, err)

	assert.Equal(t, "12345678903", repo.updatedNumber)
	assert.Equal(t, service.StatusProcessing, repo.updatedStatus)
}

func TestProcessOrder_204_NoContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	repo := &mockRepo{}
	svc := service.New(repo)
	worker := NewWorker(server.URL, svc, zap.NewNop().Sugar())

	err := worker.processOrder(context.Background(), "12345678903")

	require.NoError(t, err)
	// Nothing should be updated
	assert.Empty(t, repo.updatedNumber)
}

func TestProcessOrder_429_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	repo := &mockRepo{}
	svc := service.New(repo)
	worker := NewWorker(server.URL, svc, zap.NewNop().Sugar())

	start := time.Now()
	err := worker.processOrder(context.Background(), "12345678903")
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, time.Second, "should sleep at least Retry-After seconds")
	assert.Empty(t, repo.updatedNumber)
}
