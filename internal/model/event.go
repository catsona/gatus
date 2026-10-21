package model

import "time"

// Event names a source change with a stable ID for delivery deduplication.
type Event struct {
	ID         string
	Type       string
	Summary    string
	Url        string
	Actor      string
	OccurredAt string
	Field      string
	Old        string
	New        string
}

// TemplateData is the supported template contract for webhook text.
type TemplateData struct {
	PR      PR
	Account Account
	Event   Event
}

// Delivery persists retries without retaining credentials or a webhook URL.
type Delivery struct {
	ID          string
	Account     Account
	PR          PR
	Event       Event
	Attempts    int
	NextAttempt time.Time
	LastError   string
}
