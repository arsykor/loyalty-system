package service

import (
	"context"
	"errors"
	"time"
)

var (
	ErrLoginConflict      = errors.New("login already taken")
	ErrInvalidCredentials = errors.New("invalid login or password")
	ErrOrderConflictSelf  = errors.New("order already uploaded by this user")
	ErrOrderConflictOther = errors.New("order already uploaded by another user")
	ErrInsufficientFunds  = errors.New("insufficient balance")
)

// Order statuses
const (
	StatusNew        = "NEW"
	StatusProcessing = "PROCESSING"
	StatusInvalid    = "INVALID"
	StatusProcessed  = "PROCESSED"
)

type Order struct {
	Number     string
	Status     string
	Accrual    *float64
	UploadedAt time.Time
}

type Withdrawal struct {
	OrderNumber string
	Sum         float64
	ProcessedAt time.Time
}

type Repository interface {
	CreateUser(ctx context.Context, login, passwordHash string) (userID string, err error)
	GetUser(ctx context.Context, login string) (userID, passwordHash string, err error)
	// AddOrder returns (alreadyByThisUser, conflict, error)
	AddOrder(ctx context.Context, number, userID string) (alreadyByThisUser bool, conflict bool, err error)
	GetOrders(ctx context.Context, userID string) ([]Order, error)
	GetBalance(ctx context.Context, userID string) (current, withdrawn float64, err error)
	Withdraw(ctx context.Context, userID, orderNumber string, sum float64) error
	GetWithdrawals(ctx context.Context, userID string) ([]Withdrawal, error)
	GetPendingOrders(ctx context.Context) ([]string, error)
	UpdateOrderStatus(ctx context.Context, number, status string, accrual *float64) error
}

type Service struct {
	repo Repository
}

func New(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) CreateUser(ctx context.Context, login, passwordHash string) (string, error) {
	return s.repo.CreateUser(ctx, login, passwordHash)
}

func (s *Service) GetUser(ctx context.Context, login string) (string, string, error) {
	return s.repo.GetUser(ctx, login)
}

func (s *Service) AddOrder(ctx context.Context, number, userID string) (bool, bool, error) {
	return s.repo.AddOrder(ctx, number, userID)
}

func (s *Service) GetOrders(ctx context.Context, userID string) ([]Order, error) {
	return s.repo.GetOrders(ctx, userID)
}

func (s *Service) GetBalance(ctx context.Context, userID string) (float64, float64, error) {
	return s.repo.GetBalance(ctx, userID)
}

func (s *Service) Withdraw(ctx context.Context, userID, orderNumber string, sum float64) error {
	return s.repo.Withdraw(ctx, userID, orderNumber, sum)
}

func (s *Service) GetWithdrawals(ctx context.Context, userID string) ([]Withdrawal, error) {
	return s.repo.GetWithdrawals(ctx, userID)
}

func (s *Service) GetPendingOrders(ctx context.Context) ([]string, error) {
	return s.repo.GetPendingOrders(ctx)
}

func (s *Service) UpdateOrderStatus(ctx context.Context, number, status string, accrual *float64) error {
	return s.repo.UpdateOrderStatus(ctx, number, status, accrual)
}
