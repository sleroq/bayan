package storage

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"github.com/corona10/goimagehash"
	"github.com/go-faster/errors"
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
		return nil, errors.Wrap(err, "opening sqlite database")
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
		return nil, errors.Wrap(err, "creating messages table")
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
		return nil, errors.Wrap(err, "creating bayan_events table")
	}

	_, err = db.Exec(`
		create index if not exists idx_bayan_events_chat_user on bayan_events (chatId, userId);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating bayan_events chat user index")
	}

	_, err = db.Exec(`
		create index if not exists idx_bayan_events_chat_created_at on bayan_events (chatId, createdAt);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating bayan_events chat createdAt index")
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
		return errors.Wrap(err, "saving bayan event")
	}

	return nil
}

// BackfillBayanEvents recreates duplicate detections from the stored hashes.
// It returns the number of events inserted; existing events are left unchanged.
func (s *Storage) BackfillBayanEvents() (int, error) {
	rows, err := s.db.Query(`
		select id, userId, chatId, sentDate, isVideo, pHash
		from messages
		order by chatId asc, id asc;
	`)
	if err != nil {
		return 0, errors.Wrap(err, "querying messages for bayan event backfill")
	}

	var messages []storedMessage
	for rows.Next() {
		var msg storedMessage
		if err := rows.Scan(&msg.ID, &msg.UserID, &msg.ChatID, &msg.SentDate, &msg.isVideo, &msg.pHash); err != nil {
			_ = rows.Close()
			return 0, errors.Wrap(err, "scanning message for bayan event backfill")
		}
		messages = append(messages, msg)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, errors.Wrap(err, "iterating messages for bayan event backfill")
	}
	if err := rows.Close(); err != nil {
		return 0, errors.Wrap(err, "closing messages for bayan event backfill")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, errors.Wrap(err, "starting bayan event backfill")
	}
	defer func() { _ = tx.Rollback() }()

	insert, err := tx.Prepare(`
		insert or ignore into bayan_events (chatId, messageId, userId, matchedMessageId, distance, createdAt)
		values (?, ?, ?, ?, ?, ?);
	`)
	if err != nil {
		return 0, errors.Wrap(err, "preparing bayan event backfill insert")
	}
	defer func() { _ = insert.Close() }()

	previous := make(map[int][]storedMessage)
	inserted := 0
	for _, current := range messages {
		for _, candidate := range slices.Backward(previous[current.ChatID]) {
			if candidate.isVideo != current.isVideo {
				continue
			}

			var distance int
			if current.isVideo {
				currentHashes, err := LoadVideoHashes(bytes.NewReader(current.pHash))
				if err != nil {
					return 0, errors.Wrap(err, "loading video hashes for bayan event backfill")
				}
				candidateHashes, err := LoadVideoHashes(bytes.NewReader(candidate.pHash))
				if err != nil {
					return 0, errors.Wrap(err, "loading candidate video hashes for bayan event backfill")
				}
				distance, err = averageVideoHashDistance(currentHashes, candidateHashes)
				if err != nil {
					return 0, errors.Wrap(err, "calculating video distance for bayan event backfill")
				}
			} else {
				currentHash, err := goimagehash.LoadImageHash(bytes.NewReader(current.pHash))
				if err != nil {
					return 0, errors.Wrap(err, "loading picture hash for bayan event backfill")
				}
				candidateHash, err := goimagehash.LoadImageHash(bytes.NewReader(candidate.pHash))
				if err != nil {
					return 0, errors.Wrap(err, "loading candidate picture hash for bayan event backfill")
				}
				distance, err = currentHash.Distance(candidateHash)
				if err != nil {
					return 0, errors.Wrap(err, "calculating picture distance for bayan event backfill")
				}
			}

			if distance < 10 {
				result, err := insert.Exec(current.ChatID, current.ID, current.UserID, candidate.ID, distance, current.SentDate)
				if err != nil {
					return 0, errors.Wrap(err, "inserting bayan event backfill")
				}
				count, err := result.RowsAffected()
				if err != nil {
					return 0, errors.Wrap(err, "counting bayan event backfill insert")
				}
				inserted += int(count)
				break
			}
		}

		previous[current.ChatID] = append(previous[current.ChatID], current)
	}

	if err := tx.Commit(); err != nil {
		return 0, errors.Wrap(err, "committing bayan event backfill")
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
		return errors.Wrap(err, "dumping pHash")
	}

	var dHashDump bytes.Buffer
	err = dHash.Dump(&dHashDump)
	if err != nil {
		return errors.Wrap(err, "dumping dHash")
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
		return errors.Wrap(err, "saving message to database")
	}

	return nil
}

