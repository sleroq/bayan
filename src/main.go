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
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"time"
)

type BayanBot struct {
	token          string
	logger         *zap.Logger
	store          *storage.Storage
	kekReplyChance float64
	showSimilarity bool
}

type BayanConfig struct {
	kekReplyChance float64 `env:"KEK_REPLY_CHANCE"`
	showSimilarity bool    `env:"SHOW_SIMILARITY"`
}

func NewBayanBot(token string, store *storage.Storage, logger *zap.Logger, cfg BayanConfig) *BayanBot {
	return &BayanBot{
		token:          token,
		logger:         logger,
		store:          store,
		kekReplyChance: cfg.kekReplyChance,
		showSimilarity: cfg.showSimilarity,
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

func (b *BayanBot) processMessage(ctx context.Context, api *bot.Bot, update *models.Update) {
	if update.Message == nil {
		return
	}

	if update.Message.Photo != nil {
		err := b.processPicture(ctx, api, update.Message, update.Message.Photo[0])
		if err != nil {
			b.logger.Error("failed to process pictures", zap.Error(err))
		}
	}

	if update.Message.Video != nil {
		err := b.processVideo(ctx, api, update.Message)
		if err != nil {
			b.logger.Error("failed to process video", zap.Error(err))
		}
	}

	if update.Message.Story != nil {
		// TODO: Add story processing when telegram bot api will support it
	}

	if update.Message.Text != "" {
		matchBayan, err := regexp.MatchString(`(?i)баян`, update.Message.Text)
		if err != nil {
			b.logger.Error("failed to match string", zap.Error(err))
		}

		if matchBayan && rand.Float64() < b.kekReplyChance {
			phrases := []string{
				"Не умничай",
				"Самый умный",
				"Ок и что?",
				"Спасибо",
				"Бывает такое",
			}
			_, err = api.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:           update.Message.Chat.ID,
				Text:             phrases[rand.Intn(len(phrases))],
				ReplyToMessageID: update.Message.ID,
			})
			if err != nil {
				b.logger.Error("failed to send message", zap.Error(err))
			}
		}
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
	similar, err := b.store.FindMsgPictureFilter(
		msg.Chat.ID,
		1,
		func(msg *storage.MessagePicture) (dist int, ok bool, err error) {
			dist, err = dHash.Distance(msg.DHash)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			if dist < 10 {
				b.logger.Debug(
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
	}

	err = b.store.SaveMessagePicture(msg, pHash, dHash)
	if err != nil {
		return errors.Wrap(err, "failed to save message")
	}

	return nil
}

func (b *BayanBot) replyBayan(ctx context.Context, api *bot.Bot, msg *models.Message, similar *storage.SimilarMessage) error {
	chatID := (similar.Msg.ChatID + 1000000000000) * -1
	var text string
	if b.showSimilarity {
		text = fmt.Sprintf("[Баян](https://t.me/c/%d/%d) (distance: %d)\n", chatID, similar.Msg.ID, similar.Distance)
	} else {
		text = fmt.Sprintf("[Баян](https://t.me/c/%d/%d)\n", chatID, similar.Msg.ID)
	}

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
	similar, err := b.store.FindMsgPictureFilter(msg.Chat.ID, 0, func(m *storage.MessagePicture) (dist int, ok bool, err error) {
		if m.ID == msg.ReplyToMessage.ID {
			return 0, false, nil
		}

		dist, err = dHash.Distance(m.DHash)
		if err != nil {
			return 0, false, errors.Wrap(err, "failed to get distance")
		}

		if dist < 15 {
			b.logger.Debug(
				"found similar message",
				zap.Int("distance", dist),
				zap.Int("id", m.ID),
			)
			return dist, true, nil
		}

		return dist, false, nil
	})
	if err != nil {
		return errors.Wrap(err, "failed to find similar messages")
	}

	if len(similar) > 0 {
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
		err := b.compareVideo(ctx, api, update.Message)
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
		chatID := (s.Msg.ChatID + 1000000000000) * -1
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

func hashPicFile(path string) (dHash, pHash *goimagehash.ImageHash, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to open file")
	}

	img, err := jpeg.Decode(file)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to decode image")
	}

	pHash, err = goimagehash.PerceptionHash(img)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get perception hash")
	}

	dHash, err = goimagehash.DifferenceHash(img)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to get perception hash")
	}

	return pHash, dHash, nil
}

func (b *BayanBot) hashVideo(ctx context.Context, api *bot.Bot, video *models.Video) (pHashes, dHashes *storage.VideoHashes, err error) {
	pHashes = &storage.VideoHashes{}
	dHashes = &storage.VideoHashes{}

	file, err := b.downloadFile(ctx, api, video.FileID)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to download file")
	}

	// Create temp dir
	dirName := bot.RandomString(10)
	err = os.Mkdir(dirName, 0755)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create temp dir")
	}

	// Cleanup
	defer func() {
		clErr := os.RemoveAll(dirName)
		if clErr != nil {
			b.logger.Error("failed to remove temp dir", zap.Error(err))
		}
	}()

	// Save video to temp dir
	fileName := fmt.Sprintf("%s/%s.mp4", dirName, video.FileID)
	f, err := os.Create(fileName)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to create file")
	}

	_, err = io.Copy(f, file)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to copy file")
	}

	err = file.Close()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to close file")
	}

	err = f.Close()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to close file")
	}

	// Extract scenes from video
	err = exec.Command("ffmpeg", "-i", fileName, "-vf", "select=gt(scene,0.2)", "-vsync", "vfr", "-vf", "fps=1", dirName+"/out%d.jpg").Run()
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to extract scenes from video")
	}

	// Get frames count
	files, err := os.ReadDir(dirName)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to read dir")
	}

	if len(files) < 4 {
		return nil, nil, errors.New("not enough frames")
	}

	// Hash frames
	framesPHashes, framesDHashes, err := hashFrames(dirName, files)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to hash frames")
	}

	return framesPHashes, framesDHashes, nil
}

