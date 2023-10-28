package main

import (
	"context"
	"fmt"
	"github.com/Netflix/go-env"
	"github.com/corona10/goimagehash"
	"github.com/go-faster/errors"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/sleroq/bayan/src/storage"
	"go.uber.org/zap"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"
)

type BayanBot struct {
	token  string
	logger *zap.Logger
	store  *storage.Storage
}

func NewBayanBot(token string, store *storage.Storage, logger *zap.Logger) *BayanBot {
	return &BayanBot{
		token:  token,
		logger: logger,
		store:  store,
	}
}

func (b *BayanBot) startCmd(ctx context.Context, api *bot.Bot, update *models.Update) {
	_, err := api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: update.Message.Chat.ID,
		Text:   "Hello, world!",
	})
	if err != nil {
		return
	}
}

func (b *BayanBot) processMessage(next bot.HandlerFunc) bot.HandlerFunc {
	return func(ctx context.Context, api *bot.Bot, update *models.Update) {
		if update.Message == nil {
			next(ctx, api, update)
			return
		}

		if update.Message.Photo != nil {
			err := b.processPicture(ctx, api, update.Message, update.Message.Photo[0])
			if err != nil {
				b.logger.Error("failed to process pictures", zap.Error(err))
			}
		}

		if update.Message.Video != nil {
			err := b.processPicture(ctx, api, update.Message, *update.Message.Video.Thumbnail)
			if err != nil {
				b.logger.Error("failed to process video", zap.Error(err))
			}
		}

		if update.Message.Story != nil {
			// TODO: Add story processing when telegram bot api will support it
		}

		next(ctx, api, update)
	}
}

func (b *BayanBot) downloadFile(ctx context.Context, api *bot.Bot, fileID string) (io.ReadCloser, error) {
	fileInfo, err := api.GetFile(ctx, &bot.GetFileParams{
		FileID: fileID,
	})
	if err != nil {
		return nil, errors.Wrap(err, "failed to get file info")
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/", b.token)
	fileURL, err := url.JoinPath(apiURL, fileInfo.FilePath)
	if err != nil {
		return nil, errors.Wrap(err, "failed to join url")
	}

	client := http.Client{Timeout: time.Second * 60}
	file, err := client.Get(fileURL)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get file")
	}

	return file.Body, nil
}

func (b *BayanBot) hashPicture(ctx context.Context, api *bot.Bot, pic models.PhotoSize) (pHash, dHash *goimagehash.ImageHash, err error) {
	file, err := b.downloadFile(ctx, api, pic.FileID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to download file")
	}

	img, err := jpeg.Decode(file)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to decode image")
	}

	err = file.Close()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to close file")
	}

	pHash, err = goimagehash.PerceptionHash(img)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get perception hash")
	}

	dHash, err = goimagehash.DifferenceHash(img)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get difference hash")
	}

	return pHash, dHash, nil
}

func (b *BayanBot) processPicture(ctx context.Context, api *bot.Bot, msg *models.Message, pic models.PhotoSize) error {
	pHash, dHash, err := b.hashPicture(ctx, api, pic)
	if err != nil {
		return errors.Wrap(err, "failed to hash pictures")
	}

	// Will find the first match and stop
	similar, err := b.store.FindMsgFilter(
		msg.Chat.ID,
		1,
		func(msg *storage.Message) (dist int, ok bool, err error) {
			dist, err = dHash.Distance(msg.DHash)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			if dist < 10 {
				b.logger.Info(
					"found similar message",
					zap.Int("distance", dist),
					zap.Int("id", msg.ID),
				)
				return dist, true, nil
			}

			return dist, false, nil
		},
	)
	if err != nil {
		return errors.Wrap(err, "failed to find similar messages")
	}

	if len(similar) > 0 {
		err := b.replyBayan(ctx, api, msg, similar[0])
		if err != nil {
			return errors.Wrap(err, "failed to reply bayan")
		}
	} else {
		err = b.store.SaveMessage(msg, pHash, dHash)
		if err != nil {
			return errors.Wrap(err, "failed to save message")
		}
	}

	return nil
}

func (b *BayanBot) replyBayan(ctx context.Context, api *bot.Bot, msg *models.Message, similar *storage.SimilarMessage) error {
	chatID := similar.Msg.ChatID + 1000000000000
	text := fmt.Sprintf("[Баян](https://t.me/c/%d/%d)\n", chatID, similar.Msg.ID)

	_, err := api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:           msg.Chat.ID,
		Text:             text,
		ReplyToMessageID: msg.ID,
		ParseMode:        models.ParseModeMarkdown,
	})
	if err != nil {
		return errors.Wrap(err, "failed to send message")
	}

	return nil
}