// FindMsgPictureFilter finds messages in the database and applies a filter to them.
// The filter function should return the distance between the hashes, whether
// the message is a match and an error if any.
// The messages are sorted by distance in descending order.
// If limit is 0, all matches are returned.
func (s *Storage) FindMsgPictureFilter(chatID int64, limit int, filter func(msg *MessagePicture) (dist int, ok bool, err error)) ([]*SimilarMessage, error) {
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
		return nil, errors.Wrap(err, "querying messages")
	}
	defer func() {
		errClose := rows.Close()
		if errClose != nil {
			err = errors.Wrapf(err, "closing rows: %s", errClose)
		}
	}()

	var messages []*SimilarMessage
	for rows.Next() {
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
			return nil, errors.Wrap(err, "scanning message")
		}

		dHash, err := goimagehash.LoadImageHash(bytes.NewReader(dHashBytes))
		if err != nil {
			return nil, errors.Wrap(err, "loading dHash")
		}
		pHash, err := goimagehash.LoadImageHash(bytes.NewReader(pHashBytes))
		if err != nil {
			return nil, errors.Wrap(err, "loading pHash")
		}

		msg.PHash = pHash
		msg.DHash = dHash

		dist, ok, err := filter(&msg)
		if err != nil {
			return nil, errors.Wrap(err, "filtering message")
		}

		var sMsg SimilarMessage
		sMsg.Msg = &Message{
			ID:       msg.ID,
			UserID:   msg.UserID,
			ChatID:   msg.ChatID,
			SentDate: msg.SentDate,
		}
		sMsg.Distance = dist

		if ok {
			messages = append(messages, &sMsg)
		}

		if limit != 0 && len(messages) >= limit {
			break
		}
	}

	// Sort descending
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Distance > messages[j].Distance
	})

	return messages, nil
}

func (s *Storage) SaveMessageVideo(msg *models.Message, pHashes, dHashes *VideoHashes) error {
	var pHashDump bytes.Buffer
	err := pHashes.Dump(&pHashDump)
	if err != nil {
		return errors.Wrap(err, "dumping pHash")
	}

	var dHashDump bytes.Buffer
	err = dHashes.Dump(&dHashDump)
	if err != nil {
		return errors.Wrap(err, "dumping dHash")
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
		return errors.Wrap(err, "saving message to database")
	}

	return nil
}

func (s *Storage) FindMsgVideoFilter(chatID int64, limit int, filter func(msg *MessageVideo) (dist int, ok bool, err error)) ([]*SimilarMessage, error) {
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
		return nil, errors.Wrap(err, "querying messages")
	}
	defer func() {
		errClose := rows.Close()
		if errClose != nil {
			err = errors.Wrapf(err, "closing rows: %s", errClose)
		}
	}()

	var messages []*SimilarMessage
	for rows.Next() {
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
			return nil, errors.Wrap(err, "scanning message")
		}

		dHash, err := LoadVideoHashes(bytes.NewReader(dHashBytes))
		if err != nil {
			return nil, errors.Wrap(err, "loading dHash")
		}
		pHash, err := LoadVideoHashes(bytes.NewReader(pHashBytes))
		if err != nil {
			return nil, errors.Wrap(err, "loading pHash")
		}

		msg.DHashes = *dHash
		msg.PHashes = *pHash

		dist, ok, err := filter(&msg)
		if err != nil {
			return nil, errors.Wrap(err, "filtering message")
		}

		var sMsg SimilarMessage
		sMsg.Msg = &Message{
			ID:       msg.Msg.ID,
			UserID:   msg.Msg.UserID,
			ChatID:   msg.Msg.ChatID,
			SentDate: msg.Msg.SentDate,
		}
		sMsg.Distance = dist

		if ok {
			messages = append(messages, &sMsg)
		}

		if limit != 0 && len(messages) >= limit {
			break
		}
	}

	// Sort descending
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].Distance > messages[j].Distance
	})

	return messages, nil
}
