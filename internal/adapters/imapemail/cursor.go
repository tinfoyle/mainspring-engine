package imapemail

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"time"
)

const cursorVersion byte = 1

var cursorMagic = []byte{'S', 'E', 'M', 'C'}

type messageCursor struct {
	UID       uint32
	PartCount uint8
}

type mailboxCursor struct {
	UIDValidity uint32
	Messages    []messageCursor
}

type syncCursor struct {
	ScopeSHA256 [sha256.Size]byte
	Mailboxes   []mailboxCursor
}

func newCursor(folders []string, sinceAt, untilAt *time.Time) syncCursor {
	return syncCursor{ScopeSHA256: scopeDigest(folders, sinceAt, untilAt), Mailboxes: make([]mailboxCursor, len(folders))}
}

func decodeCursor(raw []byte, folders []string, sinceAt, untilAt *time.Time) (syncCursor, error) {
	if len(raw) == 0 {
		return newCursor(folders, sinceAt, untilAt), nil
	}
	reader := bytes.NewReader(raw)
	magic := make([]byte, len(cursorMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || !bytes.Equal(magic, cursorMagic) {
		return syncCursor{}, ErrCursor
	}
	version, err := reader.ReadByte()
	if err != nil || version != cursorVersion {
		return syncCursor{}, ErrCursor
	}
	value := syncCursor{}
	if _, err := io.ReadFull(reader, value.ScopeSHA256[:]); err != nil || value.ScopeSHA256 != scopeDigest(folders, sinceAt, untilAt) {
		return syncCursor{}, ErrCursor
	}
	mailboxCount, err := binary.ReadUvarint(reader)
	if err != nil || mailboxCount != uint64(len(folders)) {
		return syncCursor{}, ErrCursor
	}
	value.Mailboxes = make([]mailboxCursor, int(mailboxCount))
	tracked := 0
	for mailboxIndex := range value.Mailboxes {
		validity, err := binary.ReadUvarint(reader)
		if err != nil || validity > uint64(^uint32(0)) {
			return syncCursor{}, ErrCursor
		}
		value.Mailboxes[mailboxIndex].UIDValidity = uint32(validity)
		messageCount, err := binary.ReadUvarint(reader)
		if err != nil || messageCount > maximumTrackedMessages || tracked+int(messageCount) > maximumTrackedMessages {
			return syncCursor{}, ErrCursor
		}
		tracked += int(messageCount)
		value.Mailboxes[mailboxIndex].Messages = make([]messageCursor, int(messageCount))
		var previous uint64
		for messageIndex := range value.Mailboxes[mailboxIndex].Messages {
			delta, deltaErr := binary.ReadUvarint(reader)
			partCount, partErr := reader.ReadByte()
			uid := previous + delta
			if deltaErr != nil || partErr != nil || delta == 0 || uid > uint64(^uint32(0)) || partCount == 0 || partCount > maximumPartsPerMessage {
				return syncCursor{}, ErrCursor
			}
			value.Mailboxes[mailboxIndex].Messages[messageIndex] = messageCursor{UID: uint32(uid), PartCount: partCount}
			previous = uid
		}
	}
	if reader.Len() != 0 {
		return syncCursor{}, ErrCursor
	}
	return value, nil
}

func encodeCursor(value syncCursor) ([]byte, error) {
	var output bytes.Buffer
	output.Write(cursorMagic)
	output.WriteByte(cursorVersion)
	output.Write(value.ScopeSHA256[:])
	writeUvarint(&output, uint64(len(value.Mailboxes)))
	tracked := 0
	for mailboxIndex := range value.Mailboxes {
		mailbox := &value.Mailboxes[mailboxIndex]
		sort.Slice(mailbox.Messages, func(left, right int) bool { return mailbox.Messages[left].UID < mailbox.Messages[right].UID })
		tracked += len(mailbox.Messages)
		if tracked > maximumTrackedMessages {
			return nil, ErrLimit
		}
		writeUvarint(&output, uint64(mailbox.UIDValidity))
		writeUvarint(&output, uint64(len(mailbox.Messages)))
		var previous uint32
		for _, message := range mailbox.Messages {
			if message.UID <= previous || message.PartCount == 0 || message.PartCount > maximumPartsPerMessage {
				return nil, ErrCursor
			}
			writeUvarint(&output, uint64(message.UID-previous))
			output.WriteByte(message.PartCount)
			previous = message.UID
		}
	}
	if output.Len() > maximumCursorBytes {
		return nil, ErrLimit
	}
	return output.Bytes(), nil
}

func scopeDigest(folders []string, sinceAt, untilAt *time.Time) [sha256.Size]byte {
	hash := sha256.New()
	for _, folder := range folders {
		writeUvarint(hash, uint64(len(folder)))
		_, _ = hash.Write([]byte(folder))
	}
	for _, value := range []*time.Time{sinceAt, untilAt} {
		if value == nil {
			_, _ = hash.Write([]byte{0})
			continue
		}
		_, _ = hash.Write([]byte{1})
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], uint64(value.UTC().UnixNano()))
		_, _ = hash.Write(encoded[:])
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func writeUvarint(writer io.Writer, value uint64) {
	var buffer [binary.MaxVarintLen64]byte
	length := binary.PutUvarint(buffer[:], value)
	_, _ = writer.Write(buffer[:length])
}

func findMessage(messages []messageCursor, uid uint32) (int, bool) {
	index := sort.Search(len(messages), func(index int) bool { return messages[index].UID >= uid })
	return index, index < len(messages) && messages[index].UID == uid
}

func removeMessage(messages []messageCursor, index int) []messageCursor {
	copy(messages[index:], messages[index+1:])
	return messages[:len(messages)-1]
}

func insertMessage(messages []messageCursor, value messageCursor) ([]messageCursor, error) {
	index, found := findMessage(messages, value.UID)
	if found {
		return messages, errors.New("message already exists")
	}
	messages = append(messages, messageCursor{})
	copy(messages[index+1:], messages[index:])
	messages[index] = value
	return messages, nil
}
