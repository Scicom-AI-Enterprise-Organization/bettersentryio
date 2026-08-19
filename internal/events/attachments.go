package events

// Attachments from the envelope's attachment items (sentry_sdk.add_attachment).
// Keyed by the SDK's event uuid — the envelope header carries it, and the event
// page already has it, so no join through the events table is needed.

import (
	"context"
	"time"
)

// Attachment is the metadata row; the bytes are fetched separately so listing
// an event's attachments never drags blobs across the wire.
type Attachment struct {
	ID          int64     `json:"id"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	ReceivedAt  time.Time `json:"received_at"`
}

func (s *Store) SaveAttachment(ctx context.Context, projectID int64, eventUUID, filename, contentType string, data []byte) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.db.Exec(ctx, `
		insert into attachments (project_id, event_uuid, filename, content_type, size, data)
		values ($1, $2, $3, $4, $5, $6)`,
		projectID, truncate(eventUUID, 64), truncate(filename, 255), truncate(contentType, 128),
		int64(len(data)), data)
	return err
}

// Attachments lists an event's attachments, scoped to a project so one
// project's key cannot enumerate another's uploads.
func (s *Store) Attachments(ctx context.Context, projectSlug, eventUUID string) ([]Attachment, error) {
	rows, err := s.db.Query(ctx, `
		select a.id, a.filename, a.content_type, a.size, a.received_at
		from attachments a join projects p on p.id = a.project_id
		where p.slug = $1 and a.event_uuid = $2
		order by a.id`,
		projectSlug, eventUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Attachment{}
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.Filename, &a.ContentType, &a.Size, &a.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AttachmentData returns one attachment's bytes for download. ok=false means no
// such row, which the handler turns into a 404 rather than an error.
func (s *Store) AttachmentData(ctx context.Context, id int64) (filename, contentType string, data []byte, ok bool, err error) {
	err = s.db.QueryRow(ctx,
		`select filename, content_type, data from attachments where id = $1`, id,
	).Scan(&filename, &contentType, &data)
	if err != nil {
		return "", "", nil, false, nil //nolint:nilerr // absent is not an error to the caller
	}
	return filename, contentType, data, true, nil
}
