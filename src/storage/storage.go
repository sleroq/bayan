package storage

import (
	"bytes"
	"database/sql"
	"github.com/corona10/goimagehash"
	"github.com/go-faster/errors"
	"github.com/go-telegram/bot/models"
	_ "github.com/mattn/go-sqlite3"
	"sort"
	"time"
)

type Storage struct {
	db *sql.DB
}

type Message struct {
	ID       int
	UserID   int
	ChatID   int
	SentDate time.Time
	AHash    *goimagehash.ImageHash
	DHash    *goimagehash.ImageHash
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

func (s *Storage) SaveMessage(msg *models.Message, pHash *goimagehash.ImageHash, dHash *goimagehash.ImageHash) error {
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
			pHash,
			dHash
		) values (
			:id,
			:userId,
			:chatId,
			:sentDate,
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

// FindMsgFilter finds messages in the database and applies a filter to them.
// The filter function should return the distance between the hashes, whether
// the message is a match and an error if any.
// The messages are sorted by distance in descending order.
// If limit is 0, all matches are returned.
func (s *Storage) FindMsgFilter(chatID int64, limit int, filter func(msg *Message) (dist int, ok bool, err error)) ([]*SimilarMessage, error) {
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
		var msg Message
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

		msg.AHash = pHash
		msg.DHash = dHash

		dist, ok, err := filter(&msg)
		if err != nil {
			return nil, errors.Wrap(err, "filtering message")
		}

		var sMsg SimilarMessage
		sMsg.Msg = &msg
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
