package vai

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/vango-go/vai-lite/pkg/core/types"
)

func buildServerToolExecutionContext(history []types.Message) *types.ServerToolExecutionContext {
	if len(history) == 0 {
		return nil
	}

	type imageEntry struct {
		id    string
		block types.ImageBlock
	}

	seen := make(map[string]string)
	entries := make([]imageEntry, 0, 4)
	var count int

	var visitBlocks func([]types.ContentBlock)
	visitBlocks = func(blocks []types.ContentBlock) {
		for i := range blocks {
			switch b := blocks[i].(type) {
			case types.ImageBlock:
				fp := serverToolImageFingerprint(b)
				if _, ok := seen[fp]; ok {
					continue
				}
				count++
				id := fmt.Sprintf("img-%02d", count)
				seen[fp] = id
				entries = append(entries, imageEntry{id: id, block: b})
			case *types.ImageBlock:
				if b == nil {
					continue
				}
				fp := serverToolImageFingerprint(*b)
				if _, ok := seen[fp]; ok {
					continue
				}
				count++
				id := fmt.Sprintf("img-%02d", count)
				seen[fp] = id
				entries = append(entries, imageEntry{id: id, block: *b})
			case types.ToolResultBlock:
				visitBlocks(b.Content)
			case *types.ToolResultBlock:
				if b != nil {
					visitBlocks(b.Content)
				}
			}
		}
	}

	for i := range history {
		visitBlocks(history[i].ContentBlocks())
	}
	if len(entries) == 0 {
		return nil
	}

	out := &types.ServerToolExecutionContext{
		Images: make([]types.ServerToolExecutionImage, 0, len(entries)),
	}
	for i := range entries {
		out.Images = append(out.Images, types.ServerToolExecutionImage{
			ID:    entries[i].id,
			Image: entries[i].block,
		})
	}
	return out
}

func serverToolImageFingerprint(block types.ImageBlock) string {
	h := sha256.New()
	_, _ = h.Write([]byte(block.Source.Type))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(block.Source.MediaType))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(block.Source.URL))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(block.Source.Data))
	return hex.EncodeToString(h.Sum(nil))
}
