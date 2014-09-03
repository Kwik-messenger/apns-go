package apns

import (
	"encoding/hex"
	"encoding/json"
)


// payload is a handy alias for keeping payload
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

// unpackToken decodes token into byte slice.
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

// writerFunc is a wrapper, creates io.Writer from function
type writerFunc func([]byte) (int, error)

// Write is a proxy method for writerFunc
func (w writerFunc) Write(data []byte) (int, error) {
	return w(data)
}
