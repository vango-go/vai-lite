package types

// RequestHasAudioSTT returns true when system/messages contain at least one audio_stt block.
func RequestHasAudioSTT(req *MessageRequest) bool {
	if req == nil {
		return false
	}
	switch system := req.System.(type) {
	case []ContentBlock:
		if ContentBlocksHaveAudioSTT(system) {
			return true
		}
	}
	for _, msg := range req.Messages {
		if ContentBlocksHaveAudioSTT(msg.ContentBlocks()) {
			return true
		}
	}
	return false
}

// ContentBlocksHaveAudioSTT returns true when the block slice contains an audio_stt block.
func ContentBlocksHaveAudioSTT(blocks []ContentBlock) bool {
	for _, block := range blocks {
		switch b := block.(type) {
		case AudioSTTBlock, *AudioSTTBlock:
			return true
		case ToolResultBlock:
			if ContentBlocksHaveAudioSTT(b.Content) {
				return true
			}
		case *ToolResultBlock:
			if b != nil && ContentBlocksHaveAudioSTT(b.Content) {
				return true
			}
		}
	}
	return false
}
