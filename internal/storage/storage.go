package storage

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"fmt"
	"github.com/corona10/goimagehash"
	"github.com/go-telegram/bot/models"
	_ "github.com/mattn/go-sqlite3"
	"io"
	"slices"
	"sort"
	"time"
)

type Storage struct {
	db *sql.DB
}

type MessagePicture struct {
	ID       int
	UserID   int
	ChatID   int
	SentDate time.Time
	PHash    *goimagehash.ImageHash
	DHash    *goimagehash.ImageHash
}

type VideoHashes struct {
	FrameA *goimagehash.ImageHash
	FrameB *goimagehash.ImageHash
	FrameC *goimagehash.ImageHash
	FrameD *goimagehash.ImageHash
}

// Dump method writes a binary serialization into w io.Writer.
func (h *VideoHashes) Dump(w io.Writer) error {
	type H struct {
		Hash uint64
		Kind goimagehash.Kind
	}
	type D struct {
		FrameA H
		FrameB H
		FrameC H
		FrameD H
	}
	enc := gob.NewEncoder(w)
	err := enc.Encode(D{
		FrameA: H{Hash: h.FrameA.GetHash(), Kind: h.FrameA.GetKind()},
		FrameB: H{Hash: h.FrameB.GetHash(), Kind: h.FrameB.GetKind()},
		FrameC: H{Hash: h.FrameC.GetHash(), Kind: h.FrameC.GetKind()},
		FrameD: H{Hash: h.FrameD.GetHash(), Kind: h.FrameD.GetKind()},
	})
	if err != nil {
		return err
	}
	return nil
}

// LoadVideoHashes method loads a VideoHashes from io.Reader.
func LoadVideoHashes(b io.Reader) (*VideoHashes, error) {
	type H struct {
		Hash uint64
		Kind goimagehash.Kind
	}
	type D struct {
		FrameA H
		FrameB H
		FrameC H
		FrameD H
	}

	var d D
	dec := gob.NewDecoder(b)
	err := dec.Decode(&d)
	if err != nil {
		return nil, err
	}

	return &VideoHashes{
		FrameA: goimagehash.NewImageHash(d.FrameA.Hash, d.FrameA.Kind),
		FrameB: goimagehash.NewImageHash(d.FrameB.Hash, d.FrameB.Kind),
		FrameC: goimagehash.NewImageHash(d.FrameC.Hash, d.FrameC.Kind),
		FrameD: goimagehash.NewImageHash(d.FrameD.Hash, d.FrameD.Kind),
	}, nil
}

type Message struct {
	ID       int
	UserID   int
	ChatID   int
	SentDate time.Time
}

type MessageVideo struct {
	Msg     Message
	PHashes VideoHashes
	DHashes VideoHashes
}

type SimilarMessage struct {
	Msg      *Message
	Distance int
}

type storedMessage struct {
	Message
	isVideo bool
	pHash   []byte
}

