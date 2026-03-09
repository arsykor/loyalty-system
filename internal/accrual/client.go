package accrual

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/arsykor/loyalty-system/internal/service"
	"go.uber.org/zap"
)

const maxConcurrent = 5

type accrualResponse struct {
	Order   string   `json:"order"`
	Status  string   `json:"status"`
	Accrual *float64 `json:"accrual,omitempty"`
}

type Worker struct {
	baseURL string
	svc     *service.Service
	client  *http.Client
	logger  *zap.SugaredLogger
	// sem limits the number of concurrent requests to the accrual service
	sem chan struct{}
}

func NewWorker(baseURL string, svc *service.Service, logger *zap.SugaredLogger) *Worker {
	// Ensure the base URL has a scheme so http.Client can parse it.
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	return &Worker{
		baseURL: baseURL,
		svc:     svc,
		client:  &http.Client{Timeout: 10 * time.Second},
		logger:  logger,
		sem:     make(chan struct{}, maxConcurrent),
	}
}

// Run starts the background polling loop. Call in goroutine!
func (w *Worker) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.poll(ctx)
		}
	}
}

func (w *Worker) poll(ctx context.Context) {
	numbers, err := w.svc.GetPendingOrders(ctx)
	if err != nil {
		w.logger.Errorw("accrual: failed to get pending orders", "error", err)
		return
	}

	var wg sync.WaitGroup
	for _, number := range numbers {
		wg.Add(1)
		// Acquire semaphore slot before launching goroutine
		w.sem <- struct{}{}
		go func(n string) {
			defer wg.Done()
			defer func() { <-w.sem }() // Release slot when done
			if err := w.processOrder(ctx, n); err != nil {
				w.logger.Errorw("accrual: failed to process order", "number", n, "error", err)
			}
		}(number)
	}
	wg.Wait()
}

func (w *Worker) processOrder(ctx context.Context, number string) error {
	url := fmt.Sprintf("%s/api/orders/%s", w.baseURL, number)

	resp, err := w.client.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var ar accrualResponse
		if err := json.NewDecoder(resp.Body).Decode(&ar); err != nil {
			return fmt.Errorf("decode accrual response: %w", err)
		}

		// Map accrual system statuses to our statuses
		status := mapStatus(ar.Status)
		if status == "" {
			return nil // unknown status, skip
		}

		return w.svc.UpdateOrderStatus(ctx, number, status, ar.Accrual)

	case http.StatusNoContent:
		return nil

	case http.StatusTooManyRequests:
		// Respect Retry-After header
		retryAfter := resp.Header.Get("Retry-After")
		if secs, err := strconv.Atoi(retryAfter); err == nil {
			time.Sleep(time.Duration(secs) * time.Second)
		} else {
			time.Sleep(60 * time.Second)
		}
		return nil

	default:
		return fmt.Errorf("unexpected status %d for order %s", resp.StatusCode, number)
	}
}

func mapStatus(accrualStatus string) string {
	switch accrualStatus {
	case "REGISTERED":
		return service.StatusNew
	case "PROCESSING":
		return service.StatusProcessing
	case "INVALID":
		return service.StatusInvalid
	case "PROCESSED":
		return service.StatusProcessed
	default:
		return ""
	}
}