func (b *BayanBot) comparePicture(ctx context.Context, api *bot.Bot, msg *models.Message, pic models.PhotoSize) error {
	_, dHash, err := b.hashPicture(ctx, api, pic)
	if err != nil {
		return errors.Wrap(err, "failed to hash pictures")
	}

	// Will find all similar messages
	similar, err := b.store.FindMsgFilter(msg.Chat.ID, 0, func(msg *storage.Message) (dist int, ok bool, err error) {
		dist, err = dHash.Distance(msg.DHash)
		if err != nil {
			return 0, false, errors.Wrap(err, "failed to get distance")
		}

		if dist < 15 {
			b.logger.Info(
				"found similar message",
				zap.Int("distance", dist),
				zap.Int("id", msg.ID),
			)
			return dist, true, nil
		}

		return dist, false, nil
	})
	if err != nil {
		return errors.Wrap(err, "failed to find similar messages")
	}

	if len(similar) > 1 {
		err := b.replySimilar(ctx, api, msg, similar)
		if err != nil {
			return errors.Wrap(err, "failed to reply bayan")
		}
	} else {
		_, err = api.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:           msg.Chat.ID,
			Text:             "Похожих постов не видел",
			ReplyToMessageID: msg.ID,
		})
		if err != nil {
			return errors.Wrap(err, "failed to send message")
		}
	}

	return nil
}

func (b *BayanBot) compareCmd(ctx context.Context, api *bot.Bot, update *models.Update) {
	if update.Message.ReplyToMessage == nil {
		_, err := api.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:           update.Message.Chat.ID,
			Text:             "Ответь на картинку/видео, которую хотите сравнить",
			ReplyToMessageID: update.Message.ID,
		})
		if err != nil {
			b.logger.Error("failed to send message", zap.Error(err))
		}
		return
	}

	if update.Message.ReplyToMessage.Photo != nil {
		err := b.comparePicture(ctx, api, update.Message, update.Message.ReplyToMessage.Photo[0])
		if err != nil {
			b.logger.Error("failed to process pictures", zap.Error(err))
		}
	}

	if update.Message.ReplyToMessage.Video != nil {
		err := b.comparePicture(ctx, api, update.Message, *update.Message.ReplyToMessage.Video.Thumbnail)
		if err != nil {
			b.logger.Error("failed to process video", zap.Error(err))
		}
	}

	if update.Message.ReplyToMessage.Story != nil {
		// TODO: Add story processing when telegram bot api will support it
		_, err := api.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:           update.Message.Chat.ID,
			Text:             "Дуров не дает мне работать со сторисами",
			ReplyToMessageID: update.Message.ID,
		})
		if err != nil {
			b.logger.Error("failed to send message", zap.Error(err))
		}
	}
}

func (b *BayanBot) replySimilar(ctx context.Context, api *bot.Bot, msg *models.Message, similar []*storage.SimilarMessage) error {
	text := "Что-то похожее:\n"
	for _, s := range similar {
		if s.Msg.ID == msg.ReplyToMessage.ID {
			continue
		}
		chatID := s.Msg.ChatID + 1000000000000
		text += fmt.Sprintf("- https://t.me/c/%d/%d\n", chatID, s.Msg.ID)
	}

	_, err := api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:           msg.Chat.ID,
		Text:             text,
		ReplyToMessageID: msg.ID,
	})
	if err != nil {
		return errors.Wrap(err, "failed to send message")
	}

	return nil
}

type Environment struct {
	TelegramToken string `env:"BOT_TOKEN,required"`
}

func main() {
	logger, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}

	var config Environment
	_, err = env.UnmarshalFromEnviron(&config)
	if err != nil {
		logger.Fatal("failed to unmarshal environment", zap.Error(err))
	}

	store, err := storage.New("bayan.db")
	if err != nil {
		logger.Fatal("failed to create storage", zap.Error(err))
	}

	bayanBot := NewBayanBot(config.TelegramToken, store, logger)

	opts := []bot.Option{
		bot.WithMiddlewares(bayanBot.processMessage),
		bot.WithMessageTextHandler("/start", bot.MatchTypeExact, bayanBot.startCmd),
		bot.WithMessageTextHandler("/compare", bot.MatchTypeExact, bayanBot.compareCmd),
	}

	b, err := bot.New(config.TelegramToken, opts...)
	if err != nil {
		logger.Fatal("failed to create bot", zap.Error(err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	b.Start(ctx)
}
