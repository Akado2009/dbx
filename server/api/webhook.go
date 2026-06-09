package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

type WebhookPayload struct {
	Event     string `json:"event"`
	Branch    string `json:"branch"`
	Project   string `json:"project"`
	Detail    string `json:"detail,omitempty"`
	Timestamp string `json:"timestamp"`
}

// fireWebhooks sends webhook payloads to all registered URLs for the given event.
func (s *Server) fireWebhooks(ctx context.Context, projectID, projectName, branchName, event, detail string) {
	webhooks, err := s.store.ListWebhooksForProject(ctx, projectID, event)
	if err != nil || len(webhooks) == 0 {
		return
	}

	payload := WebhookPayload{
		Event:     event,
		Branch:    branchName,
		Project:   projectName,
		Detail:    detail,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)

	for _, wh := range webhooks {
		go func(url string) {
			client := &http.Client{Timeout: 10 * time.Second}
			req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				log.Printf("[webhook] bad url %s: %v", url, err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "dbx-webhook/1.0")

			resp, err := client.Do(req)
			if err != nil {
				log.Printf("[webhook] %s → %s: %v", event, url, err)
				return
			}
			resp.Body.Close()
			log.Printf("[webhook] %s → %s: %d", event, url, resp.StatusCode)
		}(wh.URL)
	}
}
