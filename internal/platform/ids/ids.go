package ids

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
)

type UserID string
type AccountID string
type MembershipID string
type RegistrationID string
type RecoveryID string
type SessionID string
type InvitationID string
type GrantID string
type CellID string
type WorkItemID string

type Generator interface {
	New() string
}

type RandomGenerator struct{}

func (RandomGenerator) New() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded)
}

func Validate(value string) error {
	if len(value) != 36 || strings.Count(value, "-") != 4 {
		return errors.New("invalid identifier")
	}
	compact := strings.ReplaceAll(value, "-", "")
	if len(compact) != 32 {
		return errors.New("invalid identifier")
	}
	_, err := hex.DecodeString(compact)
	if err != nil {
		return errors.New("invalid identifier")
	}
	return nil
}
