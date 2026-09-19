package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/Netflix/go-env"
	"github.com/corona10/goimagehash"
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
	"strings"
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

	if update.Message.Text != "" {
		b.processTextReply(ctx, api, update.Message)
	}
}

func (b *BayanBot) processTextReply(ctx context.Context, api *bot.Bot, message *models.Message) {
	matchBayan, err := regexp.MatchString(`(?i)баян`, message.Text)
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
			ChatID:          message.Chat.ID,
			Text:            phrases[rand.Intn(len(phrases))],
			ReplyParameters: &models.ReplyParameters{MessageID: message.ID},
		})
		if err != nil {
			b.logger.Error("failed to send message", zap.Error(err))
		}
	}
}

func (b *BayanBot) downloadFile(ctx context.Context, api *bot.Bot, fileID string) (io.ReadCloser, error) {
	fileInfo, err := api.GetFile(ctx, &bot.GetFileParams{
		FileID: fileID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/", b.token)
	fileURL, err := url.JoinPath(apiURL, fileInfo.FilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to join url: %w", err)
	}

	client := http.Client{Timeout: time.Second * 60}
	file, err := client.Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("failed to get file: %w", err)
	}

	return file.Body, nil
}

func (b *BayanBot) hashPicture(ctx context.Context, api *bot.Bot, pic models.PhotoSize) (pHash, dHash *goimagehash.ImageHash, err error) {
	file, err := b.downloadFile(ctx, api, pic.FileID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to download file: %w", err)
	}

	img, err := jpeg.Decode(file)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode image: %w", err)
	}

	err = file.Close()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to close file: %w", err)
	}

	pHash, err = goimagehash.PerceptionHash(img)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get perception hash: %w", err)
	}

	dHash, err = goimagehash.DifferenceHash(img)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get difference hash: %w", err)
	}

	return pHash, dHash, nil
}

func (b *BayanBot) pictureMatchFilter(pHash *goimagehash.ImageHash) func(*storage.MessagePicture) (int, bool, error) {
	return func(msg *storage.MessagePicture) (dist int, ok bool, err error) {
		dist, err = pHash.Distance(msg.PHash)
		if err != nil {
			return 0, false, fmt.Errorf("failed to get distance: %w", err)
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
	}
}

func (b *BayanBot) comparePictureMatchFilter(dHash *goimagehash.ImageHash, excludedMessageID int) func(*storage.MessagePicture) (int, bool, error) {
	return func(msg *storage.MessagePicture) (dist int, ok bool, err error) {
		if msg.ID == excludedMessageID {
			return 0, false, nil
		}

		dist, err = dHash.Distance(msg.DHash)
		if err != nil {
			return 0, false, fmt.Errorf("failed to get distance: %w", err)
		}

		if dist < 15 {
			b.logger.Debug(
				"found similar message",
				zap.Int("distance", dist),
				zap.Int("id", msg.ID),
			)
			return dist, true, nil
		}

		return dist, false, nil
	}
}

func (b *BayanBot) processPicture(ctx context.Context, api *bot.Bot, msg *models.Message, pic models.PhotoSize) error {
	pHash, dHash, err := b.hashPicture(ctx, api, pic)
	if err != nil {
		return fmt.Errorf("failed to hash pictures: %w", err)
	}

	// Will find the first match and stop
	similar, err := b.store.FindMsgPictureFilter(
		msg.Chat.ID,
		1,
		b.pictureMatchFilter(pHash),
	)
	if err != nil {
		return fmt.Errorf("failed to find similar messages: %w", err)
	}

	if len(similar) > 0 {
		err := b.replyBayan(ctx, api, msg, similar[0])
		if err != nil {
			return fmt.Errorf("failed to reply bayan: %w", err)
		}

		err = b.store.SaveBayanEvent(msg.Chat.ID, msg.ID, msg.From.ID, similar[0].Msg.ID, similar[0].Distance)
		if err != nil {
			return fmt.Errorf("failed to save bayan event: %w", err)
		}
	}

	err = b.store.SaveMessagePicture(msg, pHash, dHash)
	if err != nil {
		return fmt.Errorf("failed to save message: %w", err)
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
		ChatID:          msg.Chat.ID,
		Text:            text,
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		ParseMode:       models.ParseModeMarkdown,
	})
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

func (b *BayanBot) comparePicture(ctx context.Context, api *bot.Bot, msg *models.Message, pic models.PhotoSize) error {
	_, dHash, err := b.hashPicture(ctx, api, pic)
	if err != nil {
		return fmt.Errorf("failed to hash pictures: %w", err)
	}

	// Will find all similar messages
	similar, err := b.store.FindMsgPictureFilter(
		msg.Chat.ID,
		0,
		b.comparePictureMatchFilter(dHash, msg.ReplyToMessage.ID),
	)
	if err != nil {
		return fmt.Errorf("failed to find similar messages: %w", err)
	}

	if len(similar) > 0 {
		err := b.replySimilar(ctx, api, msg, similar)
		if err != nil {
			return fmt.Errorf("failed to reply bayan: %w", err)
		}
	} else {
		_, err = api.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          msg.Chat.ID,
			Text:            "Похожих постов не видел",
			ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
		})
		if err != nil {
			return fmt.Errorf("failed to send message: %w", err)
		}
	}

	return nil
}

