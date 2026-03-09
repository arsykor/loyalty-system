// Gophermart Loyalty System API
//
//	@title			Gophermart Loyalty System
//	@version		1.0
//	@description	Loyalty accumulation system: register, submit orders, earn and spend points.
//	@host			localhost:8086
//	@BasePath		/
//
//	@securityDefinitions.apikey	CookieAuth
//	@in							cookie
//	@name						loyalty_token
package main

import (
	"context"
	"net/http"

	"github.com/arsykor/loyalty-system/internal/accrual"
	"github.com/arsykor/loyalty-system/internal/config"
	"github.com/arsykor/loyalty-system/internal/database"
	"github.com/arsykor/loyalty-system/internal/handler"
	"github.com/arsykor/loyalty-system/internal/repository"
	"github.com/arsykor/loyalty-system/internal/service"
	"go.uber.org/zap"
)

func main() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}
	defer logger.Sync()
	sugar := logger.Sugar()

	cfg := config.Load()

	db, err := database.NewDB(cfg.DatabaseURI)
	if err != nil {
		sugar.Fatalw("failed to connect to database", "error", err)
	}
	defer db.Close()

	repo := repository.NewPostgresRepository(db.DB)
	svc := service.New(repo)
	h := handler.New(svc, sugar)

	if cfg.AccrualAddress != "" {
		worker := accrual.NewWorker(cfg.AccrualAddress, svc, sugar)
		go worker.Run(context.Background())
		sugar.Infow("accrual worker started", "address", cfg.AccrualAddress)
	}

	sugar.Infow("starting server", "addr", cfg.RunAddress)
	if err := http.ListenAndServe(cfg.RunAddress, h.Router()); err != nil {
		sugar.Fatalw("server failed", "error", err)
	}
}
