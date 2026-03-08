package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/arsykor/loyalty-system/internal/service"
	"github.com/jackc/pgerrcode"
	"github.com/lib/pq"
)

type PostgresRepository struct {
	db *sql.DB
}

func NewPostgresRepository(db *sql.DB) *PostgresRepository {
	return &PostgresRepository{db: db}
}

func (r *PostgresRepository) CreateUser(ctx context.Context, login, passwordHash string) (string, error) {
	var userID string
	err := r.db.QueryRowContext(ctx,
		`INSERT INTO users (login, password_hash) VALUES ($1, $2) RETURNING id`,
		login, passwordHash,
	).Scan(&userID)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgerrcode.UniqueViolation {
			return "", service.ErrLoginConflict
		}
		return "", fmt.Errorf("create user: %w", err)
	}
	return userID, nil
}

func (r *PostgresRepository) GetUser(ctx context.Context, login string) (string, string, error) {
	var userID, passwordHash string
	err := r.db.QueryRowContext(ctx,
		`SELECT id, password_hash FROM users WHERE login = $1`,
		login,
	).Scan(&userID, &passwordHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", "", service.ErrInvalidCredentials
		}
		return "", "", fmt.Errorf("get user: %w", err)
	}
	return userID, passwordHash, nil
}

func (r *PostgresRepository) AddOrder(ctx context.Context, number, userID string) (bool, bool, error) {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO orders (number, user_id) VALUES ($1, $2)`,
		number, userID,
	)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == pgerrcode.UniqueViolation {
			// Order already exists — check who owns it
			var ownerID string
			if scanErr := r.db.QueryRowContext(ctx,
				`SELECT user_id FROM orders WHERE number = $1`, number,
			).Scan(&ownerID); scanErr != nil {
				return false, false, fmt.Errorf("add order: check owner: %w", scanErr)
			}
			if ownerID == userID {
				return true, false, nil // same user uploaded it
			}
			return false, true, nil // different user
		}
		return false, false, fmt.Errorf("add order: %w", err)
	}
	return false, false, nil
}

func (r *PostgresRepository) GetOrders(ctx context.Context, userID string) ([]service.Order, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT number, status, accrual, uploaded_at FROM orders WHERE user_id = $1 ORDER BY uploaded_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get orders: %w", err)
	}
	defer rows.Close()

	var orders []service.Order
	for rows.Next() {
		var o service.Order
		var accrual sql.NullFloat64
		if err := rows.Scan(&o.Number, &o.Status, &accrual, &o.UploadedAt); err != nil {
			return nil, fmt.Errorf("get orders scan: %w", err)
		}
		if accrual.Valid {
			v := accrual.Float64
			o.Accrual = &v
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (r *PostgresRepository) GetBalance(ctx context.Context, userID string) (float64, float64, error) {
	var current, withdrawn float64
	err := r.db.QueryRowContext(ctx,
		`SELECT balance, withdrawn FROM users WHERE id = $1`,
		userID,
	).Scan(&current, &withdrawn)
	if err != nil {
		return 0, 0, fmt.Errorf("get balance: %w", err)
	}
	return current, withdrawn, nil
}

func (r *PostgresRepository) Withdraw(ctx context.Context, userID, orderNumber string, sum float64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("withdraw begin tx: %w", err)
	}
	defer tx.Rollback()

	var balance float64
	if err := tx.QueryRowContext(ctx,
		`SELECT balance FROM users WHERE id = $1 FOR UPDATE`,
		userID,
	).Scan(&balance); err != nil {
		return fmt.Errorf("withdraw get balance: %w", err)
	}

	if balance < sum {
		return service.ErrInsufficientFunds
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET balance = balance - $1, withdrawn = withdrawn + $1 WHERE id = $2`,
		sum, userID,
	); err != nil {
		return fmt.Errorf("withdraw update balance: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO withdrawals (user_id, order_number, sum) VALUES ($1, $2, $3)`,
		userID, orderNumber, sum,
	); err != nil {
		return fmt.Errorf("withdraw insert: %w", err)
	}

	return tx.Commit()
}

func (r *PostgresRepository) GetWithdrawals(ctx context.Context, userID string) ([]service.Withdrawal, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT order_number, sum, processed_at FROM withdrawals WHERE user_id = $1 ORDER BY processed_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get withdrawals: %w", err)
	}
	defer rows.Close()

	var result []service.Withdrawal
	for rows.Next() {
		var w service.Withdrawal
		if err := rows.Scan(&w.OrderNumber, &w.Sum, &w.ProcessedAt); err != nil {
			return nil, fmt.Errorf("get withdrawals scan: %w", err)
		}
		result = append(result, w)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) GetPendingOrders(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT number FROM orders WHERE status IN ('NEW', 'PROCESSING')`,
	)
	if err != nil {
		return nil, fmt.Errorf("get pending orders: %w", err)
	}
	defer rows.Close()

	var numbers []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("get pending orders scan: %w", err)
		}
		numbers = append(numbers, n)
	}
	return numbers, rows.Err()
}

func (r *PostgresRepository) UpdateOrderStatus(ctx context.Context, number, status string, accrual *float64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("update order begin tx: %w", err)
	}
	defer tx.Rollback()

	var userID string
	if err := tx.QueryRowContext(ctx,
		`UPDATE orders SET status = $1, accrual = $2 WHERE number = $3 RETURNING user_id`,
		status, accrual, number,
	).Scan(&userID); err != nil {
		return fmt.Errorf("update order status: %w", err)
	}

	// Credit the user if the order is now processed
	if status == service.StatusProcessed && accrual != nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET balance = balance + $1 WHERE id = $2`,
			*accrual, userID,
		); err != nil {
			return fmt.Errorf("update order credit balance: %w", err)
		}
	}

	return tx.Commit()
}
