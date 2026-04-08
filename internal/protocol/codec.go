package protocol

import (
	"encoding/json"
	"fmt"
)

// Codec defines the interface for message encoding and decoding.
type Codec interface {
	// Marshal encodes a message to bytes.
	Marshal(v any) ([]byte, error)

	// Unmarshal decodes bytes to a message.
	Unmarshal(data []byte, v any) error

	// Name returns the codec name.
	Name() string
}

// JSONCodec is the default JSON codec implementation.
type JSONCodec struct{}

func (c *JSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (c *JSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

func (c *JSONCodec) Name() string {
	return "json"
}

// RawCodec is a pass-through codec for raw bytes.
type RawCodec struct{}

func (c *RawCodec) Marshal(v any) ([]byte, error) {
	switch msg := v.(type) {
	case []byte:
		return msg, nil
	case string:
		return []byte(msg), nil
	default:
		return nil, fmt.Errorf("raw codec: unsupported type %T", v)
	}
}

func (c *RawCodec) Unmarshal(data []byte, v any) error {
	switch ptr := v.(type) {
	case *[]byte:
		*ptr = data
		return nil
	case *string:
		*ptr = string(data)
		return nil
	default:
		return fmt.Errorf("raw codec: unsupported type %T", v)
	}
}

func (c *RawCodec) Name() string {
	return "raw"
}

// DefaultCodec is the default codec used by connections.
var DefaultCodec Codec = &JSONCodec{}