func (b *BayanBot) compareCmd(ctx context.Context, api *bot.Bot, update *models.Update) {
	if update.Message.ReplyToMessage == nil {
		_, err := api.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          update.Message.Chat.ID,
			Text:            "Ответь на картинку/видео, которое хотите сравнить",
			ReplyParameters: &models.ReplyParameters{MessageID: update.Message.ID},
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
			ChatID:          update.Message.Chat.ID,
			Text:            "Дуров не дает мне работать со сторисами",
			ReplyParameters: &models.ReplyParameters{MessageID: update.Message.ID},
		})
		if err != nil {
			b.logger.Error("failed to send message", zap.Error(err))
		}
	}
}

func (b *BayanBot) replySimilar(ctx context.Context, api *bot.Bot, msg *models.Message, similar []*storage.SimilarMessage) error {
	var text strings.Builder
	text.WriteString("Что-то похожее:\n")
	for _, s := range similar {
		chatID := (s.Msg.ChatID + 1000000000000) * -1
		fmt.Fprintf(&text, "- https://t.me/c/%d/%d\n", chatID, s.Msg.ID)
	}

	_, err := api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          msg.Chat.ID,
		Text:            text.String(),
		ReplyParameters: &models.ReplyParameters{MessageID: msg.ID},
	})
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

func hashPicFile(path string) (dHash, pHash *goimagehash.ImageHash, err error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open file: %w", err)
	}

	img, err := jpeg.Decode(file)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode image: %w", err)
	}

	pHash, err = goimagehash.PerceptionHash(img)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get perception hash: %w", err)
	}

	dHash, err = goimagehash.DifferenceHash(img)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get perception hash: %w", err)
	}

	return pHash, dHash, nil
}

func (b *BayanBot) hashVideo(ctx context.Context, api *bot.Bot, video *models.Video) (pHashes, dHashes *storage.VideoHashes, err error) {
	var framesPHashes, framesDHashes *storage.VideoHashes
	processFrames := func(dirName string, files []os.DirEntry) error {
		var hashErr error
		framesPHashes, framesDHashes, hashErr = hashFrames(dirName, files)
		if hashErr != nil {
			return fmt.Errorf("failed to hash frames: %w", hashErr)
		}

		return nil
	}
	err = b.withVideoFrames(ctx, api, video, processFrames)
	if err != nil {
		return nil, nil, err
	}

	return framesPHashes, framesDHashes, nil
}

func saveVideoFile(file io.ReadCloser, dirName string, video *models.Video) (fileName string, err error) {
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close downloaded file: %w", closeErr)
		}
	}()

	fileName = fmt.Sprintf("%s/%s.mp4", dirName, video.FileID)
	f, err := os.Create(fileName)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close video file: %w", closeErr)
		}
	}()

	_, err = io.Copy(f, file)
	if err != nil {
		return "", fmt.Errorf("failed to copy file: %w", err)
	}

	return fileName, nil
}

func (b *BayanBot) withVideoFrames(ctx context.Context, api *bot.Bot, video *models.Video, process func(string, []os.DirEntry) error) (err error) {
	file, err := b.downloadFile(ctx, api, video.FileID)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	// Create temp dir
	dirName := bot.RandomString(10)
	err = os.Mkdir(dirName, 0755)
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Cleanup
	defer func() {
		clErr := os.RemoveAll(dirName)
		if clErr != nil {
			b.logger.Error("failed to remove temp dir", zap.Error(err))
		}
	}()

	// Save video to temp dir
	fileName, err := saveVideoFile(file, dirName, video)
	if err != nil {
		return err
	}

	// Extract scenes from video
	command := exec.Command(
		"ffmpeg",
		"-i", fileName,
		"-vf", "select=gt(scene,0.2)",
		"-vsync", "vfr",
		"-vf", "fps=1",
		dirName+"/out%d.jpg",
	)
	err = command.Run()
	if err != nil {
		return fmt.Errorf("failed to extract scenes from video: %w", err)
	}

	// Get frames count
	files, err := os.ReadDir(dirName)
	if err != nil {
		return fmt.Errorf("failed to read dir: %w", err)
	}

	if len(files) < 4 {
		return errors.New("not enough frames")
	}

	return process(dirName, files)
}

