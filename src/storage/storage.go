package storage

import (
	"bytes"
	"database/sql"
	"github.com/corona10/goimagehash"
	"github.com/go-faster/errors"
	"github.com/go-telegram/bot/models"
	_ "github.com/mattn/go-sqlite3"
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
			aHash blob not null,
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
			aHash,
			dHash
		) values (
			:id,
			:userId,
			:chatId,
			:sentDate,
			:aHash,
			:dHash
		);`,
		sql.Named("id", msg.ID),
		sql.Named("userId", msg.From.ID),
		sql.Named("chatId", msg.Chat.ID),
		sql.Named("sentDate", msg.Date),
		sql.Named("aHash", pHashDump.Bytes()),
		sql.Named("dHash", dHashDump.Bytes()),
	)
	if err != nil {
		return errors.Wrap(err, "saving message to database")
	}

	return nil
}

func (s *Storage) FindMsgFilter(filter func(msg *Message) (bool, error)) ([]*Message, error) {
	rows, err := s.db.Query(`
		select
			id,
			userId,
			chatId,
			sentDate,
			aHash,
			dHash
		from messages
		where chatId = :chatId
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

	var messages []*Message
	for rows.Next() {
		var msg Message
		var aHashBytes, dHashBytes []byte
		err := rows.Scan(
			&msg.ID,
			&msg.UserID,
			&msg.ChatID,
			&msg.SentDate,
			&aHashBytes,
			&dHashBytes,
		)
		if err != nil {
			return nil, errors.Wrap(err, "scanning message")
		}

		dHash, err := goimagehash.LoadImageHash(bytes.NewReader(dHashBytes))
		if err != nil {
			return nil, errors.Wrap(err, "loading dHash")
		}
		aHash, err := goimagehash.LoadImageHash(bytes.NewReader(aHashBytes))
		if err != nil {
			return nil, errors.Wrap(err, "loading aHash")
		}

		msg.AHash = aHash
		msg.DHash = dHash

		ok, err := filter(&msg)
		if err != nil {
			return nil, errors.Wrap(err, "filtering message")
		}

		if !ok {
			continue
		}

		messages = append(messages, &msg)
	}

	return messages, nil
}
