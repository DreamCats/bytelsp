package gopls

import (
	"context"
	"fmt"
	"sync"
)

type DocumentManager struct {
	client *Client
	mu     sync.Mutex
	docs   map[string]int
}

func NewDocumentManager(client *Client) *DocumentManager {
	return &DocumentManager{client: client, docs: make(map[string]int)}
}

func (dm *DocumentManager) OpenOrUpdate(ctx context.Context, uri, languageID, content string) (int, error) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	version, exists := dm.docs[uri]
	if !exists {
		version = 1
		params := map[string]interface{}{
			"textDocument": map[string]interface{}{
				"uri":        uri,
				"languageId": languageID,
				"version":    version,
				"text":       content,
			},
		}
		if err := dm.client.SendNotification("textDocument/didOpen", params); err != nil {
			return 0, fmt.Errorf("didOpen: %w", err)
		}
		dm.docs[uri] = version
		return version, nil
	}

	version++
	params := map[string]interface{}{
		"textDocument": map[string]interface{}{
			"uri":     uri,
			"version": version,
		},
		"contentChanges": []map[string]interface{}{{
			"text": content,
		}},
	}
	if err := dm.client.SendNotification("textDocument/didChange", params); err != nil {
		return 0, fmt.Errorf("didChange: %w", err)
	}
	dm.docs[uri] = version
	return version, nil
}