func averageVideoHashDistance(left, right *storage.VideoHashes) (int, error) {
	pairs := [][2]*goimagehash.ImageHash{
		{left.FrameA, right.FrameA},
		{left.FrameB, right.FrameB},
		{left.FrameC, right.FrameC},
		{left.FrameD, right.FrameD},
	}

	distance := 0
	for _, pair := range pairs {
		frameDistance, err := pair[0].Distance(pair[1])
		if err != nil {
			return 0, fmt.Errorf("failed to get distance: %w", err)
		}
		distance += frameDistance
	}

	return distance / 4, nil
}

func (b *BayanBot) processVideoMatchFilter(hashes *storage.VideoHashes) func(*storage.MessageVideo) (int, bool, error) {
	return func(msg *storage.MessageVideo) (int, bool, error) {
		distance, err := averageVideoHashDistance(hashes, &msg.PHashes)
		if err != nil {
			return 0, false, err
		}
		if distance < 10 {
			b.logger.Debug("found similar message", zap.Int("distance", distance), zap.Int("id", msg.Msg.ID))
			return distance, true, nil
		}
		return distance, false, nil
	}
}

func (b *BayanBot) compareVideoMatchFilter(hashes *storage.VideoHashes, excludedMessageID int) func(*storage.MessageVideo) (int, bool, error) {
	return func(msg *storage.MessageVideo) (int, bool, error) {
		if msg.Msg.ID == excludedMessageID {
			return 0, false, nil
		}
		distance, err := averageVideoHashDistance(hashes, &msg.DHashes)
		if err != nil {
			return 0, false, err
		}
		if distance < 15 {
			b.logger.Debug("found similar message", zap.Int("distance", distance), zap.Int("id", msg.Msg.ID))
			return distance, true, nil
		}
		return distance, false, nil
	}
}

func (b *BayanBot) saveVideoMatch(ctx context.Context, api *bot.Bot, message *models.Message, similar []*storage.SimilarMessage) error {
	if len(similar) == 0 {
		return nil
	}

	err := b.replyBayan(ctx, api, message, similar[0])
	if err != nil {
		return fmt.Errorf("failed to reply bayan: %w", err)
	}

	err = b.store.SaveBayanEvent(message.Chat.ID, message.ID, message.From.ID, similar[0].Msg.ID, similar[0].Distance)
	if err != nil {
		return fmt.Errorf("failed to save bayan event: %w", err)
	}

	return nil
}

func (b *BayanBot) replyVideoComparison(ctx context.Context, api *bot.Bot, message *models.Message, similar []*storage.SimilarMessage) error {
	if len(similar) > 0 {
		err := b.replySimilar(ctx, api, message, similar)
		if err != nil {
			return fmt.Errorf("failed to reply bayan: %w", err)
		}

		return nil
	}

	_, err := api.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          message.Chat.ID,
		Text:            "Похожих постов не видел",
		ReplyParameters: &models.ReplyParameters{MessageID: message.ID},
	})
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	return nil
}

func hashFrames(dirName string, files []os.DirEntry) (framesPHashes, framesDHashes *storage.VideoHashes, err error) {
	framesPHashes = &storage.VideoHashes{}
	framesDHashes = &storage.VideoHashes{}

	fileA := dirName + "/" + files[1].Name()
	pHashA, dHashA, err := hashPicFile(fileA)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to hash picture: %w", err)
	}
	framesPHashes.FrameA = pHashA
	framesDHashes.FrameA = dHashA

	fileB := dirName + "/" + files[len(files)/4].Name()
	pHashB, dHashB, err := hashPicFile(fileB)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to hash picture: %w", err)
	}
	framesPHashes.FrameB = pHashB
	framesDHashes.FrameB = dHashB

	fileC := dirName + "/" + files[len(files)-len(files)/4].Name()
	pHashC, dHashC, err := hashPicFile(fileC)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to hash picture: %w", err)
	}
	framesPHashes.FrameC = pHashC
	framesDHashes.FrameC = dHashC

	fileD := dirName + "/" + files[len(files)-2].Name()
	pHashD, dHashD, err := hashPicFile(fileD)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to hash picture: %w", err)
	}
	framesPHashes.FrameD = pHashD
	framesDHashes.FrameD = dHashD

	return framesPHashes, framesDHashes, nil
}

