// Package handler implements HTTP handlers for the Gophermart loyalty system.
package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	_ "github.com/arsykor/loyalty-system/docs"
	"github.com/arsykor/loyalty-system/internal/middleware"
	"github.com/arsykor/loyalty-system/internal/service"
	"github.com/go-chi/chi/v5"
	httpSwagger "github.com/swaggo/http-swagger/v2"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	svc    *service.Service
	logger *zap.SugaredLogger
}

func New(svc *service.Service, logger *zap.SugaredLogger) *Handler {
	return &Handler{svc: svc, logger: logger}
}

func (h *Handler) Router() chi.Router {
	r := chi.NewRouter()
	r.Use(middleware.WithGzipDecompression)
	r.Use(middleware.WithGzipCompression)
	r.Use(middleware.WithLogging(h.logger))

	// Swagger UI
	r.Get("/swagger/*", httpSwagger.Handler(
		httpSwagger.URL("/swagger/doc.json"),
	))

	r.Post("/api/user/register", h.handleRegister)
	r.Post("/api/user/login", h.handleLogin)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireAuth)
		r.Post("/api/user/orders", h.handleAddOrder)
		r.Get("/api/user/orders", h.handleGetOrders)
		r.Get("/api/user/balance", h.handleGetBalance)
		r.Post("/api/user/balance/withdraw", h.handleWithdraw)
		r.Get("/api/user/withdrawals", h.handleGetWithdrawals)
	})

	return r
}

// --- Request / Response types ---

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type orderResponse struct {
	Number     string   `json:"number"`
	Status     string   `json:"status"`
	Accrual    *float64 `json:"accrual,omitempty"`
	UploadedAt string   `json:"uploaded_at"`
}

type balanceResponse struct {
	Current   float64 `json:"current"`
	Withdrawn float64 `json:"withdrawn"`
}

type withdrawRequest struct {
	Order string  `json:"order"`
	Sum   float64 `json:"sum"`
}

type withdrawalResponse struct {
	Order       string  `json:"order"`
	Sum         float64 `json:"sum"`
	ProcessedAt string  `json:"processed_at"`
}

// --- Handlers ---

