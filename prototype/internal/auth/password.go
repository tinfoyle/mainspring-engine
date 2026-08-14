package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory      = 64 * 1024
	argonIterations  = 3
	argonParallelism = 2
	argonSaltLength  = 16
	argonKeyLength   = 32
)

func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}

	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	digest := argon2.IDKey([]byte(password), salt, argonIterations, argonMemory, argonParallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argonMemory,
		argonIterations,
		argonParallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

func CheckPassword(encoded, password string) bool {
	parameters, salt, expected, err := parsePasswordHash(encoded)
	if err != nil {
		return false
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		parameters.iterations,
		parameters.memory,
		parameters.parallelism,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(expected, actual) == 1
}

func ValidatePassword(password string) error {
	if len(password) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	if len(password) > 1024 {
		return errors.New("password is too long")
	}
	return nil
}

type hashParameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parsePasswordHash(encoded string) (hashParameters, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return hashParameters{}, nil, nil, errors.New("invalid password hash")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return hashParameters{}, nil, nil, errors.New("unsupported argon2 version")
	}

	parameterParts := strings.Split(parts[3], ",")
	if len(parameterParts) != 3 {
		return hashParameters{}, nil, nil, errors.New("invalid argon2 parameters")
	}
	memory, err := parseUintParameter(parameterParts[0], "m=")
	if err != nil {
		return hashParameters{}, nil, nil, err
	}
	iterations, err := parseUintParameter(parameterParts[1], "t=")
	if err != nil {
		return hashParameters{}, nil, nil, err
	}
	parallelism, err := parseUintParameter(parameterParts[2], "p=")
	if err != nil || parallelism > 255 {
		return hashParameters{}, nil, nil, errors.New("invalid argon2 parallelism")
	}
	if memory > 256*1024 || iterations > 10 || parallelism > 16 {
		return hashParameters{}, nil, nil, errors.New("argon2 parameters exceed safety limits")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return hashParameters{}, nil, nil, errors.New("invalid password salt")
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(digest) < 16 || len(digest) > 64 {
		return hashParameters{}, nil, nil, errors.New("invalid password digest")
	}

	return hashParameters{memory: uint32(memory), iterations: uint32(iterations), parallelism: uint8(parallelism)}, salt, digest, nil
}

func parseUintParameter(value, prefix string) (uint64, error) {
	if !strings.HasPrefix(value, prefix) {
		return 0, errors.New("invalid argon2 parameter")
	}
	parsed, err := strconv.ParseUint(strings.TrimPrefix(value, prefix), 10, 32)
	if err != nil || parsed == 0 {
		return 0, errors.New("invalid argon2 parameter value")
	}
	return parsed, nil
}
