package types

import "encoding/json"

func copiedRaw(data []byte) json.RawMessage {
	if len(data) == 0 {
		return nil
	}
	copyBuf := make([]byte, len(data))
	copy(copyBuf, data)
	return json.RawMessage(copyBuf)
}