func hashFrames(dirName string, files []os.DirEntry) (framesPHashes, framesDHashes *storage.VideoHashes, err error) {
	framesPHashes = &storage.VideoHashes{}
	framesDHashes = &storage.VideoHashes{}

	fileA := dirName + "/" + files[1].Name()
	pHashA, dHashA, err := hashPicFile(fileA)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to hash picture")
	}
	framesPHashes.FrameA = pHashA
	framesDHashes.FrameA = dHashA

	fileB := dirName + "/" + files[len(files)/4].Name()
	pHashB, dHashB, err := hashPicFile(fileB)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to hash picture")
	}
	framesPHashes.FrameB = pHashB
	framesDHashes.FrameB = dHashB

	fileC := dirName + "/" + files[len(files)-len(files)/4].Name()
	pHashC, dHashC, err := hashPicFile(fileC)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to hash picture")
	}
	framesPHashes.FrameC = pHashC
	framesDHashes.FrameC = dHashC

	fileD := dirName + "/" + files[len(files)-2].Name()
	pHashD, dHashD, err := hashPicFile(fileD)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failed to hash picture")
	}
	framesPHashes.FrameD = pHashD
	framesDHashes.FrameD = dHashD

	return framesPHashes, framesDHashes, nil
}

func (b *BayanBot) processVideo(ctx context.Context, api *bot.Bot, message *models.Message) error {
	if message.Video.FileSize > 20*1024*1024 {
		err := b.processVideoThumbnail(ctx, api, message)
		if err != nil {
			return errors.Wrap(err, "failed to process video thumbnail")
		}
	}

	framesPHashes, framesDHashes, err := b.hashVideo(ctx, api, message.Video)
	if err != nil {
		return errors.Wrap(err, "failed to hash video")
	}

	similar, err := b.store.FindMsgVideoFilter(
		message.Chat.ID,
		1,
		func(msg *storage.MessageVideo) (dist int, ok bool, err error) {
			// Calculate average distance
			dist = 0
			distA, err := framesDHashes.FrameA.Distance(msg.DHashes.FrameA)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			distB, err := framesDHashes.FrameB.Distance(msg.DHashes.FrameB)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			distC, err := framesDHashes.FrameC.Distance(msg.DHashes.FrameC)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			distD, err := framesDHashes.FrameD.Distance(msg.DHashes.FrameD)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			dist += distA
			dist += distB
			dist += distC
			dist += distD
			dist /= 4

			if dist < 10 {
				b.logger.Debug(
					"found similar message",
					zap.Int("distance", dist),
					zap.Int("id", msg.Msg.ID),
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
		err := b.replyBayan(ctx, api, message, similar[0])
		if err != nil {
			return errors.Wrap(err, "failed to reply bayan")
		}
	}

	err = b.store.SaveMessageVideo(message, framesPHashes, framesDHashes)
	if err != nil {
		return errors.Wrap(err, "failed to save message")
	}

	return nil
}

func (b *BayanBot) compareVideo(ctx context.Context, api *bot.Bot, message *models.Message) error {
	video := message.ReplyToMessage.Video
	if message.ReplyToMessage.Video.FileSize > 20*1024*1024 {
		err := b.processVideoThumbnail(ctx, api, message)
		if err != nil {
			return errors.Wrap(err, "failed to process video thumbnail")
		}
	}

	_, framesDHashes, err := b.hashVideo(ctx, api, video)
	if err != nil {
		return errors.Wrap(err, "failed to hash video")
	}

	similar, err := b.store.FindMsgVideoFilter(
		message.Chat.ID,
		0,
		func(msg *storage.MessageVideo) (dist int, ok bool, err error) {
			if msg.Msg.ID == message.ReplyToMessage.ID {
				return 0, false, nil
			}

			// Calculate average distance
			dist = 0
			distA, err := framesDHashes.FrameA.Distance(msg.DHashes.FrameA)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			distB, err := framesDHashes.FrameB.Distance(msg.DHashes.FrameB)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			distC, err := framesDHashes.FrameC.Distance(msg.DHashes.FrameC)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			distD, err := framesDHashes.FrameD.Distance(msg.DHashes.FrameD)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			dist += distA
			dist += distB
			dist += distC
			dist += distD
			dist /= 4

			if dist < 15 {
				b.logger.Debug(
					"found similar message",
					zap.Int("distance", dist),
					zap.Int("id", msg.Msg.ID),
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
		err := b.replySimilar(ctx, api, message, similar)
		if err != nil {
			return errors.Wrap(err, "failed to reply bayan")
		}
	} else {
		_, err = api.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:           message.Chat.ID,
			Text:             "Похожих постов не видел",
			ReplyToMessageID: message.ID,
		})
		if err != nil {
			return errors.Wrap(err, "failed to send message")
		}
	}

	return nil
}

func (b *BayanBot) processVideoThumbnail(ctx context.Context, api *bot.Bot, msg *models.Message) error {
	// TODO: Check if thumbnail is mostly black

	pHash, dHash, err := b.hashPicture(ctx, api, *msg.Video.Thumbnail)
	if err != nil {
		return errors.Wrap(err, "failed to hash pictures")
	}

	// Will find the first match and stop
	similar, err := b.store.FindMsgPictureFilter(
		msg.Chat.ID,
		1,
		func(msg *storage.MessagePicture) (dist int, ok bool, err error) {
			dist, err = dHash.Distance(msg.DHash)
			if err != nil {
				return 0, false, errors.Wrap(err, "failed to get distance")
			}

			if dist < 10 {
				b.logger.Debug(
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
		err = b.store.SaveMessagePicture(msg, pHash, dHash)
		if err != nil {
			return errors.Wrap(err, "failed to save message")
		}
	}

	return nil
}

type Environment struct {
	TelegramToken  string  `env:"BOT_TOKEN,required"`
	KekReplyChance float64 `env:"KEK_REPLY_CHANCE" envDefault:"0.3"`
	ShowSimilarity bool    `env:"SHOW_SIMILARITY" envDefault:"false"`
}

func main() {
	logger, err := zap.NewProduction()
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

	bayanBot := NewBayanBot(
		config.TelegramToken,
		store,
		logger,
		BayanConfig{
			kekReplyChance: config.KekReplyChance,
			showSimilarity: config.ShowSimilarity,
		},
	)

	opts := []bot.Option{
		bot.WithDefaultHandler(bayanBot.processMessage),
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