func New(filepath string) (*Storage, error) {
	db, err := sql.Open("sqlite3", filepath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	_, err = db.Exec(`
		create table if not exists messages (
			id integer not null,
			userId integer not null,
			chatId integer not null,
			sentDate timestamp not null,
			isVideo integer not null,
			pHash blob not null,
			dHash blob not null,
			primary key (id, chatId)
		);
	`)
	if err != nil {
		return nil, fmt.Errorf("creating messages table: %w", err)
	}

	_, err = db.Exec(`
		create table if not exists bayan_events (
			chatId integer not null,
			messageId integer not null,
			userId integer not null,
			matchedMessageId integer,
			distance integer,
			createdAt timestamp not null,
			primary key (chatId, messageId)
		);
	`)
	if err != nil {
		return nil, fmt.Errorf("creating bayan_events table: %w", err)
	}

	_, err = db.Exec(`
		create index if not exists idx_bayan_events_chat_user on bayan_events (chatId, userId);
	`)
	if err != nil {
		return nil, fmt.Errorf("creating bayan_events chat user index: %w", err)
	}

	_, err = db.Exec(`
		create index if not exists idx_bayan_events_chat_created_at on bayan_events (chatId, createdAt);
	`)
	if err != nil {
		return nil, fmt.Errorf("creating bayan_events chat createdAt index: %w", err)
	}

	return &Storage{db}, nil
}

func (s *Storage) SaveBayanEvent(chatID int64, messageID int, userID int64, matchedMessageID int, distance int) error {
	_, err := s.db.Exec(`
		insert or ignore into bayan_events (
			chatId,
			messageId,
			userId,
			matchedMessageId,
			distance,
			createdAt
		) values (
			:chatId,
			:messageId,
			:userId,
			:matchedMessageId,
			:distance,
			:createdAt
		);
	`,
		sql.Named("chatId", chatID),
		sql.Named("messageId", messageID),
		sql.Named("userId", userID),
		sql.Named("matchedMessageId", matchedMessageID),
		sql.Named("distance", distance),
		sql.Named("createdAt", time.Now()),
	)
	if err != nil {
		return fmt.Errorf("saving bayan event: %w", err)
	}

	return nil
}

// BackfillBayanEvents recreates duplicate detections from the stored hashes.
// It returns the number of events inserted; existing events are left unchanged.
func (s *Storage) BackfillBayanEvents() (int, error) {
	messages, err := s.loadBackfillMessages()
	if err != nil {
		return 0, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("starting bayan event backfill: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	insert, err := tx.Prepare(`
		insert or ignore into bayan_events (chatId, messageId, userId, matchedMessageId, distance, createdAt)
		values (?, ?, ?, ?, ?, ?);
	`)
	if err != nil {
		return 0, fmt.Errorf("preparing bayan event backfill insert: %w", err)
	}
	defer func() { _ = insert.Close() }()

	inserted, err := insertBackfillEvents(insert, messages)
	if err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing bayan event backfill: %w", err)
	}

	return inserted, nil
}

func (s *Storage) loadBackfillMessages() ([]storedMessage, error) {
	rows, err := s.db.Query(`
		select id, userId, chatId, sentDate, isVideo, pHash
		from messages
		order by chatId asc, id asc;
	`)
	if err != nil {
		return nil, fmt.Errorf("querying messages for bayan event backfill: %w", err)
	}

	var messages []storedMessage
	for rows.Next() {
		var msg storedMessage
		if err := rows.Scan(&msg.ID, &msg.UserID, &msg.ChatID, &msg.SentDate, &msg.isVideo, &msg.pHash); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scanning message for bayan event backfill: %w", err)
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterating messages for bayan event backfill: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("closing messages for bayan event backfill: %w", err)
	}

	return messages, nil
}

func storedMessageHashDistance(current, candidate storedMessage) (int, error) {
	if current.isVideo {
		currentHashes, err := LoadVideoHashes(bytes.NewReader(current.pHash))
		if err != nil {
			return 0, fmt.Errorf("loading video hashes for bayan event backfill: %w", err)
		}
		candidateHashes, err := LoadVideoHashes(bytes.NewReader(candidate.pHash))
		if err != nil {
			return 0, fmt.Errorf("loading candidate video hashes for bayan event backfill: %w", err)
		}
		distance, err := averageVideoHashDistance(currentHashes, candidateHashes)
		if err != nil {
			return 0, fmt.Errorf("calculating video distance for bayan event backfill: %w", err)
		}
		return distance, nil
	}

	currentHash, err := goimagehash.LoadImageHash(bytes.NewReader(current.pHash))
	if err != nil {
		return 0, fmt.Errorf("loading picture hash for bayan event backfill: %w", err)
	}
	candidateHash, err := goimagehash.LoadImageHash(bytes.NewReader(candidate.pHash))
	if err != nil {
		return 0, fmt.Errorf("loading candidate picture hash for bayan event backfill: %w", err)
	}
	distance, err := currentHash.Distance(candidateHash)
	if err != nil {
		return 0, fmt.Errorf("calculating picture distance for bayan event backfill: %w", err)
	}
	return distance, nil
}

func insertBayanEvent(insert *sql.Stmt, current, candidate storedMessage, distance int) (int, error) {
	result, err := insert.Exec(current.ChatID, current.ID, current.UserID, candidate.ID, distance, current.SentDate)
	if err != nil {
		return 0, fmt.Errorf("inserting bayan event backfill: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting bayan event backfill insert: %w", err)
	}
	return int(count), nil
}

func insertBackfillEvents(insert *sql.Stmt, messages []storedMessage) (int, error) {
	previous := make(map[int][]storedMessage)
	inserted := 0
	for _, current := range messages {
		for _, candidate := range slices.Backward(previous[current.ChatID]) {
			if candidate.isVideo != current.isVideo {
				continue
			}

			distance, err := storedMessageHashDistance(current, candidate)
			if err != nil {
				return 0, err
			}

			if distance >= 10 {
				continue
			}

			count, err := insertBayanEvent(insert, current, candidate, distance)
			if err != nil {
				return 0, err
			}
			inserted += count
			break
		}

		previous[current.ChatID] = append(previous[current.ChatID], current)
	}
	return inserted, nil
}

func averageVideoHashDistance(a, b *VideoHashes) (int, error) {
	distanceA, err := a.FrameA.Distance(b.FrameA)
	if err != nil {
		return 0, err
	}
	distanceB, err := a.FrameB.Distance(b.FrameB)
	if err != nil {
		return 0, err
	}
	distanceC, err := a.FrameC.Distance(b.FrameC)
	if err != nil {
		return 0, err
	}
	distanceD, err := a.FrameD.Distance(b.FrameD)
	if err != nil {
		return 0, err
	}

	return (distanceA + distanceB + distanceC + distanceD) / 4, nil
}

func (s *Storage) SaveMessagePicture(msg *models.Message, pHash *goimagehash.ImageHash, dHash *goimagehash.ImageHash) error {
	var pHashDump bytes.Buffer
	err := pHash.Dump(&pHashDump)
	if err != nil {
		return fmt.Errorf("dumping pHash: %w", err)
	}

	var dHashDump bytes.Buffer
	err = dHash.Dump(&dHashDump)
	if err != nil {
		return fmt.Errorf("dumping dHash: %w", err)
	}

	_, err = s.db.Exec(`
		insert or ignore into messages (
			id,
			userId,
			chatId,
			sentDate,
		    isVideo,
			pHash,
			dHash
		) values (
			:id,
			:userId,
			:chatId,
			:sentDate,
		    0,
			:pHash,
			:dHash
		);`,
		sql.Named("id", msg.ID),
		sql.Named("userId", msg.From.ID),
		sql.Named("chatId", msg.Chat.ID),
		sql.Named("sentDate", msg.Date),
		sql.Named("pHash", pHashDump.Bytes()),
		sql.Named("dHash", dHashDump.Bytes()),
	)
	if err != nil {
		return fmt.Errorf("saving message to database: %w", err)
	}

	return nil
}

// FindMsgPictureFilter finds messages in the database and applies a filter to them.
// The filter function should return the distance between the hashes, whether
// the message is a match and an error if any.
// The messages are sorted by distance in descending order.
// If limit is 0, all matches are returned.
func (s *Storage) FindMsgPictureFilter(chatID int64, limit int, filter func(msg *MessagePicture) (dist int, ok bool, err error)) (messages []*SimilarMessage, err error) {
	rows, err := s.db.Query(`
		select
			id,
			userId,
			chatId,
			sentDate,
			pHash,
			dHash
		from messages
		where chatId = :chatId
		and isVideo = 0
		order by id desc;
	`, sql.Named("chatId", chatID))
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("closing picture rows: %w", closeErr)
		}
	}()

	return scanPictureRows(rows, limit, filter)
}

func scanPictureRows(rows *sql.Rows, limit int, filter func(msg *MessagePicture) (dist int, ok bool, err error)) ([]*SimilarMessage, error) {
	var messages []*SimilarMessage
	for rows.Next() {
		msg, err := scanPictureRow(rows)
		if err != nil {
			return nil, err
		}

		dist, ok, err := filter(msg)
		if err != nil {
			return nil, fmt.Errorf("filtering message: %w", err)
		}

		if ok {
			messages = append(messages, pictureSimilarMessage(*msg, dist))
		}

		if limit != 0 && len(messages) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating picture rows: %w", err)
	}

	// Sort descending
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Distance > messages[j].Distance
	})

	return messages, nil
}

func scanPictureRow(rows *sql.Rows) (*MessagePicture, error) {
	var msg MessagePicture
	var pHashBytes, dHashBytes []byte
	err := rows.Scan(
		&msg.ID,
		&msg.UserID,
		&msg.ChatID,
		&msg.SentDate,
		&pHashBytes,
		&dHashBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning message: %w", err)
	}

	dHash, err := goimagehash.LoadImageHash(bytes.NewReader(dHashBytes))
	if err != nil {
		return nil, fmt.Errorf("loading dHash: %w", err)
	}
	pHash, err := goimagehash.LoadImageHash(bytes.NewReader(pHashBytes))
	if err != nil {
		return nil, fmt.Errorf("loading pHash: %w", err)
	}

	msg.PHash = pHash
	msg.DHash = dHash
	return &msg, nil
}

func pictureSimilarMessage(msg MessagePicture, distance int) *SimilarMessage {
	return &SimilarMessage{
		Msg: &Message{
			ID:       msg.ID,
			UserID:   msg.UserID,
			ChatID:   msg.ChatID,
			SentDate: msg.SentDate,
		},
		Distance: distance,
	}
}

func similarMessage(msg Message, distance int) *SimilarMessage {
	return &SimilarMessage{Msg: &msg, Distance: distance}
}

func (s *Storage) SaveMessageVideo(msg *models.Message, pHashes, dHashes *VideoHashes) error {
	var pHashDump bytes.Buffer
	err := pHashes.Dump(&pHashDump)
	if err != nil {
		return fmt.Errorf("dumping pHash: %w", err)
	}

	var dHashDump bytes.Buffer
	err = dHashes.Dump(&dHashDump)
	if err != nil {
		return fmt.Errorf("dumping dHash: %w", err)
	}

	_, err = s.db.Exec(`
		insert or ignore into messages (
			id,
			userId,
			chatId,
			sentDate,
		    isVideo,
			pHash,
			dHash
		) values (
			:id,
			:userId,
			:chatId,
			:sentDate,
		    1,
			:pHash,
			:dHash
		);`,
		sql.Named("id", msg.ID),
		sql.Named("userId", msg.From.ID),
		sql.Named("chatId", msg.Chat.ID),
		sql.Named("sentDate", msg.Date),
		sql.Named("pHash", pHashDump.Bytes()),
		sql.Named("dHash", dHashDump.Bytes()),
	)
	if err != nil {
		return fmt.Errorf("saving message to database: %w", err)
	}

	return nil
}

func (s *Storage) FindMsgVideoFilter(chatID int64, limit int, filter func(msg *MessageVideo) (dist int, ok bool, err error)) (messages []*SimilarMessage, err error) {
	rows, err := s.db.Query(`
		select
			id,
			userId,
			chatId,
			sentDate,
			pHash,
			dHash
		from messages
		where chatId = :chatId
		and isVideo = 1
		order by id desc;
	`, sql.Named("chatId", chatID))
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("closing video rows: %w", closeErr)
		}
	}()

	return scanVideoRows(rows, limit, filter)
}

func scanVideoRows(rows *sql.Rows, limit int, filter func(msg *MessageVideo) (dist int, ok bool, err error)) ([]*SimilarMessage, error) {
	var messages []*SimilarMessage
	for rows.Next() {
		msg, err := scanVideoRow(rows)
		if err != nil {
			return nil, err
		}

		dist, ok, err := filter(msg)
		if err != nil {
			return nil, fmt.Errorf("filtering message: %w", err)
		}

		if ok {
			messages = append(messages, similarMessage(msg.Msg, dist))
		}

		if limit != 0 && len(messages) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating video rows: %w", err)
	}

	// Sort descending
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Distance > messages[j].Distance
	})

	return messages, nil
}

func scanVideoRow(rows *sql.Rows) (*MessageVideo, error) {
	var msg MessageVideo
	var pHashBytes, dHashBytes []byte
	err := rows.Scan(
		&msg.Msg.ID,
		&msg.Msg.UserID,
		&msg.Msg.ChatID,
		&msg.Msg.SentDate,
		&pHashBytes,
		&dHashBytes,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning message: %w", err)
	}

	dHash, err := LoadVideoHashes(bytes.NewReader(dHashBytes))
	if err != nil {
		return nil, fmt.Errorf("loading dHash: %w", err)
	}
	pHash, err := LoadVideoHashes(bytes.NewReader(pHashBytes))
	if err != nil {
		return nil, fmt.Errorf("loading pHash: %w", err)
	}

	msg.DHashes = *dHash
	msg.PHashes = *pHash
	return &msg, nil
}
