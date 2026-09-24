package main

import (
	"time"
)

// EmailSummary provides lightweight metadata for unread or searched emails.
type EmailSummary struct {
	Account   string    `json:"account"`
	ID        string    `json:"id"`
	From      string    `json:"from"`
	Subject   string    `json:"subject"`
	Date      time.Time `json:"date"`
	Snippet   string    `json:"snippet"`
	MessageID string    `json:"message_id,omitempty"`
}

// EmailDetail represents full content and metadata of an email.
type EmailDetail struct {
	Account     string            `json:"account"`
	ID          string            `json:"id"`
	MessageID   string            `json:"message_id"`
	From        string            `json:"from"`
	To          []string          `json:"to"`
	Cc          []string          `json:"cc,omitempty"`
	Subject     string            `json:"subject"`
	Date        time.Time         `json:"date"`
	Body        string            `json:"body"`
	Attachments []string          `json:"attachments,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
}

// AccountStatus provides connection test results.
type AccountStatus struct {
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Username  string `json:"username"`
	Host      string `json:"host"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}