func (b *BayanBot) processVideo(ctx context.Context, api *bot.Bot, message *models.Message) error {
	if message.Video.FileSize > 20*1024*1024 {
		if err := b.processVideoThumbnail(ctx, api, message); err != nil {
			return fmt.Errorf("failed to process video thumbnail: %w", err)
		}
		return nil
	}

	framesPHashes, framesDHashes, err := b.hashVideo(ctx, api, message.Video)
	if err != nil {
		return fmt.Errorf("failed to hash video: %w", err)
	}

	similar, err := b.store.FindMsgVideoFilter(
		message.Chat.ID,
		1,
		b.processVideoMatchFilter(framesPHashes),
	)
	if err != nil {
		return fmt.Errorf("failed to find similar messages: %w", err)
	}

	err = b.saveVideoMatch(ctx, api, message, similar)
	if err != nil {
		return err
	}

	err = b.store.SaveMessageVideo(message, framesPHashes, framesDHashes)
	if err != nil {
		return fmt.Errorf("failed to save message: %w", err)
	}

	return nil
}

func (b *BayanBot) compareVideo(ctx context.Context, api *bot.Bot, message *models.Message) error {
	video := message.ReplyToMessage.Video
	if message.ReplyToMessage.Video.FileSize > 20*1024*1024 {
		if err := b.processVideoThumbnail(ctx, api, message); err != nil {
			return fmt.Errorf("failed to process video thumbnail: %w", err)
		}
		return nil
	}

	_, framesDHashes, err := b.hashVideo(ctx, api, video)
	if err != nil {
		return fmt.Errorf("failed to hash video: %w", err)
	}

	similar, err := b.store.FindMsgVideoFilter(
		message.Chat.ID,
		0,
		b.compareVideoMatchFilter(framesDHashes, message.ReplyToMessage.ID),
	)
	if err != nil {
		return fmt.Errorf("failed to find similar messages: %w", err)
	}

	err = b.replyVideoComparison(ctx, api, message, similar)
	if err != nil {
		return err
	}

	return nil
}

func (b *BayanBot) processVideoThumbnail(ctx context.Context, api *bot.Bot, msg *models.Message) error {
	// TODO: Check if thumbnail is mostly black

	pHash, dHash, err := b.hashPicture(ctx, api, *msg.Video.Thumbnail)
	if err != nil {
		return fmt.Errorf("failed to hash pictures: %w", err)
	}

	// Will find the first match and stop
	similar, err := b.store.FindMsgPictureFilter(
		msg.Chat.ID,
		1,
		b.pictureMatchFilter(pHash),
	)
	if err != nil {
		return fmt.Errorf("failed to find similar messages: %w", err)
	}

	if len(similar) > 0 {
		err := b.replyBayan(ctx, api, msg, similar[0])
		if err != nil {
			return fmt.Errorf("failed to reply bayan: %w", err)
		}

		err = b.store.SaveBayanEvent(msg.Chat.ID, msg.ID, msg.From.ID, similar[0].Msg.ID, similar[0].Distance)
		if err != nil {
			return fmt.Errorf("failed to save bayan event: %w", err)
		}
	}

	err = b.store.SaveMessagePicture(msg, pHash, dHash)
	if err != nil {
		return fmt.Errorf("failed to save message: %w", err)
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

	botConfig := BayanConfig{
		kekReplyChance: config.KekReplyChance,
		showSimilarity: config.ShowSimilarity,
	}
	bayanBot := NewBayanBot(
		config.TelegramToken,
		store,
		logger,
		botConfig,
	)

	opts := []bot.Option{
		bot.WithDefaultHandler(bayanBot.processMessage),
		bot.WithMessageTextHandler("/start", bot.MatchTypePrefix, bayanBot.startCmd),
		bot.WithMessageTextHandler("/compare", bot.MatchTypePrefix, bayanBot.compareCmd),
	}

	b, err := bot.New(config.TelegramToken, opts...)
	if err != nil {
		logger.Fatal("failed to create bot", zap.Error(err))
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	b.Start(ctx)
}
