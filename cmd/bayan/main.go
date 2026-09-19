package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/Netflix/go-env"
	"github.com/sleroq/bayan/internal/bayan"
	"github.com/sleroq/bayan/internal/storage"
	"go.uber.org/zap"
)

type environment struct {
	TelegramToken  string  `env:"BOT_TOKEN,required"`
	KekReplyChance float64 `env:"KEK_REPLY_CHANCE" envDefault:"0.3"`
	ShowSimilarity bool    `env:"SHOW_SIMILARITY" envDefault:"false"`
}

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		panic(err)
	}

	var config environment
	if _, err := env.UnmarshalFromEnviron(&config); err != nil {
		logger.Fatal("failed to unmarshal environment", zap.Error(err))
	}

	store, err := storage.New("bayan.db")
	if err != nil {
		logger.Fatal("failed to create storage", zap.Error(err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	botConfig := bayan.Config{
		KekReplyChance: config.KekReplyChance,
		ShowSimilarity: config.ShowSimilarity,
	}
	err = bayan.Run(ctx, config.TelegramToken, store, logger, botConfig)
	if err != nil {
		logger.Fatal("failed to run bot", zap.Error(err))
	}
}
