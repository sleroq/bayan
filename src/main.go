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

	similar, err := b.store.FindMsgFilter(msg.Chat.ID, func(msg *storage.Message) (bool, error) {
		distance, err := dHash.Distance(msg.DHash)
		if err != nil {
			return false, errors.Wrap(err, "failed to get distance")
		}

		if distance < 10 {
			b.logger.Info(
				"found similar message",
				zap.Int("distance", distance),
				zap.Int("id", msg.ID),
			)
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return errors.Wrap(err, "failed to find similar messages")
	}

	if len(similar) > 0 {
		err := b.replyBayan(ctx, api, msg, similar)
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

func (b *BayanBot) replyBayan(ctx context.Context, api *bot.Bot, msg *models.Message, similar []*storage.Message) error {
	_, err := api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:           msg.Chat.ID,
		Text:             "Баян",
		ReplyToMessageID: msg.ID,
	})
	if err != nil {
		return errors.Wrap(err, "failed to send message")
	}

	explanation := "Похоже на:\n"
	for _, similarMsg := range similar {
		chatID := similarMsg.ChatID + 1000000000000
		explanation += fmt.Sprintf("- https://t.me/c/%d/%d\n", chatID, similarMsg.ID)
	}

	_, err = api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: msg.Chat.ID,
		Text:   explanation,
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
	}

	b, err := bot.New(config.TelegramToken, opts...)
	if err != nil {
		logger.Fatal("failed to create bot", zap.Error(err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	b.Start(ctx)
}

//func main() {
//	file1, err := os.Open("pic-v4.jpg")
//	if err != nil {
//		fmt.Println(err)
//		return
//	}
//	file2, err := os.Open("pic-v5.jpg")
//	if err != nil {
//		fmt.Println(err)
//		return
//	}
//	defer file1.Close()
//	defer file2.Close()
//
//	img1, _ := jpeg.Decode(file1)
//	img2, _ := jpeg.Decode(file2)
//	hash1, _ := goimagehash.AverageHash(img1)
//	hash2, _ := goimagehash.AverageHash(img2)
//	distance, _ := hash1.Distance(hash2)
//	fmt.Printf("Distance between images: %v\n", distance)
//
//	hash1, _ = goimagehash.DifferenceHash(img1)
//	hash2, _ = goimagehash.DifferenceHash(img2)
//	distance, _ = hash1.Distance(hash2)
//	fmt.Printf("Distance between images: %v\n", distance)
//	width, height := 8, 8
//	hash3, _ := goimagehash.ExtAverageHash(img1, width, height)
//	hash4, _ := goimagehash.ExtAverageHash(img2, width, height)
//	distance, _ = hash3.Distance(hash4)
//	fmt.Printf("Distance between images: %v\n", distance)
//	fmt.Printf("hash3 bit size: %v\n", hash3.Bits())
//	fmt.Printf("hash4 bit size: %v\n", hash4.Bits())
//}
