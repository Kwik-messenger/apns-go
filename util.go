package apns

import (
	"encoding/hex"
	"encoding/json"
	"errors"
)

var (
	ErrNoAPS              = errors.New("no 'aps' section in payload")
	ErrPayloadIsTooLarge  = errors.New("payload exceed 256 bytes length")
	ErrInvalidTokenLength = errors.New("invalid token length")
)

type payload map[string]interface{}

// marshalPayload checks payload and marshals it into json
func marshalPayload(p payload) ([]byte, error) {
	_, ok := p["aps"]
	if !ok {
		return nil, ErrNoAPS
	}

	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}

	if uint16(len(data)) >= 255 {
		return nil, ErrPayloadIsTooLarge
	}

	return data, nil
}

func unpackToken(token string) ([]byte, error) {
	data, err := hex.DecodeString(token)
	if err != nil {
		return nil, err
	}

	if uint16(len(data)) != FRAME_TOKEN_LENGTH {
		return nil, ErrInvalidTokenLength
	}

	return data, nil
}

type writerFunc func([]byte) (int, error)
func (w writerFunc) Write(data []byte) (int, error) {
	return w(data)
}