// handleRegister registers a new user.
//
//	@Summary		Register user
//	@Description	Register a new user and set an auth cookie on success
//	@Tags			auth
//	@Accept			json
//	@Param			request	body	loginRequest	true	"Credentials"
//	@Success		200		"Registered and authenticated"
//	@Failure		400		"Bad request"
//	@Failure		409		"Login already taken"
//	@Failure		500		"Internal error"
//	@Router			/api/user/register [post]
func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Login == "" || req.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		h.logger.Errorw("bcrypt error", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	userID, err := h.svc.CreateUser(r.Context(), req.Login, string(hash))
	if err != nil {
		if errors.Is(err, service.ErrLoginConflict) {
			http.Error(w, "login taken", http.StatusConflict)
			return
		}
		h.logger.Errorw("create user", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	middleware.SetAuthCookie(w, userID)
	w.WriteHeader(http.StatusOK)
}

// handleLogin authenticates a user.
//
//	@Summary		Login user
//	@Description	Authenticate with login/password, sets an auth cookie on success
//	@Tags			auth
//	@Accept			json
//	@Param			request	body	loginRequest	true	"Credentials"
//	@Success		200		"Authenticated"
//	@Failure		400		"Bad request"
//	@Failure		401		"Invalid credentials"
//	@Failure		500		"Internal error"
//	@Router			/api/user/login [post]
func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Login == "" || req.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	userID, hash, err := h.svc.GetUser(r.Context(), req.Login)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.logger.Errorw("get user", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	middleware.SetAuthCookie(w, userID)
	w.WriteHeader(http.StatusOK)
}

// handleAddOrder submits an order number for accrual processing.
//
//	@Summary		Submit order
//	@Description	Submit a new order number (Luhn-validated). Requires auth cookie.
//	@Tags			orders
//	@Accept			plain
//	@Param			number	body	string	true	"Order number (digits only)"
//	@Success		200		"Already uploaded by this user"
//	@Success		202		"Accepted for processing"
//	@Failure		400		"Bad request"
//	@Failure		401		"Unauthorized"
//	@Failure		409		"Uploaded by another user"
//	@Failure		422		"Invalid order number format"
//	@Failure		500		"Internal error"
//	@Security		CookieAuth
//	@Router			/api/user/orders [post]
func (h *Handler) handleAddOrder(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	number := strings.TrimSpace(string(body))
	if number == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !luhn(number) {
		http.Error(w, "invalid order number", http.StatusUnprocessableEntity)
		return
	}

	userID, _ := middleware.GetUserID(r.Context())

	alreadyByThisUser, conflict, err := h.svc.AddOrder(r.Context(), number, userID)
	if err != nil {
		h.logger.Errorw("add order", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	switch {
	case alreadyByThisUser:
		w.WriteHeader(http.StatusOK)
	case conflict:
		http.Error(w, "conflict", http.StatusConflict)
	default:
		w.WriteHeader(http.StatusAccepted)
	}
}

// handleGetOrders returns a list of the user's orders.
//
//	@Summary		Get orders
//	@Description	Returns all orders for the authenticated user, sorted by upload time (newest first)
//	@Tags			orders
//	@Produce		json
//	@Success		200	{array}		orderResponse
//	@Success		204	"No orders yet"
//	@Failure		401	"Unauthorized"
//	@Failure		500	"Internal error"
//	@Security		CookieAuth
//	@Router			/api/user/orders [get]
func (h *Handler) handleGetOrders(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.GetUserID(r.Context())

	orders, err := h.svc.GetOrders(r.Context(), userID)
	if err != nil {
		h.logger.Errorw("get orders", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if len(orders) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	resp := make([]orderResponse, 0, len(orders))
	for _, o := range orders {
		resp = append(resp, orderResponse{
			Number:     o.Number,
			Status:     o.Status,
			Accrual:    o.Accrual,
			UploadedAt: o.UploadedAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleGetBalance returns the current loyalty balance.
//
//	@Summary		Get balance
//	@Description	Returns the current balance and total withdrawn amount for the authenticated user
//	@Tags			balance
//	@Produce		json
//	@Success		200	{object}	balanceResponse
//	@Failure		401	"Unauthorized"
//	@Failure		500	"Internal error"
//	@Security		CookieAuth
//	@Router			/api/user/balance [get]
func (h *Handler) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.GetUserID(r.Context())

	current, withdrawn, err := h.svc.GetBalance(r.Context(), userID)
	if err != nil {
		h.logger.Errorw("get balance", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(balanceResponse{Current: current, Withdrawn: withdrawn})
}

// handleWithdraw withdraws points from the balance.
//
//	@Summary		Withdraw points
//	@Description	Deduct loyalty points from the user's balance for a given order number
//	@Tags			balance
//	@Accept			json
//	@Param			request	body	withdrawRequest	true	"Withdrawal details"
//	@Success		200		"Withdrawal successful"
//	@Failure		400		"Bad request"
//	@Failure		401		"Unauthorized"
//	@Failure		402		"Insufficient funds"
//	@Failure		422		"Invalid order number"
//	@Failure		500		"Internal error"
//	@Security		CookieAuth
//	@Router			/api/user/balance/withdraw [post]
func (h *Handler) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	var req withdrawRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Order == "" || req.Sum <= 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if !luhn(req.Order) {
		http.Error(w, "invalid order number", http.StatusUnprocessableEntity)
		return
	}

	userID, _ := middleware.GetUserID(r.Context())

	err := h.svc.Withdraw(r.Context(), userID, req.Order, req.Sum)
	if err != nil {
		if errors.Is(err, service.ErrInsufficientFunds) {
			http.Error(w, "insufficient funds", http.StatusPaymentRequired)
			return
		}
		h.logger.Errorw("withdraw", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleGetWithdrawals returns withdrawal history.
//
//	@Summary		Get withdrawals
//	@Description	Returns all withdrawal records for the authenticated user, sorted newest first
//	@Tags			balance
//	@Produce		json
//	@Success		200	{array}		withdrawalResponse
//	@Success		204	"No withdrawals yet"
//	@Failure		401	"Unauthorized"
//	@Failure		500	"Internal error"
//	@Security		CookieAuth
//	@Router			/api/user/withdrawals [get]
func (h *Handler) handleGetWithdrawals(w http.ResponseWriter, r *http.Request) {
	userID, _ := middleware.GetUserID(r.Context())

	withdrawals, err := h.svc.GetWithdrawals(r.Context(), userID)
	if err != nil {
		h.logger.Errorw("get withdrawals", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if len(withdrawals) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	resp := make([]withdrawalResponse, 0, len(withdrawals))
	for _, ww := range withdrawals {
		resp = append(resp, withdrawalResponse{
			Order:       ww.OrderNumber,
			Sum:         ww.Sum,
			ProcessedAt: ww.ProcessedAt.Format(time.RFC3339),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// luhn validates an order number using the Luhn algorithm.
// https://en.wikipedia.org/wiki/Luhn_algorithm
func luhn(number string) bool {
	sum := 0
	nDigits := len(number)
	parity := nDigits % 2

	for i, ch := range number {
		if ch < '0' || ch > '9' {
			return false
		}
		digit := int(ch - '0')
		if i%2 == parity {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
	}
	return sum%10 == 0
}
