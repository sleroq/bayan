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

	return &Storage{db}, nil
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
